package editor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// runeOffset converts a (row, col) cursor position — as used by widget.Entry,
// where col is measured in runes — into a byte offset in text. It clamps to
// the end of the buffer when the row is past the last line.
func runeOffset(text string, row, col int) int {
	off := 0
	for r := 0; r < row; r++ {
		nl := strings.IndexByte(text[off:], '\n')
		if nl < 0 {
			return len(text)
		}
		off += nl + 1
	}
	for c := 0; c < col && off < len(text); c++ {
		_, size := utf8.DecodeRuneInString(text[off:])
		if size == 0 {
			break
		}
		off += size
	}
	return off
}

// rowColFromOffset is the inverse of runeOffset.
func rowColFromOffset(text string, off int) (row, col int) {
	if off > len(text) {
		off = len(text)
	}
	for i := 0; i < off; {
		_, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		if text[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
		i += size
	}
	return row, col
}

// matchAt reports whether q matches text exactly at byte offset off.
func matchAt(text, q string, off int, matchCase bool) bool {
	if off < 0 || off+len(q) > len(text) {
		return false
	}
	got := text[off : off+len(q)]
	if matchCase {
		return got == q
	}
	return strings.EqualFold(got, q)
}

// findFrom returns the byte offset of the first match of q in text at or after
// from, or -1. Matching honours matchCase and never splits a rune.
func findFrom(text, q string, from int, matchCase bool) int {
	if q == "" || from < 0 || from > len(text) {
		return -1
	}
	if matchCase {
		if i := strings.Index(text[from:], q); i >= 0 {
			return from + i
		}
		return -1
	}
	for i := from; i+len(q) <= len(text); {
		if strings.EqualFold(text[i:i+len(q)], q) {
			return i
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		i += size
	}
	return -1
}

// countWords counts whitespace-separated words without allocating.
func countWords(text string) int {
	n, inWord := 0, false
	for _, r := range text {
		if unicode.IsSpace(r) {
			inWord = false
		} else if !inWord {
			inWord = true
			n++
		}
	}
	return n
}

// lineCount reports the number of logical lines (newline-separated) in text.
func lineCount(text string) int {
	if text == "" {
		return 1
	}
	return strings.Count(text, "\n") + 1
}

// Line-ending identifiers persisted and shown in the status bar.
const (
	eolLF   = "LF"
	eolCRLF = "CRLF"
	eolCR   = "CR"
)

// normalizeEOL converts every line ending in text to \n, reporting which
// convention the file used so it can be restored on save. LF-only text is
// returned as-is, without copying it.
func normalizeEOL(text string) (string, string) {
	if !strings.ContainsRune(text, '\r') {
		return text, eolLF
	}
	style := eolCR
	if strings.Contains(text, "\r\n") {
		style = eolCRLF
	}

	var b strings.Builder
	b.Grow(len(text))
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\r' {
			continue
		}
		b.WriteString(text[start:i])
		b.WriteByte('\n')
		if i+1 < len(text) && text[i+1] == '\n' {
			i++ // \r\n is one line ending
		}
		start = i + 1
	}
	b.WriteString(text[start:])
	return b.String(), style
}

// indentScanLimit bounds how much of a file detectIndent looks at. The style is
// settled by the first indented line, so there is no reason to walk a whole
// multi-megabyte buffer — and splitting it into lines would allocate a header per
// line on the way.
const indentScanLimit = 64 << 10

// detectIndent guesses the indentation style from the first indented line,
// falling back to four spaces. Only the first indentScanLimit bytes are examined.
func detectIndent(text string) (useTabs bool, width int) {
	if len(text) > indentScanLimit {
		text = text[:indentScanLimit]
	}
	for len(text) > 0 {
		line := text
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			line, text = text[:nl], text[nl+1:]
		} else {
			text = ""
		}
		i := 0
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i == 0 || i >= len(line) {
			continue // unindented or blank line
		}
		if line[0] == '\t' {
			return true, 1
		}
		if i > 16 {
			i = 16
		}
		return false, i
	}
	return false, 4
}

// lineBounds returns the byte offsets [start, end) of the 0-based line row in
// text. end excludes the trailing newline; rows past the end clamp to len(text).
func lineBounds(text string, row int) (int, int) {
	start := 0
	for r := 0; r < row; r++ {
		nl := strings.IndexByte(text[start:], '\n')
		if nl < 0 {
			return len(text), len(text)
		}
		start += nl + 1
	}
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		return start, len(text)
	}
	return start, start + end
}

// moveLineUp swaps line row with the line above it, returning the row to place
// the caret on (one up). Rows at the top of the buffer are unchanged.
func moveLineUp(text string, row int) (string, int) {
	if row <= 0 || row >= lineCount(text) {
		return text, row
	}
	prevStart, prevEnd := lineBounds(text, row-1)
	curStart, curEnd := lineBounds(text, row)
	if curStart <= prevStart {
		return text, row
	}
	prev := text[prevStart:prevEnd]
	cur := text[curStart:curEnd]
	return text[:prevStart] + cur + "\n" + prev + text[curEnd:], row - 1
}

// moveLineDown swaps line row with the line below it, returning the row to place
// the caret on (one down). The last line is unchanged.
func moveLineDown(text string, row int) (string, int) {
	if row < 0 || row >= lineCount(text) {
		return text, row
	}
	curStart, curEnd := lineBounds(text, row)
	if curEnd >= len(text) {
		return text, row // last line, no newline to swap across
	}
	nextStart, nextEnd := lineBounds(text, row+1)
	cur := text[curStart:curEnd]
	next := text[nextStart:nextEnd]
	return text[:curStart] + next + "\n" + cur + text[nextEnd:], row + 1
}

// duplicateLine copies line row directly beneath itself.
func duplicateLine(text string, row int) (string, int) {
	if text == "" || row < 0 || row >= lineCount(text) {
		return text, row
	}
	start, end := lineBounds(text, row)
	line := text[start:end]
	return text[:end] + "\n" + line + text[end:], row + 1
}

// indentLine inserts indent at the start of line row.
func indentLine(text string, row int, indent string) string {
	start, _ := lineBounds(text, row)
	return text[:start] + indent + text[start:]
}

// outdentLine removes one indentation level from line row, returning the number
// of bytes removed.
func outdentLine(text string, row int, useTabs bool, width int) (string, int) {
	start, end := lineBounds(text, row)
	line := text[start:end]
	removed := 0
	if useTabs && len(line) > 0 && line[0] == '\t' {
		removed = 1
	} else {
		for removed < width && removed < len(line) && line[removed] == ' ' {
			removed++
		}
	}
	if removed == 0 {
		return text, 0
	}
	return text[:start] + text[start+removed:], removed
}

// trimTrailingSpace strips spaces and tabs from the end of every line. It
// returns the original string unchanged (no allocation) when there is nothing to
// trim, and builds the result in one pass otherwise.
func trimTrailingSpace(text string) string {
	if !hasTrailingSpace(text) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	start := 0
	for i := 0; i <= len(text); i++ {
		if i < len(text) && text[i] != '\n' {
			continue
		}
		b.WriteString(strings.TrimRight(text[start:i], " \t"))
		if i < len(text) {
			b.WriteByte('\n')
		}
		start = i + 1
	}
	return b.String()
}

// hasTrailingSpace reports whether any line ends in a space or tab, without
// allocating.
func hasTrailingSpace(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		if i > 0 && (text[i-1] == ' ' || text[i-1] == '\t') {
			return true
		}
	}
	if n := len(text); n > 0 && (text[n-1] == ' ' || text[n-1] == '\t') {
		return true
	}
	return false
}
