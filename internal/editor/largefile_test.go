package editor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// setSizeLimits moves the open size thresholds for one test.
func setSizeLimits(t *testing.T, large, max int64) {
	t.Helper()
	oldLarge, oldMax := LargeFileBytes, MaxOpenBytes
	LargeFileBytes, MaxOpenBytes = large, max
	t.Cleanup(func() { LargeFileBytes, MaxOpenBytes = oldLarge, oldMax })
}

// The gate has to refuse loudly, and must never start the open.
func TestOversizeFileIsRefusedBeforeReading(t *testing.T) {
	setSizeLimits(t, 4, 8)
	e := newTestEditor(t)

	opened := false
	e.confirmOpenSize("huge.log", 9, func(openMode) { opened = true })

	if opened {
		t.Fatal("a file over MaxOpenBytes must not be opened")
	}
	if !strings.Contains(e.status.Text, "too large") {
		t.Fatalf("status = %q, want a refusal", e.status.Text)
	}
}

// A file over the warning threshold asks first and does not open until answered.
func TestLargeFileAsksBeforeOpening(t *testing.T) {
	setSizeLimits(t, 4, 1<<20)
	e := newTestEditor(t)

	opened := false
	e.confirmOpenSize("big.log", 5, func(mode openMode) { opened = mode.large() })

	if opened {
		t.Fatal("the open must wait for the user's answer")
	}
	if e.win.Canvas().Overlays().Top() == nil {
		t.Fatal("a confirmation dialog should be showing")
	}
}

// A small file opens without a prompt.
func TestSmallFileOpensDirectly(t *testing.T) {
	setSizeLimits(t, 16, 1<<20)
	e := newTestEditor(t)

	var got openMode = -1
	e.confirmOpenSize("small.txt", 15, func(mode openMode) { got = mode })

	if got != openNormal {
		t.Fatalf("mode = %v, want openNormal", got)
	}
}

// The reader must fail instead of quietly truncating a file that grew past the
// limit between the stat and the read.
func TestLimitReaderRefusesInsteadOfTruncating(t *testing.T) {
	lr := &limitReader{r: strings.NewReader(strings.Repeat("a", 100)), limit: 10}
	if _, err := io.ReadAll(lr); !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want errTooLarge", err)
	}

	lr = &limitReader{r: strings.NewReader(strings.Repeat("a", 10)), limit: 10}
	if _, err := io.ReadAll(lr); err != nil {
		t.Fatalf("a file at the limit should read cleanly: %v", err)
	}
}

// A refused read must leave the current buffer alone rather than install a
// half-read file.
func TestOversizeReadDoesNotReplaceTheBuffer(t *testing.T) {
	setSizeLimits(t, 1<<20, 10)
	path := filepath.Join(t.TempDir(), "grew.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 100)), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t)
	e.SetContent("keep me", "")

	// openPath is called directly to bypass the stat-based gate and exercise the
	// reader's own limit.
	e.openPath(path, openNormal, 0, 0)

	if bufferText(e) != "keep me" {
		t.Fatalf("buffer = %q, want it untouched", bufferText(e))
	}
	if e.currentFile != "" {
		t.Fatalf("currentFile = %q, want none", e.currentFile)
	}
}

// Large-file mode drops the chrome that copies or rescans the document.
func TestLargeFileModeShedsCopyingChrome(t *testing.T) {
	e := newTestEditor(t)
	path := filepath.Join(t.TempDir(), "big.txt")

	e.setContentAs("one\ntwo\nthree", path, encUTF8, openLarge)

	if !e.largeFile {
		t.Fatal("setContentAs(openLarge) should enable large-file mode")
	}
	if e.statWords != 0 || e.statChars != 0 {
		t.Fatalf("counts = %dw/%dc, want them skipped", e.statWords, e.statChars)
	}
	if !strings.Contains(e.info.Text, "large") {
		t.Fatalf("status readout = %q, want it to name large-file mode", e.info.Text)
	}
	if strings.Contains(e.info.Text, "0w") {
		t.Fatalf("status readout = %q, want no word count", e.info.Text)
	}
	// Wrap is a preference, and large-file mode honours it like any other file.
	if e.wrapItem.Checked != e.wrap || e.wrapItem.Disabled {
		t.Fatalf("wrap item checked=%v disabled=%v, want checked=%v and usable",
			e.wrapItem.Checked, e.wrapItem.Disabled, e.wrap)
	}
	before := e.code.wrap
	e.ToggleWrap()
	if e.code.wrap == before || e.wrapItem.Checked != e.code.wrap {
		t.Fatalf("ToggleWrap: code wrap=%v checked=%v", e.code.wrap, e.wrapItem.Checked)
	}
	e.ToggleWrap()
	if e.code.wrap != before {
		t.Fatal("toggling wrap back did not restore it")
	}
}

// Snapshots, recovery copies and the trim rewrite are all skipped, because each
// one holds or builds a second copy of the buffer.
func TestLargeFileModeSkipsSnapshotsAndTrim(t *testing.T) {
	isolatedRecovery(t)
	path := filepath.Join(t.TempDir(), "big.txt")

	e := newTestEditor(t)
	e.setContentAs("a  \nb", path, encUTF8, openLarge)
	e.autosaveOn = true
	e.trimOnSave = true
	e.dirty = true
	e.lastAutosave = time.Time{}

	e.maybeAutosave()
	if _, ok := recoveryFor(path); ok {
		t.Fatal("large-file mode must not write recovery snapshots")
	}

	e.code.setCaret(pos{})
	e.DuplicateLine()
	if got := e.doc.TextRange(0, e.doc.Len()); got != "a  \na  \nb" {
		t.Fatalf("duplicate = %q", got)
	}
	// Structural commands are undoable in large-file mode too.
	e.Undo()
	if got := e.doc.TextRange(0, e.doc.Len()); got != "a  \nb" {
		t.Fatalf("undo = %q, want the original text back", got)
	}
	e.DuplicateLine()

	e.writeTo(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a  \na  \nb" {
		t.Fatalf("file = %q, want trailing spaces kept (no trim in large-file mode)", data)
	}
}

// The streaming writer must produce exactly the bytes the old whole-buffer
// encoder did, for every encoding, line ending and trim setting.
func TestWriteDocumentStreamsIdentically(t *testing.T) {
	texts := []string{
		"",
		"a",
		"a\nb\n",
		"héllo\nwörld\n😀 tail\n",
		"a  \nb\t\nc",
		"x\r\ny",
		"a" + string(rune(0xD800)) + "b", // unpaired surrogate
		strings.Repeat("x", 5000),        // longer than one utf16 chunk
		strings.Repeat("a", 2047) + "😀" + strings.Repeat("b", 10), // rune on the chunk edge
	}

	for _, enc := range []string{encUTF8, encUTF8BOM, encUTF16LE, encUTF16BE} {
		for _, eol := range []string{eolLF, eolCRLF, eolCR} {
			for _, trim := range []bool{false, true} {
				for _, text := range texts {
					wantText := text
					if trim {
						wantText = trimTrailingSpace(wantText)
					}
					wantText = strings.ReplaceAll(wantText, "\n", eolBytes(eol))
					want := encodeIndependent(t, wantText, enc)
					got := streamDocument(t, text, eol, enc, trim)
					if !bytes.Equal(got, want) {
						t.Fatalf("enc=%s eol=%s trim=%v text=%q:\n got %x\nwant %x",
							enc, eol, trim, text, got, want)
					}
				}
			}
		}
	}
}

// encodeIndependent renders s in enc without using the production writer.
func encodeIndependent(t *testing.T, s, enc string) []byte {
	t.Helper()
	switch enc {
	case encUTF8BOM:
		return append(append([]byte{}, bomUTF8...), s...)
	case encUTF16LE, encUTF16BE:
		le := enc == encUTF16LE
		out := []byte{}
		if le {
			out = append(out, bomUTF16LE...)
		} else {
			out = append(out, bomUTF16BE...)
		}
		for _, u := range utf16.Encode([]rune(s)) {
			if le {
				out = append(out, byte(u), byte(u>>8))
			} else {
				out = append(out, byte(u>>8), byte(u))
			}
		}
		return out
	}
	return []byte(s)
}

func streamDocument(t *testing.T, text, eol, enc string, trim bool) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeDocument(&b, text, eol, enc, trim); err != nil {
		t.Fatalf("writeDocument: %v", err)
	}
	return b.Bytes()
}

// detectIndent must not walk (or split) a whole large buffer.
func TestDetectIndentIsBounded(t *testing.T) {
	// A 2-space indent past the scan window is not seen: the default is used.
	late := strings.Repeat("x\n", indentScanLimit/2) + "  late\n"
	if useTabs, width := detectIndent(late); useTabs || width != 4 {
		t.Fatalf("indent past the window = %v,%d, want the 4-space default", useTabs, width)
	}

	// The same indent inside the window is detected.
	early := "x\n  early\n" + strings.Repeat("x\n", indentScanLimit/2)
	if useTabs, width := detectIndent(early); useTabs || width != 2 {
		t.Fatalf("indent inside the window = %v,%d, want 2 spaces", useTabs, width)
	}
}

// normalizeEOL must still classify and rewrite mixed endings in one pass.
func TestNormalizeEOLRewrite(t *testing.T) {
	got, style := normalizeEOL("a\r\nb\rc\n")
	if style != eolCRLF {
		t.Fatalf("style = %q, want CRLF", style)
	}
	if got != "a\nb\nc\n" {
		t.Fatalf("text = %q, want all LF", got)
	}

	// LF-only text is returned without copying.
	in := "a\nb\n"
	got, style = normalizeEOL(in)
	if style != eolLF || got != in {
		t.Fatalf("LF text = %q,%q", got, style)
	}
}
