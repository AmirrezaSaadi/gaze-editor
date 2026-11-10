package editor

import (
	"image/color"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const (
	// caretWidth is how wide the caret is drawn.
	caretWidth = 2
	// clickSlop is how far from a cell boundary a click snaps to it.
	clickSlop = 0.4
	// minThumbHeight keeps the scrollbar thumb grabbable in a huge document.
	minThumbHeight = 24
	// dragScrollZone is the height of the band at the top and bottom of the view
	// in which dragging a selection scrolls the document.
	dragScrollZone = 16
	// maxCodeUndoEdits and maxCodeUndoBytes cap the large-file undo history: each
	// entry holds only the text it replaced, so thousands of small edits are
	// cheap.
	maxCodeUndoEdits = 2000
	maxCodeUndoBytes = 8 << 20
)

// pos is a caret position: a 0-based document line and rune column.
type pos struct{ row, col int }

func (p pos) before(q pos) bool {
	return p.row < q.row || (p.row == q.row && p.col < q.col)
}

// docEdit is one reversible change: the text that was replaced and the text that
// replaced it.
type docEdit struct {
	off    int    // byte offset in the document
	old    string // text that was removed
	ins    string // text that was inserted
	caret  pos    // caret before the edit
	anchor pos    // selection anchor before the edit
	after  pos    // caret after the edit
	dirty  bool   // dirty flag before the edit
	at     time.Time
}

// codeView is the editing surface for large files: it renders the visible lines
// of a Document itself, instead of handing the whole buffer to widget.Entry.
//
// widget.Entry lays out every line, keeps its own copy of the text and can only
// be given a string, so it is the wrong tool for a large file. This widget draws
// a few dozen canvas.Text objects for the lines on screen, keeps the caret as a
// document offset and edits the Document directly, so cost tracks the size of
// the screen rather than the size of the file. Selection, undo, find and replace
// and the line operations all work over the whole document.
type codeView struct {
	widget.BaseWidget

	ed  *Editor
	doc *Document

	topRow   int  // first visible visual row (wrap-aware; == document row without wrap)
	xoffCell int  // first visible cell, only used when wrap is off
	wrap     bool // soft-wrap long lines instead of scrolling sideways

	// rows is the cumulative visual row index per document line, built only while
	// wrapping: rows[i] is the first visual row of line i, rows[n] the total.
	rows      []int
	rowsCells int // wrap width the index was built for

	// Text metrics are measured once per text size: fyne.MeasureText is far too
	// slow to call per line while indexing a multi-million-line document.
	metricSize    float32
	metricLineH   float32
	metricCharW   float32
	metricsCached bool

	caret  pos
	anchor pos

	shift, ctrl, alt bool // modifier state, tracked from KeyDown/KeyUp

	focused  bool
	dragging bool
	dragBar  bool
	hoverBar bool // the pointer is over the scrollbar gutter

	undo      []docEdit
	redo      []docEdit
	undoBytes int

	// hl holds the syntax highlighting for the document, or nil when the file
	// has no language, is too large for one, or highlighting is switched off.
	hl *highlighter
	// segs is the pool of text objects: one row of them per visible line, so a
	// highlighted line can be drawn as several coloured runs without allocating
	// anything while scrolling.
	segs    [][]*canvas.Text
	segUsed []int
	segBuf  []rowSegment
	palette [styleClasses]color.NRGBA

	plain styledLine // the fallback for a line when nothing is highlighted

	// brushes are the boxes drawn behind a matched bracket pair, and the match is
	// cached on the caret and the edit count because repainting asks every time.
	brushes [2]*canvas.Rectangle
	brColor color.NRGBA
	brValid bool
	brOK    bool
	brFrom  pos
	brTo    pos
	brCaret pos
	brSeq   uint64

	bg        *canvas.Rectangle
	sels      []*canvas.Rectangle
	caretRect *canvas.Rectangle
	track     *canvas.Rectangle
	thumb     *canvas.Rectangle
	renderer_ *codeRenderer
}

func newCodeView(ed *Editor) *codeView {
	v := &codeView{ed: ed}
	v.palette = syntaxPalette(fyne.CurrentApp().Settings().ThemeVariant())
	v.bg = canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	v.caretRect = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	for i := range v.brushes {
		v.brushes[i] = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
		v.brushes[i].CornerRadius = 2
		v.brushes[i].Hide()
	}
	v.track = canvas.NewRectangle(theme.Color(theme.ColorNameScrollBarBackground))
	v.track.CornerRadius = 2
	v.thumb = canvas.NewRectangle(theme.Color(theme.ColorNameScrollBar))
	v.thumb.CornerRadius = 2
	v.ExtendBaseWidget(v)
	return v
}

// CreateRenderer builds the renderer that draws the visible lines.
func (v *codeView) CreateRenderer() fyne.WidgetRenderer {
	r := &codeRenderer{v: v}
	v.renderer_ = r
	return r
}

// setDoc installs a document and puts the view back at the top.
// docIndex is a visual-row index built for a known wrap width, so a document that
// was parsed on a background goroutine can be installed without the UI goroutine
// spending hundreds of milliseconds indexing it afterwards.
type docIndex struct {
	rows  []int
	cells int
}

func (v *codeView) setDoc(d *Document) { v.setDocRows(d, docIndex{}) }

// setDocRows installs doc, using ix when the caller already has the index. A
// stale index is detected and rebuilt by the reflow guard in the renderer, so it
// is only ever a shortcut.
func (v *codeView) setDocRows(d *Document, ix docIndex) {
	v.brValid = false
	v.doc = d
	v.hl = nil // the language is picked for the new document by the editor
	v.topRow, v.xoffCell = 0, 0
	v.caret, v.anchor = pos{}, pos{}
	v.undo, v.redo = nil, nil
	v.undoBytes = 0
	if ix.rows != nil && ix.cells == v.wrapCells() {
		v.rows, v.rowsCells = ix.rows, ix.cells
	} else {
		v.rows = nil
		v.rebuildRows()
	}
	v.render()
}

// setWrap turns soft wrapping on or off and keeps the view on the same text.
func (v *codeView) setWrap(on bool) {
	if v.wrap == on {
		return
	}
	// Remember where the top of the view is so toggling does not jump.
	row, chunk := v.rowOf(v.topRow)
	v.wrap = on
	v.xoffCell = 0
	if on {
		v.rebuildRows()
	} else {
		v.rows = nil
	}
	v.topRow = v.rowStart(row)
	if !on {
		v.topRow = row
	} else {
		v.topRow += chunk
	}
	if last := max(0, v.totalRows()-v.visibleLines()); v.topRow > last {
		v.topRow = last
	}
	if v.topRow < 0 {
		v.topRow = 0
	}
	v.render()
}

// --- geometry ---------------------------------------------------------------

// metrics returns the height of a line and the width of a character, measured
// from the current theme exactly the way Fyne lays monospace text out.
func (v *codeView) metrics() (lineH, charW float32) {
	size := v.Theme().Size(theme.SizeNameText)
	if v.metricsCached && v.metricSize == size {
		return v.metricLineH, v.metricCharW
	}
	m := fyne.MeasureText("A", size, fyne.TextStyle{Monospace: true})
	v.metricSize, v.metricLineH, v.metricCharW, v.metricsCached = size, m.Height, m.Width, true
	return m.Height, m.Width
}

// inset is the padding between the widget edge and the first character column.
func (v *codeView) inset() float32 {
	return v.Theme().Size(theme.SizeNameInnerPadding)
}

// barWidth is the width of the scrollbar gutter.
func (v *codeView) barWidth() float32 {
	return v.Theme().Size(theme.SizeNameScrollBar) + v.Theme().Size(theme.SizeNamePadding)
}

// visibleLines is how many lines fit in the widget.
func (v *codeView) visibleLines() int {
	lineH, _ := v.metrics()
	if lineH <= 0 {
		return 1
	}
	n := int((v.Size().Height - 2*v.inset()) / lineH)
	if n < 1 {
		return 1
	}
	return n
}

// visibleCells is how many character cells fit beside the scrollbar.
func (v *codeView) visibleCells() int {
	_, charW := v.metrics()
	if charW <= 0 {
		return 1
	}
	n := int((v.Size().Width - 2*v.inset() - v.barWidth()) / charW)
	if n < 1 {
		return 1
	}
	return n
}

// wrapCells is how many character cells fit on one visual row.
func (v *codeView) wrapCells() int {
	if n := v.visibleCells(); n > 0 {
		return n
	}
	return 1
}

// cellsOf is the display width of a document line, with tabs expanded.
func (v *codeView) cellsOf(row int) int {
	cells := 0
	for _, r := range v.doc.LineText(row) {
		if r == '\t' {
			cells += v.ed.indentWidth - cells%v.ed.indentWidth
		} else {
			cells++
		}
	}
	return cells
}

// wrapScan walks a line and reports the cell each visual row starts at, breaking
// after the last space that fits so words stay whole (a word longer than the
// window is still split). It returns the number of rows; when sink is nil it only
// counts, which is what keeps a full index pass allocation-free.
func (v *codeView) wrapScan(row int, sink func(cell int)) int {
	return v.scanCells(v.doc.LineText(row), v.wrapCells(), sink)
}

// scanCells reports how many visual rows a line occupies, calling sink with the
// cell each row starts at. It takes the wrap width so that indexing a whole
// document measures the window once instead of per line.
func (v *codeView) scanCells(line string, maxW int, sink func(cell int)) int {
	if !v.wrap {
		if sink != nil {
			sink(0)
		}
		return 1
	}
	return scanCellsIndent(line, maxW, v.ed.indentWidth, sink)
}

// scanCellsIndent is the wrapping arithmetic behind scanCells, without the widget
// around it, so a whole document can be indexed off the UI goroutine.
func scanCellsIndent(line string, maxW, indentWidth int, sink func(cell int)) int {
	if sink != nil {
		sink(0)
	}
	if indentWidth < 1 {
		indentWidth = 1
	}
	if maxW < 1 {
		maxW = 1
	}
	count := 1
	cell, rowStart, lastSpace := 0, 0, -1
	for _, r := range line {
		w := 1
		if r == '\t' {
			w = indentWidth - cell%indentWidth
		}
		if cell+w > rowStart+maxW {
			if lastSpace > rowStart {
				rowStart = lastSpace + 1
			} else {
				rowStart = cell
			}
			count++
			if sink != nil {
				sink(rowStart)
			}
			lastSpace = -1
		}
		if r == ' ' && cell >= rowStart {
			lastSpace = cell
		}
		cell += w
	}
	return count
}

// chunksOf is how many visual rows a document line occupies.
func (v *codeView) chunksOf(row int) int { return v.wrapScan(row, nil) }

// wrapStarts is the cell each visual row of a line starts at.
func (v *codeView) wrapStarts(row int) []int {
	starts := make([]int, 0, 4)
	v.wrapScan(row, func(cell int) { starts = append(starts, cell) })
	return starts
}

// chunkAt is the visual row a cell of a line falls on.
func (v *codeView) chunkAt(row, cell int) int {
	if !v.wrap {
		return 0
	}
	chunk, idx := 0, -1
	v.wrapScan(row, func(start int) {
		idx++
		if cell >= start {
			chunk = idx
		}
	})
	return chunk
}

// totalRows is the number of visual rows in the document.
func (v *codeView) totalRows() int {
	if !v.wrap || len(v.rows) == 0 {
		return v.docLines()
	}
	return v.rows[len(v.rows)-1]
}

// rowOf maps a visual row to the document line and wrap chunk it starts.
func (v *codeView) rowOf(g int) (row, chunk int) {
	if !v.wrap || len(v.rows) == 0 {
		if g < 0 {
			g = 0
		}
		if g >= v.docLines() {
			g = v.docLines() - 1
		}
		return g, 0
	}
	if g < 0 {
		g = 0
	}
	if last := v.totalRows() - 1; g > last {
		g = last
	}
	row = sort.SearchInts(v.rows, g+1) - 1
	if row < 0 {
		row = 0
	}
	if row >= len(v.rows)-1 {
		row = len(v.rows) - 2
	}
	return row, g - v.rows[row]
}

// rowStart is the visual row a document line starts at.
func (v *codeView) rowStart(row int) int {
	if !v.wrap || len(v.rows) == 0 {
		return row
	}
	if row <= 0 {
		return 0
	}
	if row >= len(v.rows) {
		return v.totalRows()
	}
	return v.rows[row]
}

// caretChunk is the wrap chunk of its line the caret sits on.
func (v *codeView) caretChunk() int {
	if !v.wrap {
		return 0
	}
	c := v.chunkAt(v.caret.row, v.cellOf(v.caret))
	if n := v.chunksOf(v.caret.row); c >= n {
		c = n - 1
	}
	if c < 0 {
		c = 0
	}
	return c
}

// caretRow is the visual row of the caret.
func (v *codeView) caretRow() int { return v.rowStart(v.caret.row) + v.caretChunk() }

// rebuildRows recomputes the visual row index; only used while wrapping.
func (v *codeView) rebuildRows() {
	v.rows = buildRowIndex(v.doc, v.wrapCells(), v.ed.indentWidth, v.wrap)
	v.rowsCells = v.wrapCells()
}

// buildRowIndex is the visual-row index for doc at a wrap width of maxW cells, or
// nil when wrapping is off. It touches no widget state, so a background open can
// build the index while the file is still being read instead of blocking the UI
// with it afterwards.
func buildRowIndex(doc *Document, maxW, indentWidth int, wrap bool) []int {
	if !wrap || doc == nil {
		return nil
	}
	if maxW < 1 {
		maxW = 1
	}
	rows := make([]int, doc.LineCount()+1)
	total, i := 0, 0
	// The pages are walked directly: asking the document for each line by number
	// would search the page and line indexes per line, which for a
	// multi-million-line file costs more than the wrap arithmetic itself.
	_ = doc.eachLine(func(line string, _ bool) error {
		rows[i] = total
		i++
		// A line no longer than the window cannot wrap, and a line without tabs
		// is never wider in cells than in bytes: skip the rune walk.
		if len(line) <= maxW && strings.IndexByte(line, '\t') < 0 {
			total++
			return nil
		}
		total += scanCellsIndent(line, maxW, indentWidth, nil)
		return nil
	})
	rows[i] = total
	return rows
}

// patchRows updates the visual row index after the lines [r0, r0+oldLines) were
// replaced by newLines lines, where oldLines and newLines count the line breaks in
// the replaced and inserted text.
//
// The index holds the first visual row of every line, so an edit only moves the
// entries below it by the same amount: the starts of the lines the edit touched
// are recomputed, the rest are shifted by one delta.
func (v *codeView) patchRows(r0, oldLines, newLines int) {
	if !v.wrap || v.doc == nil {
		return
	}
	n := v.doc.LineCount()
	nOld := len(v.rows) - 1 // lines before this edit
	// The index still describes the document before the edit.
	if len(v.rows) != n+1-(newLines-oldLines) ||
		v.rowsCells != v.wrapCells() || oldLines > 2000 || newLines > 2000 {
		v.rebuildRows()
		return
	}
	if r0 < 0 {
		r0 = 0
	}
	if r0+oldLines > nOld {
		v.rebuildRows()
		return
	}

	// The first line after the replaced block: where it starts now and where it
	// used to start are what the shift below is made of.
	tailFrom, tailTo := r0+oldLines+1, r0+newLines+1
	oldTail, hasTail := 0, tailFrom <= nOld
	if hasTail {
		oldTail = v.rows[tailFrom]
	}
	grow := newLines - oldLines
	if grow > 0 {
		v.rows = append(v.rows, make([]int, grow)...)
	}
	copy(v.rows[tailTo:], v.rows[tailFrom:])
	if grow < 0 {
		v.rows = v.rows[:len(v.rows)+grow]
	}

	// Recompute the starts of the lines the edit touched, then move everything
	// below them by however much the block grew.
	acc := v.rows[r0]
	for k := 1; k <= newLines; k++ {
		acc += v.chunksOf(r0 + k - 1)
		v.rows[r0+k] = acc
	}
	end := acc + v.chunksOf(r0+newLines)
	if !hasTail {
		v.rows[len(v.rows)-1] = end // the edit reached the end of the document
		return
	}
	if delta := end - oldTail; delta != 0 {
		for i := tailTo; i < len(v.rows); i++ {
			v.rows[i] += delta
		}
	}
}

// applyReplace changes the document and keeps the visual row index in step.
func (v *codeView) applyReplace(off int, del, ins string) {
	r0 := 0
	if v.doc != nil {
		r0 = v.doc.LineOf(off)
		if v.wrap {
			r0, _ = v.doc.RowColOf(off)
		}
	}
	// An edit can turn the rest of the document into (or out of) a comment, so
	// the highlighting is only trusted above the line that changed.
	if v.hl != nil {
		v.hl.dropFrom(v.doc.LineOf(off))
	}
	v.doc.ReplaceRange(off, off+len(del), ins)
	if v.wrap {
		v.patchRows(r0, strings.Count(del, "\n"), strings.Count(ins, "\n"))
	}
}

// docLines is the number of lines in the document.
func (v *codeView) docLines() int {
	if v.doc == nil {
		return 1
	}
	return v.doc.LineCount()
}

// --- document coordinates --------------------------------------------------

// lineLen is the length of a document line in runes, not counting the newline.
func (v *codeView) lineLen(row int) int {
	return utf8Count(v.doc.LineText(row))
}

// clampPos keeps a position inside the document.
func (v *codeView) clampPos(p pos) pos {
	if p.row < 0 {
		p.row = 0
	}
	if p.row >= v.docLines() {
		p.row = v.docLines() - 1
	}
	if p.col < 0 {
		p.col = 0
	}
	if n := v.lineLen(p.row); p.col > n {
		p.col = n
	}
	return p
}

// offset converts a caret position to a byte offset in the document.
func (v *codeView) offset(p pos) int {
	p = v.clampPos(p)
	return v.doc.LineStart(p.row) + runeByteOffset(v.doc.LineText(p.row), p.col)
}

// posAt converts a byte offset to a caret position.
func (v *codeView) posAt(off int) pos {
	if v.doc == nil {
		return pos{}
	}
	row, col := v.doc.RowColOf(off)
	return pos{row: row, col: col}
}

// lineStartPos and lineEndPos are the ends of the caret's line.
func (v *codeView) lineStartPos() pos { return pos{row: v.caret.row, col: 0} }
func (v *codeView) lineEndPos() pos   { return pos{row: v.caret.row, col: v.lineLen(v.caret.row)} }

// --- selection --------------------------------------------------------------

// selRange returns the selection in document order and whether there is one.
func (v *codeView) selRange() (from, to pos, ok bool) {
	if v.anchor == v.caret {
		return pos{}, pos{}, false
	}
	if v.anchor.before(v.caret) {
		return v.anchor, v.caret, true
	}
	return v.caret, v.anchor, true
}

// HasSelection reports whether anything is selected.
func (v *codeView) HasSelection() bool { return v.anchor != v.caret }

// selectedText returns the selected text, or "".
func (v *codeView) selectedText() string {
	from, to, ok := v.selRange()
	if !ok {
		return ""
	}
	return v.doc.TextRange(v.offset(from), v.offset(to))
}

// selectAll selects the whole document.
func (v *codeView) selectAll() {
	v.anchor = pos{}
	v.caret = pos{row: v.docLines() - 1, col: v.lineLen(v.docLines() - 1)}
	v.followCaret()
	v.render()
}

// clearSelection drops the selection, keeping the caret where it is.
func (v *codeView) clearSelection() { v.anchor = v.caret }

// --- editing ----------------------------------------------------------------

// edit replaces [from, to) with ins and records it for undo. When merge is set
// and the previous edit was an adjacent insertion, they are coalesced so that
// typing a word is undone as a word rather than a character.
func (v *codeView) edit(off int, del string, ins string, merge bool) {
	if del == "" && ins == "" {
		return
	}
	before := v.caret
	if merge {
		if n := len(v.undo); n > 0 {
			last := &v.undo[n-1]
			if last.old == "" && del == "" && last.off+len(last.ins) == off &&
				time.Since(last.at) < time.Second {
				last.ins += ins
				last.at = time.Now()
				v.applyReplace(off, "", ins)
				// The run now ends at the end of the merged text, not after off.
				last.after = v.posAt(last.off + len(last.ins))
				v.afterEdit(last.after)
				return
			}
		}
	}
	v.applyReplace(off, del, ins)
	e := docEdit{off: off, old: del, ins: ins, caret: before, anchor: v.anchor,
		after: v.posAt(off + len(ins)), dirty: v.ed.dirty, at: time.Now()}
	v.undo = append(v.undo, e)
	v.undoBytes += len(del) + len(ins)
	v.redo = nil
	v.trimUndo()
	v.afterEdit(e.after)
}

// rawEdit applies a change and records it, leaving the caret to the caller.
func (v *codeView) rawEdit(off int, del, ins string) {
	v.applyReplace(off, del, ins)
	v.undo = append(v.undo, docEdit{
		off: off, old: del, ins: ins, caret: v.caret, anchor: v.anchor,
		after: v.caret, dirty: v.ed.dirty, at: time.Now(),
	})
	v.undoBytes += len(del) + len(ins)
	v.redo = nil
	v.trimUndo()
}

// trimUndo keeps the history bounded.
func (v *codeView) trimUndo() {
	for len(v.undo) > maxCodeUndoEdits || (v.undoBytes > maxCodeUndoBytes && len(v.undo) > 1) {
		v.undoBytes -= len(v.undo[0].old) + len(v.undo[0].ins)
		v.undo = v.undo[1:]
	}
	for len(v.redo) > maxCodeUndoEdits {
		v.redo = v.redo[1:]
	}
}

// afterEdit moves the caret, tells the editor the document changed and redraws.
func (v *codeView) afterEdit(p pos) {
	v.caret = p
	v.anchor = p
	v.followCaret()
	v.ed.onCodeChange()
	v.ed.updateInfo()
	v.render()
}

// insertText inserts s at the caret, replacing the selection.
func (v *codeView) insertText(s string) {
	if s == "" {
		return
	}
	if from, to, ok := v.selRange(); ok {
		v.anchor = to
		v.caret = to
		v.edit(v.offset(from), v.doc.TextRange(v.offset(from), v.offset(to)), s, false)
		return
	}
	// Typing a single rune coalesces with the run before it.
	merge := utf8.RuneCountInString(s) == 1 && !strings.ContainsRune(s, '\n')
	v.edit(v.offset(v.caret), "", s, merge)
}

// deleteSelection removes the selection if there is one, reporting whether it
// did.
func (v *codeView) deleteSelection() bool {
	from, to, ok := v.selRange()
	if !ok {
		return false
	}
	v.anchor = to
	v.caret = to
	v.edit(v.offset(from), v.doc.TextRange(v.offset(from), v.offset(to)), "", false)
	return true
}

// backspace deletes the character before the caret, or joins two lines.
func (v *codeView) backspace() {
	if v.deleteSelection() {
		return
	}
	p := v.caret
	if p.col > 0 {
		if v.backspaceIndent(p) {
			return
		}
		start := pos{row: p.row, col: p.col - 1}
		off := v.offset(start)
		v.anchor = start
		v.caret = start
		v.edit(off, v.doc.TextRange(off, v.offset(p)), "", v.caret.col == start.col)
		return
	}
	if p.row == 0 {
		return
	}
	prev := pos{row: p.row - 1, col: v.lineLen(p.row - 1)}
	off := v.offset(prev)
	v.anchor = prev
	v.caret = prev
	v.edit(off, v.doc.TextRange(off, v.offset(p)), "", false)
}

// deleteForward deletes the character after the caret, or joins the next line.
func (v *codeView) deleteForward() {
	if v.deleteSelection() {
		return
	}
	p := v.caret
	if p.col < v.lineLen(p.row) {
		end := pos{row: p.row, col: p.col + 1}
		off := v.offset(p)
		v.edit(off, v.doc.TextRange(off, v.offset(end)), "", false)
		return
	}
	if p.row >= v.docLines()-1 {
		return
	}
	end := pos{row: p.row + 1, col: 0}
	off := v.offset(p)
	v.edit(off, v.doc.TextRange(off, v.offset(end)), "", false)
}

// indents the caret's line by one level, or every selected line.
func (v *codeView) indent() {
	if v.HasSelection() && v.shiftLines(1) {
		return
	}
	unit := v.ed.indentString()
	row := v.caret.row
	was := v.caret.col
	off := v.doc.LineStart(row)
	v.edit(off, "", unit, false)
	v.setCaret(pos{row: row, col: was + utf8Count(unit)})
}

// outdents the caret's line by one level, if it starts with whitespace.
func (v *codeView) outdent() {
	if v.HasSelection() && v.shiftLines(-1) {
		return
	}
	row := v.caret.row
	indent := v.leadingSpace(row)
	if indent == "" {
		return
	}
	width := v.spaceWidth(indent)
	keep := v.indentTo(indent, (width-1)/v.indentUnit()*v.indentUnit())
	if keep >= utf8Count(indent) {
		return
	}
	col := v.caret.col - (utf8Count(indent) - keep)
	if col < 0 {
		col = 0
	}
	off := v.doc.LineStart(row)
	v.edit(off, indent[runeByteOffset(indent, keep):], "", false)
	v.setCaret(pos{row: row, col: col})
}

// moveLineUp swaps the caret's line with the one above it.
func (v *codeView) moveLineUp() {
	row := v.caret.row
	if row == 0 {
		return
	}
	v.swapLines(row-1, row)
	v.caret.row--
	v.anchor = v.caret
	v.followCaret()
	v.ed.onCodeChange()
	v.render()
}

// moveLineDown swaps the caret's line with the one below it.
func (v *codeView) moveLineDown() {
	row := v.caret.row
	if row >= v.docLines()-1 {
		return
	}
	v.swapLines(row, row+1)
	v.caret.row++
	v.anchor = v.caret
	v.followCaret()
	v.ed.onCodeChange()
	v.render()
}

// swapLines exchanges two adjacent lines in one reversible edit.
func (v *codeView) swapLines(a, b int) {
	start := v.doc.LineStart(a)
	end := v.doc.LineStart(b + 1)
	text := v.doc.TextRange(start, end)
	la := v.doc.LineText(a)
	lb := v.doc.LineText(b)
	swapped := lb + "\n" + la
	if b < v.docLines()-1 {
		swapped += "\n"
	}
	before := v.caret
	v.rawEdit(start, text, swapped)
	v.caret = before
	v.anchor = before
}

// duplicateLine inserts a copy of the caret's line below it.
func (v *codeView) duplicateLine() {
	row := v.caret.row
	line := v.doc.LineText(row)
	off := v.doc.LineStart(row) + len(line)
	v.rawEdit(off, "", "\n"+line)
	v.caret = pos{row: row + 1, col: v.caret.col}
	v.anchor = v.caret
	v.followCaret()
	v.ed.onCodeChange()
	v.ed.updateInfo()
	v.render()
}

// resetUndo clears the undo and redo history.
func (v *codeView) resetUndo() {
	v.undo, v.redo = nil, nil
	v.undoBytes = 0
}

// replaceAll swaps the whole document for text as one undoable edit.
func (v *codeView) replaceAll(old, text string) {
	v.rawEdit(0, old, text)
	v.caret, v.anchor = pos{}, pos{}
	v.followCaret()
	v.render()
}

// --- undo -------------------------------------------------------------------

// Undo reverts the last edit.
func (v *codeView) Undo() {
	n := len(v.undo)
	if n == 0 {
		v.ed.setStatus("nothing to undo")
		return
	}
	e := v.undo[n-1]
	v.undo = v.undo[:n-1]
	v.undoBytes -= len(e.old) + len(e.ins)
	v.applyReplace(e.off, e.ins, e.old)
	v.redo = append(v.redo, e)
	v.caret, v.anchor = e.caret, e.anchor
	v.followCaret()
	v.ed.onCodeChange()
	v.ed.dirty = e.dirty // back to the state the edit was made from
	v.ed.updateTitle()
	v.render()
}

// Redo reapplies the last undone edit.
func (v *codeView) Redo() {
	n := len(v.redo)
	if n == 0 {
		v.ed.setStatus("nothing to redo")
		return
	}
	e := v.redo[n-1]
	v.redo = v.redo[:n-1]
	v.applyReplace(e.off, e.old, e.ins)
	v.undo = append(v.undo, e)
	v.undoBytes += len(e.old) + len(e.ins)
	v.caret, v.anchor = e.after, e.after
	v.followCaret()
	v.ed.onCodeChange()
	v.render()
}

// --- caret movement ---------------------------------------------------------

// setCaret moves the caret and scrolls it into view.
func (v *codeView) setCaret(p pos) {
	v.caret = v.clampPos(p)
	v.anchor = v.caret
	v.followCaret()
	v.ed.updateInfo()
	v.render()
}

// moveCaret moves by rows and columns, extending the selection when extend is
// set. Moving up or down keeps the column where it was, clamped to the line.
func (v *codeView) moveCaret(dRow, dCol int, extend bool) {
	p := v.caret
	if dRow != 0 {
		p.row += dRow
		if p.row < 0 {
			p.row = 0
		}
		if p.row >= v.docLines() {
			p.row = v.docLines() - 1
		}
	}
	if dRow == 0 && dCol < 0 && p.col == 0 && p.row > 0 {
		// Left at the start of a line continues at the end of the one above.
		v.moveTo(pos{row: p.row - 1, col: v.lineLen(p.row - 1)}, extend)
		return
	}
	if dRow == 0 && dCol > 0 && p.col >= v.lineLen(p.row) && p.row < v.docLines()-1 {
		// Right at the end of a line continues at the start of the next.
		v.moveTo(pos{row: p.row + 1, col: 0}, extend)
		return
	}
	p.col += dCol
	p = v.clampPos(p)
	v.moveTo(p, extend)
}

// moveTo applies a new caret position, extending or dropping the selection.
func (v *codeView) moveTo(p pos, extend bool) {
	if v.caret == p && !extend {
		return
	}
	if extend {
		if v.anchor == v.caret {
			v.anchor = v.caret // start a new selection here
		}
	} else {
		v.anchor = p
	}
	v.caret = p
	v.followCaret()
	v.ed.updateInfo()
	v.render()
}

// wordEdge moves the caret to the next word boundary in the given direction.
func (v *codeView) wordEdge(dir int, extend bool) {
	p := v.caret
	row := p.row
	line := v.doc.LineText(row)
	runes := []rune(line)
	col := p.col

	isWord := func(r rune) bool {
		return r == '_' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') ||
			('A' <= r && r <= 'Z') || r > 127
	}

	if dir > 0 {
		if col >= len(runes) {
			if row < v.docLines()-1 {
				v.moveTo(pos{row: row + 1, col: 0}, extend)
			}
			return
		}
		// Skip the word under the caret, then the separators after it.
		for col < len(runes) && isWord(runes[col]) {
			col++
		}
		for col < len(runes) && !isWord(runes[col]) {
			col++
		}
		v.moveTo(pos{row: row, col: col}, extend)
		return
	}

	if col == 0 {
		if row > 0 {
			v.moveTo(pos{row: row - 1, col: v.lineLen(row - 1)}, extend)
		}
		return
	}
	// Skip the separators before the caret, then the word before them.
	col--
	for col > 0 && !isWord(runes[col]) {
		col--
	}
	if !isWord(runes[col]) {
		col++
	}
	for col > 0 && isWord(runes[col-1]) {
		col--
	}
	v.moveTo(pos{row: row, col: col}, extend)
}

// followCaret scrolls the document so the caret is visible.
func (v *codeView) followCaret() {
	vis := v.visibleLines()
	cg := v.caretRow()
	if cg < v.topRow {
		v.topRow = cg
	}
	if cg >= v.topRow+vis {
		v.topRow = cg - vis + 1
	}
	if max := v.totalRows() - vis; v.topRow > max {
		v.topRow = max
	}
	if v.topRow < 0 {
		v.topRow = 0
	}
	if v.wrap {
		v.xoffCell = 0
		return
	}

	cell := v.cellOf(v.caret)
	cells := v.visibleCells()
	if cell < v.xoffCell {
		v.xoffCell = cell
	}
	if cell >= v.xoffCell+cells {
		v.xoffCell = cell - cells + 1
	}
	if v.xoffCell < 0 {
		v.xoffCell = 0
	}
}

// scrollLines moves the view by delta lines.
func (v *codeView) scrollLines(delta int) {
	if v.doc == nil || delta == 0 {
		return
	}
	vis := v.visibleLines()
	max := v.totalRows() - vis
	if max < 0 {
		max = 0
	}
	v.topRow += delta
	if v.topRow > max {
		v.topRow = max
	}
	if v.topRow < 0 {
		v.topRow = 0
	}
	v.render()
}

// gotoLine moves the caret to a 0-based line and rune column.
func (v *codeView) gotoLine(row, col int) {
	v.setCaret(pos{row: row, col: col})
}

// --- cells ------------------------------------------------------------------

// cellOf is the display column of a position, with tabs expanded.
func (v *codeView) cellOf(p pos) int {
	line := v.doc.LineText(p.row)
	cells, n := 0, 0
	for _, r := range line {
		if n >= p.col {
			break
		}
		if r == '\t' {
			cells += v.ed.indentWidth - cells%v.ed.indentWidth
		} else {
			cells++
		}
		n++
	}
	return cells
}

// colAtCell is the rune column nearest to a display cell, used for clicks.
func (v *codeView) colAtCell(row, cell int) int {
	line := v.doc.LineText(row)
	cells, col := 0, 0
	for _, r := range line {
		w := 1
		if r == '\t' {
			w = v.ed.indentWidth - cells%v.ed.indentWidth
		}
		if cells+w > cell {
			// Inside or before this character: snap to the nearer boundary.
			if float32(cell-cells) > float32(w)*clickSlop {
				col++
			}
			return col
		}
		cells += w
		col++
	}
	return col
}

// --- drawing the visible row ------------------------------------------------

// maxRowSegments caps how many coloured runs one visual row may be drawn with, so
// a crowded line can only ever multiply the object count by eight whatever a
// definition asks for. Everything after the cap is drawn in the default colour.
const maxRowSegments = 8

// rowSegment is one coloured run of a visual row: the cell it starts at within
// the row, its display text (with tabs expanded) and its style.
type rowSegment struct {
	cell  int
	text  string
	class styleClass
}

// lineOf returns the stored text of line row, for painting.
func (v *codeView) lineOf(row int) *styledLine {
	if v.doc == nil {
		return &styledLine{}
	}
	if v.hl != nil {
		return v.hl.styled(v.doc, row)
	}
	v.plain.text = v.doc.LineText(row)
	v.plain.spans = nil
	return &v.plain
}

// rowSegments splits the cell window [fromCell, fromCell+maxCells) of the row into
// coloured runs, expanding tabs as the renderer wants them drawn. It returns the
// cell the row starts at, which is fromCell unless the line is shorter.
func (v *codeView) rowSegments(row, fromCell, maxCells int) ([]rowSegment, int) {
	sl := v.lineOf(row)
	segs := v.segBuf[:0]
	indent := v.ed.indentWidth
	if indent < 1 {
		indent = 1
	}
	cells, startCell := 0, -1
	ri, si := 0, 0
	var b strings.Builder
	curCell, cur := 0, stylePlain
	flush := func() {
		if b.Len() == 0 {
			return
		}
		segs = append(segs, rowSegment{cell: curCell, text: b.String(), class: cur})
		b.Reset()
	}
	for _, r := range sl.text {
		for si < len(sl.spans) && ri >= sl.spans[si].to {
			si++
		}
		cls := stylePlain
		if si < len(sl.spans) && ri >= sl.spans[si].from {
			cls = sl.spans[si].class
		}
		ri++
		w := 1
		if r == '\t' {
			w = indent - cells%indent
		}
		if cells+w <= fromCell {
			cells += w
			continue
		}
		if startCell < 0 {
			startCell = cells
		}
		if cells-startCell >= maxCells {
			break
		}
		if cls != cur && len(segs)+1 < maxRowSegments {
			flush()
			cur, curCell = cls, cells-startCell
		}
		if r == '\t' {
			for k := 0; k < w; k++ {
				b.WriteByte(' ')
			}
		} else {
			b.WriteRune(r)
		}
		cells += w
	}
	flush()
	if startCell < 0 {
		startCell = cells
	}
	v.segBuf = segs
	return segs, startCell
}

// posAtPoint is the caret position nearest to a point in widget coordinates.
func (v *codeView) posAtPoint(p fyne.Position) pos {
	lineH, charW := v.metrics()
	if lineH <= 0 || charW <= 0 {
		return v.caret
	}
	g := v.topRow + int((p.Y-v.inset())/lineH)
	// A click in the padding above or below the text still lands on a line.
	if g < v.topRow {
		g = v.topRow
	}
	if last := v.topRow + v.visibleLines() - 1; g > last {
		g = last
	}
	row, chunk := v.rowOf(g)
	cell := v.xoffCell + int(math.Round(float64((p.X-v.inset())/charW)))
	if v.wrap {
		if starts := v.wrapStarts(row); len(starts) > 0 {
			if chunk >= len(starts) {
				chunk = len(starts) - 1
			}
			cell = starts[chunk] + int(math.Round(float64((p.X-v.inset())/charW)))
		}
	}
	return pos{row: row, col: v.colAtCell(row, cell)}
}

// --- rendering --------------------------------------------------------------

// render redraws the visible lines, the selection, the caret and the scrollbar.
func (v *codeView) render() {
	if v.renderer_ == nil {
		return
	}
	v.renderer_.sync()
}

// refreshMetrics re-measures after a theme or zoom change and redraws.
func (v *codeView) refreshMetrics() {
	v.followCaret()
	v.render()
}

// --- widget interface -------------------------------------------------------

// MinSize is the smallest sensible size for the view.
func (v *codeView) MinSize() fyne.Size {
	lineH, _ := v.metrics()
	return fyne.NewSize(120, lineH*4)
}

// Scrolled moves the view with the wheel; Fyne also treats a Scrollable as a
// clip region, which is what keeps the lines out of the scrollbar gutter.
func (v *codeView) Scrolled(ev *fyne.ScrollEvent) {
	if ev.Scrolled.DY == 0 && ev.Scrolled.DX != 0 && !v.wrap {
		_, charW := v.metrics()
		if charW > 0 {
			v.xoffCell -= int(ev.Scrolled.DX / charW)
			if v.xoffCell < 0 {
				v.xoffCell = 0
			}
			v.render()
		}
		return
	}
	lineH, _ := v.metrics()
	if lineH <= 0 {
		return
	}
	if n := int(-ev.Scrolled.DY / lineH); n != 0 {
		v.scrollLines(n)
	}
}

// AcceptsTab takes the Tab key instead of moving focus.
func (v *codeView) AcceptsTab() bool { return true }

// FocusGained starts drawing the caret.
func (v *codeView) FocusGained() {
	v.focused = true
	// A key released while another window had focus never reaches us, which would
	// leave a modifier stuck down and make typing do nothing.
	v.shift, v.ctrl, v.alt = false, false, false
	v.render()
}

// FocusLost stops drawing the caret and forgets any held modifier: a key
// released while another window has focus would otherwise stay stuck down.
func (v *codeView) FocusLost() {
	v.focused = false
	v.shift, v.ctrl, v.alt = false, false, false
	v.render()
}

// Cursor is the text caret over the text and an arrow over the scrollbar: the
// bar is drawn by this widget, so without this the pointer would keep showing the
// editing cursor while dragging it.
func (v *codeView) Cursor() desktop.Cursor {
	if v.hoverBar {
		return desktop.DefaultCursor
	}
	return desktop.TextCursor
}

// MouseIn and MouseMoved keep track of whether the pointer is over the gutter.
func (v *codeView) MouseIn(ev *desktop.MouseEvent)    { v.setHover(ev.Position) }
func (v *codeView) MouseMoved(ev *desktop.MouseEvent) { v.setHover(ev.Position) }

// MouseOut leaves the text cursor showing again.
func (v *codeView) MouseOut() { v.hoverBar = false }

// setHover updates the scrollbar hover state from a pointer position.
func (v *codeView) setHover(p fyne.Position) {
	v.hoverBar = v.overBar(p.X)
}

// inGutter reports whether x is past the text, in the scrollbar gutter. Clicks
// there never move the caret, whether or not a bar is drawn.
func (v *codeView) inGutter(x float32) bool {
	return x >= v.Size().Width-v.barWidth()
}

// overBar reports whether x is over a bar that is actually drawn: a document that
// fits has no bar, so its gutter keeps the text cursor.
func (v *codeView) overBar(x float32) bool {
	return !v.thumb.Hidden && v.inGutter(x)
}

// TypedRune inserts a character at the caret.
func (v *codeView) TypedRune(r rune) {
	if v.doc == nil || v.ctrl || v.alt {
		return
	}
	v.insertText(string(r))
}

// TypedKey handles the navigation and editing keys.
func (v *codeView) TypedKey(ev *fyne.KeyEvent) {
	if v.doc == nil {
		return
	}
	extend := v.shift
	switch ev.Name {
	case fyne.KeyEnter, fyne.KeyReturn:
		v.newline()
	case fyne.KeyTab:
		switch {
		case v.shift:
			v.outdent()
		case v.HasSelection():
			v.indent()
		default:
			v.insertText(v.ed.indentString())
		}
	case fyne.KeyBackspace:
		v.backspace()
	case fyne.KeyDelete:
		v.deleteForward()
	case fyne.KeyLeft:
		if v.ctrl || v.alt {
			v.wordEdge(-1, extend)
		} else {
			v.moveCaret(0, -1, extend)
		}
	case fyne.KeyRight:
		if v.ctrl || v.alt {
			v.wordEdge(1, extend)
		} else {
			v.moveCaret(0, 1, extend)
		}
	case fyne.KeyUp:
		if v.ctrl {
			v.moveTo(pos{}, extend)
		} else {
			v.moveCaret(-1, 0, extend)
		}
	case fyne.KeyDown:
		if v.ctrl {
			last := v.docLines() - 1
			v.moveTo(pos{row: last, col: v.lineLen(last)}, extend)
		} else {
			v.moveCaret(1, 0, extend)
		}
	case fyne.KeyHome:
		if v.ctrl {
			v.moveTo(pos{}, extend)
		} else {
			v.moveTo(v.lineStartPos(), extend)
		}
	case fyne.KeyEnd:
		if v.ctrl {
			last := v.docLines() - 1
			v.moveTo(pos{row: last, col: v.lineLen(last)}, extend)
		} else {
			v.moveTo(v.lineEndPos(), extend)
		}
	case fyne.KeyPageUp:
		v.moveCaret(-v.visibleLines(), 0, extend)
	case fyne.KeyPageDown:
		v.moveCaret(v.visibleLines(), 0, extend)
	}
}

// KeyDown tracks the modifier keys, which is how shift-selection and the
// word-wise shortcuts know what is held.
func (v *codeView) KeyDown(ev *fyne.KeyEvent) {
	switch ev.Name {
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		v.shift = true
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		v.ctrl = true
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		v.alt = true
	}
}

// KeyUp clears the modifier state.
func (v *codeView) KeyUp(ev *fyne.KeyEvent) {
	switch ev.Name {
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		v.shift = false
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		v.ctrl = false
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		v.alt = false
	}
}

// TypedShortcut handles the clipboard and undo shortcuts.
func (v *codeView) TypedShortcut(sc fyne.Shortcut) {
	switch sc.(type) {
	case *fyne.ShortcutCopy:
		v.copySelection()
	case *fyne.ShortcutCut:
		if v.copySelection() {
			v.deleteSelection()
		}
	case *fyne.ShortcutPaste:
		v.paste()
	case *fyne.ShortcutSelectAll:
		v.selectAll()
	case *fyne.ShortcutUndo:
		v.Undo()
	case *fyne.ShortcutRedo:
		v.Redo()
	}
}

// copySelection puts the selected text on the clipboard.
func (v *codeView) copySelection() bool {
	text := v.selectedText()
	if text == "" {
		return false
	}
	v.ed.app.Clipboard().SetContent(text)
	return true
}

// paste inserts the clipboard at the caret.
func (v *codeView) paste() {
	text := v.ed.app.Clipboard().Content()
	if text == "" {
		return
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	v.insertText(text)
}

// --- mouse ------------------------------------------------------------------

// Tapped places the caret where the pointer is.
func (v *codeView) Tapped(ev *fyne.PointEvent) {
	if v.doc == nil || v.inGutter(ev.Position.X) {
		return
	}
	// Clicking the text is the way back to a widget that lost keyboard focus, for
	// instance because a dialog closed without giving it back.
	v.ed.takeFocus()
	v.dragging = false
	v.setCaret(v.posAtPoint(ev.Position))
}

// DoubleTapped selects the word under the pointer.
func (v *codeView) DoubleTapped(ev *fyne.PointEvent) {
	if v.doc == nil {
		return
	}
	v.setCaret(v.posAtPoint(ev.Position))
	line := v.doc.LineText(v.caret.row)
	runes := []rune(line)
	col := v.caret.col
	isWord := func(r rune) bool {
		return r == '_' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') ||
			('A' <= r && r <= 'Z') || r > 127
	}
	if col >= len(runes) || !isWord(runes[col]) {
		return
	}
	start := col
	for start > 0 && isWord(runes[start-1]) {
		start--
	}
	end := col
	for end < len(runes) && isWord(runes[end]) {
		end++
	}
	v.anchor = pos{row: v.caret.row, col: start}
	v.caret = pos{row: v.caret.row, col: end}
	v.render()
}

// MouseDown decides whether this drag is a selection or a scrollbar drag.
func (v *codeView) MouseDown(ev *desktop.MouseEvent) {
	v.dragging = true
	v.dragBar = v.inGutter(ev.Position.X)
	if v.dragBar {
		v.dragToBar(ev.Position.Y)
		return
	}
	v.ed.takeFocus()
	v.setCaret(v.posAtPoint(ev.Position))
}

// MouseUp ends a drag.
func (v *codeView) MouseUp(*desktop.MouseEvent) {
	v.dragging = false
}

// Dragged extends the selection or drags the scrollbar.
func (v *codeView) Dragged(ev *fyne.DragEvent) {
	if v.doc == nil {
		return
	}
	if v.dragBar {
		v.dragToBar(ev.Position.Y)
		return
	}
	if !v.dragging {
		return
	}
	// Dragging past the top or bottom edge scrolls, so a selection can run off
	// the visible area.
	lineH, _ := v.metrics()
	if lineH > 0 {
		if ev.Position.Y < dragScrollZone {
			v.scrollLines(-1)
		} else if ev.Position.Y > v.Size().Height-dragScrollZone {
			v.scrollLines(1)
		}
	}
	v.caret = v.clampPos(v.posAtPoint(ev.Position))
	v.render()
}

// DragEnd finishes a selection drag.
func (v *codeView) DragEnd() {
	v.dragging = false
}

// dragToBar scrolls so the scrollbar thumb sits under y.
func (v *codeView) dragToBar(y float32) {
	span := v.Size().Height - v.thumbHeight()
	if span <= 0 {
		return
	}
	max := v.totalRows() - v.visibleLines()
	if max <= 0 {
		return
	}
	top := int(float32(max) * (y - v.thumbHeight()/2) / span)
	if top > max {
		top = max
	}
	if top < 0 {
		top = 0
	}
	v.topRow = top
	v.render()
}

// thumbHeight is how tall the scrollbar thumb is drawn.
func (v *codeView) thumbHeight() float32 {
	total := float32(v.totalRows())
	vis := float32(v.visibleLines())
	h := v.Size().Height * vis / total
	if h < minThumbHeight {
		h = minThumbHeight
	}
	if h > v.Size().Height {
		h = v.Size().Height
	}
	return h
}

// --- renderer ---------------------------------------------------------------

// codeRenderer draws the visible lines of the document.
type codeRenderer struct {
	v       *codeView
	objects []fyne.CanvasObject
}

// Layout repositions everything; the widget does the work in sync.
func (r *codeRenderer) Layout(fyne.Size) { r.sync() }

// MinSize mirrors the widget's minimum size.
func (r *codeRenderer) MinSize() fyne.Size { return r.v.MinSize() }

// Refresh re-reads the theme colours and redraws.
func (r *codeRenderer) Refresh() {
	th := r.v.Theme()
	variant := r.v.ed.app.Settings().ThemeVariant()
	v := r.v
	v.bg.FillColor = th.Color(theme.ColorNameInputBackground, variant)
	v.caretRect.FillColor = th.Color(theme.ColorNamePrimary, variant)
	v.track.FillColor = th.Color(theme.ColorNameScrollBarBackground, variant)
	v.thumb.FillColor = th.Color(theme.ColorNameScrollBar, variant)
	// A matched bracket is marked with a soft box, not a coloured glyph: the glyph
	// belongs to the syntax highlighter.
	brush := color.NRGBAModel.Convert(th.Color(theme.ColorNamePrimary, variant)).(color.NRGBA)
	brush.A = 0x44
	v.brColor = brush
	for _, b := range v.brushes {
		b.FillColor = brush
		b.Refresh()
	}
	v.bg.Refresh()
	v.caretRect.Refresh()
	v.track.Refresh()
	v.thumb.Refresh()
	// The token colours follow the theme variant; sync repaints anything whose
	// colour changed.
	v.palette = syntaxPalette(variant)
	v.segDrop()
	for _, s := range v.sels {
		s.FillColor = th.Color(theme.ColorNameSelection, variant)
	}
	r.sync()
}

// Objects lists the drawing order: background, selection, text, caret, bars.
func (r *codeRenderer) Objects() []fyne.CanvasObject { return r.objects }

// Destroy drops the renderer's reference to the widget.
func (r *codeRenderer) Destroy() {}

// ensure grows the object pools. Text objects are pooled per row and segment, so
// a highlighted line can draw several coloured runs while scrolling allocates
// nothing.
func (r *codeRenderer) ensure(lines int) {
	v := r.v
	th := v.Theme()
	variant := v.ed.app.Settings().ThemeVariant()

	for len(v.segs) < lines {
		v.segs = append(v.segs, nil)
		v.segUsed = append(v.segUsed, 0)
	}
	for len(v.sels) < lines {
		v.sels = append(v.sels, canvas.NewRectangle(th.Color(theme.ColorNameSelection, variant)))
	}
	r.rebuildObjects()
}

// segText returns the text object for segment k of row, creating it on demand.
func (v *codeView) segText(row, k int) *canvas.Text {
	pool := v.segs[row]
	for len(pool) <= k {
		t := canvas.NewText("", v.palette[stylePlain])
		t.TextStyle = fyne.TextStyle{Monospace: true}
		t.TextSize = v.Theme().Size(theme.SizeNameText)
		pool = append(pool, t)
	}
	v.segs[row] = pool
	return pool[k]
}

// rebuildObjects lists the drawing order: background, selection, text, caret, bars.
func (r *codeRenderer) rebuildObjects() {
	v := r.v
	r.objects = r.objects[:0]
	r.objects = append(r.objects, v.bg)
	for _, s := range v.sels {
		r.objects = append(r.objects, s)
	}
	for _, b := range v.brushes {
		r.objects = append(r.objects, b)
	}
	for _, pool := range v.segs {
		for _, t := range pool {
			r.objects = append(r.objects, t)
		}
	}
	r.objects = append(r.objects, v.caretRect, v.track, v.thumb)
}

// textCount is how many text objects the view holds. It has to follow the size of
// the screen, never the size of the document.
func (v *codeView) textCount() int {
	n := 0
	for _, pool := range v.segs {
		n += len(pool)
	}
	return n
}

// drawnTexts lists every text object of the view, for tests.
func (v *codeView) drawnTexts() []*canvas.Text {
	out := make([]*canvas.Text, 0, v.textCount())
	for _, pool := range v.segs {
		out = append(out, pool...)
	}
	return out
}

// segDrop forgets the segment pools, e.g. after the row count of the document or
// the theme changed.
func (v *codeView) segDrop() {
	for _, pool := range v.segs {
		for _, t := range pool {
			t.Hide()
		}
	}
}

// sync lays out and fills the visible lines.
func (r *codeRenderer) sync() {
	v := r.v
	if v.doc == nil {
		return
	}
	lineH, charW := v.metrics()
	if lineH <= 0 || charW <= 0 {
		return
	}
	// The row index only exists while wrapping; rebuild it when the width or the
	// document changed under us (a find/replace elsewhere, say).
	if v.wrap && (v.rowsCells != v.wrapCells() || len(v.rows) != v.docLines()+1) {
		v.rebuildRows()
	}

	vis := v.visibleLines()
	r.ensure(vis + 1)

	inset := v.inset()
	width := v.Size().Width - inset
	v.bg.Move(fyne.NewPos(0, 0))
	v.bg.Resize(v.Size())

	total := v.totalRows()
	poolRow, poolStarts := -1, []int(nil)
	rowStartsFor := func(row int) []int {
		if row != poolRow {
			poolRow, poolStarts = row, v.wrapStarts(row)
		}
		return poolStarts
	}
	for i := range v.segs {
		if i >= vis || v.topRow+i >= total {
			for k := 0; k < v.segUsed[i]; k++ {
				v.segs[i][k].Hide()
			}
			v.segUsed[i] = 0
			continue
		}
		row, chunk := v.rowOf(v.topRow + i)
		from, limit := v.xoffCell, v.visibleCells()+1
		if v.wrap {
			starts := rowStartsFor(row)
			if chunk >= len(starts) {
				chunk = len(starts) - 1
			}
			from = starts[chunk]
			// A wrapped row ends where the next one begins, not at the window
			// edge: drawing past that would show the start of the next row.
			if chunk+1 < len(starts) {
				limit = starts[chunk+1] - from
			}
		}
		// The row is drawn as one run of text, or as several when the language
		// colours parts of it: each run is a cell offset and a colour.
		segs, _ := v.rowSegments(row, from, limit)
		used := 0
		for _, sg := range segs {
			if used >= maxRowSegments {
				break
			}
			t := v.segText(i, used)
			if t.Text != sg.text {
				t.Text = sg.text
				t.Refresh()
			}
			if c := v.palette[sg.class]; t.Color != c {
				t.Color = c
				t.Refresh()
			}
			at := fyne.NewPos(inset+float32(sg.cell)*charW, inset+float32(i)*lineH)
			if t.Position() != at {
				t.Move(at)
			}
			if sz := fyne.NewSize(width, lineH); t.Size() != sz {
				t.Resize(sz)
			}
			t.Show()
			used++
		}
		for k := used; k < v.segUsed[i]; k++ {
			v.segs[i][k].Hide()
		}
		v.segUsed[i] = used
	}
	r.rebuildObjects()

	r.syncSelection(vis, lineH, charW, inset)
	r.syncBrackets(lineH, charW, inset)
	r.syncCaret(lineH, charW, inset)

	// Scrollbar.
	size := v.Size()
	barW := v.barWidth()
	trackX := size.Width - barW
	v.track.Move(fyne.NewPos(trackX, 0))
	v.track.Resize(fyne.NewSize(barW, size.Height))
	if v.totalRows() <= vis {
		v.thumb.Hide()
	} else {
		v.thumb.Show()
		h := v.thumbHeight()
		max := float32(v.totalRows() - vis)
		y := (size.Height - h) * float32(v.topRow) / max
		v.thumb.Move(fyne.NewPos(trackX, y))
		v.thumb.Resize(fyne.NewSize(barW, h))
	}
}

// syncSelection draws one highlight per visible row that has selected text.
func (r *codeRenderer) syncSelection(vis int, lineH, charW, inset float32) {
	v := r.v
	for _, s := range v.sels {
		s.Hide()
	}
	from, to, ok := v.selRange()
	if !ok {
		return
	}
	total := v.totalRows()
	selRow, selStarts := -1, []int(nil)
	startsFor := func(row int) []int {
		if row != selRow {
			selRow, selStarts = row, v.wrapStarts(row)
		}
		return selStarts
	}
	for i := 0; i < vis; i++ {
		g := v.topRow + i
		if g >= total {
			continue
		}
		row, chunk := v.rowOf(g)
		if row < from.row || row > to.row {
			continue
		}
		startCol := 0
		if row == from.row {
			startCol = from.col
		}
		endCol := v.lineLen(row) + 1 // include the newline
		if row == to.row {
			endCol = to.col
		}
		lineCells := v.cellOf(pos{row: row, col: v.lineLen(row)})
		startCell := v.cellOf(pos{row: row, col: startCol})
		endCell := v.cellOf(pos{row: row, col: min(endCol, v.lineLen(row))})
		if endCol > v.lineLen(row) {
			endCell++ // the newline cell
		}
		// Clip the highlight to the piece of the line this visual row shows.
		lo, hi := v.xoffCell, v.xoffCell+v.visibleCells()
		if v.wrap {
			starts := startsFor(row)
			if chunk >= len(starts) {
				chunk = len(starts) - 1
			}
			lo = starts[chunk]
			if chunk+1 < len(starts) {
				hi = starts[chunk+1]
			} else {
				hi = lineCells + 1
			}
		}
		_ = lineCells
		if startCell < lo {
			startCell = lo
		}
		if endCell > hi {
			endCell = hi
		}
		if endCell <= startCell {
			continue
		}
		rect := v.sels[i]
		rect.Show()
		x := inset + float32(startCell-lo)*charW
		rect.Move(fyne.NewPos(x, inset+float32(i)*lineH))
		rect.Resize(fyne.NewSize(float32(endCell-startCell)*charW, lineH))
	}
}

// syncCaret draws the caret block.
func (r *codeRenderer) syncCaret(lineH, charW, inset float32) {
	v := r.v
	if !v.focused {
		v.caretRect.Hide()
		return
	}
	cell := v.cellOf(v.caret)
	origin := v.xoffCell
	if v.wrap {
		if starts := v.wrapStarts(v.caret.row); len(starts) > 0 {
			c := v.caretChunk()
			if c >= len(starts) {
				c = len(starts) - 1
			}
			origin = starts[c]
		}
	}
	x := inset + float32(cell-origin)*charW
	g := v.caretRow()
	y := inset + float32(g-v.topRow)*lineH
	if g < v.topRow || g >= v.topRow+v.visibleLines() {
		v.caretRect.Hide()
		return
	}
	v.caretRect.Show()
	v.caretRect.Move(fyne.NewPos(x, y))
	v.caretRect.Resize(fyne.NewSize(caretWidth, lineH))
}
