package editor

import (
	"fmt"
	"strings"
	"testing"
)

// lex is a helper: it lexes text line by line, keeping the state, and returns the
// styles of each line as "class:text" pairs.
func lex(t *testing.T, g *grammar, text string) [][]string {
	t.Helper()
	state := 0
	var out [][]string
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		runes := []rune(line)
		spans, next := g.lexLine(runes, state)
		state = next
		var parts []string
		for _, sp := range spans {
			parts = append(parts, fmt.Sprintf("%d:%s", sp.class, string(runes[sp.from:sp.to])))
		}
		out = append(out, parts)
	}
	return out
}

func grammarByID(t *testing.T, id string) *grammar {
	t.Helper()
	for _, g := range builtinGrammars() {
		if g.id == id {
			return g
		}
	}
	t.Fatalf("no built-in grammar %q", id)
	return nil
}

func wantLine(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("styles = %v, want %v", got, want)
	}
}

// Every definition shipped in the binary has to parse, and has to be findable by
// the files it claims.
func TestBuiltinGrammarsParse(t *testing.T) {
	all := builtinGrammars()
	if len(all) < 10 {
		t.Fatalf("%d built-in languages, expected the shipped set", len(all))
	}
	ids := map[string]bool{}
	for _, g := range all {
		if !g.builtin {
			t.Fatalf("%s is not marked built in", g.name)
		}
		if g.id == "" || g.name == "" {
			t.Fatalf("a definition has no id or name: %+v", g)
		}
		if ids[g.id] {
			t.Fatalf("duplicate language id %q", g.id)
		}
		ids[g.id] = true
		if !g.needsState() && len(g.states) != 0 {
			t.Fatalf("%s: states without multi-line constructs", g.name)
		}
	}
	for _, c := range []struct{ file, id string }{
		{"main.go", "go"},
		{"x.JSON", "json"},
		{"config.toml", "toml"},
		{"ci.yml", "yaml"},
		{"README.md", "markdown"},
		{"script.py", "python"},
		{"lib.rs", "rust"},
		{"a.c", "c"},
		{"a.cpp", "cpp"},
		{"app.js", "javascript"},
		{"app.ts", "typescript"},
		{"run.sh", "shell"},
		{"x.ini", "ini"},
		{"Dockerfile", "dockerfile"},
		{"Makefile", "makefile"},
		{"notes.unknown", ""},
		{"", ""},
	} {
		g := grammarFor(c.file, all)
		got := ""
		if g != nil {
			got = g.id
		}
		if got != c.id {
			t.Errorf("grammarFor(%q) = %q, want %q", c.file, got, c.id)
		}
	}
}

// A definition is data from possibly the network, so everything about it is
// checked before it is used.
func TestParseGrammarRejects(t *testing.T) {
	good := `{"schema":1,"id":"x","name":"X","extensions":["x"]}`
	if _, err := parseGrammar([]byte(good)); err != nil {
		t.Fatalf("a minimal definition was rejected: %v", err)
	}
	cases := []struct{ name, data string }{
		{"empty", ""},
		{"not json", "{"},
		{"no schema", `{"id":"x","extensions":["x"]}`},
		{"newer schema", `{"schema":99,"id":"x","extensions":["x"]}`},
		{"no name", `{"schema":1}`},
		{"no extensions", `{"schema":1,"id":"x","name":"X"}`},
		{"id with a slash", `{"schema":1,"id":"../x","name":"X","extensions":["x"]}`},
		{"unknown class", `{"schema":1,"id":"x","extensions":["x"],"line_rules":[{"pattern":"^#","class":"rainbow"}]}`},
		{"bad pattern", `{"schema":1,"id":"x","extensions":["x"],"line_rules":[{"pattern":"(","class":"keyword"}]}`},
		{"half a block comment", `{"schema":1,"id":"x","extensions":["x"],"block_comment":[{"start":"/*"}]}`},
		{"multi-rune escape", `{"schema":1,"id":"x","extensions":["x"],"strings":[{"start":"\"","end":"\"","escape":"\\\\"}]}`},
	}
	for _, c := range cases {
		if _, err := parseGrammar([]byte(c.data)); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	// A definition bigger than any real one is refused rather than read.
	big := make([]byte, maxGrammarBytes+1)
	copy(big, good)
	if _, err := parseGrammar(big); err == nil {
		t.Error("an oversized definition was accepted")
	}
}

func TestLexGo(t *testing.T) {
	g := grammarByID(t, "go")
	got := lex(t, g, "func main() {}\n\tx := 42 // a note\n\ts := \"a\\\"b\"\n")
	wantLine(t, got[0], "1:func")
	wantLine(t, got[1], "5:42", "6:// a note")
	wantLine(t, got[2], `4:"a\"b"`)
}

// A block comment keeps its style across lines, and the lines after it go back to
// normal.
func TestLexBlockCommentState(t *testing.T) {
	g := grammarByID(t, "go")
	got := lex(t, g, "a := 1 /* one\nstill a comment\ndone */ b := 2\nc := 3\n")
	wantLine(t, got[0], "5:1", "6:/* one")
	wantLine(t, got[1], "6:still a comment")
	wantLine(t, got[2], "6:done */", "5:2")
	wantLine(t, got[3], "5:3")
}

// A raw string in Go spans lines; an ordinary string does not.
func TestLexMultilineString(t *testing.T) {
	g := grammarByID(t, "go")
	got := lex(t, g, "s := `first\nsecond`\nt := \"unterminated\nu := 1\n")
	wantLine(t, got[0], "4:`first")
	wantLine(t, got[1], "4:second`")
	// An ordinary string cannot span lines: it is coloured to the end of the
	// line and the next line starts outside it.
	wantLine(t, got[2], "4:\"unterminated")
	wantLine(t, got[3], "5:1")
}

func TestLexPythonTripleQuote(t *testing.T) {
	g := grammarByID(t, "python")
	got := lex(t, g, "def f():\n    \"\"\"doc\n    string\"\"\"\n    return 1\n")
	wantLine(t, got[0], "1:def")
	wantLine(t, got[1], "4:\"\"\"doc")
	wantLine(t, got[2], "4:    string\"\"\"")
	wantLine(t, got[3], "1:return", "5:1")
}

// Line rules cover what a word list cannot: Markdown headings, YAML keys, C
// preprocessor lines.
func TestLexLineRules(t *testing.T) {
	md := grammarByID(t, "markdown")
	got := lex(t, md, "# Title\nplain text\n```go\n")
	wantLine(t, got[0], "1:# Title")
	wantLine(t, got[1])

	yaml := grammarByID(t, "yaml")
	got = lex(t, yaml, "name: gaze\n- item\n")
	wantLine(t, got[0], "2:name:")

	c := grammarByID(t, "c")
	got = lex(t, c, "#include <stdio.h>\nint main(void) {}\n")
	wantLine(t, got[0], "1:#include")
	wantLine(t, got[1], "2:int", "2:void")
}

func TestLexJSON(t *testing.T) {
	g := grammarByID(t, "json")
	got := lex(t, g, "{\"a\": [1, 2.5, true, null]}\n")
	wantLine(t, got[0], `4:"a"`, "5:1", "5:2.5", "1:true", "1:null")
}

// Keywords have to be whole words: "iffy" is not "if".
func TestLexWholeWords(t *testing.T) {
	g := grammarByID(t, "go")
	got := lex(t, g, "iffy := iffyFunc(ifelse)\n")
	wantLine(t, got[0])
}

// The state array is what makes highlighting cheap; it has to be filled forwards
// and thrown away from an edit onwards.
func TestHighlighterStatesAndInvalidation(t *testing.T) {
	g := grammarByID(t, "go")
	doc := NewDocument("a /* c\nb\nc */ d\ne\n")
	h := newHighlighter(g, doc)
	if h == nil {
		t.Fatal("no highlighter for a small document")
	}
	if got := h.stateAt(doc, 3); got != 0 {
		t.Fatalf("state at line 3 = %d, want 0", got)
	}
	if got := h.stateAt(doc, 1); got != 1 {
		t.Fatalf("state at line 1 = %d, want inside the block comment", got)
	}
	if got := h.stateAt(doc, 2); got != 1 {
		t.Fatalf("state at line 2 = %d, want inside the block comment", got)
	}
	// Editing the first line changes where the comment ends, so everything from
	// there has to be worked out again.
	doc.ReplaceRange(2, 4, "")
	h.dropFrom(0)
	if got := h.stateAt(doc, 2); got != 0 {
		t.Fatalf("state at line 2 = %d after closing the comment, want 0", got)
	}
}

// A document too large for exact state is only highlighted when its language does
// not need any.
func TestHighlightPolicy(t *testing.T) {
	goG := grammarByID(t, "go")
	jsonG := grammarByID(t, "json")

	huge := NewDocument(strings.Repeat("x\n", maxSyntaxLines+10))
	if h := newHighlighter(goG, huge); h != nil {
		t.Fatal("a document past the state limit was highlighted with a stateful language")
	}
	if h := newHighlighter(jsonG, huge); h == nil {
		t.Fatal("a stateless language was refused on a large document")
	}
	if h := newHighlighter(nil, huge); h != nil {
		t.Fatal("a nil language produced a highlighter")
	}
}

// The view draws a row as runs of cells, expanding tabs, and never more than the
// cap of runs.
func TestRowSegments(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "\tif x { /* c */ }\n")
	// The tab width comes from the file, so set it after the buffer is installed.
	e.indentUseTabs = true
	e.indentWidth = 4
	e.lang = grammarByID(t, "go")
	e.applySyntax()

	segs, start := e.code.rowSegments(0, 0, 100)
	if start != 0 {
		t.Fatalf("row starts at cell %d, want 0", start)
	}
	var joined strings.Builder
	for _, sg := range segs {
		joined.WriteString(sg.text)
	}
	if got := joined.String(); got != "    if x { /* c */ }" {
		t.Fatalf("row = %q", got)
	}
	if len(segs) > maxRowSegments {
		t.Fatalf("%d segments, cap is %d", len(segs), maxRowSegments)
	}
	// "if" must be a keyword run, and the comment a comment run.
	var keyword, comment bool
	for _, sg := range segs {
		if sg.class == styleKeyword && strings.Contains(sg.text, "if") {
			keyword = true
		}
		if sg.class == styleComment && strings.Contains(sg.text, "/* c */") {
			comment = true
		}
	}
	if !keyword || !comment {
		t.Fatalf("segments = %v", segs)
	}

	// A crowded line is capped, and what is drawn is still the whole row.
	crowded := strings.Repeat("a \"s\" 1 ", 40) + "\n"
	setBuffer(e, crowded)
	e.applySyntax()
	segs, _ = e.code.rowSegments(0, 0, 1000)
	if len(segs) > maxRowSegments {
		t.Fatalf("%d segments for a crowded line, cap is %d", len(segs), maxRowSegments)
	}
	joined.Reset()
	for _, sg := range segs {
		joined.WriteString(sg.text)
	}
	if got := joined.String(); got != strings.TrimSuffix(crowded, "\n") {
		t.Fatalf("crowded row = %d characters, want %d", len(got), len(crowded)-1)
	}
}

// Non-ASCII text keeps one cell per rune, so a segment's cell offset is its rune
// offset in the row.
func TestRowSegmentsUnicode(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "α := \"βγ\" // δ\n")
	e.lang = grammarByID(t, "go")
	e.applySyntax()
	segs, _ := e.code.rowSegments(0, 0, 100)
	joined := ""
	for _, sg := range segs {
		joined += sg.text
	}
	if joined != "α := \"βγ\" // δ" {
		t.Fatalf("row = %q", joined)
	}
	for _, sg := range segs {
		if sg.class == styleComment && sg.text != "// δ" {
			t.Fatalf("comment run = %q", sg.text)
		}
	}
}

// Opening a file picks its language from the name, and the setting turns the
// colour off without touching the text.
func TestSyntaxFollowsFile(t *testing.T) {
	e := newTestEditor(t)
	path := writeTestFile(t, "sample.go", "func main() {\n\tx := 1 // note\n}\n")
	e.startOpen(path, openNormal, nil)
	waitFor(t, "the open to finish", func() bool { return e.currentFile == path })

	if e.lang == nil || e.lang.id != "go" {
		t.Fatalf("language = %v, want go", e.lang)
	}
	if e.code.hl == nil {
		t.Fatal("the document is not highlighted")
	}
	if !strings.Contains(e.info.Text, "Go") {
		t.Fatalf("readout = %q, want it to name the language", e.info.Text)
	}
	if !strings.Contains(e.status.Text, "(Go)") {
		t.Fatalf("status = %q, want the language in it", e.status.Text)
	}

	e.ToggleSyntax()
	if e.syntaxOn || e.code.hl != nil {
		t.Fatal("highlighting stayed on after toggling it off")
	}
	if strings.Contains(e.info.Text, "Go") {
		t.Fatalf("readout = %q, want no language once it is off", e.info.Text)
	}
	if got, _ := e.code.rowSegments(1, 0, 80); len(got) != 1 {
		t.Fatalf("a row was still split into %d runs with highlighting off", len(got))
	}
	e.ToggleSyntax()
	if !e.syntaxOn || e.code.hl == nil {
		t.Fatal("highlighting did not come back")
	}

	// A file nothing matches has no language, and still draws.
	plain := writeTestFile(t, "notes.txt", "just words\n")
	e.startOpen(plain, openNormal, nil)
	waitFor(t, "the second open to finish", func() bool { return e.currentFile == plain })
	if e.lang != nil || e.code.hl != nil {
		t.Fatalf("language = %v for a .txt file, want none", e.lang)
	}
	if strings.Contains(e.status.Text, "(") && strings.Contains(e.status.Text, "Go)") {
		t.Fatalf("status = %q, want no language", e.status.Text)
	}
}

// A definition installed by the user replaces the built-in one with the same id,
// so a grammar can be fixed without a release.
func TestUserGrammarOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	old := languageDir
	languageDir = func() string { return dir }
	t.Cleanup(func() { languageDir = old })

	builtin := grammarByID(t, "json")
	e := newTestEditor(t)
	e.forgetLanguages()
	if got := grammarFor("a.json", e.languages()); got == nil || !got.builtin {
		t.Fatalf("json did not resolve to the built-in definition: %v", got)
	}

	custom := `{"schema":1,"id":"json","name":"JSON (mine)","extensions":["json"],"keywords":["yep"]}`
	if err := e.installLanguageFile(writeTestFile(t, "json.json", custom)); err != nil {
		t.Fatalf("install: %v", err)
	}
	e.forgetLanguages()
	g := grammarFor("a.json", e.languages())
	if g == nil || g.builtin || g.name != "JSON (mine)" {
		t.Fatalf("resolved to %+v, want the installed definition", g)
	}
	if g.id != builtin.id {
		t.Fatalf("id = %q", g.id)
	}
}

// Typing the start of a comment on one line has to recolour the lines below it.
func TestEditingUpdatesHighlighting(t *testing.T) {
	e := newTestEditor(t)
	setBuffer(e, "a := 1\nb := 2\nc := 3\nd := 4\n")
	e.lang = grammarByID(t, "go")
	e.applySyntax()

	classes := func(row int) []styleClass {
		segs, _ := e.code.rowSegments(row, 0, 80)
		var out []styleClass
		for _, sg := range segs {
			out = append(out, sg.class)
		}
		return out
	}
	if got := classes(1); len(got) == 0 || got[0] == styleComment {
		t.Fatalf("line 1 is already a comment: %v", got)
	}

	// Open a block comment on line 0; the lines below become part of it.
	e.code.setCaret(pos{row: 0, col: 0})
	e.code.insertText("/* ")
	for _, row := range []int{1, 2, 3} {
		if got := classes(row); len(got) == 0 || got[0] != styleComment {
			t.Fatalf("line %d is not part of the comment: %v", row, got)
		}
	}

	// Close it on line 2: line 3 has to go back to normal, and line 1 stays part
	// of the comment, which now ends above it.
	e.code.setCaret(pos{row: 2, col: 7})
	e.code.insertText(" */")
	if got := classes(1); len(got) == 0 || got[0] != styleComment {
		t.Fatalf("line 1 should still be a comment: %v", got)
	}
	if got := classes(3); len(got) == 0 || got[0] == styleComment {
		t.Fatalf("line 3 is still a comment after closing it: %v", got)
	}
}
