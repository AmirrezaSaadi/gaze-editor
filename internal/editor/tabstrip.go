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
// cross to close it and a dot while it has unsaved changes.

const (
	// tabChipPad is the space at each end of a chip.
	tabChipPad = 7
	// tabChipGap is the space between the name, the unsaved-changes dot and the
	// cross, so they never touch.
	tabChipGap = 5
	// tabChipClose is the width of the close cross, and how much of the right-hand
	// end of a chip closes it.
	tabChipClose = 16
	// tabDotSize is the dot shown while a buffer has unsaved changes. Its slot is
	// always reserved, so a chip does not change width (and shift the others) when
	// the first keystroke makes it dirty.
	tabDotSize = 6
	// tabTitleMax is how many characters of a name a chip shows before it is
	// shortened, so one long path cannot push the others off the bar.
	tabTitleMax = 20
)

// tabChipHeight is the height of the chip itself, and tabStripHeight the height the
// bar needs: the chip plus the room the horizontal scrollbar takes at the bottom when
// the chips do not fit, so it never runs through them.
func tabChipHeight() float32 {
	return theme.Size(theme.SizeNameText) + 2*tabChipPad
}

func tabStripHeight() float32 {
	return tabChipHeight() + theme.Size(theme.SizeNameScrollBar) + theme.Size(theme.SizeNamePadding)
}

// tabBarWidget is the tab bar as the window sees it: the chips, their scroller and
// the new-file button. It keeps the active chip in view when the bar changes width,
// which is what a narrower window does to it.
type tabBarWidget struct {
	widget.BaseWidget

	inner fyne.CanvasObject
	ed    *Editor
}

func newTabBarWidget(ed *Editor, inner fyne.CanvasObject) *tabBarWidget {
	t := &tabBarWidget{inner: inner, ed: ed}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tabBarWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.inner)
}

func (t *tabBarWidget) Resize(size fyne.Size) {
	t.BaseWidget.Resize(size)
	if t.ed != nil {
		t.ed.revealTab()
	}
}

// tabChip is one entry in the tab bar.
type tabChip struct {
	widget.BaseWidget

	ed    *Editor
	index int

	hovered bool
	// shown is what the chip was last painted with, so a keystroke only repaints the
	// chips whose appearance actually changed.
	shown chipState
}

// chipState is everything about a buffer that a chip draws.
type chipState struct {
	title  string
	dirty  bool
	active bool
}

func newTabChip(ed *Editor, index int) *tabChip {
	c := &tabChip{ed: ed, index: index}
	c.ExtendBaseWidget(c)
	c.shown = c.state()
	return c
}

// CreateRenderer builds the chip's pieces: a background, the name, the dot and the
// close cross.
func (c *tabChip) CreateRenderer() fyne.WidgetRenderer {
	r := &chipRenderer{
		chip:  c,
		bg:    canvas.NewRectangle(color.Transparent),
		label: canvas.NewText("", color.White),
		dot:   canvas.NewCircle(color.White),
		cross: canvas.NewText("×", color.White),
	}
	r.bg.CornerRadius = 4
	r.label.TextSize = theme.Size(theme.SizeNameText) - 1
	r.label.Alignment = fyne.TextAlignLeading
	r.cross.TextSize = theme.Size(theme.SizeNameText) + 2
	r.cross.Alignment = fyne.TextAlignCenter
	return r
}

// Tapped selects the chip, or closes the buffer when the cross was hit. Focus goes
// back to the text either way: a chip is not something to type into.
func (c *tabChip) Tapped(ev *fyne.PointEvent) {
	defer c.ed.takeFocus()
	if ev.Position.X >= c.Size().Width-tabChipPad-tabChipClose {
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

// state is what the chip should be showing now: which buffer it is, what it is
// called, whether it is the one being edited and whether it has unsaved changes.
func (c *tabChip) state() chipState {
	return chipState{
		title:  shorten(c.ed.tabTitle(c.index), tabTitleMax),
		dirty:  c.ed.TabDirty(c.index),
		active: c.ed.ActiveTab() == c.index,
	}
}

// sync repaints the chip if what it shows has changed.
func (c *tabChip) sync() {
	if st := c.state(); st != c.shown {
		c.Refresh()
	}
}

// labelText is the name the chip shows, shortened to fit.
func (c *tabChip) labelText() string { return shorten(c.ed.tabTitle(c.index), tabTitleMax) }

// chipRenderer draws a chip: a background, the name, the dot of unsaved changes and
// the close cross.
type chipRenderer struct {
	chip  *tabChip
	bg    *canvas.Rectangle
	label *canvas.Text
	dot   *canvas.Circle
	cross *canvas.Text
}

func (r *chipRenderer) Layout(size fyne.Size) {
	// The chip is a pill inside the space it is given: the row is as tall as the
	// strip, and the bottom of the strip belongs to the scrollbar.
	height := tabChipHeight()
	if height > size.Height {
		height = size.Height
	}
	top := (size.Height - height) / 2
	r.bg.Move(fyne.NewPos(0, top))
	r.bg.Resize(fyne.NewSize(size.Width, height))

	lineH := theme.Size(theme.SizeNameText) + 2
	textTop := (size.Height - lineH) / 2

	crossX := size.Width - tabChipPad - tabChipClose
	dotX := crossX - tabChipGap - tabDotSize
	nameW := dotX - tabChipGap - tabChipPad
	if nameW < 0 {
		nameW = 0
	}
	r.label.Move(fyne.NewPos(tabChipPad, textTop))
	r.label.Resize(fyne.NewSize(nameW, lineH))
	r.dot.Resize(fyne.NewSize(tabDotSize, tabDotSize))
	r.dot.Move(fyne.NewPos(dotX, (size.Height-tabDotSize)/2))
	r.cross.Move(fyne.NewPos(crossX, textTop))
	r.cross.Resize(fyne.NewSize(tabChipClose, lineH))
}

func (r *chipRenderer) MinSize() fyne.Size {
	// The dot's slot is part of the width whether or not a dot is drawn, so typing
	// into a chip does not make its neighbours jump sideways.
	w := fyne.MeasureText(r.chip.labelText(), r.label.TextSize, fyne.TextStyle{}).Width +
		2*tabChipPad + 2*tabChipGap + tabChipClose + tabDotSize
	return fyne.NewSize(w, tabChipHeight())
}

// Refresh reads the current state: which buffer this is, whether it is the one being
// edited, whether it has unsaved changes and whether the pointer is on it.
func (r *chipRenderer) Refresh() {
	c := r.chip
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	th := c.Theme()
	fg := th.Color(theme.ColorNameForeground, variant)
	dim := th.Color(theme.ColorNameDisabled, variant)
	accent := th.Color(theme.ColorNamePrimary, variant)

	st := c.state()
	active, dirty := st.active, st.dirty
	c.shown = st

	r.bg.FillColor = color.Transparent
	r.label.Color = dim
	switch {
	case active:
		r.bg.FillColor = th.Color(theme.ColorNameSelection, variant)
		r.label.Color = fg
	case c.hovered:
		r.bg.FillColor = th.Color(theme.ColorNameHover, variant)
		r.label.Color = fg
	}

	r.label.Text = st.title
	r.cross.Color = dim
	if c.hovered || active {
		r.cross.Color = fg
	}
	if dirty {
		r.dot.FillColor = accent
		r.dot.Show()
	} else {
		r.dot.Hide()
	}

	r.bg.Refresh()
	r.label.Refresh()
	r.cross.Refresh()
	r.dot.Refresh()
}

func (r *chipRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.label, r.dot, r.cross}
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
	if e.tabStrip != nil {
		// The chips follow the theme's text size, which the zoom changes.
		e.tabStrip.SetMinSize(fyne.NewSize(120, tabStripHeight()))
	}
	if len(e.tabs) > 1 {
		e.tabBar.Show()
	} else {
		e.tabBar.Hide()
	}
	e.revealTab()
}

// refreshChips repaints the chips whose dot or fill has changed, which is how the
// unsaved-changes dot appears on the first keystroke and clears on a save.
func (e *Editor) refreshChips() {
	if e.tabBox == nil {
		return
	}
	for _, o := range e.tabBox.Objects {
		if c, ok := o.(*tabChip); ok {
			c.sync()
		}
	}
}

// revealTab scrolls the strip so the whole of the active chip is visible. Without
// it, switching to a buffer whose chip is off the right-hand end changes the text
// while the bar still shows the old one.
func (e *Editor) revealTab() {
	if e.tabStrip == nil || e.tabBox == nil {
		return
	}
	if e.active < 0 || e.active >= len(e.tabBox.Objects) {
		return
	}
	view := e.tabStrip.Size().Width
	if view <= 0 {
		return // there is no strip on screen to scroll
	}
	// The scroller can only move once its content is wider than it is, and the layout
	// that does that runs on the next frame: size the row now, or the offset is
	// clamped straight back to the start.
	if min := e.tabBox.MinSize(); e.tabBox.Size().Width < min.Width {
		keep := e.tabBox.Size().Height
		if keep < min.Height {
			keep = min.Height
		}
		e.tabBox.Resize(fyne.NewSize(min.Width, keep))
	}
	// Where the chip is comes from the sizes of the ones before it rather than from
	// the layout: a buffer can be activated before its new chip has been placed.
	pad := theme.Padding()
	var x float32
	for i := 0; i < e.active; i++ {
		x += e.tabBox.Objects[i].MinSize().Width + pad
	}
	width := e.tabBox.Objects[e.active].MinSize().Width
	switch off := e.tabStrip.Offset.X; {
	case x < off:
		e.tabStrip.ScrollToOffset(fyne.NewPos(x, 0))
	case x+width > off+view:
		e.tabStrip.ScrollToOffset(fyne.NewPos(x+width-view, 0))
	}
}
