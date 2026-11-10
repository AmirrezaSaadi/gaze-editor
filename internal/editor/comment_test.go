package editor

import (
	"strings"
	"testing"
)

// language installs one of the built-in definitions on the editor, for the tests
// that need comment syntax without opening a file.
func language(t *testing.T, id string) *grammar {
	t.Helper()
	for _, g := range builtinGrammars() {
		if g.id == id {
			return g
		}
	}
	t.Fatalf("no built-in definition for %q", id)
	return nil
}

// commentEditor is an editor holding text, with a language installed and the caret
// at the top.
func commentEditor(t *testing.T, id, text string) *Editor {
	t.Helper()
	e := newTestEditor(t)
	setBuffer(e, text) // installing a buffer re-detects the language, so set it after
	e.lang = language(t, id)
	e.code.setCaret(pos{})
	return e
}

// Toggling comments the caret's line and toggles it back.
func TestToggleCommentOnOneLine(t *testing.T) {
	e := commentEditor(t, "go", "x := 1\n")
	e.ToggleComment()
	if got := e.text(); got != "// x := 1\n" {
		t.Fatalf("buffer = %q", got)
	}
	e.ToggleComment()
	if got := e.text(); got != "x := 1\n" {
		t.Fatalf("buffer after toggling back = %q", got)
	}
}

// Indentation is kept, and the comment goes after it.
func TestToggleCommentKeepsIndentation(t *testing.T) {
	e := commentEditor(t, "go", "    x := 1\n")
	e.code.setCaret(pos{row: 0, col: 6})
	e.ToggleComment()
	if got := e.text(); got != "    // x := 1\n" {
		t.Fatalf("buffer = %q", got)
	}
	e.ToggleComment()
	if got := e.text(); got != "    x := 1\n" {
		t.Fatalf("buffer after toggling back = %q", got)
	}
}

// A selection comments every line it touches, leaves blank lines alone, and is one
// undo step.
func TestToggleCommentOnABlock(t *testing.T) {
	e := commentEditor(t, "python", "def f():\n    a = 1\n\n    return a\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 3, col: 12}
	e.ToggleComment()
	want := "# def f():\n    # a = 1\n\n    # return a\n"
	if got := e.text(); got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
	v.Undo()
	if got := e.text(); got != "def f():\n    a = 1\n\n    return a\n" {
		t.Fatalf("after undo: %q", got)
	}
	// Undo restores the lines that were selected, so toggling again works.
	if v.anchor != (pos{row: 0, col: 0}) || v.caret != (pos{row: 3, col: 12}) {
		t.Fatalf("undo left the selection at %v..%v", v.anchor, v.caret)
	}
	v.Redo()
	if got := e.text(); got != want {
		t.Fatalf("after redo: %q", got)
	}
}

// A block is uncommented only when every line with anything on it is commented.
func TestToggleCommentOnAMixedBlock(t *testing.T) {
	e := commentEditor(t, "go", "// a\nb\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 1, col: 1}
	e.ToggleComment()
	if got := e.text(); got != "// // a\n// b\n" {
		t.Fatalf("buffer = %q", got)
	}
	e.ToggleComment()
	if got := e.text(); got != "// a\nb\n" {
		t.Fatalf("buffer after toggling back = %q", got)
	}
}

// A selection that stops at the start of a line does not comment that line.
func TestToggleCommentStopsAtTheLineStart(t *testing.T) {
	e := commentEditor(t, "go", "a\nb\nc\n")
	v := e.code
	v.anchor = pos{row: 0, col: 0}
	v.caret = pos{row: 1, col: 0}
	e.ToggleComment()
	if got := e.text(); got != "// a\nb\nc\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// A language without line comments gets the block comment instead, and toggling it
// off puts the block back.
func TestToggleCommentBlockComment(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "{\n  \"a\": 1\n}\n")
	g, err := parseGrammar([]byte(`{
		"schema": 1, "id": "jsonc", "name": "JSONC", "extensions": ["jsonc"],
		"block_comment": [{"start": "/*", "end": "*/"}],
		"strings": [{"start": "\"", "end": "\""}]
	}`))
	if err != nil {
		t.Fatalf("the definition did not parse: %v", err)
	}
	e.lang = g
	v := e.code
	v.anchor = pos{row: 1, col: 0}
	v.caret = pos{row: 1, col: 9}
	e.ToggleComment()
	if got := e.text(); got != "{\n  /* \"a\": 1 */\n}\n" {
		t.Fatalf("buffer = %q", got)
	}
	e.ToggleComment()
	if got := e.text(); got != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("buffer after toggling back = %q", got)
	}
}

// Without a language, or with one that has no comments at all, say so rather than
// guessing a marker.
func TestToggleCommentWithoutSyntax(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "hello\n")
	e.ToggleComment()
	if !strings.Contains(e.status.Text, "no comment syntax") {
		t.Fatalf("status = %q", e.status.Text)
	}
	if got := e.text(); got != "hello\n" {
		t.Fatalf("buffer = %q", got)
	}

	e.lang = language(t, "markdown")
	e.status.SetText("")
	e.ToggleComment()
	if !strings.Contains(e.status.Text, "no comment syntax") {
		t.Fatalf("status = %q for a language without comments", e.status.Text)
	}
}

// The caret stays on its text when the comment marker is added in front of it.
func TestToggleCommentKeepsTheCaret(t *testing.T) {
	e := commentEditor(t, "go", "x := 1\n")
	e.code.setCaret(pos{row: 0, col: 5})
	e.ToggleComment()
	if got := caretAt(e); got != "0:8" {
		t.Fatalf("caret at %s, want 0:8", got)
	}
}

// An empty line has nothing to comment.
func TestToggleCommentOnAnEmptyLine(t *testing.T) {
	e := commentEditor(t, "go", "\n")
	e.ToggleComment()
	if got := e.text(); got != "\n" {
		t.Fatalf("buffer = %q", got)
	}
	if !strings.Contains(e.status.Text, "nothing to comment") {
		t.Fatalf("status = %q", e.status.Text)
	}
}
