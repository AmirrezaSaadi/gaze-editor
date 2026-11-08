//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris

package editor

import (
	"os"

	"golang.org/x/sys/unix"
)

// mapFile maps size bytes of f read-only. The bytes come from the page cache
// rather than the heap, so a mapped gigabyte costs address space and no copy.
func mapFile(f *os.File, size int64) ([]byte, error) {
	if size <= 0 {
		return nil, errMapEmpty
	}
	if int64(int(size)) != size {
		return nil, errMapTooBig // 32-bit address space
	}
	b, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// unmapFile releases a mapping. Only a mapping nothing points into may be
// unmapped: a document keeps its mapping for the life of the process, because
// undo history and in-flight saves hold substrings of its pages (see
// Document.region). This is used for a mapped file that turned out to need the
// normalising reader instead.
func unmapFile(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return unix.Munmap(b)
}
