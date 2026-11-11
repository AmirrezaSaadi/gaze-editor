package editor

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

// enter presses Enter at the caret.
func enter(e *Editor) { key(e, fyne.KeyEnter) }

// The line a break starts on keeps its indentation in the line below.
func TestEnterKeepsIndentation(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "    x\n")
	e.code.setCaret(pos{row: 0, col: 5})
	enter(e)
	if got := e.text(); got != "    x\n    \n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "1:4" {
		t.Fatalf("caret at %s, want 1:4", caretAt(e))
	}
}

// Tab-indented files keep indenting with tabs.
func TestEnterKeepsTabs(t *testing.T) {
	e := newTestEditor(t)
	e.indentUseTabs = true
	setBuffer(e, "\tx\n")
	e.code.setCaret(pos{row: 0, col: 2})
	enter(e)
	if got := e.text(); got != "\tx\n\t\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// After an opener the new line goes one level deeper.
func TestEnterOpensABlock(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"if x {", "if x {\n    \n"},
		{"f(", "f(\n    \n"},
		{"a := [", "a := [\n    \n"},
		{"def f():", "def f():\n    \n"},
		{"case 1:", "case 1:\n    \n"},
	} {
		e := newTestEditor(t)
		setBuffer(e, tc.line+"\n")
		e.code.setCaret(pos{row: 0, col: utf8Count(tc.line)})
		enter(e)
		if got := e.text(); got != tc.want {
			t.Errorf("after %q: buffer = %q, want %q", tc.line, got, tc.want)
		}
		if caretAt(e) != "1:4" {
			t.Errorf("after %q: caret at %s, want 1:4", tc.line, caretAt(e))
		}
	}
}

// A closer sitting at the caret gets its own line, and the caret lands on the line
// in between.
func TestEnterSplitsBraces(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "if x {}\n")
	e.code.setCaret(pos{row: 0, col: 6}) // between the braces
	enter(e)
	if got := e.text(); got != "if x {\n    \n}\n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "1:4" {
		t.Fatalf("caret at %s, want 1:4", caretAt(e))
	}
	// Typing there goes inside the block, and undo is a single step.
	text(e, "work")
	if got := e.text(); got != "if x {\n    work\n}\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// A caret inside the indentation breaks below it rather than repeating the prefix.
func TestEnterInsideTheIndentation(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "        x\n")
	e.code.setCaret(pos{row: 0, col: 2})
	enter(e)
	if got := e.text(); got != "  \n        x\n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "1:2" {
		t.Fatalf("caret at %s, want 1:2", caretAt(e))
	}
}

// Enter over a selection replaces it, and the break keeps the indentation.
func TestEnterReplacesTheSelection(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "    one two\n")
	v := e.code
	v.anchor = pos{row: 0, col: 7}
	v.caret = pos{row: 0, col: 10}
	enter(e)
	// " tw" is dropped, and the break lands where it was: before the "o".
	if got := e.text(); got != "    one\n    o\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// With smart typing off a break is just a break.
func TestEnterPlainWithoutSmartTyping(t *testing.T) {
	e := newTestEditor(t)
	e.smartTyping = false
	setBuffer(e, "    if x {\n")
	e.code.setCaret(pos{row: 0, col: 10})
	enter(e)
	if got := e.text(); got != "    if x {\n\n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "1:0" {
		t.Fatalf("caret at %s, want 1:0", caretAt(e))
	}
}

// Backspace in the margin removes a whole level, not a column.
func TestBackspaceInIndentation(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "        x\n")
	e.indentWidth = 4
	e.code.setCaret(pos{row: 0, col: 8})
	key(e, fyne.KeyBackspace)
	if got := e.text(); got != "    x\n" {
		t.Fatalf("buffer = %q after one backspace", got)
	}
	if caretAt(e) != "0:4" {
		t.Fatalf("caret at %s, want 0:4", caretAt(e))
	}
	key(e, fyne.KeyBackspace)
	if got := e.text(); got != "x\n" {
		t.Fatalf("buffer = %q after two", got)
	}
	// At the start of the line there is nothing left to remove.
	key(e, fyne.KeyBackspace)
	if got := e.text(); got != "x\n" {
		t.Fatalf("buffer = %q after three", got)
	}
	// Real text is deleted a character at a time, as before.
	e.code.setCaret(pos{row: 0, col: 1})
	key(e, fyne.KeyBackspace)
	if got := e.text(); got != "\n" {
		t.Fatalf("buffer = %q after deleting the letter", got)
	}
}

// A tab counts as one level, whatever the indent width.
func TestBackspaceOverATab(t *testing.T) {
	e := newTestEditor(t)
	e.indentUseTabs = true
	setBuffer(e, "\t\tx\n")
	e.code.setCaret(pos{row: 0, col: 2})
	key(e, fyne.KeyBackspace)
	if got := e.text(); got != "\tx\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// A caret in the middle of the indentation backs up to the previous stop.
func TestBackspaceInTheMiddleOfIndentation(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "        x\n")
	e.indentWidth = 4
	e.code.setCaret(pos{row: 0, col: 6})
	key(e, fyne.KeyBackspace)
	// The two spaces before the caret go; the two after it stay.
	if got := e.text(); got != "      x\n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:4" {
		t.Fatalf("caret at %s, want 0:4", caretAt(e))
	}
}

// Tab over a selection indents every line of it, and undo puts them all back.
func TestTabIndentsTheSelection(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "a\n  b\nc\n")
	e.indentWidth = 4
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 1, col: 3}
	key(e, fyne.KeyTab)
	want := "    a\n      b\nc\n"
	if got := e.text(); got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	// The selection still covers what it covered.
	if v.anchor != (pos{row: 0, col: 4}) || v.caret != (pos{row: 1, col: 7}) {
		t.Fatalf("selection %v..%v", v.anchor, v.caret)
	}
	v.Undo()
	if got := e.text(); got != "a\n  b\nc\n" {
		t.Fatalf("after undo: %q", got)
	}
	if v.anchor != (pos{row: 0, col: 0}) || v.caret != (pos{row: 1, col: 3}) {
		t.Fatalf("undo restored the selection as %v..%v", v.anchor, v.caret)
	}
}

// Shift+Tab outdents every selected line.
func TestShiftTabOutdentsTheSelection(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "        a\n    b\n")
	e.indentWidth = 4
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 1, col: 5}
	v.shift = true
	key(e, fyne.KeyTab)
	if got := e.text(); got != "    a\nb\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// A selection that stops at the start of a line does not reach that line.
func TestIndentStopsAtTheLineStart(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "a\nb\nc\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 1, col: 0}
	key(e, fyne.KeyTab)
	if got := e.text(); got != "    a\nb\nc\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// The line command touches whole lines, never the middle of one.
func TestIndentFromMidLineKeepsTheLine(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "one\ntwo")
	e.indentWidth = 4
	e.code.setCaret(pos{row: 1, col: 2})
	e.IndentLine()
	if got := e.text(); got != "one\n    two" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "1:6" {
		t.Fatalf("caret at %s, want 1:6", caretAt(e))
	}
	e.OutdentLine()
	if got := e.text(); got != "one\ntwo" {
		t.Fatalf("buffer = %q after outdent", got)
	}
	if caretAt(e) != "1:2" {
		t.Fatalf("caret at %s after outdent, want 1:2", caretAt(e))
	}
}

// Outdenting a line with less than one level takes what is there.
func TestOutdentTakesWhatIsThere(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, " a\n")
	e.indentWidth = 4
	e.code.setCaret(pos{row: 0, col: 1})
	e.OutdentLine()
	if got := e.text(); got != "a\n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:0" {
		t.Fatalf("caret at %s, want 0:0", caretAt(e))
	}
	// A line at column 0 has nothing to outdent.
	e.OutdentLine()
	if got := e.text(); got != "a\n" {
		t.Fatalf("buffer = %q after outdenting to nothing", got)
	}
}

// Indenting from the middle of the indentation keeps the rest of the line.
func TestIndentFromInsideTheIndentation(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "  a\n")
	e.indentWidth = 4
	e.code.setCaret(pos{row: 0, col: 1})
	e.IndentLine()
	if got := e.text(); got != "      a\n" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:5" {
		t.Fatalf("caret at %s, want 0:5", caretAt(e))
	}
}

// Typing must stay cheap: the indentation work only looks at the line the caret is
// on, so a break in a huge buffer costs the same as anywhere else.
func TestEnterInABigBuffer(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, strings.Repeat("    if x {\n", 2000))
	e.indentWidth = 4
	e.code.setCaret(pos{row: 1999, col: 10})
	enter(e)
	text(e, "y")
	// The block level (4) plus the new one (4) plus the character typed.
	if got := e.code.lineLen(2000); got != 9 {
		t.Fatalf("the new line is %d runes wide, want 9", got)
	}
}

// A tab is drawn as a tab stop rather than one cell, so tab-indented code lines up
// with the same code indented with spaces.
func TestTabDisplayWidth(t *testing.T) {
	if useTabs, width := detectIndent("\tx\n"); !useTabs || width != 4 {
		t.Fatalf("detectIndent for a tab = %v,%d, want tabs at 4 cells", useTabs, width)
	}
	e := newTestEditor(t)
	setBuffer(e, "\tx\n")
	e.indentUseTabs, e.indentWidth = true, 4
	v := e.code
	if got := v.cellOf(pos{row: 0, col: 1}); got != 4 {
		t.Fatalf("the text after a tab is at cell %d, want 4", got)
	}
	if got := v.cellOf(pos{row: 0, col: 2}); got != 5 {
		t.Fatalf("the second character is at cell %d, want 5", got)
	}
	// A click four cells in lands after the tab, not on the next character.
	if got := v.colAtCell(0, 4); got != 1 {
		t.Fatalf("a click at cell 4 gives column %d, want 1", got)
	}
}

// Sorting and deduplication work on the selected lines as one undo step.
func TestSortLines(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "pear\napple\nfig\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 2, col: 3}
	e.SortLines()
	if got := e.text(); got != "apple\nfig\npear\n" {
		t.Fatalf("buffer = %q", got)
	}
	v.Undo()
	if got := e.text(); got != "pear\napple\nfig\n" {
		t.Fatalf("after undo: %q", got)
	}
	v.Redo()
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 2, col: 4}
	e.sortLines(true)
	if got := e.text(); got != "pear\nfig\napple\n" {
		t.Fatalf("descending: %q", got)
	}
}

func TestRemoveDuplicateLines(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "a\nb\na\nc\nb\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 4, col: 1}
	e.RemoveDuplicateLines()
	if got := e.text(); got != "a\nb\nc\n" {
		t.Fatalf("buffer = %q", got)
	}
	v.Undo()
	if got := e.text(); got != "a\nb\na\nc\nb\n" {
		t.Fatalf("after undo: %q", got)
	}
	// Nothing to remove is reported, not a silent rewrite.
	e.RemoveDuplicateLines()
	e.status.SetText("")
	e.RemoveDuplicateLines()
	if !strings.Contains(e.status.Text, "no duplicate") {
		t.Fatalf("status = %q", e.status.Text)
	}
}

// Case changes work on the selection, or on the caret's line.
func TestChangeCase(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "hello World\nsecond line\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 0, col: 5}
	e.Uppercase()
	if got := e.text(); got != "HELLO World\nsecond line\n" {
		t.Fatalf("buffer = %q", got)
	}
	if v.anchor != (pos{row: 0, col: 0}) || v.caret != (pos{row: 0, col: 5}) {
		t.Fatalf("selection = %v..%v, want it unchanged", v.anchor, v.caret)
	}
	v.clearSelection()
	v.setCaret(pos{row: 1, col: 3})
	e.Uppercase()
	if got := e.text(); got != "HELLO World\nSECOND LINE\n" {
		t.Fatalf("buffer after upper casing the line = %q", got)
	}
	if caretAt(e) != "1:3" {
		t.Fatalf("caret at %s, want 1:3", caretAt(e))
	}
	e.Lowercase()
	if got := e.text(); got != "HELLO World\nsecond line\n" {
		t.Fatalf("buffer after lower casing = %q", got)
	}
}

// The line commands say so when they have nothing to do.
func TestLineOpsNothingToDo(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "one\n")
	e.SortLines()
	if !strings.Contains(e.status.Text, "nothing to sort") {
		t.Fatalf("status = %q", e.status.Text)
	}
	e.Uppercase() // "one" -> "ONE" is a change, so this must work
	if got := e.text(); got != "ONE\n" {
		t.Fatalf("buffer = %q", got)
	}
	e.status.SetText("")
	e.Uppercase() // already upper case: nothing to do
	if !strings.Contains(e.status.Text, "nothing to change") {
		t.Fatalf("status = %q", e.status.Text)
	}
}
