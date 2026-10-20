package editor

import (
	"bytes"
	"errors"
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

	e.entry.SetText("one\ntwo\nthree\n")
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
