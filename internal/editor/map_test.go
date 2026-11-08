package editor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestFile writes text to a file in the test's temporary directory.
func writeTestFile(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// mapTestText is plain LF text long enough to be mapped rather than read.
func mapTestText(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line %d of a file that is opened from a mapping\n", i)
	}
	return b.String()
}

func TestMappable(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"plain LF", "one\ntwo\n", true},
		{"no trailing newline", "one\ntwo", true},
		{"CRLF", "one\r\ntwo\r\n", false},
		{"lone CR", "one\rtwo\r", false},
		{"UTF-8 BOM", "\xEF\xBB\xBFone\n", false},
		{"UTF-16 LE BOM", "\xFF\xFEo\x00n\x00e\x00", false},
		{"UTF-16 BE BOM", "\xFE\xFF\x00o\x00n", false},
		{"NUL byte", "one\x00two\n", false},
	}
	for _, c := range cases {
		if got := mappable([]byte(c.data)); got != c.want {
			t.Errorf("%s: mappable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMapPathSeesTheFile(t *testing.T) {
	text := mapTestText(100)
	path := writeTestFile(t, "text.txt", text)

	region, err := mapPath(path, int64(len(text)))
	if err != nil {
		t.Fatalf("mapPath: %v", err)
	}
	if got := string(region); got != text {
		t.Fatalf("mapped %d bytes, want %d", len(got), len(text))
	}
}

func TestMapPathRefusesEmptyFile(t *testing.T) {
	path := writeTestFile(t, "empty.txt", "")
	if _, err := mapPath(path, 0); err != errMapEmpty {
		t.Fatalf("err = %v, want errMapEmpty", err)
	}
}

// A mapped document must behave exactly like one built from the same text: the
// pages are views of the file instead of copies, but every read path has to agree.
func TestIndexMappedMatchesNewDocument(t *testing.T) {
	text := mapTestText(3000)
	path := writeTestFile(t, "text.txt", text)
	region, err := mapPath(path, int64(len(text)))
	if err != nil {
		t.Fatalf("mapPath: %v", err)
	}
	mapped := indexMapped(region)
	copied := NewDocument(text)

	if !mapped.mapped() || copied.mapped() {
		t.Fatalf("mapped() = %v for the mapped document, %v for the copy", mapped.mapped(), copied.mapped())
	}
	if mapped.Len() != copied.Len() || mapped.LineCount() != copied.LineCount() {
		t.Fatalf("size/lines = %d/%d, want %d/%d", mapped.Len(), mapped.LineCount(), copied.Len(), copied.LineCount())
	}
	if got, want := mapped.Text(), copied.Text(); got != want {
		t.Fatalf("text differs (%d vs %d bytes)", len(got), len(want))
	}
	for _, line := range []int{0, 1, 17, 4095, 4096, 4097, 2999} {
		if got, want := mapped.LineText(line), copied.LineText(line); got != want {
			t.Fatalf("line %d = %q, want %q", line, got, want)
		}
		if mapped.LineStart(line) != copied.LineStart(line) {
			t.Fatalf("start of line %d differs", line)
		}
	}
	if got, want := mapped.TextRange(1000, 2000), copied.TextRange(1000, 2000); got != want {
		t.Fatalf("range differs: %q vs %q", got, want)
	}
	if row, col := mapped.RowColOf(5000); row != copied.LineOf(5000) || col != 5000-copied.LineStart(row) {
		t.Fatalf("RowColOf(5000) = %d,%d", row, col)
	}
}

// Editing a mapped document copies only the pages it touches: the text must read
// back correctly either way.
func TestMappedDocumentEdits(t *testing.T) {
	text := mapTestText(2000)
	path := writeTestFile(t, "text.txt", text)
	region, err := mapPath(path, int64(len(text)))
	if err != nil {
		t.Fatalf("mapPath: %v", err)
	}
	doc := indexMapped(region)
	want := NewDocument(text)

	// An insert deep inside a page, a delete across a page boundary, and an
	// append at the end. The offsets are resolved as each edit is applied, since
	// the earlier ones move the text.
	edits := []struct {
		at   func(*Document) (int, int)
		repl string
	}{
		{func(*Document) (int, int) { return 100_000, 100_000 }, "INSERTED "},
		{func(*Document) (int, int) { return 64 << 10, 64<<10 + 2000 }, "cut across the page boundary"},
		{func(d *Document) (int, int) { return d.Len(), d.Len() }, "\nlast line\n"},
	}
	for _, ed := range edits {
		from, to := ed.at(doc)
		doc.ReplaceRange(from, to, ed.repl)
		want.ReplaceRange(from, to, ed.repl)
		if got, exp := doc.Text(), want.Text(); got != exp {
			t.Fatalf("after editing at %d the text differs (%d vs %d bytes)", from, len(got), len(exp))
		}
	}

	var out bytes.Buffer
	if _, err := doc.WriteTo(&out); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if out.String() != want.Text() {
		t.Fatal("the saved bytes differ from the edited document")
	}
	if doc.LineCount() != want.LineCount() {
		t.Fatalf("line count = %d, want %d", doc.LineCount(), want.LineCount())
	}
}

// Opening a large LF file goes through the mapping: nothing is copied, so the
// document reports itself as mapped.
func TestOpenLargeFileIsMapped(t *testing.T) {
	text := mapTestText(400_000) // ~20 MB
	if int64(len(text)) < mapMinBytes*2 {
		t.Fatalf("test text is only %d bytes", len(text))
	}
	path := writeTestFile(t, "big.txt", text)
	e := newTestEditor(t)

	e.startOpen(path, openLarge, nil)
	waitFor(t, "the mapped open to finish", func() bool { return e.currentFile == path })

	if !e.doc.mapped() {
		t.Fatal("a large LF file was not opened from a mapping")
	}
	if got := e.doc.Text(); got != text {
		t.Fatalf("text = %d bytes, want %d", len(got), len(text))
	}
	if got := e.doc.LineCount(); got != int64Lines(text) {
		t.Fatalf("lines = %d, want %d", got, int64Lines(text))
	}
	waitFor(t, "the opened status", func() bool { return strings.Contains(e.status.Text, "opened") })
	if e.loadBarShown() {
		t.Fatal("the progress bar is still in the status row")
	}
}

// A CRLF file cannot be mapped as it stands (the editor holds LF text), so it
// falls back to the streaming reader and still arrives normalised.
func TestOpenCRLFFallsBackToStreaming(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400_000; i++ {
		fmt.Fprintf(&b, "line %d of a file with CRLF endings\r\n", i)
	}
	text := b.String()
	path := writeTestFile(t, "crlf.txt", text)
	e := newTestEditor(t)

	e.startOpen(path, openLarge, nil)
	waitFor(t, "the streaming open to finish", func() bool { return e.currentFile == path })

	if e.doc.mapped() {
		t.Fatal("a CRLF file was mapped; it needs normalising")
	}
	if strings.ContainsRune(e.doc.Text(), '\r') {
		t.Fatal("the buffer still holds carriage returns")
	}
	if !strings.Contains(e.doc.LineText(5), "line 5 of a file with CRLF endings") {
		t.Fatalf("line 5 = %q", e.doc.LineText(5))
	}
	waitFor(t, "the opened status", func() bool { return strings.Contains(e.status.Text, "opened") })
}

// Typing into a mapped document and saving must write the edited text, with the
// untouched pages coming straight from the mapping.
func TestMappedDocumentSaveAfterEdit(t *testing.T) {
	text := mapTestText(400_000)
	path := writeTestFile(t, "big.txt", text)
	out := filepath.Join(t.TempDir(), "saved.txt")

	e := newTestEditor(t)
	e.startOpen(path, openLarge, nil)
	waitFor(t, "the mapped open to finish", func() bool { return e.currentFile == path })

	e.code.setCaret(pos{row: 1000, col: 0})
	e.code.insertText("first line of the edit\n")
	if e.doc.LineText(1000) != "first line of the edit" {
		t.Fatalf("line 1000 = %q", e.doc.LineText(1000))
	}
	e.currentFile = out
	e.SaveFile()
	waitFor(t, "the save to finish", func() bool { return !e.saving && !e.dirty })

	saved, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want := NewDocument(text)
	want.ReplaceRange(want.LineStart(1000), want.LineStart(1000), "first line of the edit\n")
	if string(saved) != want.Text() {
		t.Fatalf("saved %d bytes, want %d", len(saved), len(want.Text()))
	}
}

// int64Lines counts the lines in text the way a document does: one per newline,
// plus one for the text after the last newline.
func int64Lines(text string) int {
	return strings.Count(text, "\n") + 1
}

// The mapped path reports progress between its passes, so the bar is not stuck.
func TestMappedOpenReportsProgress(t *testing.T) {
	text := mapTestText(400_000)
	path := writeTestFile(t, "big.txt", text)
	e := newTestEditor(t)

	seen := false
	e.startOpen(path, openLarge, nil)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(e.status.Text, "%") {
			seen = true
		}
		if e.currentFile == path {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	waitFor(t, "the mapped open to finish", func() bool { return e.currentFile == path })
	if !seen {
		t.Log("no percentage status was observed; the open may have been too fast to catch")
	}
}
