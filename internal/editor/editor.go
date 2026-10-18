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

// Editor owns the Gaze window state: the text buffer, the current file, the
// find bar, the status line, recent files and the theme/zoom manager.
type Editor struct {
	app fyne.App
	win fyne.Window

	entry  *widget.Entry
	status *widget.Label
	info   *widget.Label

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
	wrap        bool
	trimOnSave  bool
	autosaveOn  bool

	eol           string
	indentUseTabs bool
	indentWidth   int

	histIndex int
	histDraft string

	lastAutosave time.Time

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
		histIndex:   -1,
	}
	w.Resize(fyne.NewSize(
		float32(a.Preferences().FloatWithFallback(prefWinW, 820)),
		float32(a.Preferences().FloatWithFallback(prefWinH, 580)),
	))
	return e
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
	e.win.Canvas().Focus(e.entry)
}

func (e *Editor) buildEntry() {
	e.entry = widget.NewMultiLineEntry()
	e.entry.TextStyle = fyne.TextStyle{Monospace: true}
	e.entry.OnChanged = func(string) { e.onChanged() }
	e.entry.OnCursorChanged = func() { e.updateInfo() }

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
	scroll := container.NewScroll(e.entry)

	main := container.NewBorder(top, bottom, nil, nil, scroll)
	e.win.SetContent(container.NewStack(main, e.tipLayer))
	if c := e.win.Canvas(); c != nil {
		c.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEscape}, func(fyne.Shortcut) { e.HideFind() })
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
	if e.loading {
		return
	}
	e.dirty = true
	e.updateStats()
	e.updateTitle()
	e.updateInfo()
	e.maybeAutosave()
}

// updateStats recomputes the cached word/character counts.
func (e *Editor) updateStats() {
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
	e.info.SetText(fmt.Sprintf("%d:%d · %dw · %dc · %s · %s · %d%%",
		e.entry.CursorRow+1, e.entry.CursorColumn+1, e.statWords, e.statChars,
		e.eolLabel(), e.indentLabel(), zoomPercent(e.themes.Zoom())))
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

// setStatus updates the left-hand status message.
func (e *Editor) setStatus(s string) {
	if e.status != nil {
		e.status.SetText(s)
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

// Undo reverts the last edit.
func (e *Editor) Undo() { e.entry.Undo() }

// Redo reapplies the last undone edit.
func (e *Editor) Redo() { e.entry.Redo() }

// SelectAll selects the entire buffer.
func (e *Editor) SelectAll() { e.entry.TypedShortcut(&fyne.ShortcutSelectAll{}) }

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
	e.entry.TypedShortcut(sc)
}

// ToggleWrap flips word wrapping and persists the choice.
func (e *Editor) ToggleWrap() {
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
	text := e.entry.Text
	if text == "" {
		return
	}
	nt, row := moveLineUp(text, e.entry.CursorRow)
	e.applyLineEdit(nt, row, e.entry.CursorColumn)
}

// MoveLineDown swaps the caret's line with the one below it.
func (e *Editor) MoveLineDown() {
	text := e.entry.Text
	if text == "" {
		return
	}
	nt, row := moveLineDown(text, e.entry.CursorRow)
	e.applyLineEdit(nt, row, e.entry.CursorColumn)
}

// DuplicateLine copies the caret's line beneath itself.
func (e *Editor) DuplicateLine() {
	text := e.entry.Text
	if text == "" {
		return
	}
	nt, row := duplicateLine(text, e.entry.CursorRow)
	e.applyLineEdit(nt, row, e.entry.CursorColumn)
}

// IndentLine adds one indentation level to the caret's line.
func (e *Editor) IndentLine() {
	row := e.entry.CursorRow
	indent := e.indentString()
	nt := indentLine(e.entry.Text, row, indent)
	e.applyLineEdit(nt, row, e.entry.CursorColumn+utf8.RuneCountInString(indent))
}

// OutdentLine removes one indentation level from the caret's line.
func (e *Editor) OutdentLine() {
	row := e.entry.CursorRow
	nt, removed := outdentLine(e.entry.Text, row, e.indentUseTabs, e.indentWidth)
	col := e.entry.CursorColumn - removed
	if col < 0 {
		col = 0
	}
	e.applyLineEdit(nt, row, col)
}

// applyLineEdit installs a whole-buffer result, keeping the caret in place.
// widget.Entry has no range-edit API, so line operations rewrite via SetText.
func (e *Editor) applyLineEdit(newText string, row, col int) {
	if newText == e.entry.Text {
		return
	}
	e.entry.SetText(newText)
	e.entry.CursorRow, e.entry.CursorColumn = row, col
	e.entry.Refresh()
	e.updateInfo()
}

func (e *Editor) applyWrap() {
	if e.wrap {
		e.entry.Wrapping = fyne.TextWrapWord
	} else {
		e.entry.Wrapping = fyne.TextWrapOff
	}
	if e.wrapItem != nil {
		e.wrapItem.Checked = e.wrap
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
	e.updateInfo()
	e.setStatus(fmt.Sprintf("zoom %d%%", zoomPercent(e.themes.Zoom())))
}

// ResetZoom restores the font zoom to 100%.
func (e *Editor) ResetZoom() {
	e.themes.ResetZoom()
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

func (e *Editor) syncTheme() {
	for n, item := range e.themeItems {
		item.Checked = n == e.themes.Current()
	}
	if menu := e.win.MainMenu(); menu != nil {
		menu.Refresh()
	}
	e.updateInfo()
	e.setStatus("theme: " + e.themes.Current())
}

func (e *Editor) showAbout() {
	dialog.ShowInformation("About Gaze",
		"Gaze — a tiny, fast Fyne text editor.\n\n"+
			"Find & replace (regex, history), go-to-line, line ops,\n"+
			"word wrap, font zoom, recent files, autosave recovery.\n"+
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
