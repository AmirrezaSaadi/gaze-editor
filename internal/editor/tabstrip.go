package editor

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The tab bar: one chip per open buffer, the active one filled in, each with a
// cross to close it.

const (
	// tabChipPad is the space around a chip's contents.
	tabChipPad = 8
	// tabChipClose is the width of the close cross, and how much of the right-hand
	// end of a chip closes it.
	tabChipClose = 20
	// tabTitleMax is how many characters of a name a chip shows before it is
	// shortened, so one long path cannot push the others off the bar.
	tabTitleMax = 20
)

// tabChip is one entry in the tab bar.
type tabChip struct {
	widget.BaseWidget

	ed    *Editor
	index int

	hovered bool
}

func newTabChip(ed *Editor, index int) *tabChip {
	c := &tabChip{ed: ed, index: index}
	c.ExtendBaseWidget(c)
	return c
}

// CreateRenderer builds the chip's two colours and its two pieces of text.
func (c *tabChip) CreateRenderer() fyne.WidgetRenderer {
	r := &chipRenderer{chip: c, label: canvas.NewText("", color.White), cross: canvas.NewText("×", color.White)}
	r.bg = canvas.NewRectangle(color.Transparent)
	r.bg.CornerRadius = 4
	r.label.TextSize = theme.Size(theme.SizeNameText) - 1
	r.cross.TextSize = theme.Size(theme.SizeNameText)
	return r
}

// Tapped selects the chip, or closes the buffer when the cross was hit.
func (c *tabChip) Tapped(ev *fyne.PointEvent) {
	if ev.Position.X >= c.Size().Width-tabChipClose {
		c.ed.closeTab(c.index, nil)
		return
	}
	c.ed.activate(c.index)
}

// MouseIn, MouseMoved and MouseOut light the chip under the pointer.
func (c *tabChip) MouseIn(*desktop.MouseEvent)    { c.setHover(true) }
func (c *tabChip) MouseMoved(*desktop.MouseEvent) {}
func (c *tabChip) MouseOut()                      { c.setHover(false) }

// Cursor is the pointing hand, because a chip is something you click.
func (c *tabChip) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (c *tabChip) setHover(hover bool) {
	if c.hovered == hover {
		return
	}
	c.hovered = hover
	c.Refresh()
}

// chipRenderer draws a chip: a background, the name and the close cross.
type chipRenderer struct {
	chip  *tabChip
	bg    *canvas.Rectangle
	label *canvas.Text
	cross *canvas.Text
}

func (r *chipRenderer) Layout(size fyne.Size) {
	r.bg.Move(fyne.NewPos(0, 0))
	r.bg.Resize(size)
	lineH := theme.Size(theme.SizeNameText) + 2
	labelW := size.Width - tabChipClose - 2*tabChipPad
	if labelW < 0 {
		labelW = 0
	}
	r.label.Resize(fyne.NewSize(labelW, lineH))
	r.label.Move(fyne.NewPos(tabChipPad, (size.Height-lineH)/2))
	r.cross.Resize(fyne.NewSize(tabChipClose, lineH))
	r.cross.Move(fyne.NewPos(size.Width-tabChipClose, (size.Height-lineH)/2))
}

func (r *chipRenderer) MinSize() fyne.Size {
	name := r.chip.ed.tabTitle(r.chip.index)
	w := fyne.MeasureText(name, r.label.TextSize, fyne.TextStyle{}).Width
	return fyne.NewSize(w+tabChipClose+2*tabChipPad, theme.Size(theme.SizeNameText)+2*tabChipPad)
}

// Refresh reads the current state: which buffer this is, whether it is the one being
// edited, whether it has unsaved changes and whether the pointer is on it.
func (r *chipRenderer) Refresh() {
	c := r.chip
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	th := c.Theme()
	fg := th.Color(theme.ColorNameForeground, variant)
	dim := th.Color(theme.ColorNameDisabled, variant)
	r.bg.FillColor = color.Transparent
	r.label.Color = dim
	if c.hovered {
		r.bg.FillColor = th.Color(theme.ColorNameHover, variant)
		r.label.Color = fg
	}
	if c.ed.ActiveTab() == c.index {
		r.bg.FillColor = th.Color(theme.ColorNameSelection, variant)
		r.label.Color = fg
	}
	name := c.ed.tabTitle(c.index)
	if c.ed.TabDirty(c.index) {
		name += " •"
	}
	r.label.Text = shorten(name, tabTitleMax)
	r.cross.Color = dim
	if c.hovered || c.ed.ActiveTab() == c.index {
		r.cross.Color = fg
	}
	r.bg.Refresh()
	r.label.Refresh()
	r.cross.Refresh()
}

func (r *chipRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.label, r.cross}
}

func (r *chipRenderer) Destroy() {}

// shorten cuts s to at most n characters, with an ellipsis when something was cut.
func shorten(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n < 2 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// tabTitle is what the tab bar calls the buffer at index i.
func (e *Editor) tabTitle(i int) string {
	if i < 0 || i >= len(e.tabs) {
		return ""
	}
	return e.tabs[i].title()
}

// refreshTabs rebuilds the tab bar. It runs when a buffer is opened, closed or
// switched, not on every keystroke.
func (e *Editor) refreshTabs() {
	if e.tabBox == nil || e.tabBar == nil {
		return
	}
	chips := make([]fyne.CanvasObject, len(e.tabs))
	for i := range e.tabs {
		chips[i] = newTabChip(e, i)
	}
	e.tabBox.Objects = chips
	e.tabBox.Refresh()
	if len(e.tabs) > 1 {
		e.tabBar.Show()
	} else {
		e.tabBar.Hide()
	}
}

// refreshTabChip repaints the chip of the buffer being edited, which is how the
// unsaved-change dot appears and disappears.
func (e *Editor) refreshTabChip() {
	if e.tabBox == nil || e.active < 0 || e.active >= len(e.tabBox.Objects) {
		return
	}
	e.tabBox.Objects[e.active].Refresh()
}
