package editor

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// viewportMargin is how many lines are kept above and below the visible area,
	// so scrolling a few lines moves the window instead of refilling it.
	viewportMargin = 40
	// minThumbHeight keeps the scrollbar thumb grabbable in a huge document.
	minThumbHeight = 24
)

// viewport shows one window of a large document inside a widget.Entry and draws
// its own scrollbar.
//
// widget.Entry can only hold a string, so the document lives in a Document and
// one window of it is swapped into the entry. The entry is laid out at its full
// window height and then moved inside the viewport; because the viewport is
// fyne.Scrollable, Fyne clips it, so scrolling *within* the window is a cheap
// Move and a refill only happens when the window runs out.
type viewport struct {
	widget.BaseWidget

	ed    *Editor
	entry *widget.Entry

	top    int // document line the window starts at
	lines  int // lines the entry currently holds
	offset int // line within the window shown at the top of the viewport
	pend   float32

	busy bool // guards against caret callbacks re-entering the refill

	track *canvas.Rectangle
	thumb *canvas.Rectangle
}

func newViewport(ed *Editor, entry *widget.Entry) *viewport {
	v := &viewport{ed: ed, entry: entry}
	v.track = canvas.NewRectangle(theme.Color(theme.ColorNameScrollBarBackground))
	v.track.CornerRadius = 2
	// Not a wrapper type: painters only draw the canvas types they know, so a
	// struct embedding canvas.Rectangle would leave the thumb invisible. The
	// drag is handled by the viewport instead.
	v.thumb = canvas.NewRectangle(theme.Color(theme.ColorNameScrollBar))
	v.thumb.CornerRadius = 2
	v.ExtendBaseWidget(v)
	return v
}

// CreateRenderer builds the renderer that lays the entry out under the scrollbar.
func (v *viewport) CreateRenderer() fyne.WidgetRenderer {
	return &viewportRenderer{vp: v}
}

// Scrolled handles the wheel and trackpad over the viewport. Fyne sends a
// positive DY for scrolling up, so the sign is flipped here.
func (v *viewport) Scrolled(ev *fyne.ScrollEvent) {
	v.pend -= ev.Scrolled.DY
	lh := v.lineHeight()
	if lh <= 0 {
		return
	}
	lines := int(v.pend / lh)
	if lines == 0 {
		return
	}
	v.pend -= float32(lines) * lh
	v.scrollLines(lines)
}

// lineHeight is the height of one rendered line, measured from the theme the same
// way Fyne lays text out.
func (v *viewport) lineHeight() float32 {
	th := v.Theme()
	return fyne.MeasureText("Mg", th.Size(theme.SizeNameText), fyne.TextStyle{Monospace: true}).Height
}

// visibleLines reports how many lines fit in the viewport.
func (v *viewport) visibleLines() int {
	lh := v.lineHeight()
	if lh <= 0 {
		return 1
	}
	n := int(v.Size().Height / lh)
	if n < 1 {
		return 1
	}
	return n
}

// windowLines is the size of the window held in the entry: the visible area plus
// a margin on each side.
func (v *viewport) windowLines() int {
	return v.visibleLines() + 2*viewportMargin
}

// docLines reports the document's line count, defensively.
func (v *viewport) docLines() int {
	if v.ed == nil || v.ed.doc == nil {
		return 1
	}
	return v.ed.doc.LineCount()
}

// guard marks the viewport as busy while it rewrites the window, so the caret
// callbacks that SetText fires cannot re-enter the refill. It returns false when
// the viewport is already busy.
func (v *viewport) guard() bool {
	if v.busy {
		return false
	}
	v.busy = true
	return true
}

// reset puts the viewport back at the top of the document.
func (v *viewport) reset() {
	if !v.guard() {
		return
	}
	defer func() { v.busy = false }()
	v.top, v.offset, v.lines, v.pend = 0, 0, 0, 0
	v.refillAt(0)
}

// refill replaces the window with the lines starting at the current top.
func (v *viewport) refill() {
	if !v.guard() {
		return
	}
	defer func() { v.busy = false }()
	v.refillAt(v.top)
}

// refillAt replaces the window with the lines starting at wantTop, keeping the
// caret on the same document line.
func (v *viewport) refillAt(wantTop int) {
	if v.ed == nil || v.ed.doc == nil {
		return
	}
	// The caret is in window coordinates, so read it before the window moves.
	row, col := v.caret()

	// Anything typed in the window belongs to the document before the window is
	// replaced, otherwise the refill would throw the edit away.
	v.ed.flushWindow()

	total := v.docLines()
	win := v.windowLines()
	if win > total {
		win = total
	}
	top := wantTop
	if top+win > total {
		top = total - win
	}
	if top < 0 {
		top = 0
	}

	v.top = top
	text, n := v.ed.doc.Window(top, win)
	v.lines = n
	v.ed.setWindowText(text, v.ed.doc.LineStart(top))

	// SetText moves the caret, so put it back where it was in document terms.
	v.setCaret(row, col)
	v.layout()
}

// caret returns the document line and column of the entry's caret.
func (v *viewport) caret() (row, col int) {
	return v.top + v.entry.CursorRow, v.entry.CursorColumn
}

// setCaret places the caret at a document line and column, clamped to the window.
func (v *viewport) setCaret(row, col int) {
	r := row - v.top
	if r < 0 {
		r = 0
	}
	if r >= v.lines {
		r = v.lines - 1
	}
	if r < 0 {
		r = 0
	}
	if col < 0 {
		col = 0
	}
	v.entry.CursorRow, v.entry.CursorColumn = r, col
	v.entry.Refresh()
}

// showLine scrolls the window so that the given document line and column are
// visible, and puts the caret there.
func (v *viewport) showLine(row, col int) {
	if !v.guard() {
		return
	}
	defer func() { v.busy = false }()
	total := v.docLines()
	if row < 0 {
		row = 0
	}
	if row >= total {
		row = total - 1
	}
	vis := v.visibleLines()
	v.offset = 0
	v.refillAt(row - vis/2)
	v.setCaret(row, col)
	// Bring the line into the visible band.
	v.offset = v.entry.CursorRow - vis/2
	v.layout()
}

// scrollLines moves the view by delta lines, refilling only when the window is
// exhausted.
func (v *viewport) scrollLines(delta int) {
	if v.ed == nil || v.ed.doc == nil || delta == 0 {
		return
	}
	if !v.guard() {
		return
	}
	defer func() { v.busy = false }()
	total := v.docLines()
	vis := v.visibleLines()
	want := v.top + v.offset + delta

	maxTop := total - vis
	if maxTop < 0 {
		maxTop = 0
	}
	if want > maxTop {
		want = maxTop
	}
	if want < 0 {
		want = 0
	}
	if want == v.top+v.offset {
		return
	}

	// Inside the window: just move it.
	if want >= v.top && want+vis <= v.top+v.lines {
		v.offset = want - v.top
		v.layout()
		return
	}

	// Otherwise re-centre the window on the requested line.
	top := want - viewportMargin
	if top < 0 {
		top = 0
	}
	if top+vis > total {
		top = total - vis
	}
	if top < 0 {
		top = 0
	}
	v.offset = 0
	v.refillAt(top)
	v.offset = want - v.top
	v.layout()
}

// caretMoved keeps the caret inside the visible band, following it off either end
// of the window.
func (v *viewport) caretMoved() {
	if v.ed == nil || v.ed.doc == nil || v.busy {
		return
	}
	row := v.entry.CursorRow
	total := v.docLines()

	// Follow the caret off either end of the window. scrollLines takes the guard
	// itself, so this has to run before it is held.
	if row >= v.lines-1 && v.top+v.lines < total {
		v.scrollLines(row - (v.lines - 1) + 1)
		return
	}
	if row <= 0 && v.top > 0 {
		v.scrollLines(-1)
		return
	}

	if !v.guard() {
		return
	}
	defer func() { v.busy = false }()

	vis := v.visibleLines()
	if row < v.offset {
		v.offset = row
		v.layout()
		return
	}
	if row >= v.offset+vis {
		v.offset = row - vis + 1
		v.layout()
	}
}

// layout positions the entry inside the viewport and refreshes the scrollbar.
func (v *viewport) layout() {
	if v.entry == nil {
		return
	}
	lh := v.lineHeight()
	vis := v.visibleLines()
	maxOffset := v.lines - vis
	if maxOffset < 0 {
		maxOffset = 0
	}
	if v.offset > maxOffset {
		v.offset = maxOffset
	}
	if v.offset < 0 {
		v.offset = 0
	}

	barW := v.barWidth()
	w := v.Size().Width - barW
	if w < 0 {
		w = 0
	}
	h := float32(v.lines) * lh
	if h < v.Size().Height {
		h = v.Size().Height
	}
	v.entry.Move(fyne.NewPos(0, -float32(v.offset)*lh))
	v.entry.Resize(fyne.NewSize(w, h))
	v.entry.Refresh()
	v.refreshScrollbar()
}

// barWidth is the width of the scrollbar gutter.
func (v *viewport) barWidth() float32 {
	return v.Theme().Size(theme.SizeNameScrollBar) + v.Theme().Size(theme.SizeNamePadding)
}

// refreshScrollbar repositions the thumb for the current document position.
func (v *viewport) refreshScrollbar() {
	if v.thumb == nil || v.track == nil {
		return
	}
	size := v.Size()
	barW := v.barWidth()
	trackX := size.Width - barW
	v.track.Move(fyne.NewPos(trackX, 0))
	v.track.Resize(fyne.NewSize(barW, size.Height))

	total := float32(v.docLines())
	vis := float32(v.visibleLines())
	if total <= vis || size.Height <= 0 {
		v.thumb.Hide()
		return
	}
	v.thumb.Show()

	h := v.thumbHeight()
	maxTop := total - vis
	y := (size.Height - h) * float32(v.top+v.offset) / maxTop
	v.thumb.Move(fyne.NewPos(trackX, y))
	v.thumb.Resize(fyne.NewSize(barW, h))
	v.thumb.Refresh()
}

// thumbHeight is how tall the scrollbar thumb is drawn.
func (v *viewport) thumbHeight() float32 {
	size := v.Size()
	total := float32(v.docLines())
	vis := float32(v.visibleLines())
	h := size.Height * vis / total
	if h < minThumbHeight {
		h = minThumbHeight
	}
	if h > size.Height {
		h = size.Height
	}
	return h
}

// Dragged scrolls the document by the fraction of the track the pointer covered.
// Only the gutter routes drags here; drags over the text belong to the entry, so
// selection still works.
func (v *viewport) Dragged(ev *fyne.DragEvent) {
	span := v.Size().Height - v.thumbHeight()
	if span <= 0 {
		return
	}
	maxTop := v.docLines() - v.visibleLines()
	if maxTop <= 0 {
		return
	}
	lines := int(ev.Dragged.DY / span * float32(maxTop))
	if lines != 0 {
		v.scrollLines(lines)
	}
}

// DragEnd satisfies fyne.Draggable.
func (v *viewport) DragEnd() {}

// viewportRenderer lays the entry out under the scrollbar.
type viewportRenderer struct {
	vp *viewport
}

func (r *viewportRenderer) Layout(fyne.Size) {
	v := r.vp
	// A taller viewport needs a taller window, otherwise the space below the
	// last line would be blank.
	if v.lines < v.windowLines() && v.docLines() > v.lines {
		v.refill()
		return
	}
	v.layout()
}

func (r *viewportRenderer) MinSize() fyne.Size {
	return fyne.NewSize(120, r.vp.lineHeight()*4)
}

func (r *viewportRenderer) Refresh() {
	v := r.vp
	th := v.Theme()
	variant := v.ed.app.Settings().ThemeVariant()
	v.track.FillColor = th.Color(theme.ColorNameScrollBarBackground, variant)
	v.thumb.FillColor = th.Color(theme.ColorNameScrollBar, variant)
	v.track.Refresh()
	v.thumb.Refresh()
	v.layout()
}

func (r *viewportRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.vp.track, r.vp.thumb, r.vp.entry}
}

func (r *viewportRenderer) Destroy() {}
