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
// File opens do not use this: they stream straight into a Document. This is for
// the paths that work on a string (standard input, tests).
func readDocument(r io.Reader) (text, enc string, err error) {
	enc, body, err := sniffEncoding(bufio.NewReader(r))
	if err != nil {
		return "", "", err
	}
	text, err = drainText(body)
	if err != nil {
		return "", "", err
	}
	return text, enc, nil
}

// sniffEncoding consumes the byte-order mark, if any, and returns the encoding
// name plus a reader that yields UTF-8 text. UTF-16 is converted on the fly, so
// even a large UTF-16 file is never held as one byte slice.
func sniffEncoding(br *bufio.Reader) (enc string, r io.Reader, err error) {
	head, _ := br.Peek(4) // a short file just yields a short head
	switch {
	case bytes.HasPrefix(head, bomUTF32LE), bytes.HasPrefix(head, bomUTF32BE):
		return "", nil, errUTF32
	case bytes.HasPrefix(head, bomUTF16LE):
		if _, err := br.Discard(len(bomUTF16LE)); err != nil {
			return "", nil, err
		}
		return encUTF16LE, &utf16Reader{src: br, little: true}, nil
	case bytes.HasPrefix(head, bomUTF16BE):
		if _, err := br.Discard(len(bomUTF16BE)); err != nil {
			return "", nil, err
		}
		return encUTF16BE, &utf16Reader{src: br, little: false}, nil
	case bytes.HasPrefix(head, bomUTF8):
		if _, err := br.Discard(len(bomUTF8)); err != nil {
			return "", nil, err
		}
		return encUTF8BOM, br, nil
	}
	return encUTF8, br, nil
}

// openDocument streams r into a Document, normalising line endings and rejecting
// data that is not text. It is how a file is opened: the text is never built as
// one string, so opening costs the size of the file plus its line index rather
// than twice the file and a doubling buffer.
func openDocument(r io.Reader) (*Document, string, string, error) {
	enc, body, err := sniffEncoding(bufio.NewReaderSize(r, 64<<10))
	if err != nil {
		return nil, "", "", err
	}
	b := newDocBuilder()
	if _, err := io.Copy(b, body); err != nil {
		return nil, "", "", err
	}
	doc, err := b.finish()
	if err != nil {
		return nil, "", "", err
	}
	return doc, b.eolStyle(), enc, nil
}

// utf16Reader converts a UTF-16 byte stream to UTF-8 as it is read. Unpaired
// surrogates become U+FFFD, matching decodeUTF16, and a stray final byte is
// reported as truncated data.
type utf16Reader struct {
	src    io.Reader
	little bool

	raw  []byte // bytes read but not yet decoded (at most one code unit)
	buf  []byte // decoded UTF-8 not yet handed out
	high uint16 // high surrogate waiting for its partner
	done bool
	err  error
}

func (r *utf16Reader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 && !r.done {
		r.more()
	}
	if len(r.buf) == 0 {
		if r.err == nil {
			r.err = io.EOF
		}
		return 0, r.err
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// more reads another chunk and decodes the whole code units in it.
func (r *utf16Reader) more() {
	var chunk [32 << 10]byte
	n, err := r.src.Read(chunk[:])
	if n > 0 {
		r.raw = append(r.raw, chunk[:n]...)
		r.decode()
	}
	if err == io.EOF {
		r.done = true
		if r.high != 0 {
			r.buf = utf8.AppendRune(r.buf, utf8.RuneError) // unpaired surrogate
			r.high = 0
		}
		if len(r.raw) > 0 {
			r.err = errUTF16Trunc
		}
		return
	}
	if err != nil {
		r.done, r.err = true, err
	}
}

// decode turns the buffered code units into UTF-8, holding back a trailing high
// surrogate so it can be paired with the first unit of the next chunk.
func (r *utf16Reader) decode() {
	units := len(r.raw) / 2
	if units == 0 {
		return
	}
	us := make([]uint16, 0, units+1)
	if r.high != 0 {
		us = append(us, r.high)
		r.high = 0
	}
	for i := 0; i < units; i++ {
		if r.little {
			us = append(us, binary.LittleEndian.Uint16(r.raw[i*2:]))
		} else {
			us = append(us, binary.BigEndian.Uint16(r.raw[i*2:]))
		}
	}
	r.raw = append(r.raw[:0], r.raw[units*2:]...)
	if n := len(us); n > 0 && us[n-1] >= 0xD800 && us[n-1] <= 0xDBFF {
		r.high = us[n-1]
		us = us[:n-1]
	}
	for _, ru := range utf16.Decode(us) {
		r.buf = utf8.AppendRune(r.buf, ru)
	}
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
	return looksUTF16Counts(len(s), even, odd)
}

// looksUTF16Counts is the same judgement from a running count of NUL bytes, for
// the streaming reader that cannot look at the whole buffer.
func looksUTF16Counts(size, even, odd int) bool {
	if size < 8 {
		return false
	}
	if (even+odd)*4 < size {
		return false // under 25% NULs: stray NULs in mostly-text data
	}
	// UTF-16LE puts the zero high byte at odd offsets, BE at even ones.
	return even*10 < odd || odd*10 < even
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

// lineSource calls fn once per line of a document. It lets a save stream either
// from a string or from the pages of a large document.
type lineSource func(fn func(line string, hasNewline bool) error) error

// textLines adapts a string to a lineSource.
func textLines(text string) lineSource {
	return func(fn func(line string, hasNewline bool) error) error {
		return eachLine(text, fn)
	}
}

// writeDocument streams text to w in enc, translating \n to eol and, when trim is
// set, stripping spaces and tabs from the end of every line. Lines go through a
// small buffer, so saving never builds a second copy of the document.
func writeDocument(w io.Writer, text, eol, enc string, trim bool) error {
	return writeDocumentLines(w, textLines(text), eol, enc, trim)
}

// writeDocumentLines is writeDocument over an arbitrary line source, which is how
// a large document is saved without materialising it.
func writeDocumentLines(w io.Writer, src lineSource, eol, enc string, trim bool) error {
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
		err = writeLinesUTF16(bw, src, eol, true, trim)
	case encUTF16BE:
		err = writeLinesUTF16(bw, src, eol, false, trim)
	default:
		err = writeLinesUTF8(bw, src, eol, trim)
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

func writeLinesUTF8(w io.Writer, src lineSource, eol string, trim bool) error {
	sep := eolBytes(eol)
	return src(func(line string, hasNewline bool) error {
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

func writeLinesUTF16(w io.Writer, src lineSource, eol string, littleEndian, trim bool) error {
	sep := eolBytes(eol)
	buf := make([]byte, 0, 4096)
	return src(func(line string, hasNewline bool) error {
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
