package editor

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// autosaveInterval throttles recovery snapshots to at most one per interval.
const autosaveInterval = 30 * time.Second

// recoveryDir is the per-user cache location for crash-recovery snapshots.
func recoveryDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gaze", "recovery")
}

// recoveryPath maps a document path to a stable snapshot filename. The hash
// keeps names short and independent of the original path's length or characters.
func recoveryPath(path string) string {
	dir := recoveryDir()
	if dir == "" || path == "" {
		return ""
	}
	h := fnv.New64a()
	h.Write([]byte(path))
	return filepath.Join(dir, "r"+strconv.FormatUint(h.Sum64(), 16)+".gaze")
}

// maybeAutosave writes a recovery snapshot of the dirty buffer, rate-limited to
// autosaveInterval so it can be called on every keystroke.
func (e *Editor) maybeAutosave() {
	if !e.autosaveOn || !e.dirty || e.currentFile == "" {
		return
	}
	now := time.Now()
	if now.Sub(e.lastAutosave) < autosaveInterval {
		return
	}
	e.lastAutosave = now
	e.writeRecovery()
}

// writeRecovery stores the current buffer as a crash-recovery snapshot.
func (e *Editor) writeRecovery() {
	if e.currentFile == "" {
		return
	}
	p := recoveryPath(e.currentFile)
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = writeFileAtomic(p, []byte(e.entry.Text))
}

// recoveryFor returns a stored snapshot for path when it exists and is not
// older than the file on disk.
func recoveryFor(path string) (string, bool) {
	p := recoveryPath(path)
	if p == "" {
		return "", false
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	if target, err := os.Stat(path); err == nil && target.ModTime().After(fi.ModTime()) {
		return "", false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// clearRecovery discards the snapshot for path (called once changes are saved
// or deliberately discarded).
func clearRecovery(path string) {
	if p := recoveryPath(path); p != "" {
		os.Remove(p)
	}
}
