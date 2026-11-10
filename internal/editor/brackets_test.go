package editor

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// bracketSetup installs text, focuses the view and puts the caret where asked.
func bracketSetup(t *testing.T, text string, at pos) *Editor {
	t.Helper()
	e := newTestEditor(t)
	setBuffer(e, text)
	e.code.focused = true
	e.code.setCaret(at)
	return e
}

// The partner of the bracket under the caret, in both directions.
func TestBracketMatchBothWays(t *testing.T) {
	e := bracketSetup(t, "func f() {\n\tif x {\n\t}\n}\n", pos{row: 1, col: 6})
	v := e.code
	from, to, ok := v.bracketMatch()
	if !ok {
		t.Fatal("no match for the brace on line 1")
	}
	if from != (pos{row: 1, col: 6}) || to != (pos{row: 2, col: 1}) {
		t.Fatalf("match = %v..%v", from, to)
	}
	// From the closing side the same pair is found.
	v.setCaret(pos{row: 2, col: 1})
	from, to, ok = v.bracketMatch()
	if !ok || from != (pos{row: 2, col: 1}) || to != (pos{row: 1, col: 6}) {
		t.Fatalf("match from the closer = %v..%v, ok %v", from, to, ok)
	}
}

// A caret just past the bracket counts as being on it.
func TestBracketMatchJustPastTheCaret(t *testing.T) {
	e := bracketSetup(t, "a(b)", pos{row: 0, col: 2})
	if _, to, ok := e.code.bracketMatch(); !ok || to != (pos{row: 0, col: 3}) {
		t.Fatalf("partner = %v, ok %v", to, ok)
	}
}

// Nesting of another kind is skipped, and a pair can span lines.
func TestBracketMatchNested(t *testing.T) {
	e := bracketSetup(t, "([{}])\n", pos{row: 0, col: 0})
	if _, to, ok := e.code.bracketMatch(); !ok || to != (pos{row: 0, col: 5}) {
		t.Fatalf("partner of ( = %v, ok %v", to, ok)
	}
	e2 := bracketSetup(t, "x := [\n\t1,\n\t2,\n]\n", pos{row: 0, col: 5})
	if _, to, ok := e2.code.bracketMatch(); !ok || to != (pos{row: 3, col: 0}) {
		t.Fatalf("partner across lines = %v, ok %v", to, ok)
	}
}

// No match, and no box, when there is nothing to match.
func TestBracketMatchNothing(t *testing.T) {
	e := bracketSetup(t, "plain text\n", pos{row: 0, col: 3})
	v := e.code
	if _, _, ok := v.bracketMatch(); ok {
		t.Fatal("a match was reported for plain text")
	}
	// An unbalanced bracket has no partner.
	e2 := bracketSetup(t, "{ unclosed\n", pos{row: 0, col: 0})
	if _, _, ok := e2.code.bracketMatch(); ok {
		t.Fatal("an unbalanced bracket got a partner")
	}
}

// The search is bounded, so a bracket whose partner is far away costs nothing.
func TestBracketMatchIsBounded(t *testing.T) {
	body := "{\n" + strings.Repeat("x\n", bracketScanChars) + "}\n"
	e := bracketSetup(t, body, pos{row: 0, col: 0})
	if _, _, ok := e.code.bracketMatch(); ok {
		t.Fatal("a partner beyond the scan budget was reported")
	}
}

// Nothing is marked while something is selected, or while the view is unfocused.
func TestBracketMatchNeedsTheCaret(t *testing.T) {
	e := bracketSetup(t, "(a)\n", pos{row: 0, col: 0})
	v := e.code
	v.anchor = pos{row: 0, col: 1}
	if _, _, ok := v.bracketMatch(); ok {
		t.Fatal("a match was reported while text is selected")
	}
	v.clearSelection()
	v.focused = false
	if _, _, ok := v.bracketMatch(); ok {
		t.Fatal("a match was reported while the view is unfocused")
	}
}

// The boxes are drawn behind both brackets, at the cells they occupy.
func TestBracketBoxesAreDrawn(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "f(a, b)\n")
	v := e.code
	v.focused = true
	v.setCaret(pos{row: 0, col: 2})
	v.render()

	if !v.brushes[0].Visible() || !v.brushes[1].Visible() {
		t.Fatal("the matched pair is not marked")
	}
	_, charW := v.metrics()
	inset := v.inset()
	wantOpen := inset + 1*charW // the "(" is the second cell
	if got := v.brushes[0].Position().X; got != wantOpen {
		t.Fatalf("first box at x=%v, want %v", got, wantOpen)
	}
	wantClose := inset + 6*charW
	if got := v.brushes[1].Position().X; got != wantClose {
		t.Fatalf("second box at x=%v, want %v", got, wantClose)
	}
	if w := v.brushes[0].Size().Width; w != charW {
		t.Fatalf("box width = %v, want one cell %v", w, charW)
	}

	// Moving off the bracket takes the boxes away.
	v.setCaret(pos{row: 0, col: 3})
	v.render()
	if v.brushes[0].Visible() || v.brushes[1].Visible() {
		t.Fatal("the boxes stayed after the caret left the bracket")
	}
}

// The boxes follow a wrapped line: the partner can be on a later visual row.
func TestBracketBoxesOnWrappedLine(t *testing.T) {
	e := newTestEditor(t)
	e.wrap = true
	setBuffer(e, "f("+strings.Repeat("x", 400)+")\n")
	v := e.code
	v.focused = true
	v.setCaret(pos{row: 0, col: 1})
	v.render()

	if !v.brushes[0].Visible() || !v.brushes[1].Visible() {
		t.Fatal("the pair is not marked on a wrapped line")
	}
	_, lineH := v.metrics()
	if a, b := v.brushes[0].Position().Y, v.brushes[1].Position().Y; b <= a {
		t.Fatalf("the partner box is at y=%v, not below the first at y=%v", b, a)
	}
	if v.brushes[0].Position().Y < 0 || v.brushes[1].Position().Y > v.Size().Height {
		t.Fatalf("a box is outside the view: %v..%v", v.brushes[0].Position(), v.brushes[1].Position())
	}
	_ = lineH
}

// The boxes are behind the text, so they never hide a glyph.
func TestBracketBoxesAreDrawnBehindTheText(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "(a)\n")
	v := e.code
	v.focused = true
	v.setCaret(pos{row: 0, col: 1})
	v.render()
	objs := test.WidgetRenderer(v).Objects()
	at := map[fyne.CanvasObject]int{}
	for i, o := range objs {
		at[o] = i
	}
	textIndex, ok := at[v.segs[0][0]]
	if !ok {
		t.Fatal("the first row draws no text")
	}
	for _, b := range v.brushes {
		if at[b] > textIndex {
			t.Fatal("a bracket box is drawn on top of the text")
		}
	}
}

// Typing an opener inserts the pair and puts the caret between them.
func TestAutoCloseBrackets(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	text(e, "f(")
	if got := e.text(); got != "f()" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:2" {
		t.Fatalf("caret at %s, want 0:2", caretAt(e))
	}
	// Typing the closer steps over the one that was inserted for it.
	text(e, "x)")
	if got := e.text(); got != "f(x)" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:4" {
		t.Fatalf("caret at %s, want 0:4", caretAt(e))
	}
}

// Quotes pair, but not in the middle of a word.
func TestAutoCloseQuotes(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	text(e, "say(\"hi\")")
	if got := e.text(); got != "say(\"hi\")" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:9" {
		t.Fatalf("caret at %s, want 0:9", caretAt(e))
	}

	e2 := newTestEditor(t)
	setBuffer(e2, "")
	text(e2, "don't")
	if got := e2.text(); got != "don't" {
		t.Fatalf("buffer = %q, want the apostrophe not to pair", got)
	}
}

// A selection is wrapped rather than replaced.
func TestAutoCloseWrapsTheSelection(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "call x now\n")
	v := e.code
	v.anchor = pos{row: 0, col: 5}
	v.caret = pos{row: 0, col: 6}
	text(e, "(")
	if got := e.text(); got != "call (x) now\n" {
		t.Fatalf("buffer = %q", got)
	}
	if v.anchor != (pos{row: 0, col: 6}) || v.caret != (pos{row: 0, col: 7}) {
		t.Fatalf("selection = %v..%v, want the wrapped text still selected", v.anchor, v.caret)
	}
}

// Backspace between the halves of an empty pair removes both.
func TestAutoCloseBackspaceRemovesThePair(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	text(e, "if {}")
	e.code.setCaret(pos{row: 0, col: 4}) // between the braces
	key(e, fyne.KeyBackspace)
	if got := e.text(); got != "if " {
		t.Fatalf("buffer = %q", got)
	}
}

// With auto-closing off nothing is inserted but the character that was typed.
func TestAutoCloseOff(t *testing.T) {
	e := newTestEditor(t)
	e.autoClose = false
	setBuffer(e, "")
	text(e, "f(")
	if got := e.text(); got != "f(" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "0:2" {
		t.Fatalf("caret at %s, want 0:2", caretAt(e))
	}
}

// Enter between a pair that was just typed splits it, which is the whole point of
// pairing them.
func TestAutoCloseAndEnterSplitThePair(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	text(e, "if {}")
	e.code.setCaret(pos{row: 0, col: 4})
	enter(e)
	if got := e.text(); got != "if {\n    \n}" {
		t.Fatalf("buffer = %q", got)
	}
	if caretAt(e) != "1:4" {
		t.Fatalf("caret at %s, want 1:4", caretAt(e))
	}
}

// Undo takes the pair back as one edit.
func TestAutoCloseIsOneUndoStep(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	text(e, "(")
	e.code.Undo()
	if got := e.text(); got != "" {
		t.Fatalf("buffer = %q after undo", got)
	}
}
