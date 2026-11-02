package editor

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// docModel is the naive oracle: the document as one string.
func modelWindow(text string, start, count int) (string, int) {
	lines := strings.Split(text, "\n")
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		start = len(lines) - 1
	}
	end := start + count
	if end > len(lines) {
		end = len(lines)
	}
	want := strings.Join(lines[start:end], "\n")
	if end > start && end < len(lines) {
		want += "\n"
	}
	return want, end - start
}

func modelRowCol(text string, off int) (row, col int) {
	row = strings.Count(text[:off], "\n")
	last := strings.LastIndexByte(text[:off], '\n')
	return row, utf8.RuneCountInString(text[last+1 : off])
}

func randomSnippet(rng *rand.Rand) string {
	choices := []string{"", "a", "b\n", "\n", "héllo", "😀", "x\ny\n", "  ", "one\ntwo"}
	return choices[rng.Intn(len(choices))]
}

// Every edit a window can produce has to leave the document exactly as a plain
// string would be, or the window and the buffer drift apart.
func TestDocumentMatchesStringModel(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	text := ""
	d := NewDocument(text)

	for i := 0; i < 3000; i++ {
		from := rng.Intn(len(text) + 1)
		span := len(text) - from
		if span > 12 {
			span = 12
		}
		to := from + rng.Intn(span+1)
		repl := randomSnippet(rng)

		text = text[:from] + repl + text[to:]
		d.ReplaceRange(from, to, repl)

		if got := d.Text(); got != text {
			t.Fatalf("edit %d: text\n got %q\nwant %q", i, got, text)
		}
		if got, want := d.Len(), len(text); got != want {
			t.Fatalf("edit %d: len = %d, want %d", i, got, want)
		}
		lines := strings.Split(text, "\n")
		if got, want := d.LineCount(), len(lines); got != want {
			t.Fatalf("edit %d: lines = %d, want %d (text %q)", i, got, want, text)
		}

		// Line starts must agree with the model.
		off := 0
		for j, ln := range lines {
			if got, want := d.LineStart(j), off; got != want {
				t.Fatalf("edit %d: LineStart(%d) = %d, want %d (text %q)", i, j, got, want, text)
			}
			off += len(ln) + 1
		}

		// Row/col and line lookup on a random offset.
		if len(text) > 0 {
			pos := rng.Intn(len(text))
			row, col := d.RowColOf(pos)
			wantRow, wantCol := modelRowCol(text, pos)
			if row != wantRow || col != wantCol {
				t.Fatalf("edit %d: RowColOf(%d) = (%d,%d), want (%d,%d) in %q",
					i, pos, row, col, wantRow, wantCol, text)
			}
			if got := d.LineOf(pos); got != wantRow {
				t.Fatalf("edit %d: LineOf(%d) = %d, want %d", i, pos, got, wantRow)
			}
		}

		// Windows, including ones that run off the end.
		start := rng.Intn(len(lines) + 1)
		count := rng.Intn(5)
		gotText, gotN := d.Window(start, count)
		wantText, wantN := modelWindow(text, start, count)
		if gotText != wantText || gotN != wantN {
			t.Fatalf("edit %d: Window(%d,%d) = %q,%d, want %q,%d in %q",
				i, start, count, gotText, gotN, wantText, wantN, text)
		}
	}
}

// Edits that straddle page boundaries are the interesting ones for the page
// bookkeeping.
func TestDocumentEditsAcrossPages(t *testing.T) {
	line := strings.Repeat("x", 63) + "\n" // 64 bytes per line
	text := strings.Repeat(line, 5000)     // ~320 KB, several pages
	d := NewDocument(text)
	if len(d.pages) < 4 {
		t.Fatalf("expected several pages, got %d", len(d.pages))
	}

	// Delete across a page boundary.
	from := docPageSize - 10
	to := docPageSize + 10
	want := text[:from] + text[to:]
	d.ReplaceRange(from, to, "")
	if got := d.Text(); got != want {
		t.Fatal("cross-page delete changed the wrong bytes")
	}
	if got, wantLines := d.LineCount(), len(strings.Split(want, "\n")); got != wantLines {
		t.Fatalf("lines = %d, want %d", got, wantLines)
	}

	// Insert a newline exactly at a page boundary.
	d = NewDocument(text)
	want = text[:docPageSize] + "\n" + text[docPageSize:]
	d.ReplaceRange(docPageSize, docPageSize, "\n")
	if got := d.Text(); got != want {
		t.Fatal("boundary insert changed the wrong bytes")
	}
	if got, wantLines := d.LineCount(), len(strings.Split(want, "\n")); got != wantLines {
		t.Fatalf("lines = %d, want %d", got, wantLines)
	}
	if row, col := d.RowColOf(docPageSize); row != d.LineOf(docPageSize) || col != 0 {
		t.Fatalf("caret after the inserted newline = (%d,%d), want col 0", row, col)
	}

	// Replace everything with nothing and back.
	d.ReplaceRange(0, d.Len(), "")
	if d.Len() != 0 || d.LineCount() != 1 {
		t.Fatalf("empty document = %d bytes, %d lines", d.Len(), d.LineCount())
	}
	d.ReplaceRange(0, 0, "a\nb")
	if got := d.Text(); got != "a\nb" {
		t.Fatalf("refill = %q", got)
	}
}

// eachLine has to produce the same lines as the materialised text, even when a
// line spans pages.
func TestDocumentEachLine(t *testing.T) {
	text := strings.Repeat("a", docPageSize-1) + "b\nsecond\nthird"
	d := NewDocument(text)

	var got []string
	var newlines []bool
	if err := d.eachLine(func(line string, hasNewline bool) error {
		got = append(got, line)
		newlines = append(newlines, hasNewline)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	want := strings.Split(text, "\n")
	if len(got) != len(want) {
		t.Fatalf("eachLine gave %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
		wantNewline := i < len(want)-1
		if newlines[i] != wantNewline {
			t.Fatalf("line %d hasNewline = %v, want %v", i, newlines[i], wantNewline)
		}
	}
}
