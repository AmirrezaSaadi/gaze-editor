package editor

import (
	"encoding/json"
	"fmt"
	"image/color"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"gaze/langs"
)

// Syntax highlighting.
//
// A language is a data file, never code: a JSON definition of comments, strings,
// word lists and a few line patterns (see langs/). That keeps a definition that
// came from the network safe to use — nothing in it can run, and Go's regexp is
// linear-time, so no pattern can make the editor hang.
//
// Highlighting is a pure function of a line and the lexer state it starts in, so
// it costs nothing to cache and nothing to throw away: the view re-lexes only the
// lines it draws.

// styleClass is what a run of text is, which decides its colour.
type styleClass int8

const (
	stylePlain styleClass = iota
	styleKeyword
	styleType
	styleBuiltin
	styleString
	styleNumber
	styleComment

	styleClasses
)

// classNames maps the names a definition may use to classes.
var classNames = map[string]styleClass{
	"keyword": styleKeyword,
	"type":    styleType,
	"builtin": styleBuiltin,
	"string":  styleString,
	"number":  styleNumber,
	"comment": styleComment,
}

// span is a run of one style inside a line, in runes of the line as stored.
type span struct {
	from, to int
	class    styleClass
}

// openState is a multi-line construct the lexer can be inside of at the start of
// a line: a block comment or a string that may span lines.
type openState struct {
	end    []rune
	escape rune
	class  styleClass
}

// grammar is a parsed language definition.
type grammar struct {
	id        string
	name      string
	exts      []string // extensions without the dot, lower case
	filenames []string // exact file names, for Dockerfile and friends
	line      [][]rune
	blocks    []struct{ start, end []rune }
	strings   []struct {
		start, end []rune
		escape     rune
		multiline  bool
	}
	words     map[string]styleClass
	numbers   bool
	rules     []lineRule
	states    []openState // flattened: blocks first, then multi-line strings
	multiline bool        // whether any construct can cross a line
	builtin   bool        // shipped with the editor
	path      string      // where a non-built-in definition came from
}

// lineRule colours a prefix of a line, for things a word list cannot express:
// Markdown headings, YAML keys, Makefile targets, C preprocessor lines.
type lineRule struct {
	re    *regexp.Regexp
	class styleClass
}

// needsState reports whether lexing this grammar needs the state a line starts
// in. A definition without multi-line constructs is exact for any line on its
// own, which is what lets such languages be highlighted in a huge file.
func (g *grammar) needsState() bool { return g.multiline }

// --- definition files -------------------------------------------------------

// maxGrammarBytes caps a definition read from disk or the network. The built-in
// ones are a couple of kilobytes; anything this large is not a definition.
const maxGrammarBytes = 256 << 10

// grammarSchema is the definition format this build understands.
const grammarSchema = 1

type grammarJSON struct {
	Schema       int      `json:"schema"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Extensions   []string `json:"extensions"`
	Filenames    []string `json:"filenames"`
	LineComment  []string `json:"line_comment"`
	BlockComment []struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"block_comment"`
	Strings []struct {
		Start     string `json:"start"`
		End       string `json:"end"`
		Escape    string `json:"escape"`
		Multiline bool   `json:"multiline"`
	} `json:"strings"`
	Keywords  []string `json:"keywords"`
	Types     []string `json:"types"`
	Builtins  []string `json:"builtins"`
	Numbers   bool     `json:"numbers"`
	LineRules []struct {
		Pattern string `json:"pattern"`
		Class   string `json:"class"`
	} `json:"line_rules"`
}

// parseGrammar turns a definition file into a grammar, rejecting anything it
// cannot honour. Only fields this build knows are read, so a definition written
// for a newer Gaze still works as far as it can.
func parseGrammar(data []byte) (*grammar, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("the definition is empty")
	}
	if len(data) > maxGrammarBytes {
		return nil, fmt.Errorf("the definition is larger than %s", humanBytes(maxGrammarBytes))
	}
	var doc grammarJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not a language definition: %v", err)
	}
	if doc.Schema <= 0 {
		return nil, fmt.Errorf("the definition has no schema version")
	}
	if doc.Schema > grammarSchema {
		return nil, fmt.Errorf("the definition needs a newer Gaze (format %d, this build reads %d)", doc.Schema, grammarSchema)
	}
	id := strings.TrimSpace(doc.ID)
	if id == "" {
		id = strings.TrimSpace(doc.Name)
	}
	if id == "" {
		return nil, fmt.Errorf("the definition has no id")
	}
	if strings.HasPrefix(id, ".") || strings.ContainsAny(id, `/\`) {
		return nil, fmt.Errorf("%q is not a usable id", id)
	}

	g := &grammar{
		id:      strings.ToLower(id),
		name:    strings.TrimSpace(doc.Name),
		numbers: doc.Numbers,
		words:   map[string]styleClass{},
	}
	if g.name == "" {
		g.name = id
	}
	for _, e := range doc.Extensions {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			g.exts = append(g.exts, e)
		}
	}
	for _, f := range doc.Filenames {
		if f = strings.TrimSpace(f); f != "" {
			g.filenames = append(g.filenames, f)
		}
	}
	for _, c := range doc.LineComment {
		if c = strings.TrimSpace(c); c != "" {
			g.line = append(g.line, []rune(c))
		}
	}
	for _, b := range doc.BlockComment {
		if b.Start == "" || b.End == "" {
			return nil, fmt.Errorf("a block comment needs both a start and an end")
		}
		g.blocks = append(g.blocks, struct{ start, end []rune }{[]rune(b.Start), []rune(b.End)})
	}
	for _, s := range doc.Strings {
		if s.Start == "" || s.End == "" {
			return nil, fmt.Errorf("a string needs both a start and an end")
		}
		var esc rune
		if s.Escape != "" {
			r := []rune(s.Escape)
			if len(r) != 1 {
				return nil, fmt.Errorf("%q is not a single-character escape", s.Escape)
			}
			esc = r[0]
		}
		g.strings = append(g.strings, struct {
			start, end []rune
			escape     rune
			multiline  bool
		}{[]rune(s.Start), []rune(s.End), esc, s.Multiline})
	}
	for _, w := range doc.Keywords {
		g.words[w] = styleKeyword
	}
	for _, w := range doc.Types {
		g.words[w] = styleType
	}
	for _, w := range doc.Builtins {
		g.words[w] = styleBuiltin
	}
	for _, r := range doc.LineRules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %v", r.Pattern, err)
		}
		class, ok := classNames[strings.ToLower(r.Class)]
		if !ok {
			return nil, fmt.Errorf("pattern %q uses unknown class %q", r.Pattern, r.Class)
		}
		g.rules = append(g.rules, lineRule{re: re, class: class})
	}

	// The lexer state is an index into one list: block comments first, then the
	// strings that may span lines.
	for _, b := range g.blocks {
		g.states = append(g.states, openState{end: b.end, class: styleComment})
	}
	for _, s := range g.strings {
		if s.multiline {
			g.states = append(g.states, openState{end: s.end, escape: s.escape, class: styleString})
		}
	}
	g.multiline = len(g.states) > 0

	if len(g.exts) == 0 && len(g.filenames) == 0 {
		return nil, fmt.Errorf("the definition has no extensions or file names to match")
	}
	return g, nil
}

// builtinGrammars is the definitions compiled into the binary. They are small
// JSON files (a couple of kilobytes each), embedded rather than generated, so a
// user can read exactly what Gaze will do with their file.
var (
	builtinsOnce sync.Once
	builtins     []*grammar
)

func builtinGrammars() []*grammar {
	builtinsOnce.Do(func() {
		names, err := fs.Glob(langs.Files(), "*.json")
		if err != nil {
			return
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "index.json" {
				continue
			}
			data, err := fs.ReadFile(langs.Files(), name)
			if err != nil {
				continue
			}
			g, err := parseGrammar(data)
			if err != nil {
				// A broken definition shipped in the binary is a bug, but it
				// must not take the rest of the languages down with it.
				continue
			}
			g.builtin = true
			g.path = "langs/" + name
			builtins = append(builtins, g)
		}
	})
	return builtins
}

// grammarFor picks the definition to use for path: the extension first, then the
// whole file name (Dockerfile, Makefile), ignoring case.
func grammarFor(path string, all []*grammar) *grammar {
	if path == "" {
		return nil
	}
	base := filepath.Base(path)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(base), "."))
	for _, g := range all {
		for _, e := range g.exts {
			if e == ext {
				return g
			}
		}
	}
	for _, g := range all {
		for _, f := range g.filenames {
			if strings.EqualFold(f, base) {
				return g
			}
		}
	}
	return nil
}

// --- the lexer --------------------------------------------------------------

// hasAt reports whether line has delimiter d at index i.
func hasAt(line []rune, i int, d []rune) bool {
	if i < 0 || i+len(d) > len(line) {
		return false
	}
	for k, r := range d {
		if line[i+k] != r {
			return false
		}
	}
	return true
}

// scanFrom returns the index just past the closing delimiter, or len(line) when
// the construct does not close on this line. An escaped closing delimiter is
// skipped when escape is set.
func scanFrom(line []rune, i int, end []rune, escape rune) (int, bool) {
	for ; i < len(line); i++ {
		if escape != 0 && line[i] == escape {
			i++ // skip the escaped rune
			continue
		}
		if hasAt(line, i, end) {
			return i + len(end), true
		}
	}
	return len(line), false
}

// addSpan appends a run, merging it with the previous one when the style is the
// same: a lexer that emits one span per token would otherwise make the renderer
// draw a dozen text objects for a line of plain punctuation.
func addSpan(spans []span, from, to int, class styleClass) []span {
	if to <= from {
		return spans
	}
	if n := len(spans); n > 0 && spans[n-1].class == class && spans[n-1].to == from {
		spans[n-1].to = to
		return spans
	}
	return append(spans, span{from: from, to: to, class: class})
}

// isWordStart reports whether r may begin an identifier.
func isWordStart(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7F && r != '’'
}

// isWordRune reports whether r may continue an identifier.
func isWordRune(r rune) bool {
	return isWordStart(r) || r >= '0' && r <= '9'
}

// isDigit reports whether r is an ASCII digit.
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// lexLine styles one line, given the state it starts in, and reports the state
// the next line starts in.
func (g *grammar) lexLine(full []rune, state int) ([]span, int) {
	spans := make([]span, 0, 8)
	line, base := full, 0
	add := func(from, to int, c styleClass) {
		spans = addSpan(spans, base+from, base+to, c)
	}
	skip := func(n int) {
		line = line[n:]
		base += n
	}

	// Finish a construct that started on an earlier line.
	for state > 0 {
		st := g.states[state-1]
		end, closed := scanFrom(line, 0, st.end, st.escape)
		add(0, end, st.class)
		if !closed {
			return spans, state
		}
		skip(end)
		state = 0
	}

	// Line rules colour a prefix and leave the rest to the ordinary lexer.
	if len(g.rules) > 0 && len(line) > 0 {
		text := string(line)
		for _, r := range g.rules {
			if loc := r.re.FindStringIndex(text); loc != nil && loc[1] > 0 {
				add(0, loc[1], r.class)
				skip(loc[1])
				break
			}
		}
	}

	for i := 0; i < len(line); {
		if len(g.line) > 0 {
			hit := false
			for _, lc := range g.line {
				if hasAt(line, i, lc) {
					add(i, len(line), styleComment)
					hit = true
					break
				}
			}
			if hit {
				return spans, 0
			}
		}
		if len(g.blocks) > 0 {
			hit := false
			for k, b := range g.blocks {
				if !hasAt(line, i, b.start) {
					continue
				}
				end, closed := scanFrom(line, i+len(b.start), b.end, 0)
				add(i, end, styleComment)
				i = end
				if !closed {
					return spans, k + 1
				}
				hit = true
				break
			}
			if hit {
				continue
			}
		}
		if len(g.strings) > 0 {
			hit := false
			for k, sr := range g.strings {
				if !hasAt(line, i, sr.start) {
					continue
				}
				end, closed := scanFrom(line, i+len(sr.start), sr.end, sr.escape)
				add(i, end, styleString)
				i = end
				if !closed && sr.multiline {
					// The state number is a position in the flattened list: the
					// block comments come first, then the multi-line strings.
					return spans, len(g.blocks) + multilineIndex(g, k) + 1
				}
				hit = true
				break
			}
			if hit {
				continue
			}
		}
		r := line[i]
		switch {
		case isDigit(r) && g.numbers:
			j := i + 1
			for j < len(line) && (isWordRune(line[j]) || line[j] == '.') {
				j++
			}
			add(i, j, styleNumber)
			i = j
		case isWordStart(r):
			j := i + 1
			for j < len(line) && isWordRune(line[j]) {
				j++
			}
			if class, ok := g.words[string(line[i:j])]; ok {
				add(i, j, class)
			}
			i = j
		default:
			i++
		}
	}
	return spans, 0
}

// multilineIndex is the position of string rule k among the multi-line strings.
func multilineIndex(g *grammar, k int) int {
	n := 0
	for i := 0; i < k; i++ {
		if g.strings[i].multiline {
			n++
		}
	}
	return n
}

// --- palette ----------------------------------------------------------------

// syntaxPalette is the colour each class is drawn in. The values are the token
// colours used by GitHub's own themes, which are known to read well on the light
// and dark backgrounds Gaze uses.
func syntaxPalette(v fyne.ThemeVariant) [styleClasses]color.NRGBA {
	var p [styleClasses]color.NRGBA
	if v == theme.VariantLight {
		p[stylePlain] = color.NRGBA{R: 0x1f, G: 0x23, B: 0x28, A: 0xff}
		p[styleKeyword] = color.NRGBA{R: 0xcf, G: 0x22, B: 0x2e, A: 0xff}
		p[styleType] = color.NRGBA{R: 0x95, G: 0x38, B: 0x00, A: 0xff}
		p[styleBuiltin] = color.NRGBA{R: 0x82, G: 0x50, B: 0xdf, A: 0xff}
		p[styleString] = color.NRGBA{R: 0x0a, G: 0x30, B: 0x69, A: 0xff}
		p[styleNumber] = color.NRGBA{R: 0x05, G: 0x50, B: 0xae, A: 0xff}
		p[styleComment] = color.NRGBA{R: 0x6e, G: 0x77, B: 0x81, A: 0xff}
		return p
	}
	p[stylePlain] = color.NRGBA{R: 0xe6, G: 0xed, B: 0xf3, A: 0xff}
	p[styleKeyword] = color.NRGBA{R: 0xff, G: 0x7b, B: 0x72, A: 0xff}
	p[styleType] = color.NRGBA{R: 0xff, G: 0xa6, B: 0x57, A: 0xff}
	p[styleBuiltin] = color.NRGBA{R: 0xd2, G: 0xa8, B: 0xff, A: 0xff}
	p[styleString] = color.NRGBA{R: 0xa5, G: 0xd6, B: 0xff, A: 0xff}
	p[styleNumber] = color.NRGBA{R: 0x79, G: 0xc0, B: 0xff, A: 0xff}
	p[styleComment] = color.NRGBA{R: 0x8b, G: 0x94, B: 0x9e, A: 0xff}
	return p
}

// --- the highlighter --------------------------------------------------------

// styledLine is a line together with its styles and the state the next line
// starts in.
type styledLine struct {
	text  string
	spans []span
	next  int
}

// highlighter keeps the lexer state of the document and the lines on screen.
//
// The state of a line is only known by lexing everything before it, so it is
// kept in an array that is filled forwards. Typing invalidates the array from the
// edited line on, and only the lines that are actually drawn are kept, which for
// a file of a hundred thousand lines is a few hundred kilobytes in the worst case
// and nothing at all for a file that cannot afford it.
type highlighter struct {
	g      *grammar
	states []int32 // state each line starts in, -1 when not worked out yet
	valid  int     // states[:valid] are known
	lines  map[int]*styledLine
}

// maxSyntaxLines is the largest document that is highlighted exactly. Beyond it
// the state array would cost megabytes and a jump could mean lexing tens of
// thousands of lines before the screen can be drawn.
const maxSyntaxLines = 100_000

// newHighlighter prepares the lexer state for doc, or returns nil when the
// definition cannot be used on a document that size.
func newHighlighter(g *grammar, doc *Document) *highlighter {
	if g == nil || doc == nil {
		return nil
	}
	h := &highlighter{g: g, lines: map[int]*styledLine{}}
	if !g.needsState() {
		return h // a line is exact on its own
	}
	n := doc.LineCount()
	if n > maxSyntaxLines {
		return nil
	}
	h.states = make([]int32, n+1)
	for i := range h.states {
		h.states[i] = -1
	}
	h.states[0] = 0
	h.valid = 1
	return h
}

// setGrammar replaces the language the document is highlighted with, keeping the
// view where it is. nil turns highlighting off for this document.
func (v *codeView) setGrammar(g *grammar) {
	if v.doc == nil {
		v.hl = nil
	} else if g == nil {
		v.hl = nil
	} else {
		v.hl = newHighlighter(g, v.doc)
	}
	v.segDrop()
	v.render()
}

// dropFrom forgets everything from line on: an edit there may have changed the
// state of every line below it.
func (h *highlighter) dropFrom(line int) {
	if h == nil {
		return
	}
	if line < 0 {
		line = 0
	}
	// The line itself changed, so its cached styles are gone too.
	for n := range h.lines {
		if n >= line {
			delete(h.lines, n)
		}
	}
	if h.states == nil {
		return
	}
	// Line 0 always starts in state 0; it is line 1 whose state an edit on line 0
	// can change.
	first := line
	if first < 1 {
		first = 1
	}
	if first < h.valid {
		h.valid = first
	}
}

// stateAt is the lexer state line starts in, filling the array forwards when it
// is not known yet.
func (h *highlighter) stateAt(doc *Document, line int) int {
	if h.states == nil || line < 0 {
		return 0
	}
	if line >= len(h.states) {
		line = len(h.states) - 1
	}
	if line == 0 {
		return 0 // a document always starts outside every construct
	}
	for n := h.valid; n <= line; n++ {
		prev := h.lineAt(doc, n-1, false)
		h.states[n] = int32(prev.next)
		if n == h.valid {
			h.valid = n + 1
		}
	}
	return int(h.states[line])
}

// lineAt returns the styled line, lexing it when it is not cached.
func (h *highlighter) lineAt(doc *Document, line int, keep bool) *styledLine {
	if h == nil || doc == nil {
		return &styledLine{}
	}
	if sl, ok := h.lines[line]; ok {
		return sl
	}
	text := doc.LineText(line)
	spans, next := h.g.lexLine([]rune(text), h.stateAt(doc, line))
	sl := &styledLine{text: text, spans: spans, next: next}
	if keep {
		if len(h.lines) > 512 {
			// The visible window is a few dozen lines; a map this large means the
			// user has scrolled far, and re-lexing what is on screen is cheap.
			h.lines = map[int]*styledLine{}
		}
		h.lines[line] = sl
	}
	return sl
}

// styled returns the styles for line, cached for the lines being drawn.
func (h *highlighter) styled(doc *Document, line int) *styledLine {
	return h.lineAt(doc, line, true)
}

// --- the editor side --------------------------------------------------------

// languageOf reports the language the current document is highlighted with, or
// nil when there is none.
func (e *Editor) languageOf() *grammar { return e.lang }

// detectLanguage picks a definition for the current file name.
func (e *Editor) detectLanguage() *grammar {
	if e.currentFile == "" {
		return nil
	}
	return grammarFor(e.currentFile, e.languages())
}

// applySyntax decides which language the document is drawn with and hands it to
// the view. It runs whenever the document, the file name or the setting changes.
func (e *Editor) applySyntax() {
	if e.code == nil {
		return
	}
	var g *grammar
	if e.syntaxOn {
		g = e.lang
		if g == nil {
			g = e.detectLanguage()
		}
	}
	e.code.setGrammar(g)
	e.updateInfo()
}

// ToggleSyntax turns highlighting on or off for everything, and remembers it.
func (e *Editor) ToggleSyntax() {
	e.syntaxOn = !e.syntaxOn
	e.app.Preferences().SetBool(prefSyntax, e.syntaxOn)
	if e.syntaxItem != nil {
		e.syntaxItem.Checked = e.syntaxOn
		if menu := e.win.MainMenu(); menu != nil {
			menu.Refresh()
		}
	}
	e.applySyntax()
	if !e.syntaxOn {
		e.setStatus("syntax highlighting off")
		return
	}
	if e.lang == nil {
		e.setStatus("syntax highlighting on — no language matches this file")
		return
	}
	e.setStatus("syntax highlighting on — " + e.lang.name)
}

// UseLanguage highlights the current document as g, keeping the choice until
// another file is opened. A nil g goes back to matching by file name.
func (e *Editor) UseLanguage(g *grammar) {
	if g == nil {
		e.lang = e.detectLanguage()
	} else {
		e.lang = g
	}
	e.applySyntax()
	switch {
	case e.lang == nil:
		e.setStatus("highlighting off for this file")
	case e.code != nil && e.code.hl == nil:
		e.setStatus("highlighting as " + e.lang.name + " — this file is too large to colour")
	default:
		e.setStatus("highlighting as " + e.lang.name)
	}
}

// rebuildLanguageMenu fills the Language submenu with what is installed.
func (e *Editor) rebuildLanguageMenu() {
	if e.langMenu == nil {
		return
	}
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Match by File Name", func() { e.UseLanguage(nil) }),
		fyne.NewMenuItemSeparator(),
	}
	for _, g := range e.languages() {
		g := g
		label := g.name
		if !g.builtin {
			label += " (installed)"
		}
		items = append(items, fyne.NewMenuItem(label, func() { e.UseLanguage(g) }))
	}
	e.langMenu.Items = items
	e.langMenu.Refresh()
}

// syntaxLabel is the language shown in the status readout, or "" when the
// document is not actually coloured: claiming a language the view is not using
// would be a lie about what is on screen.
func (e *Editor) syntaxLabel() string {
	if !e.syntaxOn || e.lang == nil || e.code == nil || e.code.hl == nil {
		return ""
	}
	return e.lang.name
}

// languageSuffix names the language in the "opened …" status message, so it is
// obvious why the file is coloured.
func (e *Editor) languageSuffix() string {
	if l := e.syntaxLabel(); l != "" {
		return " (" + l + ")"
	}
	return ""
}

// shortPath names a file for a status message: the whole path when it is short
// enough to read, otherwise just the name, since the title bar already has it.
func shortPath(path string) string {
	const max = 44
	if len(path) <= max {
		return path
	}
	return filepath.Base(path)
}
