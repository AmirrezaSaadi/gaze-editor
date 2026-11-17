package editor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
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
	// anchored marks a pattern that describes the start of the text (^ \A \b \B).
	// Such a pattern has to be judged against the whole text: matching a slice of it
	// would make the slice's first byte look like the beginning and produce matches
	// that are not there.
	anchored bool
}

// maxAnchorMatches bounds how many matches of an anchored pattern are listed at
// once. Only patterns naming the start of the text pay this: the list exists so
// that the search can step past matches before the caret, and one is normally all
// there is.
const maxAnchorMatches = 200_000

// startSensitive reports whether a regular expression refers to the start of the
// text, which a suffix of the text would fake.
func startSensitive(expr string) bool {
	return strings.Contains(expr, "^") || strings.Contains(expr, `\b`) ||
		strings.Contains(expr, `\B`) || strings.Contains(expr, `\A`)
}

// newMatcher compiles a matcher for query. Regex syntax errors are returned so
// the caller can surface them without touching the buffer.
func newMatcher(query string, regex, matchCase bool) (*matcher, error) {
	m := &matcher{query: query, literal: !regex, matchCase: matchCase}
	if !regex {
		return m, nil
	}
	m.anchored = startSensitive(query)
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

// matches returns the matches of the regular expression in the whole buffer, up
// to maxAnchorMatches of them. It is only used for patterns that name the start of
// the text; see findFrom for why that has to be the whole text.
func (m *matcher) matches(text string) [][]int {
	if m.literal || m.re == nil {
		return nil
	}
	return m.re.FindAllStringSubmatchIndex(text, maxAnchorMatches)
}

// atMatch returns the submatch indices of the regex match beginning exactly at
// byte offset off.
func (m *matcher) atMatch(text string, off int) ([]int, bool) {
	if off < 0 || off > len(text) {
		return nil, false
	}
	loc := m.findFrom(text, off)
	if loc == nil || loc[0] != off {
		return nil, false
	}
	return loc, true
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
	if loc := m.findFrom(text, from); loc != nil {
		return loc[0], loc[1] - loc[0], true
	}
	return 0, 0, false
}

// findFrom returns the leftmost match at or after from, or nil.
//
// It asks the engine for one match instead of all of them: a search for a common
// word in a large file has millions of matches, and listing their offsets costs
// more memory than the file itself. A pattern anchored with ^ to the start of the
// text is the exception, since it has to be judged against the whole text and so
// is matched there.
func (m *matcher) findFrom(text string, from int) []int {
	if from > len(text) {
		return nil
	}
	if m.anchored {
		for _, loc := range m.matches(text) {
			if loc[0] >= from {
				return loc
			}
		}
		return nil
	}
	if from == 0 {
		return m.re.FindStringSubmatchIndex(text)
	}
	loc := m.re.FindStringSubmatchIndex(text[from:])
	if loc == nil {
		return nil
	}
	for i := range loc { // indexes are relative to the slice
		if loc[i] >= 0 {
			loc[i] += from
		}
	}
	return loc
}

// at reports whether a match begins exactly at byte offset off, returning its
// length.
func (m *matcher) at(text string, off int) (length int, ok bool) {
	if m.query == "" || off < 0 || off > len(text) {
		return 0, false
	}
	if m.literal {
		if matchAt(text, m.query, off, m.matchCase) {
			return len(m.query), true
		}
		return 0, false
	}
	loc, found := m.atMatch(text, off)
	if !found {
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
	loc, ok := m.atMatch(text, off)
	if !ok {
		return repl
	}
	return string(m.re.ExpandString(nil, repl, text, loc))
}

// replaceAll replaces every match, returning the new text and match count.
func (m *matcher) replaceAll(text, repl string) (string, int) {
	if m.query == "" {
		return text, 0
	}
	if !m.literal {
		// Counting through the callback keeps this to one pass over the text: the
		// engine reports each replacement as it makes it, so no list of every match
		// has to exist at once.
		count := 0
		expand := strings.ContainsRune(repl, '$')
		out := m.re.ReplaceAllStringFunc(text, func(match string) string {
			count++
			if !expand {
				return repl
			}
			return string(m.re.ExpandString(nil, repl, match, m.re.FindStringSubmatchIndex(match)))
		})
		if count == 0 {
			return text, 0
		}
		return out, count
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
	e.refreshMarks()
	e.histIndex = -1
	e.win.Canvas().Focus(e.findEntry)
	e.setStatus("find & replace")
}

// HideFind hides the find & replace bar and returns focus to the editor.
func (e *Editor) HideFind() {
	e.findBar.Hide()
	e.clearMarks()
	e.win.Canvas().Focus(e.focusTarget())
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

	start := e.caretOffset()

	// If the cursor already sits on a match, start after it so "next" advances.
	from := start
	if n, ok := e.matchAt(m, start); ok {
		from = start + n
	}

	idx, _, found := e.nextMatch(m, from)
	if !found && from > 0 {
		idx, _, found = e.nextMatch(m, 0) // wrap
	}
	if !found {
		e.setStatus("no matches for " + strconv.Quote(query))
		return
	}

	e.revealOffset(idx)
	line, col := e.caretLineCol()
	e.setStatus(fmt.Sprintf("match at Ln %d, Col %d", line, col))
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

	start := e.caretOffset()
	n, ok := e.matchAt(m, start)
	if !ok {
		e.FindNext()
		return
	}

	got := e.textRange(start, start+n)
	repl := m.expand(got, 0, e.replEntry.Text)
	if e.code != nil {
		e.code.edit(start, got, repl, false)
	}
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

	text := e.searchText()
	defer e.releaseSearchText(text)
	at := e.caretOffset()
	out, count := m.replaceAll(text, e.replEntry.Text)
	if count == 0 {
		e.setStatus("no matches for " + strconv.Quote(query))
		return
	}
	// Stay where the caret was rather than jumping to the top of the buffer.
	if at > len(out) {
		at = len(out)
	}
	if e.doc != nil && e.code != nil {
		// One undoable edit, even though the whole document is replaced.
		e.code.edit(0, text, out, false)
		e.code.setCaret(e.code.posAt(at))
	} else {
		e.code.replaceAll(text, out)
	}
	e.setStatus(fmt.Sprintf("replaced %d occurrences", count))
}

// replaceRange rewrites the whole buffer, replacing [start, start+n) with repl.
// widget.Entry exposes no range-edit API, so a full SetText is used; the
// snapshot is what keeps the rewrite undoable.

// moveCursorTo places the caret at a byte offset and scrolls it into view.

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

// searchText is the text find and replace work on.
// nextMatch returns the first match at or after from.
//
// A literal search reads the document's pages rather than asking for the text of
// the buffer: a large file is held as pages, or as a view of a mapping, and never as
// one string, so building that string just to look through it would cost the size of
// the file in memory for every search.
func (e *Editor) nextMatch(m *matcher, from int) (start, length int, ok bool) {
	if m.literal && e.doc != nil {
		if at := findInDoc(e.doc, m.query, from, m.matchCase); at >= 0 {
			return at, len(m.query), true
		}
		return 0, 0, false
	}
	if !m.literal && !m.anchored && e.doc != nil && e.doc.size > 2*regexWindow {
		return e.regexNext(m, from)
	}
	text := e.searchText()
	defer e.releaseSearchText(text)
	return m.next(text, from)
}

// textRange is text[from:to] of the document, empty when there is no document.
func (e *Editor) textRange(from, to int) string {
	if e.doc == nil {
		return ""
	}
	return e.doc.TextRange(from, to)
}

// matchAt returns the length of the match beginning exactly at off, if there is one.
func (e *Editor) matchAt(m *matcher, off int) (int, bool) {
	start, n, ok := e.nextMatch(m, off)
	if !ok || start != off {
		return 0, false
	}
	return n, true
}

// releaseSearchText hands back the memory a search needed when it could not run over
// the mapping directly. The regexp engine takes a string, so a file that was read
// into pages has to be joined for it; a search that copied hundreds of megabytes
// should not leave them counted against the heap afterwards.
func (e *Editor) releaseSearchText(text string) {
	if e.doc == nil {
		return
	}
	if _, ok := e.doc.Whole(); ok {
		return
	}
	freeMemory(len(text))
}

// regexWindow is how much of a large document is handed to the regexp engine at a
// time, and regexOverlap how far past the window a match may run. The engine keeps
// scratch space proportional to the text it is given — tens of megabytes for a file
// of a few hundred — so a big file is searched window by window instead.
const (
	regexWindow  = 1 << 20
	regexOverlap = 256 << 10
)

// regexNext returns the first match at or after from in a large document, without
// giving the engine the whole of it. A match longer than regexOverlap that runs
// across a window boundary is not found.
func (e *Editor) regexNext(m *matcher, from int) (start, length int, ok bool) {
	d := e.doc
	for off := from; off < d.size; {
		end := off + regexWindow
		if end > d.size {
			end = d.size
		}
		stop := end + regexOverlap
		if stop > d.size {
			stop = d.size
		}
		// Windows tile the document and only overhang past their end, so the
		// leftmost match in the first window that has one is the leftmost match.
		window, ok := d.ViewRange(off, stop)
		if !ok {
			window = d.TextRange(off, stop)
		}
		if loc := m.re.FindStringSubmatchIndex(window); loc != nil {
			return off + loc[0], loc[1] - loc[0], true
		}
		off = end
	}
	return 0, 0, false
}

// findInDoc returns the offset of the first occurrence of q at or after from, or -1.
//
// Pages are searched one at a time, so nothing the size of the document is allocated;
// a query that spans a page boundary is found by looking at the few bytes either side
// of it afterwards.
func findInDoc(d *Document, q string, from int, matchCase bool) int {
	if q == "" || d == nil || from < 0 || from > d.size {
		return -1
	}
	overlap := len(q) - 1
	for i := d.pageOf(from); i < len(d.pages); i++ {
		base := d.starts[i]
		beg := 0
		if from > base {
			beg = from - base
		}
		page := d.pages[i]
		if at := findFrom(page, q, beg, matchCase); at >= 0 {
			return base + at
		}
		if overlap <= 0 || i+1 >= len(d.pages) {
			continue
		}
		// The tail of this page and the head of the next, which is where a query
		// straddling the boundary lives.
		tail := page
		if len(tail) > overlap {
			tail = tail[len(tail)-overlap:]
		}
		head := d.pages[i+1]
		if len(head) > overlap {
			head = head[:overlap]
		}
		carryAt := base + len(page) - len(tail)
		skip := 0
		if carryAt < from {
			skip = from - carryAt // the carry starts before from, so forget that part
		}
		if at := findFrom(tail+head, q, skip, matchCase); at >= 0 {
			return carryAt + at
		}
	}
	return -1
}

// searchText is the text a search runs over. A large file that is mapped hands
// back a view of the mapping rather than a copy, so looking for something in it
// does not cost its size in memory.
func (e *Editor) searchText() string {
	if e.doc != nil {
		if whole, ok := e.doc.Whole(); ok {
			return whole
		}
	}
	return e.text()
}

// caretOffset is the caret's byte offset into the document.
func (e *Editor) caretOffset() int {
	if e.code == nil {
		return 0
	}
	return e.code.offset(e.code.caret)
}

// gotoLineCol moves the caret to the 1-based line and (0-based) rune column.
func (e *Editor) gotoLineCol(line, col int) {
	if e.doc == nil || e.code == nil {
		return
	}
	total := e.doc.LineCount()
	if line < 1 {
		line = 1
	}
	if line > total {
		line = total
	}
	if col < 0 {
		col = 0
	}
	if maxCol := utf8Count(e.doc.LineText(line - 1)); col > maxCol {
		col = maxCol
	}
	e.code.gotoLine(line-1, col)
	e.setStatus(fmt.Sprintf("line %d of %d", line, total))
}

// Search marks: while the find bar is open the scrollbar shows where the matches
// are, so a long file can be read at a glance instead of by jumping through it.
const (
	// maxFindMarks caps how many ticks are drawn: past this the bar is a solid
	// stripe and the objects would be spent for nothing.
	maxFindMarks = 400
	// findMarkWindow is how many bytes are scanned at a time.
	findMarkWindow = 64 << 10
	// findMarkScanBytes bounds how much of a file is scanned for marks, so a rare
	// query in a huge buffer cannot make the bar cost a full read.
	findMarkScanBytes = 32 << 20
)

// refreshMarks rescans the document for the current query in the background and
// hands the matching lines to the code view. It runs from the UI goroutine, reads
// everything it needs from the widgets there, and drops its result if a newer query
// has arrived meanwhile.
func (e *Editor) refreshMarks() {
	if e.code == nil || e.doc == nil || e.findBar == nil || !e.findBar.Visible() {
		return
	}
	seq := e.marksSeq.Add(1)
	if e.marksTimer != nil {
		e.marksTimer.Stop()
	}
	query := e.findEntry.Text
	regex := e.regexCheck.Checked
	matchCase := e.matchCase.Checked
	if query == "" {
		e.code.setMarks(nil)
		return
	}
	// The snapshot holds the page table, so the scan reads stable text while the
	// window keeps being edited, and needs no line index of its own.
	snap := e.doc.Snapshot()
	tab := e.active
	e.marksTimer = time.AfterFunc(findMarkDelay, func() {
		m, err := newMatcher(query, regex, matchCase)
		if err != nil {
			return // the status line already reports a bad pattern
		}
		rows := findMarkLines(snap, m)
		fyne.Do(func() {
			// A newer query, or another tab, has taken over: the rows belong to a
			// document that is no longer on screen.
			if e.marksSeq.Load() != seq || e.code == nil || e.active != tab {
				return
			}
			e.code.setMarks(rows)
		})
	})
}

// clearMarks takes the marks off the scrollbar.
func (e *Editor) clearMarks() {
	e.marksSeq.Add(1)
	if e.marksTimer != nil {
		e.marksTimer.Stop()
	}
	if e.code != nil {
		e.code.setMarks(nil)
	}
}

// findMarkLines returns the document rows that hold a match, at most maxFindMarks of
// them. It walks the pages a window at a time and counts the newlines it passes, so
// neither a copy of the text nor a line index is needed. A match that straddles a
// window boundary is not seen; an overview is not a search.
func findMarkLines(snap *Document, m *matcher) []int {
	if snap == nil {
		return nil
	}
	var rows []int
	scanned, line := 0, 0
	for _, page := range snap.pages {
		for off := 0; off < len(page); off += findMarkWindow {
			if len(rows) >= maxFindMarks || scanned >= findMarkScanBytes {
				return rows
			}
			end := off + findMarkWindow
			if end > len(page) {
				end = len(page)
			}
			window := page[off:end]
			scanned += len(window)
			prev := 0
			for from := 0; ; {
				start, size, ok := m.next(window, from)
				if !ok {
					break
				}
				line += strings.Count(window[prev:start], "\n")
				prev = start
				if n := len(rows); n == 0 || rows[n-1] != line {
					rows = append(rows, line)
					if len(rows) >= maxFindMarks {
						break
					}
				}
				if size == 0 {
					size = 1
				}
				from = start + size
			}
			line += strings.Count(window[prev:], "\n")
		}
	}
	return rows
}
