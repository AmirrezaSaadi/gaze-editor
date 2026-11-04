package editor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadDocumentUTF8(t *testing.T) {
	text, enc, err := readDocument(strings.NewReader("héllo\nwörld\n"))
	if err != nil {
		t.Fatalf("readDocument: %v", err)
	}
	if text != "héllo\nwörld\n" {
		t.Fatalf("text = %q", text)
	}
	if enc != encUTF8 {
		t.Fatalf("enc = %q, want %q", enc, encUTF8)
	}
}

func TestReadDocumentEmpty(t *testing.T) {
	text, enc, err := readDocument(strings.NewReader(""))
	if err != nil || text != "" || enc != encUTF8 {
		t.Fatalf("got %q %q %v", text, enc, err)
	}
}

// A byte-order mark must be stripped from the buffer but remembered, so that
// saving does not quietly drop it and change the file's format.
func TestUTF8BOMRoundTrip(t *testing.T) {
	raw := append(append([]byte{}, bomUTF8...), "line\n"...)
	text, enc, err := readDocument(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("readDocument: %v", err)
	}
	if text != "line\n" {
		t.Fatalf("BOM not stripped, text = %q", text)
	}
	if enc != encUTF8BOM {
		t.Fatalf("enc = %q, want %q", enc, encUTF8BOM)
	}
	if got := encodeDocument(text, enc); !bytes.Equal(got, raw) {
		t.Fatalf("round-trip changed the bytes: %v", got)
	}
}

// UTF-16 is the case that used to corrupt files: the bytes were loaded as
// UTF-8 and written straight back, so the original was destroyed on save.
func TestUTF16RoundTrip(t *testing.T) {
	// Includes a non-BMP rune to exercise surrogate-pair encoding.
	const want = "héllo\nwörld 😀\n"

	for _, enc := range []string{encUTF16LE, encUTF16BE} {
		raw := encodeDocument(want, enc)

		text, gotEnc, err := readDocument(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: readDocument: %v", enc, err)
		}
		if text != want {
			t.Fatalf("%s: text = %q, want %q", enc, text, want)
		}
		if gotEnc != enc {
			t.Fatalf("%s: enc = %q", enc, gotEnc)
		}
		if got := encodeDocument(text, enc); !bytes.Equal(got, raw) {
			t.Fatalf("%s: round-trip changed the bytes", enc)
		}
	}
}

func TestReadDocumentRefusals(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"binary", []byte("abc\x00def"), errBinary},
		{"utf16 without bom", []byte("a\x00b\x00c\x00d\x00"), errUTF16NoBOM},
		{"utf32 le", append(append([]byte{}, bomUTF32LE...), 0x41, 0, 0, 0), errUTF32},
		{"utf32 be", append(append([]byte{}, bomUTF32BE...), 0, 0, 0, 0x41), errUTF32},
		{"truncated utf16", append(append([]byte{}, bomUTF16LE...), 'a', 0, 'b'), errUTF16Trunc},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := readDocument(bytes.NewReader(tc.data)); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A stray NUL in otherwise ordinary text must not be mistaken for UTF-16.
func TestStrayNULIsBinary(t *testing.T) {
	data := []byte("this is a long enough text with one \x00 nul inside it\n")
	if _, _, err := readDocument(bytes.NewReader(data)); !errors.Is(err, errBinary) {
		t.Fatalf("err = %v, want %v", err, errBinary)
	}
}

// End to end: a UTF-16 file is opened, edited and saved, and must still be
// UTF-16 with its byte-order mark afterwards.
func TestUTF16FileSurvivesEditAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "utf16.txt")
	original := encodeDocument("one\ntwo\n", encUTF16LE)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	text, enc, err := readDocument(f)
	f.Close()
	if err != nil {
		t.Fatalf("readDocument: %v", err)
	}

	e := newTestEditor(t)
	e.setContent(text, path, enc)

	setBuffer(e, "one\ntwo\nthree\n")
	e.writeTo(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := encodeDocument("one\ntwo\nthree\n", encUTF16LE); !bytes.Equal(got, want) {
		t.Fatalf("saved bytes are not UTF-16 LE: got %d bytes, want %d", len(got), len(want))
	}
}

func TestEncodingSuffixShownOnlyWhenNotPlainUTF8(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("x", "")
	if got := e.encodingSuffix(); got != "" {
		t.Fatalf("plain UTF-8 suffix = %q, want empty", got)
	}
	e.setContent("x", "", encUTF16LE)
	if got := e.encodingSuffix(); got != " · "+encUTF16LE {
		t.Fatalf("suffix = %q", got)
	}
}

// oneByteReader hands out a single byte per Read, which is the worst case for a
// streaming decoder: every chunk boundary falls inside a code unit.
type oneByteReader struct {
	r   io.Reader
	buf [1]byte
}

func (o *oneByteReader) Read(p []byte) (int, error) {
	n, err := o.r.Read(o.buf[:])
	if n > 0 {
		p[0] = o.buf[0]
		return 1, nil
	}
	return 0, err
}

// Opening streams the file into a document, and it must land on exactly the text
// readDocument would have produced, for every encoding and chunk boundary.
func TestOpenDocumentMatchesReadDocument(t *testing.T) {
	texts := []string{
		"",
		"a",
		"a\nb\n",
		"héllo\nwörld\n",
		"😀 emoji\nand more\n",
		"tabs\tand  spaces  \n",
		"crlf\r\nlines\r\n",
		"lone\rcr\rlines",
		"mixed\r\nends\rhere\n",
	}
	for _, enc := range []string{encUTF8, encUTF8BOM, encUTF16LE, encUTF16BE} {
		for _, text := range texts {
			raw := encodeDocument(text, enc)

			want, _, err := readDocument(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("%s %q: readDocument: %v", enc, text, err)
			}
			wantText, wantEOL := normalizeEOL(want)
			wantDoc := NewDocument(wantText)

			for _, r := range []io.Reader{bytes.NewReader(raw), &oneByteReader{r: bytes.NewReader(raw)}} {
				doc, eol, gotEnc, err := openDocument(r)
				if err != nil {
					t.Fatalf("%s %q: openDocument: %v", enc, text, err)
				}
				if gotEnc != enc {
					t.Fatalf("%s %q: encoding = %q", enc, text, gotEnc)
				}
				if eol != wantEOL {
					t.Fatalf("%s %q: eol = %q, want %q", enc, text, eol, wantEOL)
				}
				if got := doc.Text(); got != wantText {
					t.Fatalf("%s %q: text = %q, want %q", enc, text, got, wantText)
				}
				if doc.LineCount() != wantDoc.LineCount() {
					t.Fatalf("%s %q: %d lines, want %d", enc, text, doc.LineCount(), wantDoc.LineCount())
				}
			}
		}
	}
}

// A stream that is not text is refused before it reaches the editor.
func TestOpenDocumentRefusals(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"binary", []byte("abc\x00def"), errBinary},
		{"utf32le", []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0, 0, 0}, errUTF32},
		{"utf16 no bom", []byte{'a', 0, 'b', 0, 'c', 0, 'd', 0, 'e', 0, 'f', 0}, errUTF16NoBOM},
		{"truncated utf16", []byte{0xFF, 0xFE, 'a', 0, 'b'}, errUTF16Trunc},
	}
	for _, tc := range cases {
		if _, _, _, err := openDocument(bytes.NewReader(tc.data)); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if _, _, _, err := openDocument(&oneByteReader{r: bytes.NewReader(tc.data)}); !errors.Is(err, tc.want) {
			t.Fatalf("%s (byte at a time): err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// The size gate is the only thing that refuses a file now, and it has to allow
// files far beyond the old 256 MiB ceiling.
func TestOpenSizeLimitAllowsLargeFiles(t *testing.T) {
	if MaxOpenBytes < 1<<30 {
		t.Fatalf("MaxOpenBytes = %s, want at least 1 GiB", humanBytes(MaxOpenBytes))
	}
	if LargeFileBytes >= MaxOpenBytes {
		t.Fatalf("LargeFileBytes = %s is not below MaxOpenBytes = %s",
			humanBytes(LargeFileBytes), humanBytes(MaxOpenBytes))
	}
}
