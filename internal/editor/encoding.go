package editor

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Encodings Gaze can round-trip. The buffer is always UTF-8 internally; the
// name is remembered per document so that saving writes the original byte form
// back instead of silently converting the file to UTF-8 behind the user's back.
const (
	encUTF8    = "UTF-8"
	encUTF8BOM = "UTF-8 BOM"
	encUTF16LE = "UTF-16 LE"
	encUTF16BE = "UTF-16 BE"
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}

	// UTF-32 LE starts with the UTF-16 LE byte-order mark, so it has to be
	// ruled out before the UTF-16 cases are considered.
	bomUTF32LE = []byte{0xFF, 0xFE, 0x00, 0x00}
	bomUTF32BE = []byte{0x00, 0x00, 0xFE, 0xFF}
)

// Refusing to open is the safe outcome for these: the buffer would not survive
// a save, and writing it back would destroy the original file.
var (
	errBinary     = errors.New("not a text file: it contains NUL bytes")
	errUTF32      = errors.New("UTF-32 is not supported — convert the file to UTF-8 first")
	errUTF16NoBOM = errors.New("this looks like UTF-16 without a byte-order mark — convert it to UTF-8 or UTF-16 with a BOM")
	errUTF16Trunc = errors.New("truncated UTF-16 data: odd number of bytes")
)

// readDocument buffers r as UTF-8 text and reports the encoding that
// encodeDocument needs in order to write it back unchanged.
//
// The common UTF-8 path still streams straight into a strings.Builder so that
// no full-size byte slice is allocated; only UTF-16 needs the bytes up front.
func readDocument(r io.Reader) (text, enc string, err error) {
	br := bufio.NewReader(r)
	head, _ := br.Peek(4) // a short file just yields a short head

	switch {
	case bytes.HasPrefix(head, bomUTF32LE), bytes.HasPrefix(head, bomUTF32BE):
		return "", "", errUTF32

	case bytes.HasPrefix(head, bomUTF16LE):
		raw, err := io.ReadAll(br)
		if err != nil {
			return "", "", err
		}
		text, err := decodeUTF16(raw[len(bomUTF16LE):], true)
		return text, encUTF16LE, err

	case bytes.HasPrefix(head, bomUTF16BE):
		raw, err := io.ReadAll(br)
		if err != nil {
			return "", "", err
		}
		text, err := decodeUTF16(raw[len(bomUTF16BE):], false)
		return text, encUTF16BE, err

	case bytes.HasPrefix(head, bomUTF8):
		if _, err := br.Discard(len(bomUTF8)); err != nil {
			return "", "", err
		}
		text, err := drainText(br)
		return text, encUTF8BOM, err
	}

	text, err = drainText(br)
	return text, encUTF8, err
}

// drainText copies r into a string, rejecting data that is not text. Without
// this check Gaze would open a binary blob and write it back mangled.
func drainText(r io.Reader) (string, error) {
	var sb strings.Builder
	if _, err := io.Copy(&sb, r); err != nil {
		return "", err
	}
	s := sb.String()
	if strings.IndexByte(s, 0) < 0 {
		return s, nil
	}
	if looksUTF16(s) {
		return "", errUTF16NoBOM
	}
	return "", errBinary
}

// looksUTF16 reports whether the NUL bytes in s sit overwhelmingly on one byte
// position, which is what BOM-less UTF-16 looks like when read as single bytes.
// It only ever refines an error message, so a conservative answer is fine.
func looksUTF16(s string) bool {
	if len(s) < 8 {
		return false
	}
	var even, odd int
	for i := 0; i < len(s); i++ {
		if s[i] != 0 {
			continue
		}
		if i%2 == 0 {
			even++
		} else {
			odd++
		}
	}
	if (even+odd)*4 < len(s) {
		return false // under 25% NULs: stray NULs in mostly-text data
	}
	// UTF-16LE puts the zero high byte at odd offsets, BE at even ones.
	return even*10 < odd || odd*10 < even
}

// decodeUTF16 converts UTF-16 code units to UTF-8 (surrogate pairs included),
// mapping unpaired surrogates to U+FFFD rather than failing the whole file.
func decodeUTF16(b []byte, littleEndian bool) (string, error) {
	if len(b)%2 != 0 {
		return "", errUTF16Trunc
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		if littleEndian {
			units[i] = binary.LittleEndian.Uint16(b[i*2:])
		} else {
			units[i] = binary.BigEndian.Uint16(b[i*2:])
		}
	}
	return string(utf16.Decode(units)), nil
}

// encodeDocument renders s in enc so that a saved file keeps its original
// encoding and byte-order mark. It builds the result in memory; the save paths
// use writeDocument, which streams instead.
func encodeDocument(s, enc string) []byte {
	var b bytes.Buffer
	b.Grow(len(s) + len(bomUTF8))
	// bytes.Buffer cannot fail, and the text already carries its final line
	// endings, so an LF convention leaves them untouched.
	_ = writeDocument(&b, s, eolLF, enc, false)
	return b.Bytes()
}

// writeDocument streams text to w in enc, translating \n to eol and, when trim is
// set, stripping spaces and tabs from the end of every line. Lines go through a
// small buffer, so saving never builds a second copy of the document.
func writeDocument(w io.Writer, text, eol, enc string, trim bool) error {
	bw := bufio.NewWriterSize(w, 64<<10)

	switch enc {
	case encUTF8BOM:
		if _, err := bw.Write(bomUTF8); err != nil {
			return err
		}
	case encUTF16LE:
		if _, err := bw.Write(bomUTF16LE); err != nil {
			return err
		}
	case encUTF16BE:
		if _, err := bw.Write(bomUTF16BE); err != nil {
			return err
		}
	}

	var err error
	switch enc {
	case encUTF16LE:
		err = writeLinesUTF16(bw, text, eol, true, trim)
	case encUTF16BE:
		err = writeLinesUTF16(bw, text, eol, false, trim)
	default:
		err = writeLinesUTF8(bw, text, eol, trim)
	}
	if err != nil {
		return err
	}
	return bw.Flush()
}

// eachLine calls fn once per line without splitting the text into a slice.
func eachLine(text string, fn func(line string, hasNewline bool) error) error {
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			return fn(text, false)
		}
		if err := fn(text[:i], true); err != nil {
			return err
		}
		text = text[i+1:]
	}
}

// eolBytes is the byte sequence that replaces \n for a file's convention.
func eolBytes(eol string) string {
	switch eol {
	case eolCRLF:
		return "\r\n"
	case eolCR:
		return "\r"
	}
	return "\n"
}

func writeLinesUTF8(w io.Writer, text, eol string, trim bool) error {
	sep := eolBytes(eol)
	return eachLine(text, func(line string, hasNewline bool) error {
		if trim {
			line = strings.TrimRight(line, " \t")
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
		if !hasNewline {
			return nil
		}
		_, err := io.WriteString(w, sep)
		return err
	})
}

func writeLinesUTF16(w io.Writer, text, eol string, littleEndian, trim bool) error {
	sep := eolBytes(eol)
	buf := make([]byte, 0, 4096)
	return eachLine(text, func(line string, hasNewline bool) error {
		if trim {
			line = strings.TrimRight(line, " \t")
		}
		var err error
		if buf, err = writeUTF16(w, line, littleEndian, buf); err != nil {
			return err
		}
		if !hasNewline {
			return nil
		}
		_, err = writeUTF16(w, sep, littleEndian, buf)
		return err
	})
}

// utf16Chunk is how much of a line is encoded before writing, so a single
// enormous line cannot grow the scratch buffer without bound.
const utf16Chunk = 2048

// writeUTF16 encodes s into scratch and writes it, in rune-aligned chunks.
func writeUTF16(w io.Writer, s string, littleEndian bool, scratch []byte) ([]byte, error) {
	for len(s) > 0 {
		n := len(s)
		if n > utf16Chunk {
			n = utf16Chunk
			for n > 0 && !utf8.RuneStart(s[n]) {
				n--
			}
			if n == 0 { // defensive: no boundary found, take the chunk as-is
				n = utf16Chunk
			}
		}
		scratch = appendUTF16(scratch[:0], s[:n], littleEndian)
		if _, err := w.Write(scratch); err != nil {
			return scratch, err
		}
		s = s[n:]
	}
	return scratch, nil
}

// appendUTF16 appends the UTF-16 encoding of s to dst, matching utf16.Encode:
// unpaired surrogates and out-of-range runes become U+FFFD.
//
// utf16.EncodeRune is deliberately not used for the BMP: what it returns for
// runes below U+10000 differs between Go releases, so the cases are spelled out
// here instead.
func appendUTF16(dst []byte, s string, littleEndian bool) []byte {
	for _, r := range s {
		switch {
		case r < 0xD800, r >= 0xE000 && r < 0x10000:
			dst = appendUTF16Unit(dst, uint16(r), littleEndian)
		case r >= 0x10000 && r <= 0x10FFFF:
			r1, r2 := utf16.EncodeRune(r)
			dst = appendUTF16Unit(dst, uint16(r1), littleEndian)
			dst = appendUTF16Unit(dst, uint16(r2), littleEndian)
		default:
			dst = appendUTF16Unit(dst, 0xFFFD, littleEndian)
		}
	}
	return dst
}

func appendUTF16Unit(dst []byte, u uint16, littleEndian bool) []byte {
	if littleEndian {
		return binary.LittleEndian.AppendUint16(dst, u)
	}
	return binary.BigEndian.AppendUint16(dst, u)
}
