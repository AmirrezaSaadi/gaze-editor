package editor

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2/test"
)

// Structural commands rewrite the buffer with SetText, which clears Entry's own
// undo stack. Before the snapshot fix, none of them could be undone at all.
func TestUndoLineOperation(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("one\ntwo", "")

	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.DuplicateLine()
	if e.entry.Text != "one\none\ntwo" {
		t.Fatalf("after duplicate = %q", e.entry.Text)
	}

	e.Undo()
	if e.entry.Text != "one\ntwo" {
		t.Fatalf("undo after duplicate = %q, want %q", e.entry.Text, "one\ntwo")
	}
	if e.entry.CursorRow != 0 {
		t.Fatalf("undo caret row = %d, want 0", e.entry.CursorRow)
	}

	e.Redo()
	if e.entry.Text != "one\none\ntwo" {
		t.Fatalf("redo = %q", e.entry.Text)
	}
}

func TestUndoReplaceAll(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("cat dog cat", "")
	e.findEntry.SetText("cat")
	e.replEntry.SetText("fox")

	e.ReplaceAll()
	if e.entry.Text != "fox dog fox" {
		t.Fatalf("after replace all = %q", e.entry.Text)
	}
	e.Undo()
	if e.entry.Text != "cat dog cat" {
		t.Fatalf("undo after replace all = %q", e.entry.Text)
	}
}

// Undoing a rewrite must also restore the clean/dirty flag it was made from, so
// that undoing back to the just-loaded state does not leave a bogus "•" marker.
func TestUndoRestoresDirtyFlag(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a\nb", "")
	if e.dirty {
		t.Fatal("freshly loaded buffer should be clean")
	}

	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.DuplicateLine()
	if !e.dirty {
		t.Fatal("duplicate should mark the buffer dirty")
	}

	e.Undo()
	if e.dirty {
		t.Fatal("undo back to the loaded state should be clean again")
	}
}

// Typing is undone by Entry first, then the snapshot of the rewrite that
// cleared Entry's stack. Anything deeper is not reachable and must be a no-op
// rather than corrupting the buffer.
func TestUndoOrderTypingThenRewrite(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("", "")

	test.Type(e.entry, "hello")
	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.DuplicateLine()
	if e.entry.Text != "hello\nhello" {
		t.Fatalf("after duplicate = %q", e.entry.Text)
	}

	e.Undo()
	if e.entry.Text != "hello" {
		t.Fatalf("first undo = %q, want %q", e.entry.Text, "hello")
	}

	e.Undo() // history exhausted
	if e.entry.Text != "hello" {
		t.Fatalf("second undo changed the buffer to %q", e.entry.Text)
	}
}

func TestTypingDropsRedo(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("x\ny", "")

	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.DuplicateLine()
	e.Undo()
	if len(e.redo) == 0 {
		t.Fatal("undo should leave something to redo")
	}

	test.Type(e.entry, "Z")
	if len(e.redo) != 0 {
		t.Fatal("a new edit should invalidate the redo trail")
	}

	before := e.entry.Text
	e.Redo()
	if e.entry.Text != before {
		t.Fatalf("redo after a new edit changed the buffer to %q", e.entry.Text)
	}
}

func TestNewDocumentResetsHistory(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a\nb", "")
	e.entry.CursorRow, e.entry.CursorColumn = 0, 0
	e.DuplicateLine()

	e.SetContent("fresh", "")
	e.Undo()
	if e.entry.Text != "fresh" {
		t.Fatalf("undo after loading a new document = %q", e.entry.Text)
	}
	if len(e.undo) != 0 || len(e.redo) != 0 {
		t.Fatalf("history should be empty, got %d undo / %d redo", len(e.undo), len(e.redo))
	}
}

func TestUndoStackIsBounded(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("", "")

	for i := 0; i < maxUndoSteps*2; i++ {
		e.entry.SetText(fmt.Sprintf("step %d", i))
		e.pushUndo()
	}
	if len(e.undo) > maxUndoSteps {
		t.Fatalf("undo stack grew to %d, want at most %d", len(e.undo), maxUndoSteps)
	}
	// The newest state must survive eviction.
	if got := e.undo[len(e.undo)-1].text; got != fmt.Sprintf("step %d", maxUndoSteps*2-1) {
		t.Fatalf("newest snapshot = %q", got)
	}
}
