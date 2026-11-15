package editor

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// openFile opens path in a tab and waits for it.
func openFile(t *testing.T, e *Editor, path string) {
	t.Helper()
	e.startOpen(path, openNormal, nil)
	waitFor(t, "the open of "+path, func() bool { return e.currentFile == path })
}

// writeFile writes a file into a temporary directory.
func writeFile(t *testing.T, name, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Each file opened gets a tab, and the one before it keeps its text and caret.
func TestTabsKeepTheirBuffers(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "one\ntwo\nthree\n")
	b := writeFile(t, "b.txt", "alpha\nbeta\n")

	openFile(t, e, a)
	e.code.setCaret(pos{row: 1, col: 2})
	openFile(t, e, b)

	if got := e.TabCount(); got != 2 {
		t.Fatalf("%d tabs, want 2", got)
	}
	if got := e.text(); got != "alpha\nbeta\n" {
		t.Fatalf("the second tab holds %q", got)
	}
	if got := e.TabTitles(); got[0] != "a.txt" || got[1] != "b.txt" {
		t.Fatalf("titles = %v", got)
	}
	if e.ActiveTab() != 1 {
		t.Fatalf("active tab = %d, want the one just opened", e.ActiveTab())
	}

	e.activate(0)
	if got := e.text(); got != "one\ntwo\nthree\n" {
		t.Fatalf("the first tab holds %q", got)
	}
	if row, col := caret(e); row != 1 || col != 2 {
		t.Fatalf("caret came back at %d:%d, want 1:2", row, col)
	}
	if e.currentFile != a {
		t.Fatalf("current file = %q", e.currentFile)
	}
}

// Cycling goes forward and wraps, and Shift+Tab backwards.
func TestTabsCycle(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	c := writeFile(t, "c.txt", "c\n")
	openFile(t, e, a)
	openFile(t, e, b)
	openFile(t, e, c)

	e.NextTab()
	if e.currentFile != a {
		t.Fatalf("next from the last tab went to %q, want the first", e.currentFile)
	}
	e.PrevTab()
	if e.currentFile != c {
		t.Fatalf("previous from the first tab went to %q, want the last", e.currentFile)
	}
	e.activate(1)
	if e.currentFile != b {
		t.Fatalf("activate(1) shows %q", e.currentFile)
	}
}

// Opening a file that is already open shows its tab rather than reading it again.
func TestOpenReusesAnOpenTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "first\n")
	b := writeFile(t, "b.txt", "second\n")
	openFile(t, e, a)
	openFile(t, e, b)

	// Change the buffer in its tab, then ask for the same file again.
	e.activate(0)
	text(e, "edited ")
	openFile(t, e, b)
	if got := e.TabCount(); got != 2 {
		t.Fatalf("%d tabs after reopening an open file, want 2", got)
	}
	e.activate(0)
	if got := e.text(); got != "edited first\n" {
		t.Fatalf("buffer = %q, the second open must not reload it", got)
	}
}

// Ctrl+N gives a new, empty tab, and does not stack empty ones.
func TestNewFileAddsATab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "one\n")
	openFile(t, e, a)

	e.NewFile()
	if got := e.TabCount(); got != 2 {
		t.Fatalf("%d tabs after New, want 2", got)
	}
	if e.currentFile != "" || e.text() != "" {
		t.Fatalf("the new tab holds %q of %q", e.text(), e.currentFile)
	}
	// A second New while the empty buffer is untouched stays at two.
	e.NewFile()
	if got := e.TabCount(); got != 2 {
		t.Fatalf("%d tabs after a second New, want 2", got)
	}
}

// A file opened into a fresh window takes the place of its empty buffer.
func TestFirstOpenReplacesTheEmptyTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "one\n")
	openFile(t, e, a)
	if got := e.TabCount(); got != 1 {
		t.Fatalf("%d tabs, want the empty buffer to be replaced", got)
	}
}

// Closing a tab shows its neighbour, and the editor never ends up with no tab.
func TestCloseTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	openFile(t, e, b)

	e.activate(0)
	e.CloseTab()
	if got := e.TabCount(); got != 1 {
		t.Fatalf("%d tabs after closing one, want 1", got)
	}
	if e.currentFile != b {
		t.Fatalf("showing %q after closing the first tab, want the second", e.currentFile)
	}
	e.CloseTab()
	if got := e.TabCount(); got != 1 {
		t.Fatalf("%d tabs after closing the last one, want an empty buffer", got)
	}
	if e.currentFile != "" || e.text() != "" {
		t.Fatalf("the last tab holds %q of %q", e.text(), e.currentFile)
	}
}

// Closing a tab that is not the active one keeps the active one where it is.
func TestCloseBackgroundTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	c := writeFile(t, "c.txt", "c\n")
	openFile(t, e, a)
	openFile(t, e, b)
	openFile(t, e, c)

	e.closeTab(0, nil)
	if e.currentFile != c {
		t.Fatalf("showing %q after closing a background tab, want the active one", e.currentFile)
	}
	if got := e.TabTitles(); len(got) != 2 || got[0] != "b.txt" || got[1] != "c.txt" {
		t.Fatalf("titles = %v", got)
	}
}

// Undo history belongs to the buffer, not the window.
func TestUndoIsPerTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	text(e, "AAA")
	openFile(t, e, b)
	text(e, "BBB")

	e.activate(0)
	e.Undo()
	if got := e.text(); got != "a\n" {
		t.Fatalf("undo in the first tab gave %q", got)
	}
	e.activate(1)
	e.Undo()
	if got := e.text(); got != "b\n" {
		t.Fatalf("undo in the second tab gave %q", got)
	}
}

// Scrolling is remembered per tab too.
func TestScrollIsPerTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", codeText(5000))
	b := writeFile(t, "b.txt", codeText(5000))
	openFile(t, e, a)
	e.code.scrollLines(50)
	top := e.code.topRow
	if top == 0 {
		t.Fatal("the view did not scroll")
	}
	openFile(t, e, b)
	if e.code.topRow != 0 {
		t.Fatalf("the new tab starts at row %d", e.code.topRow)
	}
	e.activate(0)
	if e.code.topRow != top {
		t.Fatalf("the first tab came back at row %d, want %d", e.code.topRow, top)
	}
}

// Every buffer with unsaved changes is named before quitting throws them away.
func TestDirtyTabsAreAllReported(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	text(e, "x")
	openFile(t, e, b)
	text(e, "y")

	if got := e.dirtyTabs(); len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Fatalf("dirty tabs = %v", got)
	}
	if !e.TabDirty(0) || !e.TabDirty(1) {
		t.Fatal("both tabs should report unsaved changes")
	}
	// Saving one clears only its own mark.
	e.SaveFile()
	waitFor(t, "the save", func() bool { return !e.TabDirty(1) })
	if e.TabDirty(1) {
		t.Fatal("the saved tab is still dirty")
	}
	if !e.TabDirty(0) {
		t.Fatal("the other tab lost its changes with it")
	}
	msg := quitMessage(e.dirtyTabs())
	if !contains(msg, "a.txt") || contains(msg, "b.txt") {
		t.Fatalf("quit message = %q", msg)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// The window title says which tab of how many is shown.
func TestTitleNamesTheTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	openFile(t, e, b)
	if got := e.win.Title(); got != "Gaze — b.txt (2/2)" {
		t.Fatalf("title = %q", got)
	}
	e.activate(0)
	if got := e.win.Title(); got != "Gaze — a.txt (1/2)" {
		t.Fatalf("title = %q", got)
	}
}

// Switching tabs redraws at the right size and keeps the widget usable.
func TestSwitchTabsKeepsTheViewSane(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	openFile(t, e, b)
	e.win.Resize(fyne.NewSize(700, 400))
	e.win.Content().Resize(fyne.NewSize(700, 400))
	e.activate(0)
	e.code.render()
	if got := e.code.textCount(); got == 0 {
		t.Fatal("the view draws no text after switching tabs")
	}
	if e.code.doc != e.doc {
		t.Fatal("the view and the editor disagree about the document")
	}
}

// The bar only appears once there is something to switch between.
func TestTabBarAppearsWithASecondTab(t *testing.T) {
	e := newTestEditor(t)
	if e.tabBar.Visible() {
		t.Fatal("the tab bar shows with a single buffer")
	}
	a := writeFile(t, "a.txt", "a\n")
	openFile(t, e, a)
	if e.tabBar.Visible() {
		t.Fatal("the tab bar shows after one file")
	}
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, b)
	if !e.tabBar.Visible() {
		t.Fatal("the tab bar is hidden with two buffers")
	}
	if got := len(e.tabBox.Objects); got != 2 {
		t.Fatalf("%d chips, want 2", got)
	}
	openFile(t, e, a) // shows the open tab rather than adding one
	if got := len(e.tabBox.Objects); got != 2 {
		t.Fatalf("%d chips after reopening an open file, want 2", got)
	}
	e.CloseTab()
	e.activate(0)
	e.CloseTab()
	if e.tabBar.Visible() {
		t.Fatal("the tab bar stayed after the last file was closed")
	}
}

// The active chip is drawn differently from the others, and carries the dot when
// its buffer has unsaved changes.
func TestTabBarMarksTheActiveBuffer(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	openFile(t, e, b)
	e.activate(0)

	chips := e.tabBox.Objects
	active, other := chips[0].(*tabChip), chips[1].(*tabChip)
	if active.index != 0 || other.index != 1 {
		t.Fatalf("chips are at %d and %d", active.index, other.index)
	}
	// The active chip is filled; the inactive one is not.
	activeBG := test.WidgetRenderer(active).Objects()[0].(*canvas.Rectangle)
	otherBG := test.WidgetRenderer(other).Objects()[0].(*canvas.Rectangle)
	if activeBG.FillColor == otherBG.FillColor {
		t.Fatal("the active and inactive chips are drawn the same")
	}
	label := test.WidgetRenderer(active).Objects()[1].(*canvas.Text)
	if label.Text != "a.txt" {
		t.Fatalf("the chip reads %q", label.Text)
	}
	text(e, "x")
	label = test.WidgetRenderer(active).Objects()[1].(*canvas.Text)
	if label.Text != "a.txt •" {
		t.Fatalf("the chip reads %q after an edit", label.Text)
	}
}

// Tapping a chip switches to it, and tapping its cross closes it.
func TestTabBarTaps(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\n")
	openFile(t, e, a)
	openFile(t, e, b)
	e.activate(0)

	chips := e.tabBox.Objects
	tap := func(o fyne.CanvasObject) fyne.Tappable { return o.(fyne.Tappable) }
	test.Tap(tap(chips[1]))
	if e.currentFile != b {
		t.Fatalf("tapping the second chip showed %q", e.currentFile)
	}
	// The right-hand end of a chip is its close cross.
	e.activate(1)
	test.TapAt(tap(chips[1]), fyne.NewPos(chips[1].Size().Width-2, 8))
	if got := e.TabCount(); got != 1 {
		t.Fatalf("%d tabs after tapping the cross, want 1", got)
	}
	if e.currentFile != a {
		t.Fatalf("showing %q after closing the other tab", e.currentFile)
	}
}

// A launch brings back the whole set of files, in order, with the one that was
// being edited active.
func TestSessionRestoreAllTabs(t *testing.T) {
	a := writeFile(t, "a.txt", "a\n")
	b := writeFile(t, "b.txt", "b\nb2\nb3\n")
	c := writeFile(t, "c.txt", "c\n")

	a1 := test.NewApp()
	w1 := test.NewWindow(nil)
	e1 := New(a1, w1)
	e1.BuildUI()
	e1.OpenPath(a)
	e1.OpenPath(b)
	e1.OpenPath(c)
	e1.activate(1)
	e1.code.setCaret(pos{row: 2, col: 1})
	e1.saveSession()

	w2 := test.NewWindow(nil)
	e2 := New(a1, w2)
	e2.BuildUI()
	if !e2.RestoreSession() {
		t.Fatal("the session was not restored")
	}
	waitFor(t, "the tabs to come back", func() bool { return e2.TabCount() == 3 })
	if got := e2.TabPaths(); got[0] != a || got[1] != b || got[2] != c {
		t.Fatalf("restored tabs = %v", got)
	}
	if e2.currentFile != b {
		t.Fatalf("the active tab is %q, want the second", e2.currentFile)
	}
	if row, col := caret(e2); row != 2 || col != 1 {
		t.Fatalf("caret came back at %d:%d, want 2:1", row, col)
	}
}

// A file that has gone since the last session is skipped rather than reported.
func TestSessionRestoreSkipsMissingFiles(t *testing.T) {
	a := writeFile(t, "a.txt", "a\n")
	gone := filepath.Join(t.TempDir(), "gone.txt")
	if err := os.WriteFile(gone, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := test.NewApp()
	e1 := New(app, test.NewWindow(nil))
	e1.BuildUI()
	e1.OpenPath(a)
	e1.OpenPath(gone)
	e1.saveSession()
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	e2 := New(app, test.NewWindow(nil))
	e2.BuildUI()
	if !e2.RestoreSession() {
		t.Fatal("the remaining file was not restored")
	}
	waitFor(t, "the tab to come back", func() bool { return e2.TabCount() == 1 })
	if e2.currentFile != a {
		t.Fatalf("showing %q", e2.currentFile)
	}
}

// A session saved by an older version, which only knew one file, still restores.
func TestSessionRestoreSingleFilePreference(t *testing.T) {
	a := writeFile(t, "a.txt", "a\nb\n")
	app := test.NewApp()
	p := app.Preferences()
	p.SetString(prefLastFile, a)
	p.SetInt(prefLastRow, 1)

	e := New(app, test.NewWindow(nil))
	e.BuildUI()
	if !e.RestoreSession() {
		t.Fatal("the old session was not restored")
	}
	waitFor(t, "the file to open", func() bool { return e.currentFile == a })
	if got := e.TabCount(); got != 1 {
		t.Fatalf("%d tabs, want 1", got)
	}
	if row, _ := caret(e); row != 1 {
		t.Fatalf("caret at row %d, want 1", row)
	}
}

// An untitled buffer cannot be restored, and does not stop the files from coming
// back.
func TestSessionRestoreIgnoresUntitled(t *testing.T) {
	a := writeFile(t, "a.txt", "a\n")
	app := test.NewApp()
	e1 := New(app, test.NewWindow(nil))
	e1.BuildUI()
	e1.OpenPath(a)
	e1.NewFile()
	text(e1, "unsaved draft")
	e1.saveSession()

	e2 := New(app, test.NewWindow(nil))
	e2.BuildUI()
	e2.RestoreSession()
	waitFor(t, "the file to come back", func() bool { return e2.TabCount() == 1 && e2.currentFile == a })
}

// A save that finishes after the user switched tabs must land on the buffer it was
// started for, not on the one that is now on screen.
func TestSaveAppliesToItsOwnTab(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "one\n")
	b := writeFile(t, "b.txt", "two\n")
	openFile(t, e, a)
	text(e, "A")
	req := e.pendingSave(a) // the write is in flight
	openFile(t, e, b)
	text(e, "B")

	e.finishSave(req, "")

	if e.currentFile != b {
		t.Fatalf("the save renamed the visible buffer to %q", e.currentFile)
	}
	if !e.IsDirty() {
		t.Fatal("the visible buffer lost its unsaved changes to another buffer's save")
	}
	if got := e.text(); got != "Btwo\n" {
		t.Fatalf("the visible buffer = %q", got)
	}
	e.activate(0)
	if e.IsDirty() {
		t.Fatal("the saved buffer is still marked dirty")
	}
	// The saved tab took the path it was written to.
	if e.tabs[0].path != a {
		t.Fatalf("the saved tab is %q", e.tabs[0].path)
	}
}

// Edits typed into a buffer while its own save was in flight leave it dirty.
func TestEditsDuringSaveLeaveTheTabDirty(t *testing.T) {
	e := newTestEditor(t)
	a := writeFile(t, "a.txt", "one\n")
	openFile(t, e, a)
	req := e.pendingSave(a)
	text(e, "more ")
	e.finishSave(req, "")
	if !e.IsDirty() {
		t.Fatal("edits made during the save were reported as saved")
	}
	if got := e.text(); got != "more one\n" {
		t.Fatalf("buffer = %q", got)
	}
}
