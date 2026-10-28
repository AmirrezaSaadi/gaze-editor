package editor

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

const (
	prefRecent = "recent"
	prefWinW   = "winW"
	prefWinH   = "winH"

	prefLastFile = "lastFile"
	prefLastRow  = "lastRow"
	prefLastCol  = "lastCol"

	maxRecent = 10
)

// SetContent loads text into the editor as the file at path ("" for untitled),
// assuming UTF-8. Line endings are normalised to \n (remembering the original
// convention) and the dirty flag is cleared with the change callback suppressed
// while loading.
func (e *Editor) SetContent(text, path string) {
	e.setContent(text, path, encUTF8)
}

// setContent is SetContent with the document's encoding made explicit, so that
// a file opened as UTF-16 is written back as UTF-16 rather than converted.
func (e *Editor) setContent(text, path, enc string) {
	text, eol := normalizeEOL(text)
	e.eol = eol
	e.enc = enc
	if e.enc == "" {
		e.enc = encUTF8
	}
	e.indentUseTabs, e.indentWidth = detectIndent(text)

	e.loading = true
	e.entry.SetText(text)
	e.loading = false
	e.resetUndo()

	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.entry.Refresh()
	e.currentFile = path
	e.dirty = false
	e.refreshChrome()
	freeMemory(len(text))
}

// confirmDiscard runs proceed immediately, or first asks the user to confirm
// discarding unsaved changes.
func (e *Editor) confirmDiscard(proceed func()) {
	if !e.dirty {
		proceed()
		return
	}
	dialog.ShowConfirm("Unsaved changes",
		"The current file has unsaved changes.\nDiscard them and continue?",
		func(ok bool) {
			if !ok {
				return
			}
			e.discardChanges()
			proceed()
		}, e.win)
}

// discardChanges drops the recovery snapshot belonging to the buffer that is
// about to be abandoned. Without this the snapshot outlives the discard and the
// next open of the same file offers the thrown-away edits back.
func (e *Editor) discardChanges() {
	clearRecovery(e.recoveryKey())
}

// NewFile clears the buffer and detaches the backing path.
func (e *Editor) NewFile() {
	e.confirmDiscard(func() {
		e.SetContent("", "")
		e.setStatus("new file — unsaved")
	})
}

// OpenFile shows the native open dialog and loads the chosen file.
func (e *Editor) OpenFile() {
	e.confirmDiscard(func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, e.win)
				return
			}
			if r == nil {
				return // cancelled
			}
			defer r.Close()

			text, enc, err := readDocument(r)
			if err != nil {
				dialog.ShowError(err, e.win)
				return
			}
			e.loadContent(r.URI().Path(), text, enc)
		}, e.win)
		d.Show()
	})
}

// OpenRecent loads a file previously opened from the recent-files list.
func (e *Editor) OpenRecent(path string) {
	e.confirmDiscard(func() { e.OpenPath(path) })
}

// OpenPath loads path directly, reporting any error in the UI.
func (e *Editor) OpenPath(path string) {
	f, err := os.Open(path)
	if err != nil {
		dialog.ShowError(err, e.win)
		e.setStatus("open failed")
		return
	}
	defer f.Close()

	text, enc, err := readDocument(f)
	if err != nil {
		dialog.ShowError(err, e.win)
		e.setStatus("open failed")
		return
	}
	e.loadContent(path, text, enc)
}

// OpenPathAt loads path and, when line > 0, places the caret at line:col. Both
// numbers are 1-based, matching the file:line:col form on the command line, so
// column 0 and column 1 both mean the start of the line.
func (e *Editor) OpenPathAt(path string, line, col int) {
	e.OpenPath(path)
	if line <= 0 {
		return
	}
	if col > 0 {
		col--
	}
	e.gotoLineCol(line, col)
}

// loadContent installs file text, offering a crash-recovery snapshot first when
// one exists and is at least as new as the file on disk.
func (e *Editor) loadContent(path, text, enc string) {
	e.recordDiskState(path)
	if rec, ok := recoveryFor(path); ok {
		name := filepath.Base(path)
		dialog.ShowConfirm("Recover unsaved changes?",
			"Gaze found unsaved changes for "+name+" from an earlier session.\nRestore them?",
			func(restore bool) {
				if restore {
					e.setContent(rec, path, enc)
					e.dirty = true
					e.refreshChrome()
					e.setStatus("recovered " + path)
				} else {
					clearRecovery(path)
					e.setContent(text, path, enc)
					e.setStatus("opened " + path)
				}
				e.addRecent(path)
			}, e.win)
		return
	}
	clearRecovery(path)
	e.setContent(text, path, enc)
	e.addRecent(path)
	e.setStatus("opened " + path)
}

// SaveFile writes back to the current path, or falls back to Save As. The write
// itself happens off the UI goroutine, so a slow disk or network mount cannot
// freeze the window.
func (e *Editor) SaveFile() {
	if e.saving {
		e.setStatus("save already in progress")
		return
	}
	if e.currentFile == "" {
		e.SaveAsFile()
		return
	}
	path := e.currentFile
	if e.externalChange(path) {
		dialog.ShowConfirm("File changed on disk",
			filepath.Base(path)+" was changed by another program after Gaze opened it.\nOverwrite it with this buffer?",
			func(overwrite bool) {
				if !overwrite {
					e.setStatus("save cancelled — file changed on disk")
					return
				}
				e.recordDiskState(path) // accept the change so we do not ask twice
				e.saveAsync(path)
			}, e.win)
		return
	}
	e.saveAsync(path)
}

// saveRequest captures everything a save needs, so the file write can happen on
// a background goroutine without reading widgets concurrently with editing.
type saveRequest struct {
	path    string
	text    string
	eol     string
	enc     string
	trim    bool
	prevKey string // recovery key the buffer had before the save
}

// pendingSave snapshots the current buffer and document settings.
func (e *Editor) pendingSave(path string) saveRequest {
	return saveRequest{
		path:    path,
		text:    e.entry.Text,
		eol:     e.eol,
		enc:     e.enc,
		trim:    e.trimOnSave,
		prevKey: e.recoveryKey(),
	}
}

// render produces the bytes for disk and the \n-only buffer they came from:
// trailing whitespace is stripped when the option is on and line endings are
// converted back to the file's style.
func (r saveRequest) render() (out, trimmed string) {
	trimmed = r.text
	if r.trim {
		trimmed = trimTrailingSpace(trimmed)
	}
	out = trimmed
	switch r.eol {
	case eolCRLF:
		out = strings.ReplaceAll(out, "\n", "\r\n")
	case eolCR:
		out = strings.ReplaceAll(out, "\n", "\r")
	}
	return out, trimmed
}

// write renders and writes the request atomically, returning the buffer that
// was actually written.
func (r saveRequest) write() (trimmed string, err error) {
	out, trimmed := r.render()
	if err := writeFileAtomic(r.path, encodeDocument(out, r.enc)); err != nil {
		return "", err
	}
	return trimmed, nil
}

// writeTo writes the buffer to path synchronously, reporting errors in the UI.
// It is kept synchronous for callers that need the file on disk immediately;
// the interactive save paths use saveAsync.
func (e *Editor) writeTo(path string) {
	req := e.pendingSave(path)
	trimmed, err := req.write()
	if err != nil {
		dialog.ShowError(err, e.win)
		e.setStatus("save failed")
		return
	}
	e.finishSave(req, trimmed)
}

// saveAsync writes the buffer on a background goroutine and applies the result
// on the UI goroutine.
func (e *Editor) saveAsync(path string) {
	req := e.pendingSave(path)
	e.saving = true
	e.setStatus("saving " + path)
	go func() {
		trimmed, err := req.write()
		fyne.Do(func() {
			e.saving = false
			if err != nil {
				dialog.ShowError(err, e.win)
				e.setStatus("save failed")
				return
			}
			e.finishSave(req, trimmed)
		})
	}()
}

// finishSave installs the results of a successful write. Edits made while the
// write was in flight are preserved and leave the document dirty.
func (e *Editor) finishSave(req saveRequest, trimmed string) {
	sameBuffer := e.entry.Text == req.text
	if sameBuffer {
		e.adoptTrimmed(trimmed)
	}
	e.currentFile = req.path
	e.dirty = e.entry.Text != trimmed
	e.recordDiskState(req.path)
	e.refreshChrome()
	e.addRecent(req.path)
	e.saveSession(req.path)
	clearRecovery(req.path)
	if sameBuffer {
		// The buffer is what was written, so any older snapshot (a draft or a
		// previous session's) is obsolete. If the buffer moved on while the
		// write was running, its snapshot still holds recoverable edits.
		clearRecovery(req.prevKey)
	}
	e.setStatus("saved " + req.path)
	freeMemory(len(trimmed))
}

// serialize renders the buffer for disk. It also returns the trimmed \n-only
// buffer so callers can keep the editor in sync with what was written.
func (e *Editor) serialize() (out, trimmed string) {
	return e.pendingSave(e.currentFile).render()
}

// adoptTrimmed mirrors a save-time trim back into the buffer without marking
// the document dirty. The pre-trim buffer is snapshotted so the trim can still
// be undone.
func (e *Editor) adoptTrimmed(trimmed string) {
	if trimmed == e.entry.Text {
		return
	}
	e.pushUndo()
	e.loading = true
	e.entry.SetText(trimmed)
	e.loading = false
	e.updateStats()
	e.updateInfo()
}

// recordDiskState remembers the on-disk identity of path so a later save can
// tell whether another program changed the file underneath us.
func (e *Editor) recordDiskState(path string) {
	e.diskPath, e.diskMod, e.diskSize, e.diskKnown = "", time.Time{}, 0, false
	if path == "" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	e.diskPath, e.diskMod, e.diskSize, e.diskKnown = path, fi.ModTime(), fi.Size(), true
}

// externalChange reports whether path was modified by someone else since Gaze
// last read or wrote it. An unknown state (a new file) is never a conflict.
func (e *Editor) externalChange(path string) bool {
	if path == "" || !e.diskKnown || e.diskPath != path {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false // gone: let the save recreate it
	}
	return !fi.ModTime().Equal(e.diskMod) || fi.Size() != e.diskSize
}

// writeFileAtomic writes data to path via a same-directory temp file,
// preserving the target's permissions and resolving symlinks so the link itself
// survives.
func writeFileAtomic(path string, data []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}

	mode := os.FileMode(0o644)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}

	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, ".gaze-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	// Best-effort directory sync so the rename itself is durable.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// SaveAsFile shows the native save dialog and re-targets the buffer.
func (e *Editor) SaveAsFile() {
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, e.win)
			return
		}
		if wc == nil {
			return // cancelled
		}

		path := wc.URI().Path()
		req := e.pendingSave(path)
		e.saving = true
		e.setStatus("saving " + path)
		go func() {
			out, trimmed := req.render()
			_, werr := wc.Write(encodeDocument(out, req.enc))
			cerr := wc.Close()
			fyne.Do(func() {
				e.saving = false
				if werr != nil {
					dialog.ShowError(werr, e.win)
					e.setStatus("save failed")
					return
				}
				if cerr != nil {
					dialog.ShowError(cerr, e.win)
					e.setStatus("save failed")
					return
				}
				e.finishSave(req, trimmed)
			})
		}()
	}, e.win)

	d.SetFileName(e.defaultFileName())
	d.Show()
}

// defaultFileName suggests a sensible name in the save dialog.
func (e *Editor) defaultFileName() string {
	if e.currentFile != "" {
		return filepath.Base(e.currentFile)
	}
	return "untitled.txt"
}

// recentFiles returns the persisted recent-file list.
func (e *Editor) recentFiles() []string {
	return e.app.Preferences().StringListWithFallback(prefRecent, nil)
}

// addRecent promotes path to the front of the recent list and persists it.
func (e *Editor) addRecent(path string) {
	if path == "" {
		return
	}
	list := e.recentFiles()
	out := []string{path}
	for _, p := range list {
		if p != path && len(out) < maxRecent {
			out = append(out, p)
		}
	}
	e.app.Preferences().SetStringList(prefRecent, out)
	e.refreshRecentMenu()
}

// ClearRecent empties the recent-files list.
func (e *Editor) ClearRecent() {
	e.app.Preferences().SetStringList(prefRecent, nil)
	e.refreshRecentMenu()
}

// refreshRecentMenu rebuilds the Open Recent submenu.
func (e *Editor) refreshRecentMenu() {
	if e.recentMenu == nil {
		return
	}
	e.recentMenu.Items = nil

	paths := e.recentFiles()
	if len(paths) == 0 {
		empty := fyne.NewMenuItem("No recent files", nil)
		empty.Disabled = true
		e.recentMenu.Items = append(e.recentMenu.Items, empty)
	} else {
		for _, p := range paths {
			p := p
			e.recentMenu.Items = append(e.recentMenu.Items,
				fyne.NewMenuItem(p, func() { e.OpenRecent(p) }))
		}
		e.recentMenu.Items = append(e.recentMenu.Items,
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Clear Recent", e.ClearRecent))
	}

	if e.win.MainMenu() != nil {
		e.win.MainMenu().Refresh()
	}
}

// saveSession records the file and caret for the next launch.
func (e *Editor) saveSession(path string) {
	if path == "" {
		return
	}
	p := e.app.Preferences()
	p.SetString(prefLastFile, path)
	p.SetInt(prefLastRow, e.entry.CursorRow)
	p.SetInt(prefLastCol, e.entry.CursorColumn)
}

// RestoreSession reopens the file from the previous session (if it still
// exists) and returns to where the caret was. It is a no-op when there is
// nothing to restore.
// RestoreSession reopens the file from the previous session (if it still
// exists) and returns to where the caret was. It reports whether a document was
// actually restored.
func (e *Editor) RestoreSession() bool {
	path := e.app.Preferences().StringWithFallback(prefLastFile, "")
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	e.OpenPath(path)
	if e.currentFile != path {
		return false
	}
	row := e.app.Preferences().IntWithFallback(prefLastRow, 0)
	col := e.app.Preferences().IntWithFallback(prefLastCol, 0)
	if row > 0 || col > 0 {
		e.gotoLineCol(row+1, col)
	}
	return true
}

// onDropped opens the first file dropped onto the window.
func (e *Editor) onDropped(_ fyne.Position, uris []fyne.URI) {
	if len(uris) == 0 {
		return
	}
	path := uris[0].Path()
	if path == "" {
		return
	}
	e.OpenRecent(path)
}

// Quit persists the session and window geometry, then closes the app, prompting
// first if there are unsaved changes.
func (e *Editor) Quit() {
	e.confirmDiscard(func() {
		e.saveSession(e.currentFile)
		e.saveGeometry()
		e.stopTimers()
		e.app.Quit()
	})
}

func (e *Editor) saveGeometry() {
	if e.win == nil || e.win.Content() == nil {
		return
	}
	size := e.win.Content().Size()
	p := e.app.Preferences()
	p.SetFloat(prefWinW, float64(size.Width))
	p.SetFloat(prefWinH, float64(size.Height))
}

// freeMemoryThreshold is the buffer size above which a save or load is followed
// by a hand-back of free heap. debug.FreeOSMemory stops the world, so running it
// after every small file would cost more than it saves.
const freeMemoryThreshold = 8 << 20

// freeMemory returns freed heap back to the OS after a large file operation so
// the resident set does not creep up as files are opened and saved.
func freeMemory(n int) {
	if n < freeMemoryThreshold {
		return
	}
	debug.FreeOSMemory()
}
