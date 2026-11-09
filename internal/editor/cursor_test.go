package editor

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// The pointer over the scrollbar is an arrow, not the text caret: the bar is
// drawn by this widget, so without this the editing cursor stays over it.
func TestCursorOverScrollbar(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, strings.Repeat("a line of text\n", 400))
	e.win.Resize(fyne.NewSize(600, 300))
	e.win.Content().Resize(fyne.NewSize(600, 300))
	e.code.render()

	v := e.code
	width, height := v.Size().Width, v.Size().Height
	barX := width - v.barWidth()
	if barX <= 0 || barX >= width {
		t.Fatalf("the scrollbar gutter starts at %v of %v", barX, width)
	}

	inText := &desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(10, height/2)}}
	onBar := &desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(barX+2, height/2)}}

	v.MouseIn(inText)
	if got := v.Cursor(); got != desktop.TextCursor {
		t.Fatalf("cursor over the text = %v, want the text caret", got)
	}
	v.MouseMoved(onBar)
	if got := v.Cursor(); got != desktop.DefaultCursor {
		t.Fatalf("cursor over the scrollbar = %v, want the default arrow", got)
	}
	v.MouseMoved(inText)
	if got := v.Cursor(); got != desktop.TextCursor {
		t.Fatalf("cursor back over the text = %v, want the text caret", got)
	}
	v.MouseOut()
	if got := v.Cursor(); got != desktop.TextCursor {
		t.Fatalf("cursor after leaving = %v, want the text caret", got)
	}

	// A document that fits has no bar, so its gutter is not the bar either.
	setBuffer(e, "short\n")
	e.code.render()
	v.MouseMoved(onBar)
	if v.Cursor() != desktop.TextCursor {
		t.Fatal("the cursor over an empty gutter is not a text caret")
	}
}
