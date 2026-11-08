// Package editor implements the Gaze window: a monospaced editing surface
// wrapped in a slim toolbar, menu bar and status line, with find & replace,
// go-to-line, word wrap, font zoom and a persistent recent-files list.
package editor

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

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

	status *widget.Label
	info   *widget.Label

	// Every file is held in doc and edited through code, which draws the lines
	// on screen itself, so the buffer never reaches a widget.Entry.
	code *codeView
	doc  *Document

	editSeq    uint64
	statsKnown bool // whether the word/character counts are current

	// loadSeq numbers open requests: a background read abandons itself as soon
	// as a newer open (or a quit) bumps it.
	loadSeq   atomic.Uint64
	lastInfo  string // last text set on the readout, to skip no-op relayouts
	lastTitle string

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
	largeFile   bool
	wrap        bool
	trimOnSave  bool
	autosaveOn  bool

	eol           string
	enc           string
	indentUseTabs bool
	indentWidth   int

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

// text is the whole buffer as a string. It is for the commands that need the
// text as a whole (find, counts, recovery); the renderer and the save path work
// on the document directly.
func (e *Editor) text() string {
	if e.doc == nil {
		return ""
	}
	return e.doc.Text()
}

// focusTarget is the widget that takes keyboard focus.
func (e *Editor) focusTarget() fyne.Focusable { return e.code }

// caretLineCol is the 1-based document position of the caret.
func (e *Editor) caretLineCol() (line, col int) {
	if e.code == nil {
		return 1, 1
	}
	return e.code.caret.row + 1, e.code.caret.col + 1
}

// resetUndo clears the undo history, for a document that was just opened.
func (e *Editor) resetUndo() {
	if e.code != nil {
		e.code.resetUndo()
	}
}

// onCodeChange is called by the code view after it has edited the document: the
// text is already changed, so this only updates the state around it.
func (e *Editor) onCodeChange() {
	if e.loading {
		return
	}
	e.dirty = true
	e.editSeq++
	e.updateStatsForChange()
	e.updateTitle()
	e.updateInfo()
	e.maybeAutosave()
}

// applyOpenMode shows the widget that matches how the file was opened.

// revealOffset makes a document offset visible and puts the caret there.
func (e *Editor) revealOffset(off int) {
	if e.code != nil {
		e.code.setCaret(e.code.posAt(off))
	}
}

// BuildUI constructs the editor, toolbar, find bar, status line and menus, and
// installs them on the window.
func (e *Editor) BuildUI() {
	e.buildWidgets()
	if e.doc == nil {
		e.doc = NewDocument("")
		e.code.setDoc(e.doc)
	}
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
	e.win.Canvas().Focus(e.focusTarget())
}

func (e *Editor) buildWidgets() {
	e.code = newCodeView(e)

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
	main := container.NewBorder(top, bottom, nil, nil, e.code)
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

// updateStatsForChange keeps typing cheap in a big buffer: small files are
// recounted immediately, bigger ones once the user pauses.
func (e *Editor) updateStatsForChange() {
	if e.doc == nil {
		return
	}
	if e.doc.Len() <= statsSyncLimit {
		e.updateStats()
		e.updateInfo()
		return
	}
	if e.statsTimer != nil {
		e.statsTimer.Stop()
	}
	e.statsTimer = time.AfterFunc(statsDelay, func() {
		fyne.Do(func() {
			if e.doc == nil || e.info == nil {
				return
			}
			e.updateStats()
			e.updateInfo()
		})
	})
}

// updateStats recomputes the cached word/character counts. A document big enough
// to make the scan noticeable is counted off the UI goroutine instead, and the
// readout leaves the numbers out until they arrive.
func (e *Editor) updateStats() {
	if e.doc == nil {
		e.statChars, e.statWords, e.statsKnown = 0, 0, false
		return
	}
	if e.doc.Len() > statsSyncLimit {
		e.countStatsAsync()
		return
	}
	e.statChars, e.statWords = e.doc.counts()
	e.statsKnown = true
}

// countStatsAsync counts a big document off the UI goroutine. The count is
// dropped if the document changed while it ran: the next pause counts again.
func (e *Editor) countStatsAsync() {
	doc, seq := e.doc.Snapshot(), e.editSeq
	go func() {
		chars, words := doc.counts()
		fyne.Do(func() {
			if e.doc == nil || e.editSeq != seq {
				return
			}
			e.statChars, e.statWords, e.statsKnown = chars, words, true
			e.updateInfo()
		})
	}()
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
	title := appTitle + " — " + name + mark
	// Setting the same title again makes the window do nothing useful, and the
	// readout below is updated on every keystroke.
	if title == e.lastTitle {
		return
	}
	e.lastTitle = title
	e.win.SetTitle(title)
}

// updateInfo refreshes the compact cursor/document readout in the status bar.
func (e *Editor) updateInfo() {
	if e.info == nil || e.code == nil {
		return
	}
	line, col := e.caretLineCol()
	pos := fmt.Sprintf("%d:%d", line, col)
	tail := fmt.Sprintf("%s · %s%s · %d%%", e.eolLabel(), e.indentLabel(),
		e.encodingSuffix(), zoomPercent(e.themes.Zoom()))
	var out string
	switch {
	case !e.statsKnown:
		// The counts of a big document are still being worked out.
		out = pos + " · " + tail
	case e.largeFile:
		out = fmt.Sprintf("%s · %dw · %dc · %s · large", pos, e.statWords, e.statChars, tail)
	default:
		out = fmt.Sprintf("%s · %dw · %dc · %s", pos, e.statWords, e.statChars, tail)
	}
	if out == e.lastInfo {
		return
	}
	e.lastInfo = out
	e.info.SetText(out)
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

// Undo reverts the last edit.
func (e *Editor) Undo() {
	if e.code != nil {
		e.code.Undo()
	}
}

// Redo reapplies the last undone edit.
func (e *Editor) Redo() {
	if e.code != nil {
		e.code.Redo()
	}
}

// SelectAll selects the entire buffer.
// SelectAll selects the whole document.
func (e *Editor) SelectAll() {
	if e.code != nil {
		e.code.selectAll()
	}
}

// clipboard routes a cut/copy/paste shortcut through the entry with the
// window's clipboard attached.
func (e *Editor) clipboard(sc fyne.Shortcut) {
	if e.code != nil {
		e.code.TypedShortcut(sc)
	}
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
	if e.code != nil {
		e.code.moveLineUp()
	}
}

// MoveLineDown swaps the caret's line with the one below it.
func (e *Editor) MoveLineDown() {
	if e.code != nil {
		e.code.moveLineDown()
	}
}

// DuplicateLine copies the caret's line beneath itself.
func (e *Editor) DuplicateLine() {
	if e.code != nil {
		e.code.duplicateLine()
	}
}

// IndentLine adds one indentation level to the caret's line.
func (e *Editor) IndentLine() {
	if e.code != nil {
		e.code.indent()
	}
}

// OutdentLine removes one indentation level from the caret's line.
func (e *Editor) OutdentLine() {
	if e.code != nil {
		e.code.outdent()
	}
}

// applyLineEdit installs a whole-buffer result, keeping the caret in place.
// widget.Entry has no range-edit API, so line operations rewrite via SetText,
// which clears Entry's own history — the snapshot is what makes them undoable.

func (e *Editor) applyWrap() {
	if e.code != nil {
		// The code view wraps itself: it splits a line into visual rows of the
		// width that fits, so a large file can be wrapped too.
		e.code.setWrap(e.wrap)
	}
	if e.wrapItem != nil {
		e.wrapItem.Checked = e.wrap
		if menu := e.win.MainMenu(); menu != nil {
			menu.Refresh()
		}
	}
}

// zoom adjusts the font zoom by direction (+1 in, -1 out).
func (e *Editor) zoom(direction int) {
	if direction > 0 {
		e.themes.ZoomIn()
	} else {
		e.themes.ZoomOut()
	}
	e.refreshMetrics()
	e.updateInfo()
	e.setStatus(fmt.Sprintf("zoom %d%%", zoomPercent(e.themes.Zoom())))
}

// ResetZoom restores the font zoom to 100%.
func (e *Editor) ResetZoom() {
	e.themes.ResetZoom()
	e.refreshMetrics()
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

// refreshMetrics re-measures the editing surface after a theme or zoom change:
// a line is a different number of pixels tall, so every row moves.
func (e *Editor) refreshMetrics() {
	if e.code != nil {
		e.code.refreshMetrics()
	}
}

func (e *Editor) syncTheme() {
	for n, item := range e.themeItems {
		item.Checked = n == e.themes.Current()
	}
	if menu := e.win.MainMenu(); menu != nil {
		menu.Refresh()
	}
	e.refreshMetrics()
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
