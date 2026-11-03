package editor

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"gaze/internal/theme"
)

func newTestEditor(t *testing.T) *Editor {
	t.Helper()
	a := test.NewApp()
	w := test.NewWindow(nil)
	e := New(a, w)
	e.BuildUI()
	w.Resize(fyne.NewSize(900, 650))
	// Recovery snapshots are exercised explicitly by the tests that care. Left
	// on, a test that types into an untitled buffer would write to the shared
	// draft slot from a background goroutine while another test inspects it.
	e.autosaveOn = false
	return e
}

// setBuffer installs text in the editor the way a loaded file would, and puts
// the caret at the top.
func setBuffer(e *Editor, text string) {
	e.setContentAs(text, e.currentFile, e.enc, openNormal)
	e.code.setCaret(pos{})
}

// bufferText is the whole buffer as a string.
func bufferText(e *Editor) string { return e.text() }

// caret is the caret position, 0-based.
func caret(e *Editor) (row, col int) {
	if e.code == nil {
		return 0, 0
	}
	return e.code.caret.row, e.code.caret.col
}

func TestThemeSwitching(t *testing.T) {
	e := newTestEditor(t)

	for _, name := range theme.Names {
		e.SetTheme(name)
		if got := e.ThemeName(); got != name {
			t.Fatalf("theme = %q, want %q", got, name)
		}
		if !e.themeItems[name].Checked {
			t.Fatalf("menu item %q should be checked", name)
		}
	}

	e.ToggleTheme()
	if got := e.ThemeName(); got != theme.NameLight {
		t.Fatalf("toggle from dark = %q, want %q", got, theme.NameLight)
	}
	e.CycleTheme()
	if got := e.ThemeName(); got != theme.NameDark {
		t.Fatalf("cycle after light = %q, want %q", got, theme.NameDark)
	}
}

func TestDirtyTrackingAndStats(t *testing.T) {
	e := newTestEditor(t)

	if e.dirty {
		t.Fatal("new editor should be clean")
	}
	test.Type(e.code, "hello world\nsecond")
	if !e.dirty {
		t.Fatal("typing should mark the buffer dirty")
	}
	if got := e.win.Title(); !strings.HasSuffix(got, "•") {
		t.Fatalf("window title %q should flag unsaved changes", got)
	}
	if e.statWords != 3 {
		t.Fatalf("word count = %d, want 3", e.statWords)
	}
	if e.info.Text == "" {
		t.Fatal("position readout should not be empty")
	}

	e.SetContent("clean", "")
	if e.dirty {
		t.Fatal("SetContent should reset the dirty flag")
	}
}

func TestFindMovesCursor(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("alpha\nbeta\ngamma\nbeta\n", "")

	e.findEntry.SetText("beta")
	e.FindNext()
	if caretRow(e) != 1 {
		t.Fatalf("first match row = %d, want 1", caretRow(e))
	}
	e.FindNext()
	if caretRow(e) != 3 {
		t.Fatalf("second match row = %d, want 3", caretRow(e))
	}
	e.FindNext() // wraps
	if caretRow(e) != 1 {
		t.Fatalf("wrapped match row = %d, want 1", caretRow(e))
	}
}

func TestReplaceAll(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("cat dog CAT", "")
	e.findEntry.SetText("cat")
	e.replEntry.SetText("fox")

	e.ReplaceAll()
	if bufferText(e) != "fox dog fox" {
		t.Fatalf("ReplaceAll = %q, want %q", bufferText(e), "fox dog fox")
	}

	e.matchCase.Checked = true
	// Reset and check case-sensitive matching.
	e.SetContent("cat dog CAT", "")
	e.ReplaceAll()
	if bufferText(e) != "fox dog CAT" {
		t.Fatalf("case-sensitive ReplaceAll = %q, want %q", bufferText(e), "fox dog CAT")
	}
}

func TestGoToLine(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("one\ntwo\nthree", "")
	e.gotoLine(3)
	if caretRow(e) != 2 || caretCol(e) != 0 {
		t.Fatalf("gotoLine(3) = (%d,%d), want (2,0)", caretRow(e), caretCol(e))
	}
	e.gotoLine(99) // clamps
	if caretRow(e) != 2 {
		t.Fatalf("gotoLine(99) row = %d, want 2", caretRow(e))
	}
}

func TestWordWrapToggle(t *testing.T) {
	e := newTestEditor(t)
	start := e.wrap
	e.ToggleWrap()
	if e.wrap == start {
		t.Fatal("ToggleWrap should flip the flag")
	}
	if e.wrapItem.Checked != e.wrap {
		t.Fatal("wrap menu check should follow the flag")
	}
	if e.code.wrap != e.wrap {
		t.Fatalf("code view wrapping = %v, want %v", e.code.wrap, e.wrap)
	}
	// Wrapping is checked against a line that is wider than the window.
	if !e.wrap {
		e.ToggleWrap()
	}
	e.SetContent("", "")
	setBuffer(e, strings.Repeat("x", 400))
	if rows := e.code.totalRows(); rows < 2 {
		t.Fatalf("a 400 cell line took %d rows, want it wrapped", rows)
	}
	e.ToggleWrap()
	if got := e.code.totalRows(); got != 1 {
		t.Fatalf("after turning wrap off the line takes %d rows, want 1", got)
	}
}

func TestZoom(t *testing.T) {
	e := newTestEditor(t)
	e.zoom(1)
	if e.themes.Zoom() <= 1 {
		t.Fatalf("zoom in = %v, want > 1", e.themes.Zoom())
	}
	e.ResetZoom()
	if e.themes.Zoom() != 1 {
		t.Fatalf("reset zoom = %v, want 1", e.themes.Zoom())
	}
}

func TestTooltipOnHover(t *testing.T) {
	e := newTestEditor(t)
	tapped := false
	btn := newTipButton("Save", nil, "Save (Ctrl+S)", func() { tapped = true })
	e.bindTips(btn)

	if e.tipLayer.Visible() {
		t.Fatal("tooltip layer should start hidden")
	}
	if btn.MinSize().Width <= 0 {
		t.Fatal("button should have a non-zero minimum size")
	}
	btn.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{AbsolutePosition: fyne.NewPos(20, 20)}})
	if !e.tipLayer.Visible() {
		t.Fatal("hover should reveal the tooltip")
	}
	if e.tipText.Text != "Save (Ctrl+S)" {
		t.Fatalf("tooltip text = %q, want %q", e.tipText.Text, "Save (Ctrl+S)")
	}

	btn.Tapped(&fyne.PointEvent{})
	if !tapped {
		t.Fatal("tap should run the button action")
	}
	if e.tipLayer.Visible() {
		t.Fatal("tap should hide the tooltip")
	}

	btn.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{AbsolutePosition: fyne.NewPos(20, 20)}})
	btn.MouseOut()
	if e.tipLayer.Visible() {
		t.Fatal("mouse out should hide the tooltip")
	}
}

// TestTooltipViaCanvasHover checks that Fyne's hit testing actually routes
// mouse movement to the toolbar button (rather than only direct calls).
func TestTooltipViaCanvasHover(t *testing.T) {
	a := test.NewApp()
	w := test.NewWindow(nil)
	defer w.Close()

	e := &Editor{app: a, win: w}
	e.buildTipLayer()
	tapped := false
	btn := newTipButton("Save", nil, "Save (Ctrl+S)", func() { tapped = true })
	e.bindTips(btn)
	w.SetContent(container.NewStack(container.NewCenter(btn), e.tipLayer))
	w.Resize(fyne.NewSize(240, 80))

	size := w.Canvas().Size()
	center := fyne.NewPos(size.Width/2, size.Height/2)
	test.MoveMouse(w.Canvas(), center)
	if !e.tipLayer.Visible() {
		t.Fatal("hovering the button on the canvas should reveal the tooltip")
	}
	if e.tipText.Text != "Save (Ctrl+S)" {
		t.Fatalf("tooltip text = %q, want %q", e.tipText.Text, "Save (Ctrl+S)")
	}

	// The tooltip must not swallow clicks meant for the button beneath it.
	test.TapCanvas(w.Canvas(), center)
	if !tapped {
		t.Fatal("clicking through a visible tooltip should still tap the button")
	}
	if e.tipLayer.Visible() {
		t.Fatal("tapping a button should hide its tooltip")
	}
}

func TestTextHelpers(t *testing.T) {
	text := "ab\ncd"
	if got := runeOffset(text, 1, 1); got != 4 {
		t.Fatalf("runeOffset = %d, want 4", got)
	}
	row, col := rowColFromOffset(text, 4)
	if row != 1 || col != 1 {
		t.Fatalf("rowColFromOffset = (%d,%d), want (1,1)", row, col)
	}
	if got := findFrom("Hello hello", "hello", 0, false); got != 0 {
		t.Fatalf("findFrom fold = %d, want 0", got)
	}
	if got := findFrom("Hello hello", "hello", 0, true); got != 6 {
		t.Fatalf("findFrom case = %d, want 6", got)
	}
	if got := countWords("  one two   three "); got != 3 {
		t.Fatalf("countWords = %d, want 3", got)
	}
	if got := lineCount("a\nb\nc"); got != 3 {
		t.Fatalf("lineCount = %d, want 3", got)
	}
	if got := lineCount(""); got != 1 {
		t.Fatalf("lineCount empty = %d, want 1", got)
	}
}

// caretRow and caretCol are the 0-based caret position.
func caretRow(e *Editor) int {
	row, _ := caret(e)
	return row
}

func caretCol(e *Editor) int {
	_, col := caret(e)
	return col
}
