package editor

import (
	"errors"
	"fmt"
	"io"
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

// Size limits for opening a file. Gaze keeps the whole document in a page store
// and draws only the lines on screen, so a big file no longer crashes the window
// — but the chrome around it (word counts, undo, recovery) still scans the whole
// buffer, so past a certain size it is shed instead. They are variables so tests
// can move them.
var (
	// LargeFileBytes is where opening asks first and switches to large-file mode.
	LargeFileBytes int64 = 16 << 20
	// MaxOpenBytes is the hard limit; anything larger is refused. Opening a file
	// costs its own size in page storage plus eight bytes per line, so this is
	// about what a machine with a few gigabytes free can take, not about what
	// the renderer can draw (which is always one screenful).
	MaxOpenBytes int64 = 1 << 30
)

// openMode records how a document was opened.
type openMode int

const (
	openNormal openMode = iota
	openLarge
)

// large reports whether the document is in large-file mode.
func (m openMode) large() bool { return m == openLarge }

// errTooLarge is returned when a file grows past MaxOpenBytes while it is being
// read. Truncating the buffer instead would hand the user something that looks
// whole and then saves back short.
var errTooLarge = errors.New("file is larger than the open size limit")

// limitReader fails once more than limit bytes have been read, rather than
// silently truncating the way io.LimitReader would.
type limitReader struct {
	r     io.Reader
	n     int64
	limit int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.n > l.limit {
		return 0, errTooLarge
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.limit {
		return 0, errTooLarge
	}
	return n, err
}

// fileSize reports the size of path, for the open size gate.
func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// humanBytes renders a byte count the way a person reads it.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// confirmOpenSize decides whether a file of size bytes may be opened, asking the
// user first when it is merely large. proceed may run asynchronously, once the
// user has answered.
func (e *Editor) confirmOpenSize(name string, size int64, proceed func(openMode)) {
	switch {
	case size >= MaxOpenBytes:
		e.reportTooLarge(name, size)
	case size >= LargeFileBytes:
		dialog.ShowConfirm("Open large file?",
			fmt.Sprintf("%s is %s.\n\nGaze can open it in large-file mode: the text is kept in 64 KB pages and only the lines on screen are drawn, so editing stays fast. Opening it will hold about %s of memory and take a few seconds. Continue?",
				name, humanBytes(size), humanBytes(size)),
			func(ok bool) {
				if !ok {
					e.setStatus("open cancelled — " + name)
					return
				}
				proceed(openLarge)
			}, e.win)
	default:
		proceed(openNormal)
	}
}

// reportTooLarge explains a refusal and why the limit exists. A size of zero
// means "at least the limit, exact size unknown".
func (e *Editor) reportTooLarge(name string, size int64) {
	detail := fmt.Sprintf("%s is over Gaze's %s limit.", name, humanBytes(MaxOpenBytes))
	if size > 0 {
		detail = fmt.Sprintf("%s is %s, over Gaze's %s limit.", name, humanBytes(size), humanBytes(MaxOpenBytes))
	}
	dialog.ShowInformation("File too large",
		detail+"\n\nGaze holds the whole document in memory (about one and a half times its size once the line index is counted), so this is where it stops. A tool built for files this big (less, grep, or an editor that memory-maps them) is a better fit.",
		e.win)
	e.setStatus("refused " + name + " — too large (over " + humanBytes(MaxOpenBytes) + ")")
}

// reportOpenError surfaces a failed open, naming the size limit when that is what
// went wrong.
func (e *Editor) reportOpenError(err error, name string) {
	if errors.Is(err, errTooLarge) {
		e.reportTooLarge(name, 0)
		return
	}
	dialog.ShowError(err, e.win)
	e.setStatus("open failed")
}

// SetContent loads text into the editor as the file at path ("" for untitled),
// assuming UTF-8. Line endings are normalised to \n (remembering the original
// convention) and the dirty flag is cleared with the change callback suppressed
// while loading.
func (e *Editor) SetContent(text, path string) {
	e.setContent(text, path, encUTF8)
}

// SetContentLarge loads text as a large-file buffer: the chrome that rescans or
// copies the document (live counts, the save-time trim, a long undo history) is
// switched off.
func (e *Editor) SetContentLarge(text, path string) {
	e.setContentAs(text, path, encUTF8, openLarge)
}

// setContent is SetContent with the document's encoding made explicit, so that
// a file opened as UTF-16 is written back as UTF-16 rather than converted.
func (e *Editor) setContent(text, path, enc string) {
	e.setContentAs(text, path, enc, openNormal)
}

// setContentAs installs a string as the buffer and applies the open mode it was
// approved for.
func (e *Editor) setContentAs(text, path, enc string, mode openMode) {
	text, eol := normalizeEOL(text)
	e.setDocumentAs(NewDocument(text), path, eol, enc, mode)
}

// setDocumentAs installs an already-built document, which is how a file that was
// streamed in (rather than read into a string) reaches the editor.
func (e *Editor) setDocumentAs(doc *Document, path, eol, enc string, mode openMode) {
	e.eol = eol
	e.enc = enc
	if e.enc == "" {
		e.enc = encUTF8
	}
	// The indent style is settled by the first indented line, so only the head of
	// the document is looked at.
	e.indentUseTabs, e.indentWidth = detectIndent(doc.TextRange(0, indentScanLimit))

	// Every file lives in a document; the open mode only decides which chrome is
	// worth keeping (counts, snapshots, the save-time trim).
	e.largeFile = mode.large()
	e.editSeq++ // a new document invalidates any save request still in flight
	// The counts on screen belong to the previous document; drop them until this
	// one has been counted.
	e.statsKnown = false
	e.loading = true
	e.doc = doc
	e.resetUndo()
	e.currentFile = path
	e.dirty = false
	e.applyWrap()
	e.code.setDoc(e.doc)
	e.loading = false
	e.refreshChrome()
	freeMemory(doc.Len())
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

			path := r.URI().Path()
			name := filepath.Base(path)
			if path == "" {
				name = r.URI().Name()
			}
			// The dialog gives a reader, not a size. A file URI can be
			// stat'ed; anything else (a portal URI, say) is treated as small
			// and left to the limitReader below.
			var size int64
			if fi, serr := os.Stat(path); serr == nil {
				size = fi.Size()
			}

			e.confirmOpenSize(name, size, func(mode openMode) {
				defer r.Close()
				doc, eol, enc, err := openDocument(&limitReader{r: r, limit: MaxOpenBytes})
				if err != nil {
					e.reportOpenError(err, name)
					return
				}
				e.loadDocumentAs(path, doc, eol, enc, mode)
			})
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
	e.OpenPathAt(path, 0, 0)
}

// OpenPathAt loads path and, when line > 0, places the caret at line:col. Both
// numbers are 1-based, matching the file:line:col form on the command line, so
// column 0 and column 1 both mean the start of the line.
func (e *Editor) OpenPathAt(path string, line, col int) {
	size, err := fileSize(path)
	if err != nil {
		dialog.ShowError(err, e.win)
		e.setStatus("open failed")
		return
	}
	e.confirmOpenSize(filepath.Base(path), size, func(mode openMode) {
		e.openPath(path, mode, line, col)
	})
}

// openPath reads and installs path. It runs once the size gate has approved the
// open, and is the only place that reads a file into the buffer.
func (e *Editor) openPath(path string, mode openMode, line, col int) {
	f, err := os.Open(path)
	if err != nil {
		e.reportOpenError(err, filepath.Base(path))
		return
	}
	defer f.Close()

	doc, eol, enc, err := openDocument(&limitReader{r: f, limit: MaxOpenBytes})
	if err != nil {
		e.reportOpenError(err, filepath.Base(path))
		return
	}
	e.loadDocumentAs(path, doc, eol, enc, mode)

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
	e.loadContentAs(path, text, enc, openNormal)
}

// loadContentAs is loadContent with the open mode the file was approved for.
func (e *Editor) loadContentAs(path, text, enc string, mode openMode) {
	text, eol := normalizeEOL(text)
	e.loadDocumentAs(path, NewDocument(text), eol, enc, mode)
}

// loadDocumentAs is loadContentAs for a document that was streamed in, so a big
// file does not have to pass through a string to be installed.
func (e *Editor) loadDocumentAs(path string, doc *Document, eol, enc string, mode openMode) {
	e.recordDiskState(path)
	suffix := ""
	if mode.large() {
		suffix = " — large-file mode"
	}
	if rec, ok := recoveryFor(path); ok {
		name := filepath.Base(path)
		dialog.ShowConfirm("Recover unsaved changes?",
			"Gaze found unsaved changes for "+name+" from an earlier session.\nRestore them?",
			func(restore bool) {
				if restore {
					e.setContentAs(rec, path, enc, mode)
					e.dirty = true
					e.refreshChrome()
					e.setStatus("recovered " + path + suffix)
				} else {
					clearRecovery(path)
					e.setDocumentAs(doc, path, eol, enc, mode)
					e.setStatus("opened " + path + suffix)
				}
				e.addRecent(path)
			}, e.win)
		return
	}
	clearRecovery(path)
	e.setDocumentAs(doc, path, eol, enc, mode)
	e.addRecent(path)
	e.setStatus("opened " + path + suffix)
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
	doc     *Document // the text is streamed from its pages
	eol     string
	enc     string
	trim    bool
	prevKey string // recovery key the buffer had before the save
	seq     uint64 // editSeq when the request was made
}

// pendingSave snapshots the current buffer and document settings.
func (e *Editor) pendingSave(path string) saveRequest {
	req := saveRequest{
		path: path,
		eol:  e.eol,
		enc:  e.enc,
		// Trimming needs a second copy of the buffer to compare against, so it is
		// skipped entirely in large-file mode.
		trim:    e.trimOnSave && !e.largeFile,
		prevKey: e.recoveryKey(),
		seq:     e.editSeq,
	}
	// The write runs on a background goroutine, so it gets its own view of the
	// pages: editing must not change what is being written.
	if e.doc != nil {
		req.doc = e.doc.Snapshot()
	}
	return req
}

// bytes reports the size of the text the request will write.
func (r saveRequest) bytes() int {
	if r.doc == nil {
		return 0
	}
	return r.doc.Len()
}

// render produces the bytes for disk and the \n-only buffer they came from:
// trailing whitespace is stripped when the option is on and line endings are
// converted back to the file's style.
func (r saveRequest) render() (out, trimmed string) {
	if r.doc == nil {
		return "", ""
	}
	trimmed = r.doc.Text()
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

// write renders and writes the request atomically, returning the buffer that was
// actually written.
func (r saveRequest) write() (trimmed string, err error) {
	err = writeFileAtomicWith(r.path, func(w io.Writer) error {
		if r.doc == nil {
			return errNoDocument
		}
		// Streamed straight from the document's pages: no copy of the file is
		// built in memory.
		return writeDocumentLines(w, r.doc.eachLine, r.eol, r.enc, r.trim)
	})
	if err != nil {
		return "", err
	}
	return r.bufferAfterWrite(), nil
}

// writeTo writes the request to an already-open writer, for Save As.
func (r saveRequest) writeTo(w io.Writer) error {
	if r.doc == nil {
		return errNoDocument
	}
	return writeDocumentLines(w, r.doc.eachLine, r.eol, r.enc, r.trim)
}

// errNoDocument is reported if a save is attempted with no document behind it.
var errNoDocument = errors.New("gaze: no document to save")

// bufferAfterWrite is the buffer state that matches what write put on disk.
// Trimming is mirrored back into the editor, so the trimmed text is returned when
// it is in use.
func (r saveRequest) bufferAfterWrite() string {
	if !r.trim || r.doc == nil {
		// Nothing to mirror: the buffer is already what was written.
		return ""
	}
	return trimTrailingSpace(r.doc.Text())
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
	// The buffer is unchanged if nothing was typed since the request was made.
	sameBuffer := e.editSeq == req.seq
	if sameBuffer && trimmed != "" {
		e.adoptTrimmed(trimmed)
	}
	e.currentFile = req.path
	e.dirty = !sameBuffer
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
	freeMemory(req.bytes())
}

// serialize renders the buffer for disk. It also returns the trimmed \n-only
// buffer so callers can keep the editor in sync with what was written.
func (e *Editor) serialize() (out, trimmed string) {
	return e.pendingSave(e.currentFile).render()
}

// adoptTrimmed mirrors a save-time trim back into the document so the buffer is
// what is on disk. The trim is recorded as one undoable edit, so it can still be
// undone.
func (e *Editor) adoptTrimmed(trimmed string) {
	if e.doc == nil || trimmed == "" {
		return
	}
	cur := e.text()
	if cur == trimmed {
		return
	}
	e.loading = true
	e.code.replaceAll(cur, trimmed)
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

// writeFileAtomic writes data to path via a same-directory temp file, preserving
// the target's permissions and resolving symlinks so the link itself survives.
func writeFileAtomic(path string, data []byte) error {
	return writeFileAtomicWith(path, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// writeFileAtomicWith writes path atomically, letting fill stream the content so
// that a large document never has to be built in memory first.
func writeFileAtomicWith(path string, fill func(io.Writer) error) error {
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
	if err := fill(f); err != nil {
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
			werr := req.writeTo(wc)
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
				e.finishSave(req, req.bufferAfterWrite())
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
	// Document coordinates, so a large file reopens at the right line rather
	// than at the top of whichever window happened to be loaded.
	line, col := e.caretLineCol()
	p.SetInt(prefLastRow, line-1)
	p.SetInt(prefLastCol, col-1)
}

// RestoreSession reopens the file from the previous session (if it still
// exists) and returns to where the caret was. It reports whether an open was
// started — a large file asks before it loads, so the outcome is known later.
func (e *Editor) RestoreSession() bool {
	path := e.app.Preferences().StringWithFallback(prefLastFile, "")
	if path == "" {
		return false
	}
	size, err := fileSize(path)
	if err != nil {
		return false
	}
	row := e.app.Preferences().IntWithFallback(prefLastRow, 0)
	col := e.app.Preferences().IntWithFallback(prefLastCol, 0)
	e.confirmOpenSize(filepath.Base(path), size, func(mode openMode) {
		e.openPath(path, mode, 0, 0)
		if e.currentFile != path {
			return
		}
		if row > 0 || col > 0 {
			e.gotoLineCol(row+1, col)
		}
	})
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
