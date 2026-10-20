package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
)

func TestLineMoveAndDuplicate(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("one\ntwo\nthree", "")

	e.entry.CursorRow, e.entry.CursorColumn = 1, 0
	e.MoveLineUp()
	if e.entry.Text != "two\none\nthree" {
		t.Fatalf("MoveLineUp = %q", e.entry.Text)
	}
	if e.entry.CursorRow != 0 {
		t.Fatalf("MoveLineUp caret row = %d, want 0", e.entry.CursorRow)
	}

	e.MoveLineDown()
	if e.entry.Text != "one\ntwo\nthree" {
		t.Fatalf("MoveLineDown = %q", e.entry.Text)
	}
	if e.entry.CursorRow != 1 {
		t.Fatalf("MoveLineDown caret row = %d, want 1", e.entry.CursorRow)
	}

	e.DuplicateLine()
	if e.entry.Text != "one\ntwo\ntwo\nthree" {
		t.Fatalf("DuplicateLine = %q", e.entry.Text)
	}
	if e.entry.CursorRow != 2 {
		t.Fatalf("DuplicateLine caret row = %d, want 2", e.entry.CursorRow)
	}

	// The first line cannot move up.
	e.entry.CursorRow = 0
	e.MoveLineUp()
	if e.entry.Text != "one\ntwo\ntwo\nthree" {
		t.Fatalf("MoveLineUp at top changed text: %q", e.entry.Text)
	}
}

func TestIndentOutdent(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("x\ny", "")
	e.entry.CursorRow = 0

	e.IndentLine()
	if e.entry.Text != "    x\ny" {
		t.Fatalf("IndentLine = %q", e.entry.Text)
	}
	if e.entry.CursorColumn != 4 {
		t.Fatalf("after indent col = %d, want 4", e.entry.CursorColumn)
	}
	e.OutdentLine()
	if e.entry.Text != "x\ny" {
		t.Fatalf("OutdentLine = %q", e.entry.Text)
	}
	if e.entry.CursorColumn != 0 {
		t.Fatalf("after outdent col = %d, want 0", e.entry.CursorColumn)
	}

	// Tab-indented documents outdent a full tab.
	e.SetContent("\tfoo\n\tbar", "")
	if !e.indentUseTabs {
		t.Fatal("tab indentation should be detected")
	}
	e.OutdentLine()
	if e.entry.Text != "foo\n\tbar" {
		t.Fatalf("tab outdent = %q", e.entry.Text)
	}
}

func TestSerializeTrimAndEOL(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a  \nb\t\nc", "")
	e.trimOnSave = true

	out, trimmed := e.serialize()
	if out != "a\nb\nc" {
		t.Fatalf("serialize = %q, want %q", out, "a\nb\nc")
	}
	if trimmed != "a\nb\nc" {
		t.Fatalf("trimmed = %q", trimmed)
	}

	// CRLF files are normalised in memory and restored on save.
	e.SetContent("a\r\nb\r\n", "")
	if e.eol != eolCRLF {
		t.Fatalf("eol = %q, want CRLF", e.eol)
	}
	if e.entry.Text != "a\nb\n" {
		t.Fatalf("normalised text = %q", e.entry.Text)
	}
	if out, _ = e.serialize(); out != "a\r\nb\r\n" {
		t.Fatalf("CRLF serialize = %q", out)
	}
}

func TestWriteToTrimsAndAdopts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")

	e := newTestEditor(t)
	e.SetContent("hello   \nworld", path)
	e.trimOnSave = true
	e.writeTo(path)

	if e.dirty {
		t.Fatal("writeTo should clear the dirty flag")
	}
	if e.entry.Text != "hello\nworld" {
		t.Fatalf("buffer not trimmed: %q", e.entry.Text)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\nworld" {
		t.Fatalf("file = %q", data)
	}
}

func TestWriteFileAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("file = %q", data)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestRegexReplaceAndGroups(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("a1 b22 c333", "")
	e.findEntry.SetText(`\d+`)
	e.regexCheck.Checked = true
	e.replEntry.SetText("N")
	e.ReplaceAll()
	if e.entry.Text != "aN bN cN" {
		t.Fatalf("regex ReplaceAll = %q", e.entry.Text)
	}

	e.SetContent("john smith", "")
	e.findEntry.SetText(`(\w+) (\w+)`)
	e.replEntry.SetText("$2 $1")
	e.ReplaceAll()
	if e.entry.Text != "smith john" {
		t.Fatalf("capture-group replace = %q", e.entry.Text)
	}

	// An invalid pattern is reported without touching the buffer.
	e.SetContent("unchanged", "")
	e.findEntry.SetText("(")
	e.FindNext()
	if e.entry.Text != "unchanged" {
		t.Fatalf("invalid regex changed buffer: %q", e.entry.Text)
	}
	if !strings.Contains(e.status.Text, "regex") {
		t.Fatalf("status = %q, want a regex error", e.status.Text)
	}
}

func TestFindHistory(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("alpha beta\n", "")

	e.findEntry.SetText("alpha")
	e.FindNext()
	e.findEntry.SetText("beta")
	e.FindNext()

	hist := e.findHistory()
	if len(hist) != 2 || hist[0] != "beta" || hist[1] != "alpha" {
		t.Fatalf("history = %v, want [beta alpha]", hist)
	}

	e.findEntry.SetText("draft")
	e.historyPrev()
	if e.findEntry.Text != "beta" {
		t.Fatalf("historyPrev = %q, want beta", e.findEntry.Text)
	}
	e.historyPrev()
	if e.findEntry.Text != "alpha" {
		t.Fatalf("historyPrev second = %q, want alpha", e.findEntry.Text)
	}
	e.historyNext()
	if e.findEntry.Text != "beta" {
		t.Fatalf("historyNext = %q, want beta", e.findEntry.Text)
	}
	e.historyNext()
	if e.findEntry.Text != "draft" {
		t.Fatalf("historyNext to draft = %q, want draft", e.findEntry.Text)
	}
}

func TestGotoLineCol(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("abc\ndefgh\nij", "")
	e.gotoLineCol(2, 3)
	if e.entry.CursorRow != 1 || e.entry.CursorColumn != 3 {
		t.Fatalf("gotoLineCol = (%d,%d), want (1,3)", e.entry.CursorRow, e.entry.CursorColumn)
	}
	e.gotoLineCol(2, 99) // col clamps
	if e.entry.CursorColumn != 5 {
		t.Fatalf("clamped col = %d, want 5", e.entry.CursorColumn)
	}
}

func TestSessionRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := test.NewApp()
	w1 := test.NewWindow(nil)
	e1 := New(a, w1)
	e1.BuildUI()
	e1.OpenPath(path)
	e1.entry.CursorRow, e1.entry.CursorColumn = 2, 3
	e1.saveSession(path)

	w2 := test.NewWindow(nil)
	e2 := New(a, w2)
	e2.BuildUI()
	e2.RestoreSession()

	if e2.currentFile != path {
		t.Fatalf("restored file = %q, want %q", e2.currentFile, path)
	}
	if e2.entry.CursorRow != 2 || e2.entry.CursorColumn != 3 {
		t.Fatalf("restored caret = (%d,%d), want (2,3)", e2.entry.CursorRow, e2.entry.CursorColumn)
	}
}

func TestDropOpensFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dropped.txt")
	if err := os.WriteFile(path, []byte("dropped"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t)
	e.onDropped(fyne.Position{}, []fyne.URI{storage.NewFileURI(path)})
	if e.currentFile != path {
		t.Fatalf("dropped file = %q, want %q", e.currentFile, path)
	}
	if e.entry.Text != "dropped" {
		t.Fatalf("dropped content = %q", e.entry.Text)
	}
}

func TestRecoverySnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer clearRecovery(path)

	e := newTestEditor(t)
	e.currentFile = path
	e.entry.SetText("unsaved edit")
	e.writeRecovery()

	rec, ok := recoveryFor(path)
	if !ok {
		t.Fatal("recovery snapshot should be found")
	}
	if rec != "unsaved edit" {
		t.Fatalf("recovery = %q", rec)
	}

	clearRecovery(path)
	if _, ok := recoveryFor(path); ok {
		t.Fatal("recovery should be gone after clearing")
	}
}

func TestTextLineHelpers(t *testing.T) {
	if start, end := lineBounds("ab\ncd\nef", 1); start != 3 || end != 5 {
		t.Fatalf("lineBounds = (%d,%d), want (3,5)", start, end)
	}
	if out, row := moveLineUp("a\nb\nc", 2); out != "a\nc\nb" || row != 1 {
		t.Fatalf("moveLineUp = %q,%d", out, row)
	}
	if out, row := moveLineDown("a\nb\nc", 0); out != "b\na\nc" || row != 1 {
		t.Fatalf("moveLineDown = %q,%d", out, row)
	}
	if out, _ := duplicateLine("x\ny", 0); out != "x\nx\ny" {
		t.Fatalf("duplicateLine = %q", out)
	}
	if out, removed := outdentLine("    x", 0, false, 4); out != "x" || removed != 4 {
		t.Fatalf("outdentLine = %q,%d", out, removed)
	}
	if got := trimTrailingSpace("a  \nb\t\nc"); got != "a\nb\nc" {
		t.Fatalf("trimTrailingSpace = %q", got)
	}
	if got := trimTrailingSpace("clean"); got != "clean" {
		t.Fatalf("trimTrailingSpace clean = %q", got)
	}
	if useTabs, width := detectIndent("    indented\n"); useTabs || width != 4 {
		t.Fatalf("detectIndent spaces = %v,%d", useTabs, width)
	}
	if useTabs, _ := detectIndent("\tindented"); !useTabs {
		t.Fatal("detectIndent should detect tabs")
	}
	if s, eol := normalizeEOL("a\r\nb"); eol != eolCRLF || s != "a\nb" {
		t.Fatalf("normalizeEOL = %q,%q", s, eol)
	}
	if s, eol := normalizeEOL("a\rb"); eol != eolCR || s != "a\nb" {
		t.Fatalf("normalizeEOL CR = %q,%q", s, eol)
	}
}
