package editor

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
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
