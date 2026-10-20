package editor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2/dialog"
)

const prefFindHist = "findHistory"

// maxHistory bounds the persisted find-history list.
const maxHistory = 20

// matcher unifies literal and regular-expression searching so the find/replace
// commands do not care which mode is active.
type matcher struct {
	query     string
	literal   bool
	matchCase bool
	re        *regexp.Regexp
}

// newMatcher compiles a matcher for query. Regex syntax errors are returned so
// the caller can surface them without touching the buffer.
func newMatcher(query string, regex, matchCase bool) (*matcher, error) {
	m := &matcher{query: query, literal: !regex, matchCase: matchCase}
	if !regex {
		return m, nil
	}
	expr := query
	if !matchCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	m.re = re
	return m, nil
}

// next returns the first match beginning at or after byte offset from.
func (m *matcher) next(text string, from int) (start, length int, ok bool) {
	if m.query == "" || from > len(text) {
		return 0, 0, false
	}
	if m.literal {
		i := findFrom(text, m.query, from, m.matchCase)
		if i < 0 {
			return 0, 0, false
		}
		return i, len(m.query), true
	}
	if from < 0 {
		from = 0
	}
	loc := m.re.FindStringIndex(text[from:])
	if loc == nil {
		return 0, 0, false
	}
	return from + loc[0], loc[1] - loc[0], true
}

// at reports whether a match begins exactly at byte offset off, returning its
// length.
func (m *matcher) at(text string, off int) (length int, ok bool) {
	if m.query == "" {
		return 0, false
	}
	if m.literal {
		if matchAt(text, m.query, off, m.matchCase) {
			return len(m.query), true
		}
		return 0, false
	}
	if off < 0 || off > len(text) {
		return 0, false
	}
	loc := m.re.FindStringSubmatchIndex(text[off:])
	if loc == nil || loc[0] != 0 {
		return 0, false
	}
	return loc[1] - loc[0], true
}

// expand returns the replacement for the match beginning at off, expanding
// capture-group references such as $1 in regex mode.
func (m *matcher) expand(text string, off int, repl string) string {
	if m.literal {
		return repl
	}
	loc := m.re.FindStringSubmatchIndex(text[off:])
	if loc == nil || loc[0] != 0 {
		return repl
	}
	return string(m.re.ExpandString(nil, repl, text[off:], loc))
}

// replaceAll replaces every match, returning the new text and match count.
func (m *matcher) replaceAll(text, repl string) (string, int) {
	if m.query == "" {
		return text, 0
	}
	if !m.literal {
		matches := m.re.FindAllStringIndex(text, -1)
		if len(matches) == 0 {
			return text, 0
		}
		return m.re.ReplaceAllString(text, repl), len(matches)
	}

	var b strings.Builder
	b.Grow(len(text))
	count := 0
	for i := 0; i < len(text); {
		if matchAt(text, m.query, i, m.matchCase) {
			b.WriteString(repl)
			i += len(m.query)
			count++
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		b.WriteString(text[i : i+size])
		i += size
	}
	if count == 0 {
		return text, 0
	}
	return b.String(), count
}

// ShowFind reveals the find & replace bar and focuses the find field.
func (e *Editor) ShowFind() {
	e.findBar.Show()
	e.histIndex = -1
	e.win.Canvas().Focus(e.findEntry)
	e.setStatus("find & replace")
}

// HideFind hides the find & replace bar and returns focus to the editor.
func (e *Editor) HideFind() {
	e.findBar.Hide()
	e.win.Canvas().Focus(e.entry)
}

// matcher builds the matcher for the current find-bar state, reporting syntax
// errors in the status line.
func (e *Editor) matcher() (*matcher, bool) {
	m, err := newMatcher(e.findEntry.Text, e.regexCheck.Checked, e.matchCase.Checked)
	if err != nil {
		e.setStatus("regex: " + err.Error())
		return nil, false
	}
	return m, true
}

// FindNext moves the cursor to the next match, wrapping around the buffer.
func (e *Editor) FindNext() {
	query := e.findEntry.Text
	if query == "" {
		e.ShowFind()
		return
	}
	m, ok := e.matcher()
	if !ok {
		return
	}
	e.recordFind(query)

	text := e.entry.Text
	start := runeOffset(text, e.entry.CursorRow, e.entry.CursorColumn)

	// If the cursor already sits on a match, start after it so "next" advances.
	from := start
	if n, ok := m.at(text, start); ok {
		from = start + n
	}

	idx, _, found := m.next(text, from)
	if !found {
		idx, _, found = m.next(text, 0) // wrap
	}
	if !found {
		e.setStatus("no matches for " + strconv.Quote(query))
		return
	}

	e.moveCursorTo(idx)
	e.setStatus(fmt.Sprintf("match at Ln %d, Col %d", e.entry.CursorRow+1, e.entry.CursorColumn+1))
}

// ReplaceCurrent replaces the match at the cursor, or advances to the next one.
func (e *Editor) ReplaceCurrent() {
	query := e.findEntry.Text
	if query == "" {
		e.ShowFind()
		return
	}
	m, ok := e.matcher()
	if !ok {
		return
	}
	e.recordFind(query)

	text := e.entry.Text
	start := runeOffset(text, e.entry.CursorRow, e.entry.CursorColumn)
	n, ok := m.at(text, start)
	if !ok {
		e.FindNext()
		return
	}

	repl := m.expand(text, start, e.replEntry.Text)
	e.replaceRange(start, n, repl)
	e.moveCursorTo(start + len(repl))
	e.setStatus("replaced 1 occurrence")
}

// ReplaceAll replaces every match in the buffer.
func (e *Editor) ReplaceAll() {
	query := e.findEntry.Text
	if query == "" {
		e.ShowFind()
		return
	}
	m, ok := e.matcher()
	if !ok {
		return
	}
	e.recordFind(query)

	text := e.entry.Text
	out, count := m.replaceAll(text, e.replEntry.Text)
	if count == 0 {
		e.setStatus("no matches for " + strconv.Quote(query))
		return
	}
	e.replaceRange(0, len(text), out)
	e.moveCursorTo(0)
	e.setStatus(fmt.Sprintf("replaced %d occurrences", count))
}

// replaceRange rewrites the whole buffer, replacing [start, start+n) with repl.
// widget.Entry exposes no range-edit API, so a full SetText is used; the
// snapshot is what keeps the rewrite undoable.
func (e *Editor) replaceRange(start, n int, repl string) {
	text := e.entry.Text
	if start < 0 || start+n > len(text) {
		return
	}
	next := text[:start] + repl + text[start+n:]
	if next == text {
		return
	}
	e.pushUndo()
	e.entry.SetText(next)
}

// moveCursorTo places the caret at a byte offset and scrolls it into view.
func (e *Editor) moveCursorTo(offset int) {
	row, col := rowColFromOffset(e.entry.Text, offset)
	e.entry.CursorRow, e.entry.CursorColumn = row, col
	e.entry.Refresh()
}

// recordFind promotes query to the front of the persisted find history.
func (e *Editor) recordFind(query string) {
	if query == "" {
		return
	}
	out := []string{query}
	for _, q := range e.findHistory() {
		if q != query && len(out) < maxHistory {
			out = append(out, q)
		}
	}
	e.app.Preferences().SetStringList(prefFindHist, out)
	e.histIndex = -1
	e.histDraft = ""
}

// findHistory returns the persisted find-history list (most recent first).
func (e *Editor) findHistory() []string {
	return e.app.Preferences().StringListWithFallback(prefFindHist, nil)
}

// historyPrev steps back through the find history (Alt+Up).
func (e *Editor) historyPrev() {
	hist := e.findHistory()
	if len(hist) == 0 {
		return
	}
	if e.histIndex == -1 {
		e.histDraft = e.findEntry.Text
	}
	if e.histIndex < len(hist)-1 {
		e.histIndex++
	}
	e.setFindText(hist[e.histIndex])
}

// historyNext steps forward through the find history (Alt+Down).
func (e *Editor) historyNext() {
	if e.histIndex == -1 {
		return
	}
	hist := e.findHistory()
	if e.histIndex > 0 {
		e.histIndex--
		e.setFindText(hist[e.histIndex])
		return
	}
	e.histIndex = -1
	e.setFindText(e.histDraft)
}

// setFindText replaces the find field contents with the caret at the end.
func (e *Editor) setFindText(s string) {
	e.findEntry.SetText(s)
	e.findEntry.CursorColumn = utf8.RuneCountInString(s)
	e.findEntry.Refresh()
}

// GoToLine asks for a line number and jumps to it.
func (e *Editor) GoToLine() {
	d := dialog.NewEntryDialog("Go to Line", "Line number:", func(value string) {
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 1 {
			e.setStatus("invalid line number")
			return
		}
		e.gotoLine(n)
	}, e.win)
	d.Show()
}

func (e *Editor) gotoLine(n int) {
	e.gotoLineCol(n, 0)
}

// gotoLineCol moves the caret to the 1-based line and (0-based) rune column.
func (e *Editor) gotoLineCol(line, col int) {
	text := e.entry.Text
	total := lineCount(text)
	if line < 1 {
		line = 1
	}
	if line > total {
		line = total
	}
	start, end := lineBounds(text, line-1)
	maxCol := utf8.RuneCountInString(text[start:end])
	if col < 0 {
		col = 0
	}
	if col > maxCol {
		col = maxCol
	}
	e.moveCursorTo(runeOffset(text, line-1, col))
	e.setStatus(fmt.Sprintf("line %d of %d", line, total))
}
