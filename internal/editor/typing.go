package editor

import (
	"strings"
	"unicode/utf8"
)

// This file holds the parts of typing that know about code structure: keeping the
// indentation of the line you broke, opening a level after a block opener,
// backspacing over one level of indentation, and indenting a whole selection.

// indentUnit is the width of one level, guarding the arithmetic against a
// document that reports no indent width at all.
func (v *codeView) indentUnit() int {
	if w := v.ed.indentWidth; w > 0 {
		return w
	}
	return 4
}

// leadingSpace returns the run of spaces and tabs line row begins with.
func (v *codeView) leadingSpace(row int) string {
	line := v.doc.LineText(row)
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

// spaceWidth is how many cells a run of spaces and tabs takes.
func (v *codeView) spaceWidth(s string) int {
	unit := v.indentUnit()
	width := 0
	for _, r := range s {
		if r == '\t' {
			width += unit - width%unit
		} else {
			width++
		}
	}
	return width
}

// indentTo is the number of runes of prefix that remain once its width is at most
// target: the indentation above the previous stop.
func (v *codeView) indentTo(prefix string, target int) int {
	if target < 0 {
		target = 0
	}
	keep := utf8Count(prefix)
	for keep > 0 && v.spaceWidth(prefix[:runeByteOffset(prefix, keep)]) > target {
		keep--
	}
	return keep
}

// newline inserts a line break the way a code editor does: the new line keeps the
// indentation of the line it starts on, opens a level after a block opener, and
// pushes a closing bracket that was sitting at the caret onto its own line.
func (v *codeView) newline() {
	if v.doc == nil {
		return
	}
	if !v.ed.smartTyping {
		v.insertText("\n")
		return
	}
	// A selection is replaced by the break, and the line it was on decides the
	// indentation.
	v.deleteSelection()

	row := v.caret.row
	line := v.doc.LineText(row)
	col := v.caret.col
	if runes := utf8Count(line); col > runes {
		col = runes
	}
	cut := runeByteOffset(line, col)
	before, after := line[:cut], line[cut:]

	base := v.leadingSpace(row)
	if col < utf8Count(base) {
		// The caret sits in the indentation itself, so the break starts below it
		// rather than repeating the whole prefix.
		base = base[:runeByteOffset(base, col)]
	}
	unit := v.ed.indentString()
	ins, back := "\n"+base, 0
	if opens, closer := blockOpener(before); opens {
		if closer != 0 && firstRune(after) == closer {
			// Braces that were closed on one line get their closing line back.
			ins = "\n" + base + unit + "\n" + base
			back = len(base) + 1
		} else {
			ins += unit
		}
	}
	off := v.offset(v.caret)
	v.edit(off, "", ins, false)
	if back > 0 {
		v.moveTo(v.posAt(off+len(ins)-back), false)
	}
}

// blockOpener reports whether the text up to the caret opens a block, and which
// rune closes it when it has one.
func blockOpener(before string) (bool, rune) {
	s := strings.TrimRight(before, " \t")
	if s == "" {
		return false, 0
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	switch r {
	case '{':
		return true, '}'
	case '(':
		return true, ')'
	case '[':
		return true, ']'
	case ':':
		// Python blocks, YAML mappings and case labels all continue deeper.
		return true, 0
	}
	return false, 0
}

// firstRune is the first rune of s that is not a space or a tab.
func firstRune(s string) rune {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

// backspaceIndent removes one indentation level when the caret is inside a line's
// leading whitespace, so a backspace in the margin undoes the level rather than a
// single column. It reports whether it did anything.
func (v *codeView) backspaceIndent(p pos) bool {
	if !v.ed.smartTyping || p.col == 0 {
		return false
	}
	line := v.doc.LineText(p.row)
	if p.col > utf8Count(line) {
		return false
	}
	prefix := line[:runeByteOffset(line, p.col)]
	if strings.Trim(prefix, " \t") != "" {
		return false // there is real text before the caret
	}
	width := v.spaceWidth(prefix)
	if width == 0 {
		return false
	}
	unit := v.indentUnit()
	keep := v.indentTo(prefix, (width-1)/unit*unit)
	if keep >= utf8Count(prefix) {
		return false
	}
	start := pos{row: p.row, col: keep}
	off := v.offset(start)
	v.anchor = start
	v.caret = start
	v.edit(off, v.doc.TextRange(off, v.offset(p)), "", false)
	return true
}

// lineBlock is the row range a whole-line command covers: the selection expanded
// to whole lines, or the caret's line. A selection that stops at the very start of
// a line does not reach that line.
func (v *codeView) lineBlock() (first, last int, ok bool) {
	if v.doc == nil {
		return 0, 0, false
	}
	first, last = v.caret.row, v.caret.row
	if from, to, sel := v.selRange(); sel {
		first, last = from.row, to.row
		if last > first && to.col == 0 {
			last--
		}
	}
	if lines := v.docLines(); last >= lines {
		last = lines - 1
	}
	if first > last {
		first = last
	}
	return first, last, last >= 0
}

// shiftCol moves a position by the per-line indentation deltas of a block edit.
func (v *codeView) shiftCol(p pos, first int, deltas []int) pos {
	if p.row < first || p.row-first >= len(deltas) {
		return p
	}
	p.col += deltas[p.row-first]
	if p.col < 0 {
		p.col = 0
	}
	return v.clampPos(p)
}

// shiftLines indents (delta > 0) or outdents (delta < 0) the selected lines, or the
// caret's line, as a single undoable edit.
func (v *codeView) shiftLines(delta int) bool {
	first, last, ok := v.lineBlock()
	if !ok {
		return false
	}
	start, end := v.doc.LineStart(first), v.doc.LineStart(last+1)
	text := v.doc.TextRange(start, end)
	if text == "" {
		return false
	}
	lines := strings.Split(text, "\n")
	unit := v.ed.indentString()
	unitRunes := utf8Count(unit)
	width := v.indentUnit()
	deltas := make([]int, len(lines))
	changed := false
	for i, ln := range lines {
		if i == len(lines)-1 && ln == "" {
			continue // the newline that ends the block, not a line of its own
		}
		if delta > 0 {
			if ln == "" {
				continue // an empty line has no indentation to deepen
			}
			lines[i] = unit + ln
			deltas[i] = unitRunes
			changed = true
			continue
		}
		body := strings.TrimLeft(ln, " \t")
		indent := ln[:len(ln)-len(body)]
		w := v.spaceWidth(indent)
		if w == 0 {
			continue
		}
		keep := v.indentTo(indent, (w-1)/width*width)
		lines[i] = indent[:runeByteOffset(indent, keep)] + body
		deltas[i] = keep - utf8Count(indent)
		changed = true
	}
	if !changed {
		return false
	}
	v.rawEdit(start, text, strings.Join(lines, "\n"))
	v.caret = v.shiftCol(v.caret, first, deltas)
	v.anchor = v.shiftCol(v.anchor, first, deltas)
	v.followCaret()
	v.ed.onCodeChange()
	v.ed.updateInfo()
	v.render()
	return true
}
