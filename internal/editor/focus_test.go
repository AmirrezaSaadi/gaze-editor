package editor

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

// Typing must not depend on the canvas having a focused object: Fyne calls the
// canvas hook when nothing is focused, which is what a closed dialog leaves.
func TestTypingWithNothingFocused(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	e.win.Canvas().Unfocus()
	if f := e.win.Canvas().Focused(); f != nil {
		t.Fatalf("focused = %T after Unfocus", f)
	}

	runeHook := e.win.Canvas().OnTypedRune()
	keyHook := e.win.Canvas().OnTypedKey()
	if runeHook == nil || keyHook == nil {
		t.Fatal("the editor installs no fallback for a canvas with nothing focused")
	}
	for _, r := range "hi" {
		runeHook(r)
	}
	keyHook(&fyne.KeyEvent{Name: fyne.KeyEnter})
	for _, r := range "there" {
		runeHook(r)
	}
	if got := e.text(); got != "hi\nthere" {
		t.Fatalf("buffer = %q", got)
	}
	// The hook hands focus back, so the next key goes the normal way.
	if f := e.win.Canvas().Focused(); f != e.code {
		t.Fatalf("focused = %T after typing into an unfocused canvas", f)
	}
}

// While a dialog is up it owns the keyboard: the fallback must not type into the
// document behind it.
func TestDialogKeepsTheKeyboard(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	e.ShowLanguages()
	defer func() {
		if e.langsDialog != nil {
			e.langsDialog.Hide()
		}
	}()
	e.win.Canvas().Unfocus()
	e.win.Canvas().OnTypedRune()('x')
	e.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEnter})
	if got := e.text(); got != "" {
		t.Fatalf("buffer = %q, want nothing typed while a dialog is up", got)
	}
}

// Closing a dialog hands keyboard focus back to the editor instead of leaving the
// window unable to take a keystroke at all.
func TestClosingADialogRestoresFocus(t *testing.T) {
	e := newTestEditor(t)
	e.ShowLanguages()
	if e.langsDialog == nil {
		t.Fatal("the languages dialog was not kept")
	}
	e.langsDialog.Hide() // what the Close button does
	if f := e.win.Canvas().Focused(); f != e.code {
		t.Fatalf("focused = %T after the dialog closed, want the editor", f)
	}
	if e.langsDialog != nil {
		t.Fatal("the dialog was not forgotten")
	}
	test.Type(e.code, "x")
	if got := e.text(); got != "x" {
		t.Fatalf("buffer = %q after typing once the dialog closed", got)
	}
}

// Clicking in the text is another way back from a window that lost focus.
func TestClickRestoresFocus(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "one\ntwo\n")
	e.win.Canvas().Unfocus()
	test.Tap(e.code)
	if f := e.win.Canvas().Focused(); f != e.code {
		t.Fatalf("focused = %T after clicking the text", f)
	}
	test.Type(e.code, "!")
	if got := e.text(); !strings.Contains(got, "!") {
		t.Fatalf("buffer = %q after typing once the text was clicked", got)
	}
}

// A key released while the window was not focused never reaches the widget, so a
// modifier can look held down. Regaining focus has to clear it, or typing does
// nothing.
func TestFocusGainedClearsStuckModifiers(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "")
	e.code.KeyDown(&fyne.KeyEvent{Name: desktop.KeyControlLeft})
	e.code.FocusLost() // the KeyUp goes to another window
	e.code.FocusGained()
	test.Type(e.code, "ok")
	if got := e.text(); got != "ok" {
		t.Fatalf("buffer = %q, want typing to work after focus was regained", got)
	}
}

// The caret position, and therefore typing, must survive a document install: the
// code view is the focus target after an open.
func TestOpenLeavesTheEditorFocused(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "x")
	e.win.Canvas().Unfocus()
	path := writeTestFile(t, "a.txt", "hello\n")
	e.startOpen(path, openNormal, nil)
	waitFor(t, "the open to finish", func() bool { return e.currentFile == path })
	if f := e.win.Canvas().Focused(); f != e.code {
		t.Fatalf("focused = %T after opening a file", f)
	}
	test.Type(e.code, "y")
	if got := e.text(); got != "yhello\n" {
		t.Fatalf("buffer = %q", got)
	}
}
