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
	return e
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
	test.Type(e.entry, "hello world\nsecond")
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
	if e.entry.CursorRow != 1 {
		t.Fatalf("first match row = %d, want 1", e.entry.CursorRow)
	}
	e.FindNext()
	if e.entry.CursorRow != 3 {
		t.Fatalf("second match row = %d, want 3", e.entry.CursorRow)
	}
	e.FindNext() // wraps
	if e.entry.CursorRow != 1 {
		t.Fatalf("wrapped match row = %d, want 1", e.entry.CursorRow)
	}
}

func TestReplaceAll(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("cat dog CAT", "")
	e.findEntry.SetText("cat")
	e.replEntry.SetText("fox")

	e.ReplaceAll()
	if e.entry.Text != "fox dog fox" {
		t.Fatalf("ReplaceAll = %q, want %q", e.entry.Text, "fox dog fox")
	}

	e.matchCase.Checked = true
	// Reset and check case-sensitive matching.
	e.SetContent("cat dog CAT", "")
	e.ReplaceAll()
	if e.entry.Text != "fox dog CAT" {
		t.Fatalf("case-sensitive ReplaceAll = %q, want %q", e.entry.Text, "fox dog CAT")
	}
}

func TestGoToLine(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("one\ntwo\nthree", "")
	e.gotoLine(3)
	if e.entry.CursorRow != 2 || e.entry.CursorColumn != 0 {
		t.Fatalf("gotoLine(3) = (%d,%d), want (2,0)", e.entry.CursorRow, e.entry.CursorColumn)
	}
	e.gotoLine(99) // clamps
	if e.entry.CursorRow != 2 {
		t.Fatalf("gotoLine(99) row = %d, want 2", e.entry.CursorRow)
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
	want := fyne.TextWrapWord
	if !e.wrap {
		want = fyne.TextWrapOff
	}
	if e.entry.Wrapping != want {
		t.Fatalf("entry wrapping = %v, want %v", e.entry.Wrapping, want)
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
