//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris) && !windows

package editor

import "os"

// mapFile is not available here, so every file takes the streaming reader.
func mapFile(*os.File, int64) ([]byte, error) { return nil, errMapUnsupported }

func unmapFile([]byte) error { return nil }
