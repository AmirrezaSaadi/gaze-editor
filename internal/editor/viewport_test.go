package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

// bigText builds a document large enough to be opened in large-file mode.
func bigText(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line %d of the big file\n", i)
	}
	return b.String()
}

// openLargeDoc opens text in large-file mode and returns the editor and the path
// it pretends to have come from.
func openLargeDoc(t *testing.T, text string) (*Editor, string) {
	t.Helper()
	e := newTestEditor(t)
	path := filepath.Join(t.TempDir(), "big.txt")
	e.setContentAs(text, path, encUTF8, openLarge)
	if e.doc == nil {
		t.Fatal("large-file mode did not create a document")
	}
	return e, path
}

// docLine returns a document line as the user would see it.
func docLine(e *Editor, row int) string {
	return e.doc.LineText(row)
}

// topLine is the document line shown at the top of the viewport.
func topLine(e *Editor) int { return e.view.top + e.view.offset }

// Only a window of the document may ever reach widget.Entry: that is the whole
// point of large-file mode.
func TestLargeFileWindowIsBounded(t *testing.T) {
	text := bigText(200000)
	e, _ := openLargeDoc(t, text)

	if got, want := e.doc.LineCount(), 200001; got != want {
		t.Fatalf("document lines = %d, want %d", got, want)
	}
	window := e.view.visibleLines() + 2*viewportMargin
	if e.view.lines != window {
		t.Fatalf("window = %d lines, want %d", e.view.lines, window)
	}
	if got := strings.Count(e.largeEntry.Text, "\n"); got != window {
		t.Fatalf("entry holds %d lines, want %d", got, window)
	}
	// The text in the entry must be a small fraction of the file.
	if len(e.largeEntry.Text)*20 > len(text) {
		t.Fatalf("entry holds %d bytes of a %d byte document", len(e.largeEntry.Text), len(text))
	}
	if e.entry.Text != "" {
		t.Fatalf("the normal entry should be empty, holds %d bytes", len(e.entry.Text))
	}
	if e.largeArea.Hidden != false || e.normalArea.Hidden != true {
		t.Fatalf("wrong widget shown: large hidden=%v normal hidden=%v",
			e.largeArea.Hidden, e.normalArea.Hidden)
	}

	// Scrolling all the way down and back up must keep the window bounded.
	for i := 0; i < 250; i++ {
		e.view.scrollLines(1000)
	}
	if got := e.view.lines; got != window {
		t.Fatalf("window grew to %d lines while scrolling", got)
	}
	if got, want := topLine(e), e.doc.LineCount()-e.view.visibleLines(); got != want {
		t.Fatalf("scrolled to line %d, want the last screen %d", got, want)
	}
	for i := 0; i < 250; i++ {
		e.view.scrollLines(-1000)
	}
	if got := topLine(e); got != 0 {
		t.Fatalf("scrolled back to line %d, want 0", got)
	}
}

// Scrolling has to land on the requested line, and small moves must not refill.
func TestLargeFileScrolling(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(50000))

	e.view.scrollLines(500)
	if got := topLine(e); got != 500 {
		t.Fatalf("after scrolling 500 lines the top line is %d, want 500", got)
	}
	if got, want := docLine(e, topLine(e)), "line 500 of the big file"; got != want {
		t.Fatalf("top line text = %q, want %q", got, want)
	}

	// A move inside the window must not replace the entry's text.
	before := e.largeEntry.Text
	e.view.scrollLines(5)
	if got := topLine(e); got != 505 {
		t.Fatalf("top line = %d, want 505", got)
	}
	if e.largeEntry.Text != before {
		t.Fatal("a small scroll refilled the window")
	}

	// The wheel scrolls the same way: negative DY is "down" in Fyne.
	e.view.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, -e.view.lineHeight()*3)})
	if got := topLine(e); got != 508 {
		t.Fatalf("after one wheel tick the top line is %d, want 508", got)
	}
	e.view.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, e.view.lineHeight())})
	if got := topLine(e); got != 507 {
		t.Fatalf("after scrolling up the top line is %d, want 507", got)
	}

	// Neither end may scroll past the document.
	e.view.scrollLines(-100000)
	if got := topLine(e); got != 0 {
		t.Fatalf("top line = %d, want 0", got)
	}
	e.view.scrollLines(1000000)
	if got, want := topLine(e), e.doc.LineCount()-e.view.visibleLines(); got != want {
		t.Fatalf("top line = %d, want %d", got, want)
	}
}

// The caret's document position must survive a refill, and moving it to the edge
// of the window must scroll the window rather than lose it.
func TestLargeFileCaretFollowsWindow(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(50000))

	e.view.showLine(1000, 2)
	if line, col := e.caretLineCol(); line != 1001 || col != 3 {
		t.Fatalf("caret at %d:%d, want 1001:3", line, col)
	}
	if got, want := e.largeEntry.CursorRow, 1000-e.view.top; got != want {
		t.Fatalf("entry row = %d, want %d", got, want)
	}
	// The caret must be inside the visible band.
	if r := e.largeEntry.CursorRow; r < e.view.offset || r >= e.view.offset+e.view.visibleLines() {
		t.Fatalf("caret row %d is outside the visible band [%d,%d)",
			r, e.view.offset, e.view.offset+e.view.visibleLines())
	}

	// Walking the caret down drags the window along: the caret must stay on
	// screen at every step, even when that means refilling the window.
	startTop := e.view.top
	for i := 0; i < 120; i++ {
		e.largeEntry.CursorRow++
		e.onCursorChanged()
		r := e.largeEntry.CursorRow
		if r < e.view.offset || r >= e.view.offset+e.view.visibleLines() {
			t.Fatalf("step %d: caret row %d outside the visible band [%d,%d)",
				i, r, e.view.offset, e.view.offset+e.view.visibleLines())
		}
	}
	if e.view.top == startTop {
		t.Fatal("the window did not follow the caret")
	}
	if line, _ := e.caretLineCol(); line != 1121 {
		t.Fatalf("caret line = %d, want 1121", line)
	}
}

// Typing in the window has to reach the document, and a save has to write the
// whole document with that edit in it.
func TestLargeFileEditReachesDisk(t *testing.T) {
	text := bigText(30000)
	e, path := openLargeDoc(t, text)

	e.largeEntry.CursorRow, e.largeEntry.CursorColumn = 0, 0
	e.largeEntry.TypedRune('X')
	if !e.dirty {
		t.Fatal("typing should mark the document dirty")
	}
	e.flushWindow()
	if got, want := docLine(e, 0), "Xline 0 of the big file"; got != want {
		t.Fatalf("document line 0 = %q, want %q", got, want)
	}

	e.writeTo(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "X" + text; string(data) != want {
		t.Fatalf("file is %d bytes, want %d (edit lost or document truncated)", len(data), len(want))
	}
	if e.dirty {
		t.Fatal("a finished save should clear the dirty flag")
	}
}

// An edit made at one place must survive scrolling far away and back.
func TestLargeFileEditSurvivesScrolling(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(100000))

	e.view.showLine(10, 0)
	e.largeEntry.CursorColumn = 0
	e.largeEntry.TypedRune('>')

	e.view.scrollLines(50000)
	if got := docLine(e, 10); got != ">line 10 of the big file" {
		t.Fatalf("edit lost on the way down: %q", got)
	}
	e.view.scrollLines(-50000)
	if got := docLine(e, 10); got != ">line 10 of the big file" {
		t.Fatalf("edit lost on the way back: %q", got)
	}
	if !strings.Contains(e.largeEntry.Text, ">line 10 of the big file") {
		t.Fatal("the window does not show the edit")
	}
}

// A newline typed in the window changes the document's line count, and the line
// after it must stay intact.
func TestLargeFileNewlineEdit(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(5000))
	before := e.doc.LineCount()

	e.view.showLine(100, 0)
	e.largeEntry.CursorColumn = 0
	e.largeEntry.TypedRune('\n')
	e.flushWindow()

	if got, want := e.doc.LineCount(), before+1; got != want {
		t.Fatalf("lines = %d, want %d", got, want)
	}
	if got := docLine(e, 100); got != "" {
		t.Fatalf("new line = %q, want empty", got)
	}
	if got := docLine(e, 101); got != "line 100 of the big file" {
		t.Fatalf("line after the break = %q", got)
	}
	// Go to the last line: the tail of the document must still be there.
	e.gotoLineCol(e.doc.LineCount(), 0)
	if got := docLine(e, e.doc.LineCount()-2); got != "line 4999 of the big file" {
		t.Fatalf("last line = %q", got)
	}
}

// Go to line works anywhere in the document, not just inside the window.
func TestLargeFileGotoLine(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(200000))

	e.gotoLineCol(150000, 3)
	if line, col := e.caretLineCol(); line != 150000 || col != 4 {
		t.Fatalf("caret at %d:%d, want 150000:4", line, col)
	}
	if got := e.largeEntry.Text; !strings.Contains(got, "line 149999 of the big file") {
		t.Fatal("the window does not contain the line jumped to")
	}
	// Out-of-range requests clamp instead of failing.
	e.gotoLineCol(999999, 0)
	if line, _ := e.caretLineCol(); line != e.doc.LineCount() {
		t.Fatalf("caret line = %d, want %d", line, e.doc.LineCount())
	}
	e.gotoLineCol(-5, 0)
	if line, col := e.caretLineCol(); line != 1 || col != 1 {
		t.Fatalf("caret at %d:%d, want 1:1", line, col)
	}
}

// Find has to search the document, not just the window, and reveal the match.
func TestLargeFileFind(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(100000))
	e.findEntry.SetText("line 77777 of")

	e.FindNext()
	if line, _ := e.caretLineCol(); line != 77778 {
		t.Fatalf("found at line %d, want 77778", line)
	}
	if top := topLine(e); top > 77777 || top+e.view.visibleLines() <= 77777 {
		t.Fatalf("match line 77777 is not on screen (top %d, %d visible)", top, e.view.visibleLines())
	}

	// A second search wraps to the same single match.
	e.FindNext()
	if line, _ := e.caretLineCol(); line != 77778 {
		t.Fatalf("after wrapping the caret is at line %d, want 77778", line)
	}

	// A missing query is reported, not silently ignored.
	e.findEntry.SetText("nothing matches this")
	e.FindNext()
	if !strings.Contains(e.status.Text, "no matches") {
		t.Fatalf("status = %q, want a no-matches message", e.status.Text)
	}
}

// Replace all rewrites the whole document, however large it is.
func TestLargeFileReplaceAll(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(50000))
	e.findEntry.SetText("big file")
	e.replEntry.SetText("huge file")

	e.ReplaceAll()
	if got := e.doc.LineCount(); got != 50001 {
		t.Fatalf("lines = %d after replace all", got)
	}
	if got := docLine(e, 49999); got != "line 49999 of the huge file" {
		t.Fatalf("last line = %q", got)
	}
	if strings.Contains(e.doc.Text(), "big file") {
		t.Fatal("replace all left occurrences behind")
	}
	if !strings.Contains(e.status.Text, "50000 occurrences") {
		t.Fatalf("status = %q", e.status.Text)
	}
}

// Replace current edits the document in place and leaves the caret after the
// replacement.
func TestLargeFileReplaceCurrent(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(20000))
	e.findEntry.SetText("line 4242 of")
	e.replEntry.SetText("row 4242 of")

	e.FindNext()
	e.ReplaceCurrent()
	if got := docLine(e, 4242); got != "row 4242 of the big file" {
		t.Fatalf("line 4242 = %q", got)
	}
	// The caret ends up just after the replacement, on the same line.
	if line, col := e.caretLineCol(); line != 4243 || col != 12 {
		t.Fatalf("caret at %d:%d, want 4243:12", line, col)
	}
	if !e.dirty {
		t.Fatal("a replacement should mark the document dirty")
	}
}

// Saving a large document must reproduce it byte for byte, in every encoding and
// line ending, because it is written page by page rather than from one string.
func TestLargeFileSaveRoundTrips(t *testing.T) {
	cases := []struct {
		name string
		eol  string
		enc  string
	}{
		{"lf-utf8", eolLF, encUTF8},
		{"crlf-utf8", eolCRLF, encUTF8},
		{"cr-utf8", eolCR, encUTF8},
		{"lf-utf16le", eolLF, encUTF16LE},
		{"crlf-utf16be", eolCRLF, encUTF16BE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, path := openLargeDoc(t, bigText(20000))
			e.eol, e.enc = tc.eol, tc.enc
			e.writeTo(path)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text, enc, err := readDocument(strings.NewReader(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			text, _ = normalizeEOL(text)
			if enc != tc.enc {
				t.Fatalf("encoding on disk = %q, want %q", enc, tc.enc)
			}
			if text != bigText(20000) {
				t.Fatalf("round trip changed the document (%d bytes)", len(text))
			}
		})
	}
}

// The window and the document must not drift apart when an edit lands in the
// middle of the window and the view is refilled afterwards.
func TestLargeFileWindowStaysInSync(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(40000))

	e.view.showLine(20000, 0)
	// Type three characters on three consecutive document lines.
	for i := 0; i < 3; i++ {
		e.view.setCaret(20000+i, 0)
		e.largeEntry.TypedRune('*')
	}
	e.flushWindow()

	if got, want := e.windowText, e.largeEntry.Text; got != want {
		t.Fatal("window text is not what the entry holds")
	}
	if got := docLine(e, 20000); got != "*line 20000 of the big file" {
		t.Fatalf("line 20000 = %q", got)
	}
	if got := docLine(e, 20002); got != "*line 20002 of the big file" {
		t.Fatalf("line 20002 = %q", got)
	}

	// Refilling from the document must show the edits back.
	e.view.scrollLines(1000)
	e.view.scrollLines(-1000)
	if !strings.Contains(e.largeEntry.Text, "*line 20000 of the big file") {
		t.Fatal("the refilled window lost an edit")
	}

	// Saving after all that writes the edited document.
	path := filepath.Join(t.TempDir(), "out.txt")
	e.writeTo(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), bigText(20000)) {
		t.Fatal("saved file does not start with the untouched prefix")
	}
	if !strings.Contains(string(data), "*line 20000 of the big file") {
		t.Fatal("saved file lost the edits")
	}
}

// A small file opened after a large one goes back to the plain entry, so large
// mode never leaks into normal editing.
func TestSmallFileAfterLargeUsesPlainEntry(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(20000))

	e.setContentAs("hello\nworld\n", "/tmp/small.txt", encUTF8, openNormal)
	if e.largeFile || e.doc != nil {
		t.Fatal("opening a small file left large-file mode on")
	}
	if e.entry.Text != "hello\nworld\n" {
		t.Fatalf("entry = %q", e.entry.Text)
	}
	if e.normalArea.Hidden || !e.largeArea.Hidden {
		t.Fatalf("wrong widget shown: large hidden=%v normal hidden=%v",
			e.largeArea.Hidden, e.normalArea.Hidden)
	}
	if e.buffer() != e.entry {
		t.Fatal("the plain entry should be the buffer again")
	}
	// And the wire is back: typing reaches the entry directly.
	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.entry.TypedRune('H')
	if e.entry.Text != "Hhello\nworld\n" {
		t.Fatalf("entry after typing = %q", e.entry.Text)
	}
}

// The scrollbar thumb appears only when there is something to scroll, and
// dragging it moves the document.
func TestLargeFileScrollbarThumb(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(100000))
	if e.view.thumb.Hidden {
		t.Fatal("the thumb should be visible in a long document")
	}
	before := topLine(e)
	e.view.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(0, 120)})
	if got := topLine(e); got <= before {
		t.Fatalf("dragging the thumb moved the top line from %d to %d", before, got)
	}
	if got := topLine(e); got >= e.doc.LineCount() {
		t.Fatalf("dragging the thumb scrolled past the end: %d", got)
	}

	// A document that fits needs no scrollbar.
	e2, _ := openLargeDoc(t, "one\ntwo\n")
	if !e2.view.thumb.Hidden {
		t.Fatal("the thumb should be hidden when the document fits")
	}
}

// A taller window must hold more lines, not leave a blank strip at the bottom.
func TestLargeFileWindowGrowsWithViewport(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(50000))
	small := e.view.lines
	smallVis := e.view.visibleLines()

	e.win.Resize(fyne.NewSize(900, 1300))
	if got := e.view.visibleLines(); got <= smallVis {
		t.Fatalf("visible lines = %d after resizing, want more than %d", got, smallVis)
	}
	if got, want := e.view.lines, e.view.visibleLines()+2*viewportMargin; got != want {
		t.Fatalf("window = %d lines, want %d (was %d)", got, want, small)
	}
}

// A save writes the document as it was when the request was made: typing while
// the background write runs must not tear the file or change what it contains.
func TestLargeFileSaveUsesSnapshot(t *testing.T) {
	text := bigText(30000)
	e, path := openLargeDoc(t, text)

	req := e.pendingSave(path)
	if req.doc == nil {
		t.Fatal("a large-file save request should carry a document")
	}

	// Edit after the snapshot has been taken.
	e.view.setCaret(5, 0)
	e.largeEntry.TypedRune('Z')
	e.flushWindow()
	if got := docLine(e, 5); got != "Zline 5 of the big file" {
		t.Fatalf("the edit did not reach the document: %q", got)
	}

	if _, err := req.write(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != text {
		t.Fatalf("the save captured edits made after the request (%d bytes, want %d)",
			len(data), len(text))
	}
	if e.doc.LineCount() != 30001 {
		t.Fatalf("document lines = %d, want 30001", e.doc.LineCount())
	}
}

// Session restore has to remember a document position, not a window position,
// or a large file reopens at the top of the wrong window.
func TestLargeFileSessionSavesDocumentCaret(t *testing.T) {
	e, _ := openLargeDoc(t, bigText(50000))

	e.gotoLineCol(30000, 4)
	e.saveSession(e.currentFile)

	prefs := e.app.Preferences()
	if got, want := prefs.IntWithFallback(prefLastRow, -1), 29999; got != want {
		t.Fatalf("saved row = %d, want %d", got, want)
	}
	if got, want := prefs.IntWithFallback(prefLastCol, -1), 4; got != want {
		t.Fatalf("saved col = %d, want %d", got, want)
	}
}
