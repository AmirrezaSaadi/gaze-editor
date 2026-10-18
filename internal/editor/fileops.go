package editor

import (
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

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

// SetContent loads text into the editor as the file at path ("" for untitled).
// Line endings are normalised to \n (remembering the original convention) and
// the dirty flag is cleared with the change callback suppressed while loading.
func (e *Editor) SetContent(text, path string) {
	text, eol := normalizeEOL(text)
	e.eol = eol
	e.indentUseTabs, e.indentWidth = detectIndent(text)

	e.loading = true
	e.entry.SetText(text)
	e.loading = false

	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.entry.Refresh()
	e.currentFile = path
	e.dirty = false
	e.refreshChrome()
	freeMemory()
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
			if ok {
				proceed()
			}
		}, e.win)
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

			data, err := readText(r)
			if err != nil {
				dialog.ShowError(err, e.win)
				return
			}
			e.loadContent(r.URI().Path(), data)
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

	text, err := readText(f)
	if err != nil {
		dialog.ShowError(err, e.win)
		e.setStatus("open failed")
		return
	}
	e.loadContent(path, text)
}

// OpenPathAt loads path and, when line > 0, places the caret at line:col.
func (e *Editor) OpenPathAt(path string, line, col int) {
	e.OpenPath(path)
	if line > 0 {
		e.gotoLineCol(line, col)
	}
}

// loadContent installs file text, offering a crash-recovery snapshot first when
// one exists and is at least as new as the file on disk.
func (e *Editor) loadContent(path, text string) {
	if rec, ok := recoveryFor(path); ok {
		name := filepath.Base(path)
		dialog.ShowConfirm("Recover unsaved changes?",
			"Gaze found unsaved changes for "+name+" from an earlier session.\nRestore them?",
			func(restore bool) {
				if restore {
					e.SetContent(rec, path)
					e.dirty = true
					e.refreshChrome()
					e.setStatus("recovered " + path)
				} else {
					clearRecovery(path)
					e.SetContent(text, path)
					e.setStatus("opened " + path)
				}
				e.addRecent(path)
			}, e.win)
		return
	}
	clearRecovery(path)
	e.SetContent(text, path)
	e.addRecent(path)
	e.setStatus("opened " + path)
}

// readText drains r into a string with as few copies as possible: a
// strings.Builder grows in place and String() does not re-copy on modern Go.
func readText(r io.Reader) (string, error) {
	var sb strings.Builder
	if _, err := io.Copy(&sb, r); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// SaveFile writes back to the current path, or falls back to Save As.
func (e *Editor) SaveFile() {
	if e.currentFile == "" {
		e.SaveAsFile()
		return
	}
	e.writeTo(e.currentFile)
}

// writeTo trims (when enabled), restores the file's line-ending convention and
// writes atomically: a temp file in the same directory is fsynced and renamed
// over the target so a crash cannot truncate the original.
func (e *Editor) writeTo(path string) {
	out, trimmed := e.serialize()
	if err := writeFileAtomic(path, out); err != nil {
		dialog.ShowError(err, e.win)
		e.setStatus("save failed")
		return
	}
	e.adoptTrimmed(trimmed)

	e.currentFile = path
	e.dirty = false
	e.refreshChrome()
	e.addRecent(path)
	e.saveSession(path)
	clearRecovery(path)
	e.setStatus("saved " + path)
	freeMemory()
}

// serialize renders the buffer for disk: trailing whitespace is stripped when
// the option is on and line endings are converted back to the file's style. It
// also returns the trimmed \n-only buffer so callers can keep the editor in
// sync with what was written.
func (e *Editor) serialize() (out, trimmed string) {
	trimmed = e.entry.Text
	if e.trimOnSave {
		trimmed = trimTrailingSpace(trimmed)
	}
	out = trimmed
	switch e.eol {
	case eolCRLF:
		out = strings.ReplaceAll(out, "\n", "\r\n")
	case eolCR:
		out = strings.ReplaceAll(out, "\n", "\r")
	}
	return out, trimmed
}

// adoptTrimmed mirrors a save-time trim back into the buffer without marking
// the document dirty.
func (e *Editor) adoptTrimmed(trimmed string) {
	if trimmed == e.entry.Text {
		return
	}
	e.loading = true
	e.entry.SetText(trimmed)
	e.loading = false
	e.updateStats()
	e.updateInfo()
}

// writeFileAtomic writes s to path via a same-directory temp file, preserving
// the target's permissions and resolving symlinks so the link itself survives.
func writeFileAtomic(path, s string) error {
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
	if _, err := io.WriteString(f, s); err != nil {
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

		out, trimmed := e.serialize()
		if _, err := io.WriteString(wc, out); err != nil {
			wc.Close()
			dialog.ShowError(err, e.win)
			return
		}
		wc.Close()
		e.adoptTrimmed(trimmed)

		e.currentFile = wc.URI().Path()
		e.dirty = false
		e.refreshChrome()
		e.addRecent(e.currentFile)
		e.saveSession(e.currentFile)
		clearRecovery(e.currentFile)
		e.setStatus("saved " + e.currentFile)
		freeMemory()
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
func (e *Editor) RestoreSession() {
	path := e.app.Preferences().StringWithFallback(prefLastFile, "")
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	e.OpenPath(path)
	row := e.app.Preferences().IntWithFallback(prefLastRow, 0)
	col := e.app.Preferences().IntWithFallback(prefLastCol, 0)
	if row > 0 || col > 0 {
		e.gotoLineCol(row+1, col)
	}
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

// freeMemory returns freed heap back to the OS after large file operations so
// the resident set does not creep up as files are opened and saved.
func freeMemory() { debug.FreeOSMemory() }
