// Package editor implements the Gaze window: a monospaced editing surface
// wrapped in a slim toolbar, menu bar and status line, with find & replace,
// go-to-line, word wrap, font zoom and a persistent recent-files list.
package editor

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	ftheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"gaze/internal/theme"
)

const (
	appTitle = "Gaze"

	prefWrap     = "wordWrap"
	prefTrim     = "trimOnSave"
	prefAutosave = "autosave"
)

// Timing and size knobs. These are variables rather than constants so tests can
// shorten or narrow them without sleeping for seconds.
var (
	// statusTimeout is how long a status message stays before it clears.
	statusTimeout = 4 * time.Second
	// statsDelay is how long typing has to pause before a large buffer's
	// counts are recomputed.
	statsDelay = 150 * time.Millisecond
	// statsSyncLimit is the buffer size up to which the counts are recomputed
	// on every keystroke. Beyond it the recount is deferred so that a large
	// file stays responsive.
	statsSyncLimit = 256 << 10
)

// Editor owns the Gaze window state: the text buffer, the current file, the
// find bar, the status line, recent files and the theme/zoom manager.
type Editor struct {
	app fyne.App
	win fyne.Window

	entry  *widget.Entry // normal-mode buffer: the entry *is* the document
	status *widget.Label
	info   *widget.Label

	// Large-file mode: the document lives in doc, and largeEntry holds only the
	// window the viewport is showing.
	largeEntry *widget.Entry
	view       *viewport
	doc        *Document

	normalArea *fyne.Container
	largeArea  *fyne.Container

	windowFrom int    // document byte offset of the window's first byte
	windowText string // text currently installed in largeEntry
	editSeq    uint64

	findBar    *fyne.Container
	findEntry  *widget.Entry
	replEntry  *widget.Entry
	matchCase  *widget.Check
	regexCheck *widget.Check

	tipLayer *fyne.Container
	tipBG    *canvas.Rectangle
	tipText  *canvas.Text

	recentMenu   *fyne.Menu
	themeItems   map[string]*fyne.MenuItem
	wrapItem     *fyne.MenuItem
	trimItem     *fyne.MenuItem
	autosaveItem *fyne.MenuItem

	themes *theme.Manager

	currentFile string
	dirty       bool
	loading     bool
	restoring   bool
	largeFile   bool
	wrap        bool
	trimOnSave  bool
	autosaveOn  bool

	eol           string
	enc           string
	indentUseTabs bool
	indentWidth   int

	undo      []snapshot
	redo      []snapshot
	undoBytes int
	redoBytes int

	histIndex int
	histDraft string

	lastAutosave time.Time

	statusSeq   int
	statusTimer *time.Timer
	statsTimer  *time.Timer
	saving      bool

	// Identity of the file on disk as Gaze last read or wrote it, so a save
	// can tell whether another program changed it underneath us.
	diskPath  string
	diskMod   time.Time
	diskSize  int64
	diskKnown bool

	statWords int
	statChars int
}

// New wires an Editor to an existing app+window and restores the persisted
// window size, theme and word-wrap preference. Call BuildUI next.
func New(a fyne.App, w fyne.Window) *Editor {
	e := &Editor{
		app:         a,
		win:         w,
		themes:      theme.NewManager(a),
		themeItems:  map[string]*fyne.MenuItem{},
		wrap:        a.Preferences().BoolWithFallback(prefWrap, true),
		trimOnSave:  a.Preferences().BoolWithFallback(prefTrim, false),
		autosaveOn:  a.Preferences().BoolWithFallback(prefAutosave, true),
		indentWidth: 4,
		eol:         eolLF,
		enc:         encUTF8,
		histIndex:   -1,
	}
	w.Resize(fyne.NewSize(
		float32(a.Preferences().FloatWithFallback(prefWinW, 820)),
		float32(a.Preferences().FloatWithFallback(prefWinH, 580)),
	))
	return e
}

// buffer is the entry that currently holds the document: the plain entry for a
// normal file, the windowed one in large-file mode.
func (e *Editor) buffer() *widget.Entry {
	if e.largeFile && e.largeEntry != nil {
		return e.largeEntry
	}
	return e.entry
}

// caretLineCol is the 1-based document position of the caret.
func (e *Editor) caretLineCol() (line, col int) {
	buf := e.buffer()
	line, col = buf.CursorRow+1, buf.CursorColumn+1
	if e.largeFile && e.view != nil {
		line += e.view.top
	}
	return line, col
}

// onCursorChanged keeps the readout and the window in step with the caret. It is
// ignored while a window is being installed: SetText moves the caret to the end
// of the new text, and following that would refill again and again.
func (e *Editor) onCursorChanged() {
	if e.loading {
		return
	}
	e.updateInfo()
	if e.largeFile && e.view != nil {
		e.view.caretMoved()
	}
}

// applyOpenMode shows the widget that matches how the file was opened.
func (e *Editor) applyOpenMode() {
	if e.normalArea == nil || e.largeArea == nil {
		return
	}
	if e.largeFile {
		e.normalArea.Hide()
		e.largeArea.Show()
	} else {
		e.largeArea.Hide()
		e.normalArea.Show()
	}
	e.applyWrap()
	if e.win != nil && e.win.Canvas() != nil {
		e.win.Canvas().Focus(e.buffer())
	}
}

// setWindowText installs a window of the document in the large-mode entry.
func (e *Editor) setWindowText(text string, from int) {
	if e.largeEntry == nil {
		return
	}
	e.loading = true
	e.largeEntry.SetText(text)
	e.loading = false
	e.windowText = text
	e.windowFrom = from
}

// flushWindow commits the edits made in the window to the document. Every
// document-wide operation (scroll refill, save, find, replace, go to line) calls
// it first, so the document is never stale when it is read.
func (e *Editor) flushWindow() {
	if e.doc == nil || e.largeEntry == nil {
		return
	}
	text := e.largeEntry.Text
	if text == e.windowText {
		return
	}
	e.doc.ReplaceRange(e.windowFrom, e.windowFrom+len(e.windowText), text)
	e.windowText = text
	if e.view != nil {
		e.view.refreshScrollbar()
	}
}

// refillFromDoc moves the window so that a document offset is visible and puts
// the caret there.
func (e *Editor) refillFromDoc(off int) {
	if e.doc == nil || e.view == nil {
		return
	}
	row, col := e.doc.RowColOf(off)
	e.view.showLine(row, col)
	e.updateInfo()
}

// BuildUI constructs the editor, toolbar, find bar, status line and menus, and
// installs them on the window.
func (e *Editor) BuildUI() {
	e.buildEntry()
	e.buildFindBar()
	e.buildTipLayer()
	e.buildMenus()

	// Populate the status labels before the layout computes their minimum
	// sizes, otherwise the right-hand readout is clipped to an empty width.
	e.refreshChrome()
	e.buildLayout()

	e.applyWrap()
	if e.win != nil {
		e.win.SetOnDropped(e.onDropped)
	}
	e.win.Canvas().Focus(e.buffer())
}

func (e *Editor) buildEntry() {
	e.entry = widget.NewMultiLineEntry()
	e.entry.TextStyle = fyne.TextStyle{Monospace: true}
	e.entry.OnChanged = func(string) { e.onChanged() }
	e.entry.OnCursorChanged = func() { e.onCursorChanged() }

	e.largeEntry = widget.NewMultiLineEntry()
	e.largeEntry.TextStyle = fyne.TextStyle{Monospace: true}
	e.largeEntry.Wrapping = fyne.TextWrapOff
	// With wrap off *and* scrolling off, Fyne lays the entry's content out
	// directly instead of inside its own scroller. The viewport does the
	// scrolling, so the entry must not scroll itself out from under it.
	e.largeEntry.Scroll = fyne.ScrollNone
	e.largeEntry.OnChanged = func(string) { e.onChanged() }
	e.largeEntry.OnCursorChanged = func() { e.onCursorChanged() }
	e.view = newViewport(e, e.largeEntry)

	e.status = widget.NewLabel("")
	e.status.TextStyle = fyne.TextStyle{Monospace: true}

	e.info = widget.NewLabel("")
	e.info.Alignment = fyne.TextAlignTrailing
	e.info.TextStyle = fyne.TextStyle{Monospace: true}
}

func (e *Editor) buildFindBar() {
	e.findEntry = widget.NewEntry()
	e.findEntry.SetPlaceHolder("Find")
	e.findEntry.OnSubmitted = func(string) { e.FindNext() }

	e.replEntry = widget.NewEntry()
	e.replEntry.SetPlaceHolder("Replace")
	e.replEntry.OnSubmitted = func(string) { e.ReplaceCurrent() }

	e.matchCase = widget.NewCheck("Match case", nil)
	e.regexCheck = widget.NewCheck("Regex", nil)

	next := newTipButton("Next", nil, "Find next match (Enter)", e.FindNext)
	replace := newTipButton("Replace", nil, "Replace the current match", e.ReplaceCurrent)
	all := newTipButton("All", nil, "Replace every match", e.ReplaceAll)
	close := newTipButton("", ftheme.CancelIcon(), "Close find bar (Esc)", e.HideFind)
	e.bindTips(next, replace, all, close)

	findRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(next, close), e.findEntry)
	replaceRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(e.matchCase, e.regexCheck, replace, all), e.replEntry)

	e.findBar = container.NewVBox(findRow, replaceRow)
	e.findBar.Hide()
}

func (e *Editor) buildLayout() {
	newBtn := newTipButton("", ftheme.DocumentCreateIcon(), "New file (Ctrl+N)", e.NewFile)
	openBtn := newTipButton("", ftheme.FolderOpenIcon(), "Open file (Ctrl+O)", e.OpenFile)
	saveBtn := newTipButton("", ftheme.DocumentSaveIcon(), "Save (Ctrl+S)", e.SaveFile)
	findBtn := newTipButton("", ftheme.SearchReplaceIcon(), "Find & replace (Ctrl+F)", e.ShowFind)
	themeBtn := newTipButton("", ftheme.ColorChromaticIcon(), "Toggle light/dark theme", e.ToggleTheme)
	e.bindTips(newBtn, openBtn, saveBtn, findBtn, themeBtn)

	toolbar := container.NewHBox(newBtn, openBtn, saveBtn, findBtn, layout.NewSpacer(), themeBtn)

	top := container.NewVBox(toolbar, e.findBar)
	bottom := container.NewBorder(nil, nil, container.NewPadded(e.status), container.NewPadded(e.info))
	e.normalArea = container.NewStack(container.NewScroll(e.entry))
	e.largeArea = container.NewStack(e.view)
	e.largeArea.Hide()
	center := container.NewStack(e.normalArea, e.largeArea)

	main := container.NewBorder(top, bottom, nil, nil, center)
	e.win.SetContent(container.NewStack(main, e.tipLayer))
	if c := e.win.Canvas(); c != nil {
		c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(fyne.Shortcut) { e.escape() })
		c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyUp, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) { e.historyPrev() })
		c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyDown, Modifier: fyne.KeyModifierAlt}, func(fyne.Shortcut) { e.historyNext() })
	}
}

func (e *Editor) buildMenus() {
	e.recentMenu = fyne.NewMenu("Open Recent")
	e.refreshRecentMenu()

	file := fyne.NewMenu("File",
		accel("New", fyne.KeyN, fyne.KeyModifierControl, e.NewFile),
		accel("Open…", fyne.KeyO, fyne.KeyModifierControl, e.OpenFile),
		&fyne.MenuItem{Label: "Open Recent", ChildMenu: e.recentMenu},
		fyne.NewMenuItemSeparator(),
		accel("Save", fyne.KeyS, fyne.KeyModifierControl, e.SaveFile),
		accel("Save As…", fyne.KeyS, fyne.KeyModifierControl|fyne.KeyModifierShift, e.SaveAsFile),
		fyne.NewMenuItemSeparator(),
		accel("Quit", fyne.KeyQ, fyne.KeyModifierControl, e.Quit),
	)

	edit := fyne.NewMenu("Edit",
		accel("Undo", fyne.KeyZ, fyne.KeyModifierControl, e.Undo),
		accel("Redo", fyne.KeyY, fyne.KeyModifierControl, e.Redo),
		fyne.NewMenuItemSeparator(),
		accel("Cut", fyne.KeyX, fyne.KeyModifierControl, func() { e.clipboard(&fyne.ShortcutCut{}) }),
		accel("Copy", fyne.KeyC, fyne.KeyModifierControl, func() { e.clipboard(&fyne.ShortcutCopy{}) }),
		accel("Paste", fyne.KeyV, fyne.KeyModifierControl, func() { e.clipboard(&fyne.ShortcutPaste{}) }),
		accel("Select All", fyne.KeyA, fyne.KeyModifierControl, e.SelectAll),
		fyne.NewMenuItemSeparator(),
		accel("Find & Replace…", fyne.KeyF, fyne.KeyModifierControl, e.ShowFind),
		accel("Go to Line…", fyne.KeyG, fyne.KeyModifierControl, e.GoToLine),
		fyne.NewMenuItemSeparator(),
		accel("Indent", fyne.KeyRightBracket, fyne.KeyModifierControl, e.IndentLine),
		accel("Outdent", fyne.KeyLeftBracket, fyne.KeyModifierControl, e.OutdentLine),
		accel("Move Line Up", fyne.KeyUp, fyne.KeyModifierControl|fyne.KeyModifierShift, e.MoveLineUp),
		accel("Move Line Down", fyne.KeyDown, fyne.KeyModifierControl|fyne.KeyModifierShift, e.MoveLineDown),
		accel("Duplicate Line", fyne.KeyD, fyne.KeyModifierControl|fyne.KeyModifierShift, e.DuplicateLine),
	)

	e.wrapItem = fyne.NewMenuItem("Word Wrap", e.ToggleWrap)
	e.wrapItem.Checked = e.wrap
	e.trimItem = fyne.NewMenuItem("Trim Trailing Space on Save", e.ToggleTrim)
	e.trimItem.Checked = e.trimOnSave
	e.autosaveItem = fyne.NewMenuItem("Autosave for Recovery", e.ToggleAutosave)
	e.autosaveItem.Checked = e.autosaveOn

	view := fyne.NewMenu("View",
		e.wrapItem,
		e.trimItem,
		e.autosaveItem,
		fyne.NewMenuItemSeparator(),
		accel("Zoom In", fyne.KeyEqual, fyne.KeyModifierControl, func() { e.zoom(1) }),
		accel("Zoom Out", fyne.KeyMinus, fyne.KeyModifierControl, func() { e.zoom(-1) }),
		accel("Reset Zoom", fyne.Key0, fyne.KeyModifierControl, e.ResetZoom),
		fyne.NewMenuItemSeparator(),
		e.themeItem(theme.NameLight),
		e.themeItem(theme.NameDark),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Toggle Theme", e.ToggleTheme),
	)

	help := fyne.NewMenu("Help",
		fyne.NewMenuItem("About Gaze", e.showAbout),
	)

	e.win.SetMainMenu(fyne.NewMainMenu(file, edit, view, help))
	e.win.SetCloseIntercept(e.Quit)
}

// accel builds a menu entry with a desktop accelerator.
func accel(label string, key fyne.KeyName, mod fyne.KeyModifier, action func()) *fyne.MenuItem {
	it := fyne.NewMenuItem(label, action)
	it.Shortcut = &desktop.CustomShortcut{KeyName: key, Modifier: mod}
	return it
}

// themeItem builds a checkable menu entry for the named theme.
func (e *Editor) themeItem(name string) *fyne.MenuItem {
	item := fyne.NewMenuItem(name, func() { e.SetTheme(name) })
	item.Checked = e.themes.Current() == name
	e.themeItems[name] = item
	return item
}

// onChanged marks the buffer dirty, refreshes the chrome and snapshots a
// recovery copy after user edits.
func (e *Editor) onChanged() {
	if e.loading || e.restoring {
		return
	}
	e.dropRedo()
	e.dirty = true
	e.editSeq++
	e.updateStatsForChange()
	e.updateTitle()
	e.updateInfo()
	e.maybeAutosave()
}

// updateStatsForChange keeps typing cheap in large buffers: small files are
// recounted immediately, big ones once the user pauses.
func (e *Editor) updateStatsForChange() {
	if e.largeFile {
		return
	}
	if len(e.entry.Text) <= statsSyncLimit {
		e.updateStats()
		return
	}
	if e.statsTimer != nil {
		e.statsTimer.Stop()
	}
	e.statsTimer = time.AfterFunc(statsDelay, func() {
		fyne.Do(func() {
			if e.entry == nil || e.info == nil {
				return
			}
			e.updateStats()
			e.updateInfo()
		})
	})
}

// updateStats recomputes the cached word/character counts. Large-file mode skips
// it: the scan is O(buffer) and the counts are not shown.
func (e *Editor) updateStats() {
	if e.largeFile {
		e.statChars, e.statWords = 0, 0
		return
	}
	text := e.entry.Text
	e.statChars = utf8.RuneCountInString(text)
	e.statWords = countWords(text)
}

// updateTitle refreshes the window title from the current file and dirty flag.
func (e *Editor) updateTitle() {
	name := "untitled"
	if e.currentFile != "" {
		name = filepath.Base(e.currentFile)
	}
	mark := ""
	if e.dirty {
		mark = " •"
	}
	e.win.SetTitle(appTitle + " — " + name + mark)
}

// updateInfo refreshes the compact cursor/document readout in the status bar.
func (e *Editor) updateInfo() {
	if e.info == nil || e.entry == nil {
		return
	}
	line, col := e.caretLineCol()
	pos := fmt.Sprintf("%d:%d", line, col)
	tail := fmt.Sprintf("%s · %s%s · %d%%", e.eolLabel(), e.indentLabel(),
		e.encodingSuffix(), zoomPercent(e.themes.Zoom()))
	if e.largeFile {
		// No live counts: they would mean rescanning the whole buffer.
		e.info.SetText(pos + " · " + tail + " · large")
		return
	}
	e.info.SetText(fmt.Sprintf("%s · %dw · %dc · %s", pos, e.statWords, e.statChars, tail))
}

// encodingSuffix names the file's encoding in the status bar, but only when it
// is not plain UTF-8, so the readout stays short for the common case.
func (e *Editor) encodingSuffix() string {
	if e.enc == "" || e.enc == encUTF8 {
		return ""
	}
	return " · " + e.enc
}

// eolLabel names the file's line-ending convention for the status bar.
func (e *Editor) eolLabel() string {
	if e.eol == "" {
		return eolLF
	}
	return e.eol
}

// indentLabel describes the detected indentation for the status bar.
func (e *Editor) indentLabel() string {
	if e.indentUseTabs {
		return "tab"
	}
	w := e.indentWidth
	if w <= 0 {
		w = 4
	}
	return fmt.Sprintf("sp%d", w)
}

// setStatus updates the left-hand status message and schedules it to clear, so
// one-off remarks like "save failed" do not sit in the bar forever.
func (e *Editor) setStatus(s string) {
	if e.status == nil {
		return
	}
	e.status.SetText(s)
	e.statusSeq++
	seq := e.statusSeq
	if e.statusTimer != nil {
		e.statusTimer.Stop()
		e.statusTimer = nil
	}
	if s == "" {
		return
	}
	e.statusTimer = time.AfterFunc(statusTimeout, func() {
		fyne.Do(func() {
			// A newer message may have replaced this one already.
			if e.status == nil || e.statusSeq != seq {
				return
			}
			e.status.SetText("")
		})
	})
}

// escape closes the find bar when it is open and does nothing otherwise.
// Forcing focus back to the editor on every Esc would steal the keystroke from
// dialogs and from the find bar's own fields.
func (e *Editor) escape() {
	if e.findBar == nil || !e.findBar.Visible() {
		return
	}
	e.HideFind()
}

// stopTimers cancels the deferred UI work so a closing window does not fire
// callbacks into a dead canvas.
func (e *Editor) stopTimers() {
	if e.statusTimer != nil {
		e.statusTimer.Stop()
		e.statusTimer = nil
	}
	if e.statsTimer != nil {
		e.statsTimer.Stop()
		e.statsTimer = nil
	}
}

// SetStatus updates the left-hand status message.
func (e *Editor) SetStatus(s string) { e.setStatus(s) }

// refreshChrome refreshes every piece of window chrome at once.
func (e *Editor) refreshChrome() {
	e.updateTitle()
	e.updateStats()
	e.updateInfo()
}

// Undo reverts the last edit. Entry's native stack handles typing; the
// snapshots cover the whole-buffer rewrites that clear it.
func (e *Editor) Undo() {
	if e.entry == nil {
		return
	}
	before := e.buffer().Text
	e.buffer().Undo()
	if e.buffer().Text != before {
		return // Entry undid a keystroke (and fired OnChanged for us)
	}
	e.popUndo()
}

// Redo reapplies the last undone edit, preferring Entry's stack for the same
// reason Undo does.
func (e *Editor) Redo() {
	if e.entry == nil {
		return
	}
	before := e.buffer().Text
	e.buffer().Redo()
	if e.buffer().Text != before {
		return
	}
	e.popRedo()
}

// SelectAll selects the entire buffer.
func (e *Editor) SelectAll() { e.buffer().TypedShortcut(&fyne.ShortcutSelectAll{}) }

// clipboard routes a cut/copy/paste shortcut through the entry with the
// window's clipboard attached.
func (e *Editor) clipboard(sc fyne.Shortcut) {
	switch s := sc.(type) {
	case *fyne.ShortcutCut:
		s.Clipboard = e.win.Clipboard()
	case *fyne.ShortcutCopy:
		s.Clipboard = e.win.Clipboard()
	case *fyne.ShortcutPaste:
		s.Clipboard = e.win.Clipboard()
	}
	e.buffer().TypedShortcut(sc)
}

// ToggleWrap flips word wrapping and persists the choice.
func (e *Editor) ToggleWrap() {
	if e.largeFile {
		e.setStatus("word wrap is off in large-file mode")
		return
	}
	e.wrap = !e.wrap
	e.app.Preferences().SetBool(prefWrap, e.wrap)
	e.applyWrap()
}

// ToggleTrim flips trim-trailing-space-on-save and persists the choice.
func (e *Editor) ToggleTrim() {
	e.trimOnSave = !e.trimOnSave
	e.app.Preferences().SetBool(prefTrim, e.trimOnSave)
	if e.trimItem != nil {
		e.trimItem.Checked = e.trimOnSave
		if menu := e.win.MainMenu(); menu != nil {
			menu.Refresh()
		}
	}
	e.setStatus("trim on save: " + onOff(e.trimOnSave))
}

// ToggleAutosave flips crash-recovery snapshots and persists the choice.
func (e *Editor) ToggleAutosave() {
	e.autosaveOn = !e.autosaveOn
	e.app.Preferences().SetBool(prefAutosave, e.autosaveOn)
	if e.autosaveItem != nil {
		e.autosaveItem.Checked = e.autosaveOn
		if menu := e.win.MainMenu(); menu != nil {
			menu.Refresh()
		}
	}
	e.setStatus("autosave: " + onOff(e.autosaveOn))
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// indentString returns the unit of indentation for the current document.
func (e *Editor) indentString() string {
	if e.indentUseTabs {
		return "\t"
	}
	w := e.indentWidth
	if w <= 0 {
		w = 4
	}
	return strings.Repeat(" ", w)
}

// MoveLineUp swaps the caret's line with the one above it.
func (e *Editor) MoveLineUp() {
	buf := e.buffer()
	text := buf.Text
	if text == "" {
		return
	}
	nt, row := moveLineUp(text, buf.CursorRow)
	e.applyLineEdit(nt, row, buf.CursorColumn)
}

// MoveLineDown swaps the caret's line with the one below it.
func (e *Editor) MoveLineDown() {
	buf := e.buffer()
	text := buf.Text
	if text == "" {
		return
	}
	nt, row := moveLineDown(text, buf.CursorRow)
	e.applyLineEdit(nt, row, buf.CursorColumn)
}

// DuplicateLine copies the caret's line beneath itself.
func (e *Editor) DuplicateLine() {
	buf := e.buffer()
	text := buf.Text
	if text == "" {
		return
	}
	nt, row := duplicateLine(text, buf.CursorRow)
	e.applyLineEdit(nt, row, buf.CursorColumn)
}

// IndentLine adds one indentation level to the caret's line.
func (e *Editor) IndentLine() {
	buf := e.buffer()
	row := buf.CursorRow
	indent := e.indentString()
	nt := indentLine(buf.Text, row, indent)
	e.applyLineEdit(nt, row, buf.CursorColumn+utf8.RuneCountInString(indent))
}

// OutdentLine removes one indentation level from the caret's line.
func (e *Editor) OutdentLine() {
	buf := e.buffer()
	row := buf.CursorRow
	nt, removed := outdentLine(buf.Text, row, e.indentUseTabs, e.indentWidth)
	col := buf.CursorColumn - removed
	if col < 0 {
		col = 0
	}
	e.applyLineEdit(nt, row, col)
}

// applyLineEdit installs a whole-buffer result, keeping the caret in place.
// widget.Entry has no range-edit API, so line operations rewrite via SetText,
// which clears Entry's own history — the snapshot is what makes them undoable.
func (e *Editor) applyLineEdit(newText string, row, col int) {
	buf := e.buffer()
	if newText == buf.Text {
		return
	}
	e.pushUndo()
	buf.SetText(newText)
	buf.CursorRow, buf.CursorColumn = row, col
	buf.Refresh()
	e.updateInfo()
}

func (e *Editor) applyWrap() {
	wrap := e.wrap && !e.largeFile
	if wrap {
		e.entry.Wrapping = fyne.TextWrapWord
	} else {
		e.entry.Wrapping = fyne.TextWrapOff
	}
	if e.largeEntry != nil {
		// The window is a slice of lines: wrapping it would break the line maths.
		e.largeEntry.Wrapping = fyne.TextWrapOff
	}
	if e.wrapItem != nil {
		e.wrapItem.Checked = wrap
		// Wrapping is what makes Fyne split a large buffer into per-line
		// segments, so it is not offered in large-file mode.
		e.wrapItem.Disabled = e.largeFile
		if menu := e.win.MainMenu(); menu != nil {
			menu.Refresh()
		}
	}
	e.entry.Refresh()
}

// zoom adjusts the font zoom by direction (+1 in, -1 out).
func (e *Editor) zoom(direction int) {
	if direction > 0 {
		e.themes.ZoomIn()
	} else {
		e.themes.ZoomOut()
	}
	e.refreshLargeLayout()
	e.updateInfo()
	e.setStatus(fmt.Sprintf("zoom %d%%", zoomPercent(e.themes.Zoom())))
}

// ResetZoom restores the font zoom to 100%.
func (e *Editor) ResetZoom() {
	e.themes.ResetZoom()
	e.refreshLargeLayout()
	e.updateInfo()
	e.setStatus("zoom 100%")
}

// SetTheme switches to a named theme and refreshes the chrome.
func (e *Editor) SetTheme(name string) {
	e.themes.Set(name)
	e.syncTheme()
}

// ToggleTheme flips between light and dark.
func (e *Editor) ToggleTheme() {
	e.themes.Toggle()
	e.syncTheme()
}

// CycleTheme advances to the next theme.
func (e *Editor) CycleTheme() { e.SetTheme(e.themes.Next()) }

// refreshLargeLayout re-measures the window after the text size changed: a line
// is a different number of pixels tall, so the window has to be rebuilt.
func (e *Editor) refreshLargeLayout() {
	if e.largeFile && e.view != nil {
		e.view.refill()
	}
}

func (e *Editor) syncTheme() {
	for n, item := range e.themeItems {
		item.Checked = n == e.themes.Current()
	}
	if menu := e.win.MainMenu(); menu != nil {
		menu.Refresh()
	}
	e.refreshLargeLayout()
	e.updateInfo()
	e.setStatus("theme: " + e.themes.Current())
}

func (e *Editor) showAbout() {
	dialog.ShowInformation("About Gaze",
		"Gaze — a tiny, fast Fyne text editor.\n\n"+
			"Find & replace (regex, history), go-to-line, line ops,\n"+
			"word wrap, font zoom, recent files, autosave recovery.\n"+
			"UTF-8, UTF-8 BOM and UTF-16 in and out.\n"+
			"Light and Dark themes.\n\n"+
			"usage: gaze [file[:line[:col]]]  ·  gaze -  reads stdin\n\n"+
			"File · Edit · View · built with Fyne v2.",
		e.win)
}

// CurrentFile returns the backing path ("" == untitled).
func (e *Editor) CurrentFile() string { return e.currentFile }

// IsDirty reports whether the buffer holds unsaved changes.
func (e *Editor) IsDirty() bool { return e.dirty }

// ThemeName reports the active theme name.
func (e *Editor) ThemeName() string { return e.themes.Current() }

// zoomPercent renders a zoom factor as a rounded percentage.
func zoomPercent(z float32) int { return int(z*100 + 0.5) }
