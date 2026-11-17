package editor

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// bigDoc builds a document of n pages filled with a repeated line, which is enough
// to reach every boundary the search has to handle.
func bigDoc(t *testing.T, lines int) string {
	t.Helper()
	var b strings.Builder
	b.Grow(lines * 32)
	for i := 0; i < lines; i++ {
		b.WriteString("a line of filler text here\n")
	}
	return b.String()
}

// A query that spans the boundary between two pages still has to be found, in both
// directions of case sensitivity.
func TestFindInDocAcrossPages(t *testing.T) {
	body := bigDoc(t, 20000) // ~520 KB, eight pages
	for _, matchCase := range []bool{true, false} {
		for _, at := range []int{docPageSize - 3, 2*docPageSize - 1, 3 * docPageSize} {
			text := body[:at] + "NEEDLE-XYZ" + body[at:]
			d := NewDocument(text)
			if len(d.pages) < 3 {
				t.Fatalf("the document has %d pages, wanted several", len(d.pages))
			}
			query := "NEEDLE-XYZ"
			if !matchCase {
				query = "needle-xyz"
			}
			if got := findInDoc(d, query, 0, matchCase); got != at {
				t.Fatalf("matchCase=%v at %d: found at %d", matchCase, at, got)
			}
			// Searching from just past it finds nothing, from the match finds it.
			if got := findInDoc(d, query, at, matchCase); got != at {
				t.Fatalf("matchCase=%v from %d: found %d", matchCase, at, got)
			}
			if got := findInDoc(d, query, at+1, matchCase); got != -1 {
				t.Fatalf("matchCase=%v from %d: found %d", matchCase, at+1, got)
			}
		}
	}
}

// A query that spans the boundary between two pages of a real buffer is found by
// Find, whose page walking has to agree with a plain string search.
func TestFindAcrossPagesInTheBuffer(t *testing.T) {
	at := docPageSize - 4
	text := bigDoc(t, 5000)[:at] + "needle" + bigDoc(t, 10)
	e := newTestEditor(t)
	setBuffer(e, text)
	e.findEntry.SetText("needle")
	e.code.setCaret(pos{})
	e.FindNext()
	if got := e.caretOffset(); got != at {
		t.Fatalf("the caret is at %d, wanted %d", got, at)
	}
}

// A regex match that crosses the boundary of the window handed to the engine is
// still found, and windowed searching agrees with searching the whole text.
func TestRegexSearchAcrossWindows(t *testing.T) {
	base := bigDoc(t, 400000) // ~10.8 MB, several windows
	cases := []struct {
		body  string
		query string
		at    int
	}{
		{base[:regexWindow-3] + "needle one" + base[regexWindow-3:], "needle", regexWindow - 3},
		{base[:2*regexWindow-2] + "needle two" + base[2*regexWindow-2:], `needle\s+two`, 2*regexWindow - 2},
		{base + "needle last", "needle", len(base)},
	}
	for _, c := range cases {
		d := NewDocument(c.body)
		if d.size < 2*regexWindow {
			t.Fatalf("the document is only %d bytes", d.size)
		}
		e := newTestEditor(t)
		e.doc = d
		e.code.setDoc(d)
		e.findEntry.SetText(c.query)
		e.regexCheck.SetChecked(true)
		e.code.setCaret(pos{})
		e.FindNext()

		m, ok := e.matcher()
		if !ok {
			t.Fatal("the query was rejected")
		}
		start, _, ok := e.nextMatch(m, 0)
		if !ok {
			t.Fatalf("%q: no match found, wanted %d", c.query, c.at)
		}
		if start != c.at {
			t.Fatalf("%q: found at %d, wanted %d", c.query, start, c.at)
		}
		// The windowed result has to be the one the whole text gives.
		want, _, _ := m.next(d.Text(), 0)
		if start != want {
			t.Fatalf("%q: windowed %d, whole text %d", c.query, start, want)
		}
	}
}

// Searching a large buffer must not copy it: the document is pages, or a view of a
// mapping, and a search that built the whole text would cost the file's size in
// memory every time.
func TestSearchDoesNotCopyTheBuffer(t *testing.T) {
	body := bigDoc(t, 500000) // ~13.5 MB, many pages
	e := newTestEditor(t)
	setBuffer(e, body+"needle at the end\n")
	// The editor's own working set over the whole buffer — the word counts and the
	// row index — is built once and would otherwise be counted against the search.
	waitFor(t, "the counts", func() bool { return e.statsKnown })
	e.code.render()
	waitFor(t, "the rows", func() bool { return len(e.code.rows) > 0 })

	m, err := newMatcher("needle", false, false)
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, _, found := e.nextMatch(m, 0); !found {
		t.Fatal("the literal search found nothing")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 32<<10 {
		t.Fatalf("a literal search allocated %d bytes for a %d byte buffer", grew, len(body))
	}

	// A regex over a mapping is read window by window, so it never has the file as
	// one string either.
	path := t.TempDir() + "/big.txt"
	if err := os.WriteFile(path, []byte(body+"needle at the end\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mapped := newTestEditor(t)
	mapped.startOpen(path, openLarge, nil)
	waitFor(t, "the file to open", func() bool { return mapped.currentFile == path })
	if !mapped.doc.mapped() {
		t.Fatal("the file was not mapped")
	}
	waitFor(t, "the counts", func() bool { return mapped.statsKnown })
	mapped.code.render()
	re, err := newMatcher("needle", true, false)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, _, found := mapped.nextMatch(re, 0); !found {
		t.Fatal("the regex search found nothing")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 512<<10 {
		t.Fatalf("a regex search allocated %d bytes for a %d byte mapped buffer", grew, len(body))
	}
}

// A regex has to keep meaning the same thing when the document is searched in
// windows, including where the caret is and when it wraps.
func TestRegexWindowsStepAndWrap(t *testing.T) {
	body := strings.Repeat("alpha beta gamma delta\n", 400000) // ~9.6 MB
	e := newTestEditor(t)
	setBuffer(e, body+"alpha end\n")
	e.findEntry.SetText("alpha")
	e.regexCheck.SetChecked(true)
	e.code.setCaret(e.code.posAt(e.doc.size)) // at the end, so the first find wraps
	e.FindNext()
	first, _ := e.caretLineCol()
	if first != 1 {
		t.Fatalf("first match at line %d", first)
	}
	e.FindNext()
	if second, _ := e.caretLineCol(); second != 2 {
		t.Fatalf("second match at line %d, wanted 2", second)
	}
	e.code.setCaret(e.code.posAt(e.doc.size - 1))
	e.FindNext() // wraps back to the top
	if got, _ := e.caretLineCol(); got != 1 {
		t.Fatalf("the wrap landed on line %d", got)
	}
}
