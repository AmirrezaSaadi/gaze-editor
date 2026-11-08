//go:build windows

package editor

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mapFile maps size bytes of f read-only, so the file's bytes live in the page
// cache instead of the heap.
func mapFile(f *os.File, size int64) ([]byte, error) {
	if size <= 0 {
		return nil, errMapEmpty
	}
	if int64(int(size)) != size {
		return nil, errMapTooBig
	}
	h, err := windows.CreateFileMapping(windows.Handle(f.Fd()), nil, windows.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	addr, err := windows.MapViewOfFile(h, windows.FILE_MAP_READ, 0, 0, uintptr(size))
	if err != nil {
		return nil, err
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(addr)), int(size)), nil
}

// unmapFile is a no-op: a Windows mapping is released when the process exits.
// Documents keep theirs anyway (see Document.region), and the one case that would
// release one — a mapped file that needs the normalising reader — is not worth an
// unsafe pointer round-trip through uintptr.
func unmapFile([]byte) error { return nil }
