package editor

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// Bracket matching: the bracket at the caret and its partner are drawn with a box
// behind them, the way every editor shows you which block you are in.

// bracketScanChars bounds how far the partner search walks. A pair that is further
// apart than this is not worth the keystroke it would cost to find.
const bracketScanChars = 100_000

// brackets maps each bracket to its partner.
var brackets = map[rune]rune{
	'(': ')',
	')': '(',
	'[': ']',
	']': '[',
	'{': '}',
	'}': '{',
}

// opensBracket reports whether r is the opening side of a pair.
func opensBracket(r rune) bool {
	return r == '(' || r == '[' || r == '{'
}

// runeAt returns the rune at p, if the position holds one.
func (v *codeView) runeAt(p pos) (rune, bool) {
	if p.row < 0 || p.row >= v.docLines() || p.col < 0 {
		return 0, false
	}
	line := []rune(v.doc.LineText(p.row))
	if p.col >= len(line) {
		return 0, false
	}
	return line[p.col], true
}

// scanBracket walks from p to the partner of the bracket at p. Brackets of another
// kind in between are ignored, which is what makes nesting readable.
func (v *codeView) scanBracket(p pos, r rune) (pos, bool) {
	partner := brackets[r]
	if partner == 0 {
		return pos{}, false
	}
	dir := 1
	if !opensBracket(r) {
		dir = -1
	}
	depth := 0
	budget := bracketScanChars
	row, col := p.row, p.col
	for {
		line := []rune(v.doc.LineText(row))
		for {
			col += dir
			if col < 0 || col >= len(line) {
				break
			}
			budget--
			if budget <= 0 {
				return pos{}, false
			}
			switch line[col] {
			case r:
				depth++
			case partner:
				if depth == 0 {
					return pos{row: row, col: col}, true
				}
				depth--
			}
		}
		row += dir
		if row < 0 || row >= v.docLines() {
			return pos{}, false
		}
		if dir > 0 {
			col = -1
		} else {
			col = len([]rune(v.doc.LineText(row)))
		}
	}
}

// bracketMatch is the bracket the caret is on or just past, and its partner. It is
// cached on the caret and the edit count, because the drawing code asks for it on
// every repaint.
func (v *codeView) bracketMatch() (from, to pos, ok bool) {
	if v.doc == nil || !v.focused || v.HasSelection() {
		return pos{}, pos{}, false
	}
	if v.brValid && v.brCaret == v.caret && v.brSeq == v.ed.editSeq {
		return v.brFrom, v.brTo, v.brOK
	}
	v.brValid, v.brCaret, v.brSeq = true, v.caret, v.ed.editSeq
	v.brOK = false
	for _, p := range [2]pos{v.caret, {row: v.caret.row, col: v.caret.col - 1}} {
		r, found := v.runeAt(p)
		if !found || brackets[r] == 0 {
			continue
		}
		partner, found := v.scanBracket(p, r)
		if !found {
			continue
		}
		v.brFrom, v.brTo, v.brOK = p, partner, true
		return v.brFrom, v.brTo, true
	}
	return pos{}, pos{}, false
}

// placeBrush draws one bracket box, if the bracket is on screen.
func (v *codeView) placeBrush(b *canvas.Rectangle, p pos, lineH, charW, inset float32) {
	cell := v.cellOf(p)
	origin := v.xoffCell
	g := p.row
	if v.wrap {
		chunk := v.chunkAt(p.row, cell)
		if starts := v.wrapStarts(p.row); len(starts) > 0 {
			if chunk >= len(starts) {
				chunk = len(starts) - 1
			}
			origin = starts[chunk]
		}
		g = v.rowStart(p.row) + chunk
	}
	if g < v.topRow || g >= v.topRow+v.visibleLines() {
		return
	}
	b.Show()
	b.Move(fyne.NewPos(inset+float32(cell-origin)*charW, inset+float32(g-v.topRow)*lineH))
	b.Resize(fyne.NewSize(charW, lineH))
}

// syncBrackets draws the boxes behind the matched pair, hiding them when there is
// no pair under the caret.
func (r *codeRenderer) syncBrackets(lineH, charW, inset float32) {
	v := r.v
	for _, b := range v.brushes {
		b.Hide()
	}
	from, to, ok := v.bracketMatch()
	if !ok {
		return
	}
	v.placeBrush(v.brushes[0], from, lineH, charW, inset)
	v.placeBrush(v.brushes[1], to, lineH, charW, inset)
}
