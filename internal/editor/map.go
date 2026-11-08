package editor

import (
	"bytes"
	"errors"
	"os"
)

// Opening a file that is large enough for the read to be worth avoiding maps it
// instead: the pages of the document then point into the mapping, so the file's
// bytes never enter the heap and only the parts being looked at are in memory.
//
// A mapping cannot be rewritten in place (the editor never writes to a page) and
// cannot be normalised on the way in, so it is used only for files that are
// already plain LF-terminated UTF-8. Anything else — CRLF, a BOM, UTF-16, a
// binary blob — goes to the streaming reader, which copies and normalises as it
// goes and can name what is wrong with the file.
const mapMinBytes = 8 << 20

var (
	errMapEmpty       = errors.New("gaze: file is empty")
	errMapTooBig      = errors.New("gaze: file is too big to map")
	errMapUnsupported = errors.New("gaze: memory mapping is not supported here")
)

// mapPath maps path read-only. The descriptor is closed straight away: the
// mapping stays valid on its own.
func mapPath(path string, size int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return mapFile(f, size)
}

var (
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}
	utf16LE = []byte{0xFF, 0xFE}
	utf16BE = []byte{0xFE, 0xFF}
)

// mappable reports whether the mapped bytes can be used as they stand. It walks
// the file with the byte scanners rather than a Go loop: this is the same pass
// that decides how the file is opened, and on a gigabyte it is a few hundred
// milliseconds either way.
func mappable(b []byte) bool {
	switch {
	case bytes.HasPrefix(b, utf8BOM), bytes.HasPrefix(b, utf16LE), bytes.HasPrefix(b, utf16BE):
		return false // the streaming reader strips the mark
	case bytes.IndexByte(b, '\r') >= 0:
		return false // CRLF or a lone CR: normalised while copying
	case bytes.IndexByte(b, 0) >= 0:
		return false // binary, or UTF-16 without a mark, which the reader names
	}
	return true
}
