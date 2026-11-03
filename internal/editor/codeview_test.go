package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// codeText builds a document with the given number of distinct lines.
func codeText(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line %d of the big file\n", i)
	}
	return b.String()
}

// openCode opens text in large-file mode, where codeView is the editing surface.
func openCode(t *testing.T, text string) (*Editor, string) {
	t.Helper()
	e := newTestEditor(t)
	path := filepath.Join(t.TempDir(), "big.txt")
	e.setContentAs(text, path, encUTF8, openLarge)
	if e.doc == nil || e.code == nil {
		t.Fatal("large-file mode did not set up the code view")
	}
	return e, path
}

// docText is the whole document as it stands.
func docText(e *Editor) string { return e.doc.TextRange(0, e.doc.Len()) }

// caretAt reports the caret as "row:col" so failures are readable.
func caretAt(e *Editor) string {
	return fmt.Sprintf("%d:%d", e.code.caret.row, e.code.caret.col)
}

// key presses a key at the code view.
func key(e *Editor, name fyne.KeyName) {
	e.code.TypedKey(&fyne.KeyEvent{Name: name})
}

// text types a string into the code view.
func text(e *Editor, s string) {
	for _, r := range s {
		e.code.TypedRune(r)
	}
}

// The widgets drawn must depend on the size of the screen, never on the size of
// the document: this is the whole reason large files get their own renderer.
func TestCodeViewDrawsOnlyVisibleLines(t *testing.T) {
	small, _ := openCode(t, codeText(2000))
	big, _ := openCode(t, codeText(200000))

	vis := small.code.visibleLines()
	if vis < 5 {
		t.Fatalf("only %d visible lines in a 900x650 window", vis)
	}
	if got := len(small.code.texts); got > vis+1 {
		t.Fatalf("%d text objects for %d visible lines", got, vis)
	}
	if len(big.code.texts) != len(small.code.texts) {
		t.Fatalf("a 100x bigger document uses %d text objects instead of %d",
			len(big.code.texts), len(small.code.texts))
	}
	if len(big.code.texts) > 200 {
		t.Fatalf("%d text objects for one screen", len(big.code.texts))
	}

	// What is drawn is the screenful at the top, and nothing else.
	first := ""
	for _, t := range big.code.texts {
		if t.Visible() {
			first = t.Text
			break
		}
	}
	if first != "line 0 of the big file" {
		t.Fatalf("first drawn line = %q", first)
	}

	// Scrolling far down keeps the object count and moves the content.
	big.code.scrollLines(150000)
	if len(big.code.texts) > 200 {
		t.Fatalf("%d text objects after scrolling", len(big.code.texts))
	}
	found := false
	for _, t := range big.code.texts {
		if t.Visible() && strings.Contains(t.Text, "150000") {
			found = true
		}
	}
	if !found {
		t.Fatal("the scrolled-to line was not drawn")
	}
}

// Scrolling moves the view, clamps at both ends and leaves the caret alone.
func TestCodeViewScrolling(t *testing.T) {
	e, _ := openCode(t, codeText(5000))
	v := e.code

	v.scrollLines(100)
	if v.topRow != 100 {
		t.Fatalf("top = %d, want 100", v.topRow)
	}
	if got := v.texts[0].Text; got != "line 100 of the big file" {
		t.Fatalf("top line = %q", got)
	}
	if got := caretAt(e); got != "0:0" {
		t.Fatalf("scrolling moved the caret to %s", got)
	}

	v.scrollLines(-1000)
	if v.topRow != 0 {
		t.Fatalf("top = %d after scrolling past the start", v.topRow)
	}
	v.scrollLines(100000)
	want := e.doc.LineCount() - v.visibleLines()
	if v.topRow != want {
		t.Fatalf("top = %d, want %d at the end of the document", v.topRow, want)
	}

	// The wheel scrolls the same way: negative DY is "down" in Fyne.
	v.scrollLines(-100000)
	lineH, _ := v.metrics()
	v.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, -lineH*3)})
	if v.topRow != 3 {
		t.Fatalf("one wheel tick moved the view to line %d, want 3", v.topRow)
	}
	v.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.NewDelta(0, lineH)})
	if v.topRow != 2 {
		t.Fatalf("wheel up moved the view to line %d, want 2", v.topRow)
	}
}

// Typing, Enter, Backspace, Delete and Tab all edit the document directly.
func TestCodeViewTyping(t *testing.T) {
	e, path := openCode(t, "one\ntwo\n")
	v := e.code

	v.setCaret(pos{})
	text(e, "hello")
	if got := docText(e); got != "helloone\ntwo\n" {
		t.Fatalf("after typing: %q", got)
	}
	if caretAt(e) != "0:5" {
		t.Fatalf("caret at %s, want 0:5", caretAt(e))
	}

	key(e, fyne.KeyEnter)
	text(e, "world")
	if got := docText(e); got != "hello\nworldone\ntwo\n" {
		t.Fatalf("after Enter: %q", got)
	}

	// Backspace at the start of a line joins it with the one above.
	v.setCaret(pos{row: 1, col: 0})
	key(e, fyne.KeyBackspace)
	if got := docText(e); got != "helloworldone\ntwo\n" {
		t.Fatalf("after backspace: %q", got)
	}

	// Delete at the end of a line joins the next one onto it.
	v.setCaret(pos{row: 0, col: v.lineLen(0)})
	key(e, fyne.KeyDelete)
	if got := docText(e); got != "helloworldonetwo\n" {
		t.Fatalf("after delete: %q", got)
	}

	// Tab inserts one indentation level, Shift+Tab takes it back.
	v.setCaret(pos{row: 0, col: 0})
	key(e, fyne.KeyTab)
	if got := e.doc.LineText(0); got != "    helloworldonetwo" {
		t.Fatalf("after tab: %q", got)
	}
	if caretAt(e) != "0:4" {
		t.Fatalf("caret at %s, want 0:4", caretAt(e))
	}
	v.shift = true
	key(e, fyne.KeyTab)
	v.shift = false
	if got := e.doc.LineText(0); got != "helloworldonetwo" {
		t.Fatalf("after shift+tab: %q", got)
	}

	// And it all lands on disk.
	e.writeTo(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), docText(e); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

// Undo and redo work over the whole document, and consecutive typing is undone
// as a run rather than a character at a time.
func TestCodeViewUndoRedo(t *testing.T) {
	e, _ := openCode(t, "abc\ndef\n")
	v := e.code
	e.code.setCaret(pos{row: 0, col: 3})

	text(e, "XYZ")
	if got := docText(e); got != "abcXYZ\ndef\n" {
		t.Fatalf("after typing: %q", got)
	}
	v.Undo()
	if got := docText(e); got != "abc\ndef\n" {
		t.Fatalf("one undo should remove the whole run: %q", got)
	}
	if caretAt(e) != "0:3" {
		t.Fatalf("caret after undo at %s, want 0:3", caretAt(e))
	}
	v.Redo()
	if got := docText(e); got != "abcXYZ\ndef\n" {
		t.Fatalf("redo: %q", got)
	}

	// A structural command is one undoable step.
	e.DuplicateLine()
	if got := strings.Count(docText(e), "\n"); got != 3 {
		t.Fatalf("duplicate gave %d newlines in %q", got, docText(e))
	}
	if got := e.doc.LineText(1); got != "abcXYZ" {
		t.Fatalf("duplicated line = %q", got)
	}
	v.Undo()
	if got := docText(e); got != "abcXYZ\ndef\n" {
		t.Fatalf("undo of duplicate: %q", got)
	}

	// Replace all is a single step too.
	e.findEntry.SetText("def")
	e.replEntry.SetText("ghi")
	e.ReplaceAll()
	if got := docText(e); got != "abcXYZ\nghi\n" {
		t.Fatalf("replace all: %q", got)
	}
	v.Undo()
	if got := docText(e); got != "abcXYZ\ndef\n" {
		t.Fatalf("undo of replace all: %q", got)
	}

	// Nothing left to undo is reported, not crashed on.
	for i := 0; i < 10; i++ {
		v.Undo()
	}
	if got := docText(e); got != "abc\ndef\n" {
		t.Fatalf("undoing past the start changed the document: %q", got)
	}
}

// Selection with shift and the mouse, and the clipboard operations that use it.
func TestCodeViewSelectionAndClipboard(t *testing.T) {
	e, _ := openCode(t, "alpha\nbravo\ncharlie\n")
	v := e.code
	v.setCaret(pos{})

	v.shift = true
	for i := 0; i < 5; i++ {
		key(e, fyne.KeyRight)
	}
	v.shift = false
	if got := v.selectedText(); got != "alpha" {
		t.Fatalf("selection = %q, want alpha", got)
	}
	if caretAt(e) != "0:5" {
		t.Fatalf("caret at %s", caretAt(e))
	}

	// Typing replaces the selection.
	text(e, "ALPHA")
	if got := docText(e); got != "ALPHA\nbravo\ncharlie\n" {
		t.Fatalf("after replacing the selection: %q", got)
	}

	// Select all and copy.
	e.SelectAll()
	if got := v.selectedText(); got != "ALPHA\nbravo\ncharlie\n" {
		t.Fatalf("select all got %q", got)
	}
	v.TypedShortcut(&fyne.ShortcutCopy{})
	if got := e.app.Clipboard().Content(); got != "ALPHA\nbravo\ncharlie\n" {
		t.Fatalf("clipboard = %q", got)
	}

	// Cut removes it, paste puts it back.
	v.TypedShortcut(&fyne.ShortcutCut{})
	if got := docText(e); got != "" {
		t.Fatalf("after cut: %q", got)
	}
	v.TypedShortcut(&fyne.ShortcutPaste{})
	if got := docText(e); got != "ALPHA\nbravo\ncharlie\n" {
		t.Fatalf("after paste: %q", got)
	}

	// A paste with \r\n line endings is normalised.
	e.app.Clipboard().SetContent("x\r\ny")
	v.setCaret(pos{row: 2, col: 7})
	v.TypedShortcut(&fyne.ShortcutPaste{})
	if got := e.doc.LineText(2); got != "charliex" {
		t.Fatalf("line 2 = %q", got)
	}
	if got := e.doc.LineText(3); got != "y" {
		t.Fatalf("line 3 = %q", got)
	}
}

// Clicking places the caret, and dragging selects.
func TestCodeViewMouse(t *testing.T) {
	e, _ := openCode(t, "first line\nsecond line\nthird\n")
	v := e.code
	lineH, charW := v.metrics()
	inset := v.inset()

	at := func(row, col int) fyne.Position {
		// A point a fifth of the way into the character: nearest boundary is its start.
		return fyne.NewPos(inset+float32(col)*charW+charW/5, inset+float32(row)*lineH+lineH/2)
	}

	v.Tapped(&fyne.PointEvent{Position: at(1, 3)})
	if got := caretAt(e); got != "1:3" {
		t.Fatalf("click put the caret at %s, want 1:3", got)
	}

	// Dragging from the click extends the selection.
	v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(0, 0)}})
	v.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at(0, 6)}})
	if got := v.selectedText(); got != "first " {
		t.Fatalf("drag selected %q, want %q", got, "first ")
	}
	v.DragEnd()
	v.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(0, 6)}})

	// Double-click selects the word under the pointer.
	v.DoubleTapped(&fyne.PointEvent{Position: at(1, 4)})
	if got := v.selectedText(); got != "second" {
		t.Fatalf("double click selected %q, want second", got)
	}

	// A click past the end of a line clamps to its end.
	v.Tapped(&fyne.PointEvent{Position: at(2, 40)})
	if got := caretAt(e); got != "2:5" {
		t.Fatalf("caret at %s, want 2:5", got)
	}
}

// Go to line and find/replace work over the whole document.
func TestCodeViewGotoAndFind(t *testing.T) {
	e, _ := openCode(t, codeText(100000))
	v := e.code

	e.gotoLineCol(50000, 3)
	if got := caretAt(e); got != "49999:3" {
		t.Fatalf("caret at %s, want 49999:3", got)
	}
	if v.topRow > 49999 || v.topRow+v.visibleLines() <= 49999 {
		t.Fatalf("line 49999 is not on screen (top %d, %d visible)", v.topRow, v.visibleLines())
	}
	if e.info.Text == "" || !strings.Contains(e.info.Text, "50000:4") {
		t.Fatalf("readout = %q, want it to show 50000:4", e.info.Text)
	}

	// Find moves to the match wherever it is.
	e.findEntry.SetText("line 77777 of")
	e.FindNext()
	if got := caretAt(e); got != "77777:0" {
		t.Fatalf("find put the caret at %s, want 77777:0", got)
	}
	if v.topRow > 77777 || v.topRow+v.visibleLines() <= 77777 {
		t.Fatal("the match is not on screen")
	}

	// Replace current edits in place.
	e.replEntry.SetText("row 77777 of")
	e.ReplaceCurrent()
	if got := e.doc.LineText(77777); got != "row 77777 of the big file" {
		t.Fatalf("line 77777 = %q", got)
	}

	// Replace all rewrites the document and is one undo step.
	e.findEntry.SetText("big file")
	e.replEntry.SetText("huge file")
	e.ReplaceAll()
	if strings.Contains(docText(e), "big file") {
		t.Fatal("replace all left occurrences behind")
	}
	if got := e.doc.LineText(99999); got != "line 99999 of the huge file" {
		t.Fatalf("last line = %q", got)
	}
	e.Undo()
	if !strings.Contains(docText(e), "big file") {
		t.Fatal("undo of replace all did not restore the document")
	}
}

// Saving a large document reproduces it byte for byte in every encoding: the
// document is streamed page by page rather than rebuilt as one string.
func TestCodeViewSaveRoundTrips(t *testing.T) {
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
	want := codeText(20000)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, path := openCode(t, want)
			e.eol, e.enc = tc.eol, tc.enc
			e.writeTo(path)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, enc, err := readDocument(strings.NewReader(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			got, _ = normalizeEOL(got)
			if enc != tc.enc {
				t.Fatalf("encoding on disk = %q, want %q", enc, tc.enc)
			}
			if got != want {
				t.Fatalf("round trip changed the document (%d bytes)", len(got))
			}
		})
	}
}

// The scrollbar appears when it is needed, and dragging it scrolls.
func TestCodeViewScrollbar(t *testing.T) {
	e, _ := openCode(t, codeText(50000))
	v := e.code

	if v.thumb.Hidden {
		t.Fatal("the thumb should be visible in a long document")
	}
	before := v.topRow
	v.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(v.Size().Width-2, 100)}})
	if !v.dragBar {
		t.Fatal("a press in the gutter should start a scrollbar drag")
	}
	if v.topRow <= before {
		t.Fatalf("dragging the thumb from %d did not scroll", before)
	}
	v.MouseUp(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(v.Size().Width-2, 100)}})

	// A document that fits needs no scrollbar.
	small, _ := openCode(t, "one\ntwo\n")
	if !small.code.thumb.Hidden {
		t.Fatal("the thumb should be hidden when the document fits")
	}
}

// A single enormous line must not cost anything: only the cells on screen are
// laid out, and the caret still lands in the right place.
func TestCodeViewLongLine(t *testing.T) {
	line := strings.Repeat("abcdefghij", 20000) // 200k characters on one line
	e, _ := openCode(t, line+"\nsecond\n")
	v := e.code

	if got, _, _ := v.lineDisplay(0, 0, 20); got != "abcdefghijabcdefghij" {
		t.Fatalf("display slice = %q", got)
	}
	got, start, _ := v.lineDisplay(0, 199990, 10)
	if start != 199990 || got != "abcdefghij" {
		t.Fatalf("far slice = %q from cell %d", got, start)
	}
	// Cell/column conversions are exact for a long line.
	if got := v.cellOf(pos{row: 0, col: 199990}); got != 199990 {
		t.Fatalf("cellOf = %d", got)
	}
	if got := v.colAtCell(0, 199990); got != 199990 {
		t.Fatalf("colAtCell = %d", got)
	}
	// With wrapping off, moving the caret to the end scrolls horizontally.
	v.setWrap(false)
	if len(v.rows) != 0 {
		t.Fatalf("the row index should be dropped when wrapping is off (%d entries)", len(v.rows))
	}
	v.setCaret(pos{row: 0, col: 200000})
	if v.xoffCell <= 0 {
		t.Fatalf("xoffCell = %d, want the view scrolled right", v.xoffCell)
	}
	if got := v.xoffCell + v.visibleCells(); got <= 200000-2 {
		t.Fatalf("the caret cell is off screen: xoff %d + %d cells", v.xoffCell, v.visibleCells())
	}
}

// Tabs are expanded for display, and the cell arithmetic accounts for them.
func TestCodeViewTabs(t *testing.T) {
	e, _ := openCode(t, "\tx\nab\tc\n")
	v := e.code
	e.indentUseTabs = true
	e.indentWidth = 4

	if got := v.cellOf(pos{row: 0, col: 1}); got != 4 {
		t.Fatalf("cell after one tab = %d, want 4", got)
	}
	// A tab at cell 2 advances to the next tab stop, cell 4.
	if got := v.cellOf(pos{row: 1, col: 3}); got != 4 {
		t.Fatalf("cell after tab at column 2 = %d, want 4", got)
	}
	disp, start, _ := v.lineDisplay(0, 0, 10)
	if disp != "    x" || start != 0 {
		t.Fatalf("tab display = %q from %d", disp, start)
	}
	// Clicking inside a tab lands on the rune after it.
	if got := v.colAtCell(0, 3); got != 1 {
		t.Fatalf("colAtCell(3) = %d, want 1", got)
	}
	// Outdent removes the tab.
	v.setCaret(pos{row: 0, col: 1})
	v.outdent()
	if got := e.doc.LineText(0); got != "x" {
		t.Fatalf("after outdent: %q", got)
	}
}

// The caret position saved for the session is a document position.
func TestCodeViewSessionCaret(t *testing.T) {
	e, _ := openCode(t, codeText(5000))
	e.gotoLineCol(3000, 4)
	e.saveSession(e.currentFile)

	prefs := e.app.Preferences()
	if got, want := prefs.IntWithFallback(prefLastRow, -1), 2999; got != want {
		t.Fatalf("saved row = %d, want %d", got, want)
	}
	if got, want := prefs.IntWithFallback(prefLastCol, -1), 4; got != want {
		t.Fatalf("saved col = %d, want %d", got, want)
	}
}

// A small file opened after a large one comes back with counts and snapshots on
// and the caret at the top.
func TestSmallFileAfterLarge(t *testing.T) {
	e, _ := openCode(t, codeText(5000))
	if !e.largeFile {
		t.Fatal("the large document should be in large-file mode")
	}

	e.setContentAs("hello\nworld\n", "/tmp/small.txt", encUTF8, openNormal)
	if e.largeFile {
		t.Fatal("opening a small file left large-file mode on")
	}
	if got := bufferText(e); got != "hello\nworld\n" {
		t.Fatalf("buffer = %q", got)
	}
	if row, col := caret(e); row != 0 || col != 0 {
		t.Fatalf("caret at %d:%d, want the top", row, col)
	}
	if e.statWords != 2 || e.statChars != 12 {
		t.Fatalf("counts = %dw/%dc, want 2w/12c", e.statWords, e.statChars)
	}
	if e.focusTarget() != fyne.Focusable(e.code) {
		t.Fatal("focus should be on the code view")
	}
}

// Edits made while a save is running must not change what is written, and must
// still be in the document afterwards.
func TestCodeViewSaveUsesSnapshot(t *testing.T) {
	want := codeText(5000)
	e, path := openCode(t, want)

	req := e.pendingSave(path)
	if req.doc == nil {
		t.Fatal("a large-file save request should carry a document")
	}
	e.code.setCaret(pos{row: 5, col: 0})
	e.code.TypedRune('Z')

	if _, err := req.write(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("the save captured edits made after the request (%d bytes, want %d)",
			len(data), len(want))
	}
	if got := e.doc.LineText(5); got != "Zline 5 of the big file" {
		t.Fatalf("the live document lost the edit: %q", got)
	}
}

// The view must keep working when the window is resized: more lines are drawn,
// and the caret stays on screen.
func TestCodeViewResize(t *testing.T) {
	e, _ := openCode(t, codeText(5000))
	v := e.code
	small := v.visibleLines()

	e.win.Resize(fyne.NewSize(900, 1200))
	if got := v.visibleLines(); got <= small {
		t.Fatalf("visible lines = %d after growing the window, want more than %d", got, small)
	}
	e.gotoLineCol(4000, 0)
	if v.topRow > 3999 || v.topRow+v.visibleLines() <= 3999 {
		t.Fatalf("caret line 3999 is off screen after resize (top %d)", v.topRow)
	}
	// Narrowing the window reduces the visible cells and keeps the caret in view.
	v.setCaret(pos{row: 3999, col: 0})
	e.win.Resize(fyne.NewSize(400, 1200))
	v.xoffCell = 10
	v.followCaret()
	v.render()
}

// The caret walks across line boundaries the way editors do, and up/down keep
// the column.
func TestCodeViewCaretWrapsLines(t *testing.T) {
	e, _ := openCode(t, "ab\ncdef\n")
	v := e.code

	v.setCaret(pos{row: 1, col: 0})
	key(e, fyne.KeyLeft)
	if got := caretAt(e); got != "0:2" {
		t.Fatalf("left at the start of a line put the caret at %s, want 0:2", got)
	}
	key(e, fyne.KeyRight)
	if got := caretAt(e); got != "1:0" {
		t.Fatalf("right at the end of a line put the caret at %s, want 1:0", got)
	}

	v.setCaret(pos{row: 0, col: 2})
	key(e, fyne.KeyDown)
	if got := caretAt(e); got != "1:2" {
		t.Fatalf("down put the caret at %s, want 1:2", got)
	}
	key(e, fyne.KeyUp)
	if got := caretAt(e); got != "0:2" {
		t.Fatalf("up put the caret at %s, want 0:2", got)
	}

	// Ctrl+Home and Ctrl+End reach the ends of the document.
	v.ctrl = true
	key(e, fyne.KeyEnd)
	v.ctrl = false
	// "ab\ncdef\n" ends just after the final newline, on an empty last line.
	if got := caretAt(e); got != "2:0" {
		t.Fatalf("ctrl+end put the caret at %s, want 2:0", got)
	}
	v.ctrl = true
	key(e, fyne.KeyHome)
	v.ctrl = false
	if got := caretAt(e); got != "0:0" {
		t.Fatalf("ctrl+home put the caret at %s, want 0:0", got)
	}

	// Clicking the gutter scrolls, it does not move the caret.
	before := caretAt(e)
	v.Tapped(&fyne.PointEvent{Position: fyne.NewPos(v.Size().Width-2, 200)})
	if got := caretAt(e); got != before {
		t.Fatalf("a click in the scrollbar moved the caret to %s", got)
	}
}

// Word-wise movement with Ctrl.
func TestCodeViewWordMovement(t *testing.T) {
	e, _ := openCode(t, "alpha beta gamma\n")
	v := e.code
	v.setCaret(pos{})

	v.ctrl = true
	key(e, fyne.KeyRight)
	v.ctrl = false
	if got := caretAt(e); got != "0:6" {
		t.Fatalf("ctrl+right put the caret at %s, want 0:6", got)
	}
	v.ctrl = true
	key(e, fyne.KeyRight)
	v.ctrl = false
	if got := caretAt(e); got != "0:11" {
		t.Fatalf("second ctrl+right put the caret at %s, want 0:11", got)
	}
	v.ctrl = true
	key(e, fyne.KeyLeft)
	v.ctrl = false
	if got := caretAt(e); got != "0:6" {
		t.Fatalf("ctrl+left put the caret at %s, want 0:6", got)
	}
}

// Wrapping splits a long line into visual rows of the width that fits, and the
// rows on screen are consecutive pieces of the document.
func TestCodeViewWrapRendersVisualRows(t *testing.T) {
	long := strings.Repeat("abcdefgh", 120) // 960 cells
	e, _ := openCode(t, "short\n"+long+"\n"+codeText(200))
	v := e.code
	v.setWrap(true)
	cells := v.wrapCells()

	chunks := v.chunksOf(1)
	if want := (960 + cells - 1) / cells; chunks != want {
		t.Fatalf("the long line takes %d visual rows, want %d", chunks, want)
	}
	if got, want := v.totalRows(), v.docLines()-1+chunks; got != want {
		t.Fatalf("total rows = %d, want %d", got, want)
	}

	// Every visual row of the long line is on screen when we scroll there.
	v.topRow = v.rowStart(1)
	v.render()
	for i := 0; i < min(chunks, v.visibleLines()); i++ {
		row, chunk := v.rowOf(v.topRow + i)
		if row != 1 || chunk != i {
			t.Fatalf("visual row %d is line %d chunk %d, want 1/%d", i, row, chunk, i)
		}
		want := long[i*cells : min((i+1)*cells, len(long))]
		if got := v.texts[i].Text; got != want {
			t.Fatalf("visual row %d shows %q, want %q", i, got, want)
		}
	}

	// Turning wrapping off drops the index and keeps the view on the same line.
	v.setWrap(false)
	if len(v.rows) != 0 {
		t.Fatal("the row index should be gone when wrapping is off")
	}
	if v.topRow != 1 {
		t.Fatalf("topRow = %d after unwrapping, want the long line (1)", v.topRow)
	}
	// Unwrapped, only the cells that fit are drawn, from the left.
	if got := v.texts[0].Text; got != long[:v.visibleCells()+1] {
		t.Fatalf("the unwrapped line shows %d characters, want %d",
			len(got), v.visibleCells()+1)
	}
}

// With wrapping on, the caret lives on the visual row its column falls in.
func TestCodeViewWrapCaretAndMouse(t *testing.T) {
	long := strings.Repeat("x", 300)
	e, _ := openCode(t, long+"\n")
	v := e.code
	v.setWrap(true)
	cells := v.wrapCells()
	lineH, charW := v.metrics()
	inset := v.inset()

	// The caret at the end of the line is on the last visual row.
	v.setCaret(pos{row: 0, col: 300})
	lastChunk := (300) / cells
	if got := v.caretChunk(); got != min(lastChunk, v.chunksOf(0)-1) {
		t.Fatalf("caret chunk = %d, want %d", got, lastChunk)
	}
	if v.topRow+v.visibleLines() <= v.caretRow() {
		t.Fatal("the caret's visual row is off screen")
	}

	// Clicking the second visual row lands on a column in that row's range.
	y := inset + 1.5*lineH
	got := v.posAtPoint(fyne.NewPos(inset+charW*2+charW/5, y))
	if got.row != 0 {
		t.Fatalf("click landed on line %d, want 0", got.row)
	}
	if got.col < cells || got.col > 2*cells {
		t.Fatalf("click landed on column %d, want it inside the second row [%d,%d]",
			got.col, cells, 2*cells)
	}
}

// The row index is patched on every edit; it must always match a full rebuild.
func TestCodeViewWrapIndexTracksEdits(t *testing.T) {
	e, _ := openCode(t, codeText(20)+strings.Repeat("y", 400)+"\nend\n")
	v := e.code
	v.setWrap(true)

	pieces := []string{"a", "\n", "\t", "héllo", "😀", "", "zzz"}
	states := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		v.rebuildRows()
		text := docText(e)
		off := 0
		if len(text) > 0 {
			off = int(uint(i*37+11) % uint(len(text)))
		}
		// Only cut on rune boundaries.
		for off > 0 && off < len(text) && !utf8.RuneStart(text[off]) {
			off--
		}
		end := off
		for end > 0 && !utf8.RuneStart(text[end-1]) {
			end--
		}
		delLen := 0
		if end < len(text) {
			_, delLen = utf8.DecodeRuneInString(text[end:])
		}
		ins := pieces[i%len(pieces)]

		v.applyReplace(off, text[end:end+delLen], ins)

		patched := append([]int(nil), v.rows...)
		v.rebuildRows()
		if len(patched) != len(v.rows) {
			t.Fatalf("step %d: patched index has %d entries, rebuilt has %d",
				i, len(patched), len(v.rows))
		}
		for j := range patched {
			if patched[j] != v.rows[j] {
				t.Fatalf("step %d: patched rows[%d] = %d, rebuilt = %d (doc %q)",
					i, j, patched[j], v.rows[j], docText(e))
			}
		}
		states = append(states, fmt.Sprint(v.totalRows()))
	}
	if len(states) == 0 {
		t.Fatal("no edits were applied")
	}
}

// Wrapping survives a width change: the index is rebuilt for the new width.
func TestCodeViewWrapOnResize(t *testing.T) {
	e, _ := openCode(t, strings.Repeat("q", 500)+"\n")
	v := e.code
	v.setWrap(true)

	before := v.totalRows()
	e.win.Resize(fyne.NewSize(450, 900))
	v.render()
	after := v.totalRows()
	if after <= before {
		t.Fatalf("a narrower window gave %d rows, want more than %d", after, before)
	}
	if v.rowsCells != v.wrapCells() {
		t.Fatalf("the index was built for %d cells, the view is %d wide",
			v.rowsCells, v.wrapCells())
	}
}

// Wrapping breaks between words, not through them, when it can.
func TestCodeViewWrapBreaksAtWords(t *testing.T) {
	line := strings.Repeat("word ", 60) // 300 cells of short words
	e, _ := openCode(t, line+"\n")
	v := e.code
	v.setWrap(true)

	starts := v.wrapStarts(0)
	if len(starts) < 2 {
		t.Fatalf("the line wrapped into %d rows, want several", len(starts))
	}
	maxW := v.wrapCells()
	for i, start := range starts {
		if i == 0 {
			continue
		}
		if start <= 0 || start > len(line) || line[start-1] != ' ' {
			t.Fatalf("row %d starts at %d (%q), want it right after a space",
				i, start, line[max(0, start-6):min(len(line), start+6)])
		}
	}
	for i := 0; i+1 < len(starts); i++ {
		if w := starts[i+1] - starts[i]; w > maxW {
			t.Fatalf("row %d is %d cells wide, want at most %d", i, w, maxW)
		}
	}
	// The caret after the first space is on the first row, not the second.
	v.setCaret(pos{row: 0, col: 5})
	if got := v.caretChunk(); got != 0 {
		t.Fatalf("caret chunk = %d, want 0", got)
	}
	// And the caret at the start of the second row is on it.
	v.setCaret(pos{row: 0, col: starts[1]})
	if got := v.caretChunk(); got != 1 {
		t.Fatalf("caret chunk = %d, want 1", got)
	}
}
