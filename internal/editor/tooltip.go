package editor

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	ftheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// tipButton is a slim toolbar button that shows a one-line tooltip on hover.
// widget.Button already implements desktop.Hoverable, so we wrap it (by
// pointer, to avoid copying the widget) and extend its hover callbacks.
type tipButton struct {
	*widget.Button

	tip    string
	onShow func(text string, at fyne.Position)
	onHide func()
}

// newTipButton builds a labelled/icon button with an associated tooltip.
//
// The widget is constructed directly (rather than via widget.NewButton) so we
// can point its BaseWidget at the wrapper first; that keeps a single renderer
// and lets hover/tap route through this type.
func newTipButton(label string, icon fyne.Resource, tip string, action func()) *tipButton {
	b := &tipButton{tip: tip}
	b.Button = &widget.Button{Text: label, Icon: icon}
	b.ExtendBaseWidget(b)
	b.OnTapped = func() {
		if b.onHide != nil {
			b.onHide()
		}
		if action != nil {
			action()
		}
	}
	return b
}

// bind wires the tooltip callbacks to the owning editor.
func (b *tipButton) bind(show func(string, fyne.Position), hide func()) {
	b.onShow = show
	b.onHide = hide
}

func (b *tipButton) MouseIn(ev *desktop.MouseEvent) {
	b.Button.MouseIn(ev)
	if b.onShow != nil && b.tip != "" {
		b.onShow(b.tip, ev.AbsolutePosition)
	}
}

func (b *tipButton) MouseMoved(ev *desktop.MouseEvent) { b.Button.MouseMoved(ev) }

func (b *tipButton) MouseOut() {
	b.Button.MouseOut()
	if b.onHide != nil {
		b.onHide()
	}
}

// bindTips attaches the editor tooltip handlers to the given buttons.
func (e *Editor) bindTips(buttons ...*tipButton) {
	for _, b := range buttons {
		b.bind(e.showTip, e.hideTip)
	}
}

// buildTipLayer creates the non-interactive overlay used to draw tooltips.
//
// It is a stack sibling of the main UI and contains only a rectangle and text,
// neither of which is Hoverable or Tappable, so it never steals mouse events
// from the buttons underneath.
func (e *Editor) buildTipLayer() {
	e.tipBG = canvas.NewRectangle(ftheme.OverlayBackgroundColor())
	e.tipBG.CornerRadius = ftheme.InputRadiusSize()
	th := e.app.Settings().Theme()
	v := e.app.Settings().ThemeVariant()
	e.tipBG.StrokeColor = th.Color(ftheme.ColorNameSeparator, v)
	e.tipBG.StrokeWidth = 1

	e.tipText = canvas.NewText("", th.Color(ftheme.ColorNameForeground, v))
	e.tipText.TextStyle = fyne.TextStyle{Monospace: true}
	e.tipText.TextSize = th.Size(ftheme.SizeNameText)

	e.tipLayer = container.NewWithoutLayout(e.tipBG, e.tipText)
	e.tipLayer.Hide()
}

// showTip positions and reveals the tooltip near at, clamped to the window.
func (e *Editor) showTip(text string, at fyne.Position) {
	if e.tipLayer == nil || e.win == nil || e.win.Canvas() == nil {
		return
	}
	size := e.win.Canvas().Size()
	if e.tipLayer.Size() != size {
		e.tipLayer.Resize(size)
	}

	th := e.app.Settings().Theme()
	v := e.app.Settings().ThemeVariant()
	pad := th.Size(ftheme.SizeNameInnerPadding)
	if e.tipBG.StrokeWidth != 1 {
		e.tipBG.StrokeWidth = 1
	}
	e.tipBG.FillColor = th.Color(ftheme.ColorNameOverlayBackground, v)
	e.tipBG.StrokeColor = th.Color(ftheme.ColorNameSeparator, v)
	e.tipText.Color = th.Color(ftheme.ColorNameForeground, v)
	e.tipText.TextSize = th.Size(ftheme.SizeNameText)
	e.tipText.Text = text

	ts := fyne.MeasureText(text, e.tipText.TextSize, e.tipText.TextStyle)
	w := ts.Width + 2*pad
	h := ts.Height + 2*pad

	pos := fyne.NewPos(at.X+12, at.Y+20)
	if pos.X+w > size.Width {
		pos.X = size.Width - w - 4
	}
	if pos.Y+h > size.Height {
		pos.Y = at.Y - h - 6
	}
	if pos.X < 4 {
		pos.X = 4
	}
	if pos.Y < 4 {
		pos.Y = 4
	}

	e.tipBG.Resize(fyne.NewSize(w, h))
	e.tipBG.Move(pos)
	e.tipText.Resize(ts)
	e.tipText.Move(fyne.NewPos(pos.X+pad, pos.Y+pad))

	e.tipLayer.Show()
	e.tipBG.Refresh()
	e.tipText.Refresh()
}

// hideTip hides the tooltip.
func (e *Editor) hideTip() {
	if e.tipLayer != nil {
		e.tipLayer.Hide()
	}
}
