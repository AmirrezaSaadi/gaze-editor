package editor

import (
	"strings"
	"testing"
	"time"
)

// marksEditor installs text and makes the mark scans immediate.
func marksEditor(t *testing.T, text string) *Editor {
	t.Helper()
	old := findMarkDelay
	findMarkDelay = 5 * time.Millisecond
	t.Cleanup(func() { findMarkDelay = old })
	e := newTestEditor(t)
	setBuffer(e, text)
	return e
}

// findMarks types a query into the find bar and waits for the marks.
func findMarks(t *testing.T, e *Editor, query string) []int {
	t.Helper()
	e.findEntry.SetText(query)
	waitFor(t, "the marks to be scanned", func() bool { return e.code.marks != nil || query == "" })
	return e.code.marks
}

// The rows that hold a match, one mark per line however many matches it has.
func TestFindMarkLines(t *testing.T) {
	e := marksEditor(t, "one two\nthree\ntwo two\nfour\n")
	m, err := newMatcher("two", false, true)
	if err != nil {
		t.Fatal(err)
	}
	rows := findMarkLines(e.doc.Snapshot(), m)
	if len(rows) != 2 || rows[0] != 0 || rows[1] != 2 {
		t.Fatalf("marks at %v, want [0 2]", rows)
	}
}

// The scan stops at the mark cap rather than walking the whole buffer.
func TestFindMarkLinesAreCapped(t *testing.T) {
	e := marksEditor(t, strings.Repeat("match\n", maxFindMarks+500))
	m, err := newMatcher("match", false, true)
	if err != nil {
		t.Fatal(err)
	}
	rows := findMarkLines(e.doc.Snapshot(), m)
	if len(rows) != maxFindMarks {
		t.Fatalf("%d marks, want the cap of %d", len(rows), maxFindMarks)
	}
	if rows[0] != 0 || rows[len(rows)-1] != maxFindMarks-1 {
		t.Fatalf("marks run from %d to %d", rows[0], rows[len(rows)-1])
	}
}

// Marks are found across window boundaries, not just in the first window.
func TestFindMarkLinesAcrossWindows(t *testing.T) {
	var b strings.Builder
	// Three windows of hay, then the needle.
	for b.Len() < findMarkWindow*3 {
		b.WriteString("hay, hay, hay, hay\n")
	}
	lines := strings.Count(b.String(), "\n")
	b.WriteString("needle\n")
	b.WriteString(strings.Repeat("hay\n", 10))

	e := marksEditor(t, b.String())
	m, err := newMatcher("needle", false, true)
	if err != nil {
		t.Fatal(err)
	}
	rows := findMarkLines(e.doc.Snapshot(), m)
	if len(rows) != 1 || rows[0] != lines {
		t.Fatalf("marks = %v, want one at line %d", rows, lines)
	}
}

// Typing in the find bar marks the scrollbar, and closing it takes the marks away.
func TestFindMarksShowAndClear(t *testing.T) {
	e := marksEditor(t, strings.Repeat("alpha\nbeta\nalpha\n", 100))
	e.ShowFind()
	if got := findMarks(t, e, "alpha"); len(got) != 200 {
		t.Fatalf("%d marks, want 200", len(got))
	}
	// The ticks are drawn along the bar, in order.
	e.code.render()
	if got := len(e.code.markRects); got != 200 {
		t.Fatalf("%d tick objects, want 200", got)
	}
	first, last := e.code.markRects[0], e.code.markRects[199]
	if !first.Visible() || !last.Visible() {
		t.Fatal("the ticks are not drawn")
	}
	if last.Position().Y <= first.Position().Y {
		t.Fatalf("ticks at y=%v and y=%v are not in order", first.Position().Y, last.Position().Y)
	}
	if first.Position().X < e.code.Size().Width-e.code.barWidth() {
		t.Fatalf("a tick at x=%v is outside the scrollbar", first.Position().X)
	}

	e.HideFind()
	if e.code.marks != nil {
		t.Fatalf("marks = %v after closing the find bar", e.code.marks)
	}
	e.code.render()
	for _, r := range e.code.markRects {
		if r.Visible() {
			t.Fatal("a tick stayed after the find bar closed")
		}
	}
}

// Editing rescans the marks, so they follow the text.
func TestFindMarksFollowEdits(t *testing.T) {
	e := marksEditor(t, "alpha\nbeta\n")
	e.ShowFind()
	if got := findMarks(t, e, "alpha"); len(got) != 1 {
		t.Fatalf("%d marks, want 1", len(got))
	}
	text(e, "alpha ") // a second match on the same line: still one mark
	waitFor(t, "the marks to be rescanned", func() bool { return len(e.code.marks) == 1 })
	e.code.setCaret(pos{row: 1, col: 4})
	text(e, "\nalpha")
	waitFor(t, "a fourth line", func() bool { return e.doc.LineCount() == 4 })
	waitFor(t, "the new match to be marked", func() bool { return len(e.code.marks) == 2 })
}

// An empty query marks nothing rather than everything.
func TestFindMarksWithNoQuery(t *testing.T) {
	e := marksEditor(t, "alpha\n")
	e.ShowFind()
	findMarks(t, e, "alpha")
	if len(e.code.marks) != 1 {
		t.Fatalf("marks = %v", e.code.marks)
	}
	e.findEntry.SetText("")
	waitFor(t, "the marks to be cleared", func() bool { return e.code.marks == nil })
}

// A bad pattern leaves the previous marks alone instead of failing out loud: the
// status line already reports it.
func TestFindMarksWithABadRegex(t *testing.T) {
	e := marksEditor(t, "alpha\n")
	e.ShowFind()
	findMarks(t, e, "alpha")
	e.regexCheck.SetChecked(true)
	e.findEntry.SetText("a(")
	time.Sleep(30 * time.Millisecond)
	if len(e.code.marks) != 0 && len(e.code.marks) != 1 {
		t.Fatalf("marks = %v after a bad pattern", e.code.marks)
	}
}
