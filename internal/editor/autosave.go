package editor

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"fyne.io/fyne/v2/dialog"
)

// autosaveInterval throttles recovery snapshots to at most one per interval.
const autosaveInterval = 30 * time.Second

// draftKey is the recovery key for a buffer that has no file yet. A session
// edits one document at a time, so a single slot is enough to survive a crash.
const draftKey = "untitled"

// recoveryDir is the per-user cache location for crash-recovery snapshots. It
// is a variable so tests can redirect the snapshots away from the real cache.
var recoveryDir = func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gaze", "recovery")
}

// recoveryKey names the snapshot for the current buffer: the file path when
// there is one, the draft slot otherwise.
func (e *Editor) recoveryKey() string {
	if e.currentFile != "" {
		return e.currentFile
	}
	return draftKey
}

// recoveryPath maps a document key to a stable snapshot filename. The hash
// keeps names short and independent of the original path's length or
// characters.
func recoveryPath(key string) string {
	dir := recoveryDir()
	if dir == "" || key == "" {
		return ""
	}
	if key == draftKey {
		return filepath.Join(dir, "draft.gaze")
	}
	h := fnv.New64a()
	h.Write([]byte(key))
	return filepath.Join(dir, "r"+strconv.FormatUint(h.Sum64(), 16)+".gaze")
}

// maybeAutosave writes a recovery snapshot of the dirty buffer, rate-limited to
// autosaveInterval so it can be called on every keystroke. Untitled buffers are
// included: they have no other copy anywhere, so they need this most.
func (e *Editor) maybeAutosave() {
	if !e.autosaveOn || !e.dirty {
		return
	}
	now := time.Now()
	if now.Sub(e.lastAutosave) < autosaveInterval {
		return
	}
	e.lastAutosave = now
	e.writeRecoveryAsync()
}

// writeRecovery stores the current buffer as a crash-recovery snapshot.
func (e *Editor) writeRecovery() {
	p := recoveryPath(e.recoveryKey())
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = writeFileAtomic(p, []byte(e.entry.Text))
}

// writeRecoveryAsync snapshots the buffer off the UI goroutine: the temp file,
// fsync and rename in writeFileAtomic are fast but not free, and this runs while
// the user is typing.
func (e *Editor) writeRecoveryAsync() {
	p := recoveryPath(e.recoveryKey())
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	text := []byte(e.entry.Text)
	go func() { _ = writeFileAtomic(p, text) }()
}

// recoveryFor returns a stored snapshot for key when it exists and is not older
// than the file on disk.
func recoveryFor(key string) (string, bool) {
	p := recoveryPath(key)
	if p == "" {
		return "", false
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", false
	}
	// The draft has no file to compare against.
	if key != draftKey {
		if target, err := os.Stat(key); err == nil && target.ModTime().After(fi.ModTime()) {
			return "", false
		}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// clearRecovery discards the snapshot for key (called once changes are saved or
// deliberately discarded).
func clearRecovery(key string) {
	if p := recoveryPath(key); p != "" {
		os.Remove(p)
	}
}

// OfferDraftRecovery asks whether to restore an untitled buffer left behind by a
// crash. Such a buffer has no path, so it cannot come back through the session
// restore that reopens the last file.
func (e *Editor) OfferDraftRecovery() {
	text, ok := recoveryFor(draftKey)
	if !ok {
		return
	}
	dialog.ShowConfirm("Recover unsaved buffer?",
		"Gaze found an untitled buffer with unsaved changes from an earlier session.\nRestore it?",
		func(restore bool) {
			if !restore {
				clearRecovery(draftKey)
				e.setStatus("discarded recovered buffer")
				return
			}
			e.setContent(text, "", encUTF8)
			e.dirty = true
			e.refreshChrome()
			e.setStatus("recovered untitled buffer")
		}, e.win)
}
