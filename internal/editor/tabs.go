package editor

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2/dialog"
)

// A tab is one open buffer. The window edits the active tab directly — the editor
// keeps its document, its caret and everything known about its file in the fields
// it always had — while the other tabs keep the same state stashed, so switching
// back to one costs a copy of a few fields rather than a reload.
type tab struct {
	doc  *Document
	path string // "" for a buffer that has never been saved

	dirty     bool
	large     bool
	eol       string
	enc       string
	lang      *grammar
	useTabs   bool
	width     int
	diskPath  string
	diskMod   time.Time
	diskSize  int64
	diskKnown bool
	statWords int
	statChars int
	statKnown bool
	// edits is the edit counter of the window when this buffer was last changed,
	// so a save can tell whether the buffer it wrote has moved on since.
	edits uint64

	view viewState
}

// viewState is the part of the code view that belongs to one document: where the
// caret is, how to get back, where the view is scrolled to, and what is cached about
// the text.
type viewState struct {
	caret, anchor pos
	undo          []docEdit
	redo          []docEdit
	undoBytes     int
	topRow        int
	xoffCell      int
	rows          []int
	rowsCells     int
	marks         []int
}

// title is what the tab bar and the window title call this buffer.
func (t *tab) title() string {
	if t.path == "" {
		return "untitled"
	}
	return filepath.Base(t.path)
}

// untouched reports whether nothing has been done to this buffer, so a file opened
// into a fresh window can take its place instead of sitting next to an empty tab.
func (t *tab) untouched() bool {
	return t.path == "" && !t.dirty && t.doc != nil && t.doc.Len() == 0
}

// --- moving a document in and out of the view ---------------------------------

// stash cuts the view's state for its document out of the widget.
func (v *codeView) stash(t *tab) {
	t.view = viewState{
		caret: v.caret, anchor: v.anchor,
		undo: v.undo, redo: v.redo, undoBytes: v.undoBytes,
		topRow: v.topRow, xoffCell: v.xoffCell,
		rows: v.rows, rowsCells: v.rowsCells,
		marks: v.marks,
	}
}

// restore puts a tab's document and state back into the view.
func (v *codeView) restore(t *tab) {
	st := t.view
	v.doc = t.doc
	v.caret, v.anchor = st.caret, st.anchor
	v.undo, v.redo, v.undoBytes = st.undo, st.redo, st.undoBytes
	v.topRow, v.xoffCell = st.topRow, st.xoffCell
	v.rows, v.rowsCells = st.rows, st.rowsCells
	v.marks = st.marks
	v.brValid = false
	v.dragging, v.dragBar = false, false
	// The grammar belongs to the document and is applied by the editor; the pooled
	// text objects are reused, but what they hold belongs to the rows they were
	// filled with.
	v.hl = nil
	v.segDrop()
	v.render()
}

// --- the tab list -------------------------------------------------------------

// tabsInit makes sure the window has a tab to edit. It runs once, from BuildUI.
func (e *Editor) tabsInit() {
	if len(e.tabs) > 0 {
		return
	}
	e.tabs = append(e.tabs, &tab{})
	e.active = 0
	e.saveActive()
}

// ActiveTab returns the index of the tab being edited.
func (e *Editor) ActiveTab() int { return e.active }

// TabCount returns how many buffers are open.
func (e *Editor) TabCount() int { return len(e.tabs) }

// TabPaths returns the path of each open buffer, in tab order ("" for untitled).
func (e *Editor) TabPaths() []string {
	out := make([]string, len(e.tabs))
	for i, t := range e.tabs {
		out[i] = t.path
	}
	return out
}

// TabTitles returns what the tab bar calls each buffer, in tab order.
func (e *Editor) TabTitles() []string {
	out := make([]string, len(e.tabs))
	for i, t := range e.tabs {
		out[i] = t.title()
	}
	return out
}

// TabDirty reports whether the buffer at index i has unsaved changes.
func (e *Editor) TabDirty(i int) bool {
	if i < 0 || i >= len(e.tabs) {
		return false
	}
	if i == e.active {
		return e.dirty
	}
	return e.tabs[i].dirty
}

// saveActive copies what the window knows about the buffer being edited into its
// tab, so it can be put back later.
func (e *Editor) saveActive() {
	if e.active < 0 || e.active >= len(e.tabs) {
		return
	}
	t := e.tabs[e.active]
	t.doc = e.doc
	t.path = e.currentFile
	t.dirty = e.dirty
	t.large = e.largeFile
	t.eol = e.eol
	t.enc = e.enc
	t.lang = e.lang
	t.useTabs = e.indentUseTabs
	t.width = e.indentWidth
	t.diskPath, t.diskMod, t.diskSize, t.diskKnown = e.diskPath, e.diskMod, e.diskSize, e.diskKnown
	t.statWords, t.statChars, t.statKnown = e.statWords, e.statChars, e.statsKnown
	if e.code != nil {
		e.code.stash(t)
	}
}

// loadActive puts a tab's state into the window. The tab list itself is not
// touched, so callers own the order and the active index.
func (e *Editor) loadActive(t *tab) {
	e.doc = t.doc
	e.currentFile = t.path
	e.dirty = t.dirty
	e.largeFile = t.large
	e.eol = t.eol
	if e.eol == "" {
		e.eol = eolLF
	}
	e.enc = t.enc
	if e.enc == "" {
		e.enc = encUTF8
	}
	e.lang = t.lang
	e.indentUseTabs, e.indentWidth = t.useTabs, t.width
	if e.indentWidth <= 0 {
		e.indentWidth = 4
	}
	e.diskPath, e.diskMod, e.diskSize, e.diskKnown = t.diskPath, t.diskMod, t.diskSize, t.diskKnown
	e.statWords, e.statChars, e.statsKnown = t.statWords, t.statChars, t.statKnown
	e.editSeq++ // a different document invalidates anything still counting for the old one
	e.loading = true
	if e.code != nil {
		e.code.restore(t)
	}
	e.loading = false
	e.applyWrap()
	e.applySyntax()
	e.refreshChrome()
	e.focusCode()
	e.refreshMarks()
}

// activate makes tab i the one being edited.
func (e *Editor) activate(i int) {
	if i < 0 || i >= len(e.tabs) || i == e.active {
		return
	}
	e.saveActive()
	e.active = i
	e.loadActive(e.tabs[i])
	e.refreshTabs()
}

// NextTab and PrevTab cycle through the open buffers, wrapping around.
func (e *Editor) NextTab() {
	if n := len(e.tabs); n > 1 {
		e.activate((e.active + 1) % n)
	}
}

func (e *Editor) PrevTab() {
	if n := len(e.tabs); n > 1 {
		e.activate((e.active - 1 + n) % n)
	}
}

// tabFor returns the index of the tab showing path, or -1.
func (e *Editor) tabFor(path string) int {
	if path == "" {
		return -1
	}
	clean := filepath.Clean(path)
	for i, t := range e.tabs {
		if t.path == "" {
			continue
		}
		if t.path == path || filepath.Clean(t.path) == clean {
			return i
		}
	}
	return -1
}

// addTab installs a new tab and makes it the one being edited, replacing the empty
// buffer of a fresh window rather than leaving it behind.
func (e *Editor) addTab(t *tab) {
	e.saveActive()
	if e.active >= 0 && e.active < len(e.tabs) && e.tabs[e.active].untouched() {
		e.tabs[e.active] = t
	} else {
		e.tabs = append(e.tabs, t)
		e.active = len(e.tabs) - 1
	}
	e.loadActive(t)
	e.refreshTabs()
}

// addUntitled opens an empty buffer in a new tab, unless the one being edited is
// already empty and untouched.
func (e *Editor) addUntitled() {
	if e.active >= 0 && e.active < len(e.tabs) && e.tabs[e.active].untouched() {
		return
	}
	e.addTab(&tab{doc: NewDocument(""), eol: eolLF, enc: encUTF8, width: 4})
}

// CloseTab closes the buffer being edited, asking first when it has changes.
func (e *Editor) CloseTab() { e.closeTab(e.active, nil) }

// closeTab closes tab i and runs then after it is gone. A buffer with unsaved
// changes is shown first, so the question is about what the user can see.
func (e *Editor) closeTab(i int, then func()) {
	if i < 0 || i >= len(e.tabs) {
		if then != nil {
			then()
		}
		return
	}
	if !e.TabDirty(i) {
		e.removeTab(i)
		if then != nil {
			then()
		}
		return
	}
	e.activate(i)
	name := e.tabs[i].title()
	dialog.ShowConfirm("Unsaved changes",
		"Close "+name+" and discard its changes?",
		func(ok bool) {
			defer e.focusCode()
			if !ok {
				return
			}
			e.removeTab(i)
			if then != nil {
				then()
			}
		}, e.win)
}

// removeTab drops tab i and shows the buffer that takes its place.
func (e *Editor) removeTab(i int) {
	if i < 0 || i >= len(e.tabs) {
		return
	}
	wasActive := i == e.active
	if wasActive {
		// The recovery snapshot of the buffer being abandoned is not needed.
		e.discardChanges()
	}
	e.tabs = append(e.tabs[:i], e.tabs[i+1:]...)
	if len(e.tabs) == 0 {
		// An editor with no buffer would have nowhere to type: leave an empty one.
		e.tabs = append(e.tabs, &tab{doc: NewDocument(""), eol: eolLF, enc: encUTF8, width: 4})
		e.active = 0
		e.loadActive(e.tabs[0])
		e.refreshTabs()
		return
	}
	switch {
	case i < e.active:
		e.active--
	case wasActive:
		if e.active >= len(e.tabs) {
			e.active = len(e.tabs) - 1
		}
		e.loadActive(e.tabs[e.active])
	}
	e.refreshTabs()
}

// dirtyTabs names the buffers with unsaved changes.
func (e *Editor) dirtyTabs() []string {
	var out []string
	for i := range e.tabs {
		if e.TabDirty(i) {
			out = append(out, e.tabs[i].title())
		}
	}
	return out
}

// quitMessage is what to ask before throwing away unsaved work.
func quitMessage(names []string) string {
	if len(names) == 1 {
		return "The file " + names[0] + " has unsaved changes.\nDiscard them and quit?"
	}
	return fmt.Sprintf("%d buffers have unsaved changes (%s).\nDiscard them and quit?",
		len(names), strings.Join(names, ", "))
}
