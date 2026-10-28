package editor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

// Regression tests for the bugs fixed after 1.2.2.

// isolatedRecovery points the crash-recovery cache at a temporary directory so a
// test never touches the real one, and restores the default afterwards.
func isolatedRecovery(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := recoveryDir
	recoveryDir = func() string { return dir }
	t.Cleanup(func() { recoveryDir = old })
	return dir
}

// waitFor polls cond until it holds, failing the test after a grace period. It is
// for work that deliberately happens on a background goroutine, and it must only
// observe state that is not touched by the UI (files, mostly).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Regex searched text[from:], which made the slice's own first byte look like
// the start of the text: ^ matched mid-buffer and \b fired inside a word.
func TestMatcherAnchorsOnBufferNotOffset(t *testing.T) {
	caret, err := newMatcher("^foo", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := caret.next("foo bar foo", 8); ok {
		t.Fatal("^foo should not match at offset 8")
	}
	if _, ok := caret.at("foo bar foo", 8); ok {
		t.Fatal("^foo should not match at offset 8")
	}
	if start, _, ok := caret.next("foo bar foo", 0); !ok || start != 0 {
		t.Fatalf("^foo at offset 0 = %d,%v, want 0,true", start, ok)
	}

	boundary, err := newMatcher(`\bfoo`, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := boundary.at("xfoo foo", 1); ok {
		t.Fatal(`\bfoo should not match inside "xfoo"`)
	}
	if start, _, ok := boundary.next("xfoo foo", 1); !ok || start != 5 {
		t.Fatalf(`\bfoo from offset 1 = %d,%v, want 5,true`, start, ok)
	}
}

// The same bug made ReplaceCurrent rewrite text that was not a match: with the
// caret sitting on "foo" inside "xxfoo", a ^ anchored pattern appeared to match
// right there.
func TestRegexAnchorDoesNotReplaceAtOffset(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("foo xxfoo", "")
	e.findEntry.SetText("^foo")
	e.regexCheck.Checked = true
	e.replEntry.SetText("Z")

	e.entry.CursorRow, e.entry.CursorColumn = 0, 7 // on the "foo" inside "xxfoo"
	e.ReplaceCurrent()

	if e.entry.Text != "foo xxfoo" {
		t.Fatalf("ReplaceCurrent modified text that is not a ^ match: %q", e.entry.Text)
	}
	if e.entry.CursorColumn != 0 {
		t.Fatalf("caret column = %d, want 0 (the real ^ match)", e.entry.CursorColumn)
	}
}

// \b must consider the character before the search window. Starting inside
// "xfoo" made the offset look like a word boundary.
func TestRegexWordBoundaryDoesNotMatchInsideWord(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("xfoo foo", "")
	e.findEntry.SetText(`\bfoo`)
	e.regexCheck.Checked = true
	e.replEntry.SetText("Z")

	e.entry.CursorRow, e.entry.CursorColumn = 0, 1 // inside "xfoo"
	e.ReplaceCurrent()

	if e.entry.Text != "xfoo foo" {
		t.Fatalf("ReplaceCurrent modified a non-match: %q", e.entry.Text)
	}
	if e.entry.CursorColumn != 5 {
		t.Fatalf("caret column = %d, want 5 (the real \\b match)", e.entry.CursorColumn)
	}
}

// Replace All used to dump the caret at the top of the buffer.
func TestReplaceAllKeepsCaret(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a\nb\na", "")
	e.entry.CursorRow, e.entry.CursorColumn = 1, 0
	e.findEntry.SetText("a")
	e.replEntry.SetText("x")

	e.ReplaceAll()

	if e.entry.Text != "x\nb\nx" {
		t.Fatalf("ReplaceAll = %q, want %q", e.entry.Text, "x\nb\nx")
	}
	if e.entry.CursorRow != 1 {
		t.Fatalf("ReplaceAll moved the caret to row %d, want 1", e.entry.CursorRow)
	}
}

// The command line says file:line:col with a 1-based column, so goto has to
// subtract one. It used to land one character to the right.
func TestOpenPathAtColumnIsOneBased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "at.txt")
	if err := os.WriteFile(path, []byte("aaa\nbbb\nccc"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t)
	e.OpenPathAt(path, 2, 3)
	if e.entry.CursorRow != 1 || e.entry.CursorColumn != 2 {
		t.Fatalf("caret after 2:3 = (%d,%d), want (1,2)", e.entry.CursorRow, e.entry.CursorColumn)
	}

	e.OpenPathAt(path, 3, 0) // column 0 is also the start of the line
	if e.entry.CursorRow != 2 || e.entry.CursorColumn != 0 {
		t.Fatalf("caret after 3:0 = (%d,%d), want (2,0)", e.entry.CursorRow, e.entry.CursorColumn)
	}
}

// Discarding unsaved changes must delete the recovery snapshot too, otherwise the
// next open of the same file offers the edits the user just threw away.
func TestDiscardDropsRecoverySnapshot(t *testing.T) {
	isolatedRecovery(t)
	path := filepath.Join(t.TempDir(), "d.txt")

	e := newTestEditor(t)
	e.SetContent("draft text", path)
	e.dirty = true
	e.writeRecovery()
	if _, ok := recoveryFor(path); !ok {
		t.Fatal("recovery snapshot should exist before the discard")
	}

	e.discardChanges()

	if _, ok := recoveryFor(path); ok {
		t.Fatal("discarding changes must drop the recovery snapshot")
	}
}

// An untitled buffer has no copy anywhere else, so it needs a snapshot as well.
func TestUntitledBufferGetsRecoverySnapshot(t *testing.T) {
	isolatedRecovery(t)

	e := newTestEditor(t)
	e.autosaveOn = true // the helper turns it off; this is the test that wants it
	e.SetContent("unsaved draft", "")
	e.dirty = true
	e.lastAutosave = time.Time{} // ignore the 30s throttle
	e.maybeAutosave()

	waitFor(t, "the draft snapshot", func() bool {
		text, ok := recoveryFor(draftKey)
		return ok && text == "unsaved draft"
	})

	if recoveryPath(draftKey) == recoveryPath(filepath.Join(t.TempDir(), "x.txt")) {
		t.Fatal("the draft must not share a snapshot slot with a real file")
	}
}

// Saving over a file that another program changed must not happen silently.
func TestExternalChangeIsDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ext.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t)
	e.loadContent(path, "one", encUTF8)
	if e.externalChange(path) {
		t.Fatal("a freshly loaded file should not report a conflict")
	}

	if err := os.WriteFile(path, []byte("one and more"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !e.externalChange(path) {
		t.Fatal("an externally modified file was not detected")
	}

	e.entry.SetText("mine")
	e.writeTo(path) // the test confirms the conflict dialog path separately
	if e.externalChange(path) {
		t.Fatal("a save should re-baseline the file state")
	}
}

// An edit typed while a background save is in flight must survive it: the save
// only mirrors its trimmed buffer back when the buffer still matches.
func TestEditsDuringSaveAreKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inflight.txt")

	e := newTestEditor(t)
	e.SetContent("first", path)
	req := e.pendingSave(path)      // snapshot handed to the writer
	e.entry.SetText("first + more") // the user keeps typing
	e.finishSave(req, "first")      // …and the write completes

	if e.entry.Text != "first + more" {
		t.Fatalf("buffer overwritten by the save: %q", e.entry.Text)
	}
	if !e.dirty {
		t.Fatal("edits made during a save must leave the document dirty")
	}
}

// Trimming on save rewrites the buffer, so that rewrite has to be undoable.
func TestTrimOnSaveIsUndoable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trim.txt")

	e := newTestEditor(t)
	e.SetContent("a  \nb", path)
	e.trimOnSave = true
	e.writeTo(path)

	if e.entry.Text != "a\nb" {
		t.Fatalf("buffer after trim-on-save = %q, want %q", e.entry.Text, "a\nb")
	}

	e.Undo()
	if e.entry.Text != "a  \nb" {
		t.Fatalf("undo after trim = %q, want the untrimmed buffer", e.entry.Text)
	}
}

// Status messages used to sit in the bar forever; setting one now arms a timer
// that clears it, and setting an empty message disarms it again.
func TestStatusMessageIsScheduledToClear(t *testing.T) {
	e := newTestEditor(t)

	e.setStatus("no matches for \"x\"")
	if e.status.Text == "" || e.statusTimer == nil {
		t.Fatal("a status message should arm the clear timer")
	}

	e.setStatus("")
	if e.status.Text != "" || e.statusTimer != nil {
		t.Fatal("clearing the status should disarm the timer")
	}
}

// Esc used to move focus into the editor even when the find bar was closed.
func TestEscapeDoesNotStealFocusWhenFindBarIsClosed(t *testing.T) {
	e := newTestEditor(t)
	canvas := e.win.Canvas()

	btn := firstToolbarButton(e.win.Content())
	if btn == nil {
		t.Fatal("no toolbar button found in the window content")
	}
	canvas.Focus(btn)
	if canvas.Focused() != fyne.Focusable(btn) {
		t.Skip("the test canvas does not honour focus requests")
	}

	e.escape() // the find bar is closed
	if got := canvas.Focused(); got != fyne.Focusable(btn) {
		t.Fatalf("Esc with the find bar closed moved focus to %T", got)
	}

	e.ShowFind()
	e.escape()
	if e.findBar.Visible() {
		t.Fatal("Esc should close an open find bar")
	}
	if got := canvas.Focused(); got != fyne.Focusable(e.entry) {
		t.Fatalf("focus after closing the find bar = %T, want the editor", got)
	}
}

// firstToolbarButton walks the window content for a tip button.
func firstToolbarButton(obj fyne.CanvasObject) *tipButton {
	switch o := obj.(type) {
	case *tipButton:
		return o
	case *fyne.Container:
		for _, child := range o.Objects {
			if b := firstToolbarButton(child); b != nil {
				return b
			}
		}
	}
	return nil
}

// Recounting a large buffer on every keystroke is what made typing stutter, so
// the recount is deferred instead of run inline.
func TestLargeBufferStatsAreDeferred(t *testing.T) {
	old := statsSyncLimit
	statsSyncLimit = 8
	t.Cleanup(func() { statsSyncLimit = old })

	e := newTestEditor(t)
	e.SetContent("hello world", "")

	e.statChars = 0 // poison the cached count
	e.updateStatsForChange()

	if e.statChars != 0 {
		t.Fatal("a large buffer should not be recounted on the keystroke path")
	}
	if e.statsTimer == nil {
		t.Fatal("the recount should be deferred")
	}
	e.statsTimer.Stop()
}
