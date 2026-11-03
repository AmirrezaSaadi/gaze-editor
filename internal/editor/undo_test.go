package editor

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

// Structural commands are single undoable steps.
func TestUndoLineOperation(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("one\ntwo", "")

	e.code.setCaret(pos{row: 0, col: 0})
	e.DuplicateLine()
	if bufferText(e) != "one\none\ntwo" {
		t.Fatalf("after duplicate = %q", bufferText(e))
	}

	e.Undo()
	if bufferText(e) != "one\ntwo" {
		t.Fatalf("undo after duplicate = %q, want %q", bufferText(e), "one\ntwo")
	}
	if caretRow(e) != 0 {
		t.Fatalf("undo caret row = %d, want 0", caretRow(e))
	}

	e.Redo()
	if bufferText(e) != "one\none\ntwo" {
		t.Fatalf("redo = %q", bufferText(e))
	}
}

// Replace all is one step, however many matches it touched.
func TestUndoReplaceAll(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("cat dog cat", "")
	e.findEntry.SetText("cat")
	e.replEntry.SetText("fox")

	e.ReplaceAll()
	if bufferText(e) != "fox dog fox" {
		t.Fatalf("after replace all = %q", bufferText(e))
	}
	e.Undo()
	if bufferText(e) != "cat dog cat" {
		t.Fatalf("undo after replace all = %q", bufferText(e))
	}
}

// Undoing back to the state a file was loaded in clears the dirty flag, so the
// title does not keep a "•" for changes that are gone.
func TestUndoRestoresDirtyFlag(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a\nb", "")
	if e.dirty {
		t.Fatal("freshly loaded buffer should be clean")
	}

	e.code.setCaret(pos{row: 0, col: 0})
	e.DuplicateLine()
	if !e.dirty {
		t.Fatal("duplicate should mark the buffer dirty")
	}

	e.Undo()
	if e.dirty {
		t.Fatal("undo back to the loaded state should be clean again")
	}
}

// Typing is undone as a run, and the commands after it are undone one at a time.
func TestUndoOrderTypingThenCommand(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("", "")

	test.Type(e.code, "hello")
	if bufferText(e) != "hello" {
		t.Fatalf("after typing = %q", bufferText(e))
	}
	e.code.setCaret(pos{row: 0, col: 0})
	e.DuplicateLine()
	if bufferText(e) != "hello\nhello" {
		t.Fatalf("after duplicate = %q", bufferText(e))
	}

	e.Undo()
	if bufferText(e) != "hello" {
		t.Fatalf("first undo = %q, want %q", bufferText(e), "hello")
	}
	e.Undo()
	if bufferText(e) != "" {
		t.Fatalf("second undo = %q, want an empty buffer", bufferText(e))
	}
	e.Undo() // history exhausted
	if bufferText(e) != "" {
		t.Fatalf("undoing past the start changed the buffer to %q", bufferText(e))
	}
}

func TestTypingDropsRedo(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("x\ny", "")

	e.code.setCaret(pos{row: 0, col: 0})
	e.DuplicateLine()
	e.Undo()
	if len(e.code.redo) == 0 {
		t.Fatal("undo should leave something to redo")
	}

	test.Type(e.code, "Z")
	if len(e.code.redo) != 0 {
		t.Fatal("a new edit should invalidate the redo trail")
	}

	before := bufferText(e)
	e.Redo()
	if bufferText(e) != before {
		t.Fatalf("redo after a new edit changed the buffer to %q", bufferText(e))
	}
}

func TestNewDocumentResetsHistory(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a\nb", "")
	e.code.setCaret(pos{row: 0, col: 0})
	e.DuplicateLine()

	e.SetContent("fresh", "")
	e.Undo()
	if bufferText(e) != "fresh" {
		t.Fatalf("undo after loading a new document = %q", bufferText(e))
	}
	if len(e.code.undo) != 0 || len(e.code.redo) != 0 {
		t.Fatalf("history should be empty, got %d undo / %d redo", len(e.code.undo), len(e.code.redo))
	}
}

// The history is bounded, and the newest edits are the ones kept.
func TestUndoStackIsBounded(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("", "")

	for i := 0; i < maxCodeUndoEdits+50; i++ {
		// Typing at the start each time breaks the run, so every edit is its own
		// history entry.
		e.code.setCaret(pos{row: 0, col: 0})
		e.code.insertText("x")
	}
	if got := len(e.code.undo); got > maxCodeUndoEdits {
		t.Fatalf("undo history grew to %d, want at most %d", got, maxCodeUndoEdits)
	}
	// The newest edit must survive eviction.
	if got := e.code.undo[len(e.code.undo)-1].ins; got != "x" {
		t.Fatalf("newest entry inserts %q", got)
	}
	e.Undo()
	if got := bufferText(e); len(got) != maxCodeUndoEdits+50-1 {
		t.Fatalf("after undo the buffer is %d bytes, want %d", len(got), maxCodeUndoEdits+49)
	}
}
