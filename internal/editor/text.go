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
// convention the file used so it can be restored on save.
func normalizeEOL(text string) (string, string) {
	if strings.Contains(text, "\r\n") {
		return strings.ReplaceAll(text, "\r\n", "\n"), eolCRLF
	}
	if strings.ContainsRune(text, '\r') {
		return strings.ReplaceAll(text, "\r", "\n"), eolCR
	}
	return text, eolLF
}

// detectIndent guesses the indentation style from the first indented line,
// falling back to four spaces.
func detectIndent(text string) (useTabs bool, width int) {
	for _, line := range strings.Split(text, "\n") {
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
// returns the original string unchanged (no allocation) when there is nothing
// to trim.
func trimTrailingSpace(text string) string {
	changed := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimRight(line, " \t") != line {
			changed = true
			break
		}
	}
	if !changed {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}
