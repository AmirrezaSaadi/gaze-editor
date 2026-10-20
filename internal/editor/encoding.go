package editor

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
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
// encoding and byte-order mark.
func encodeDocument(s, enc string) []byte {
	switch enc {
	case encUTF8BOM:
		out := make([]byte, 0, len(bomUTF8)+len(s))
		out = append(out, bomUTF8...)
		return append(out, s...)
	case encUTF16LE:
		return encodeUTF16(s, true)
	case encUTF16BE:
		return encodeUTF16(s, false)
	default:
		return []byte(s)
	}
}

func encodeUTF16(s string, littleEndian bool) []byte {
	units := utf16.Encode([]rune(s))
	bom := bomUTF16BE
	if littleEndian {
		bom = bomUTF16LE
	}
	out := make([]byte, len(bom)+len(units)*2)
	copy(out, bom)
	body := out[len(bom):]
	for i, u := range units {
		if littleEndian {
			binary.LittleEndian.PutUint16(body[i*2:], u)
		} else {
			binary.BigEndian.PutUint16(body[i*2:], u)
		}
	}
	return out
}
