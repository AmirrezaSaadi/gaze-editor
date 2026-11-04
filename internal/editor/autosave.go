package editor

import (
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"fyne.io/fyne/v2/dialog"
)

// autosaveInterval throttles recovery snapshots to at most one per interval.
const autosaveInterval = 30 * time.Second

// largeAutosaveInterval is the same throttle for a large document. The snapshot
// itself is streamed from the page store, but writing tens of megabytes is still
// disk traffic worth spacing out.
const largeAutosaveInterval = 2 * time.Minute

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

// autosaveEvery is how often a dirty buffer may be snapshotted, which depends on
// how big it is.
func (e *Editor) autosaveEvery() time.Duration {
	if e.largeFile {
		return largeAutosaveInterval
	}
	return autosaveInterval
}

// autosaveDue reports whether enough time has passed for another snapshot.
func (e *Editor) autosaveDue(now time.Time) bool {
	return now.Sub(e.lastAutosave) >= e.autosaveEvery()
}

// maybeAutosave writes a recovery snapshot of the dirty buffer, rate-limited so
// it can be called on every keystroke. Untitled buffers are included: they have
// no other copy anywhere, so they need this most.
func (e *Editor) maybeAutosave() {
	if !e.autosaveOn || !e.dirty {
		return
	}
	now := time.Now()
	if !e.autosaveDue(now) {
		return
	}
	e.lastAutosave = now
	e.writeRecoveryAsync()
}

// recoveryFile is the snapshot path for the current buffer, with its directory
// created, or "" when snapshots are not possible.
func (e *Editor) recoveryFile() string {
	p := recoveryPath(e.recoveryKey())
	if p == "" {
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return ""
	}
	return p
}

// writeRecovery stores the current buffer as a crash-recovery snapshot, streaming
// it from the page store so no second copy of the document is built.
func (e *Editor) writeRecovery() {
	if e.doc == nil {
		return
	}
	p := e.recoveryFile()
	if p == "" {
		return
	}
	_ = writeFileAtomicWith(p, func(w io.Writer) error {
		_, err := e.doc.WriteTo(w)
		return err
	})
}

// writeRecoveryAsync snapshots the buffer off the UI goroutine: the temp file,
// fsync and rename in writeFileAtomic are fast but not free, and this runs while
// the user is typing.
func (e *Editor) writeRecoveryAsync() {
	if e.doc == nil {
		return
	}
	p := e.recoveryFile()
	if p == "" {
		return
	}
	// The write runs off the UI goroutine, so it gets its own view of the pages.
	doc := e.doc.Snapshot()
	go func() {
		_ = writeFileAtomicWith(p, func(w io.Writer) error {
			_, err := doc.WriteTo(w)
			return err
		})
	}()
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
