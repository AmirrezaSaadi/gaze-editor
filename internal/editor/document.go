package editor

import (
	"io"
	"sort"
	"strings"
	"unicode"
)

// docPageSize is the target size of a document page. Pages are the unit of a
// local edit: replacing text re-chunks only the pages the edit touches, so typing
// costs work proportional to the page, not to the file.
const docPageSize = 64 << 10

// Document is the storage for a large-file buffer.
//
// widget.Entry can only be handed a string, so a document that is too big for the
// entry is kept here as a list of pages plus a line index, and the editor shows
// one window of it at a time. Small files never use this: for them the entry
// itself is the document, exactly as before.
//
// The line index stores the byte offset of every line start, so line lookup is a
// binary search and an edit only has to shift the offsets after it. That is
// O(lines after the edit) integer adds per keystroke — cheap next to the ~1 KB
// per line Fyne spends laying the same text out.
type Document struct {
	pages  []string // contiguous byte ranges, each at most docPageSize (last may be shorter)
	starts []int    // byte offset of each page in document space
	lines  []int    // byte offset of each line start in document space
	size   int
}

// NewDocument builds a document over text. The pages are substrings of text, so
// nothing is copied.
func NewDocument(text string) *Document {
	d := &Document{size: len(text)}
	d.pages, d.starts = chunkPages(text)
	d.rebuildLines()
	return d
}

// chunkPages splits text into pages and records where each one starts.
func chunkPages(text string) (pages []string, starts []int) {
	for off := 0; off < len(text); off += docPageSize {
		end := off + docPageSize
		if end > len(text) {
			end = len(text)
		}
		pages = append(pages, text[off:end])
		starts = append(starts, off)
	}
	if len(pages) == 0 {
		return []string{""}, []int{0}
	}
	return pages, starts
}

// rebuildLines recomputes the whole line index. It is used on load and after a
// whole-document replacement; ordinary edits update the index incrementally.
func (d *Document) rebuildLines() {
	d.lines = d.lines[:0]
	d.lines = append(d.lines, 0)
	base := 0
	for _, p := range d.pages {
		for i := 0; i < len(p); i++ {
			if p[i] == '\n' {
				d.lines = append(d.lines, base+i+1)
			}
		}
		base += len(p)
	}
}

// rebuildStarts recomputes page offsets, which is O(pages) and therefore cheap.
func (d *Document) rebuildStarts() {
	d.starts = d.starts[:0]
	off := 0
	for _, p := range d.pages {
		d.starts = append(d.starts, off)
		off += len(p)
	}
}

// Len reports the document size in bytes.
func (d *Document) Len() int { return d.size }

// LineCount reports the number of logical lines.
func (d *Document) LineCount() int { return len(d.lines) }

// LineStart returns the byte offset where line i starts (0-based).
func (d *Document) LineStart(i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(d.lines) {
		return d.size
	}
	return d.lines[i]
}

// LineText returns line i without its newline.
func (d *Document) LineText(i int) string {
	if i < 0 || i >= len(d.lines) {
		return ""
	}
	return strings.TrimSuffix(d.slice(d.lines[i], d.LineStart(i+1)), "\n")
}

// LineOf returns the 0-based line containing a byte offset.
func (d *Document) LineOf(off int) int {
	return sort.SearchInts(d.lines, off+1) - 1
}

// RowColOf converts a byte offset to a 0-based line and rune column.
func (d *Document) RowColOf(off int) (row, col int) {
	if off > d.size {
		off = d.size
	}
	row = d.LineOf(off)
	if row < 0 {
		row = 0
	}
	return row, utf8Count(d.slice(d.LineStart(row), off))
}

// pageOf returns the index of the page containing off, clamping to the last page.
func (d *Document) pageOf(off int) int {
	if off >= d.size {
		return len(d.pages) - 1
	}
	if off < 0 {
		return 0
	}
	i := sort.SearchInts(d.starts, off+1) - 1
	if i < 0 {
		return 0
	}
	return i
}

// slice returns the text between two byte offsets.
func (d *Document) slice(from, to int) string {
	if from < 0 {
		from = 0
	}
	if to > d.size {
		to = d.size
	}
	if from >= to {
		return ""
	}
	pi, pj := d.pageOf(from), d.pageOf(to-1)
	if pi == pj {
		return d.pages[pi][from-d.starts[pi] : to-d.starts[pi]]
	}
	var b strings.Builder
	b.Grow(to - from)
	b.WriteString(d.pages[pi][from-d.starts[pi]:])
	for i := pi + 1; i < pj; i++ {
		b.WriteString(d.pages[i])
	}
	b.WriteString(d.pages[pj][:to-d.starts[pj]])
	return b.String()
}

// TextRange returns the text between two byte offsets.
func (d *Document) TextRange(from, to int) string { return d.slice(from, to) }

// Window returns up to count lines starting at startLine, and how many it got.
// The returned text ends with a newline unless it reaches the end of the
// document, matching how widget.Entry represents a buffer.
func (d *Document) Window(startLine, count int) (string, int) {
	if startLine < 0 {
		startLine = 0
	}
	if startLine >= len(d.lines) {
		startLine = len(d.lines) - 1
	}
	end := startLine + count
	if end > len(d.lines) {
		end = len(d.lines)
	}
	if end < startLine {
		end = startLine
	}
	to := d.size
	if end < len(d.lines) {
		to = d.lines[end]
	}
	return d.slice(d.lines[startLine], to), end - startLine
}

// ReplaceRange replaces the bytes in [from, to) with repl and keeps the page and
// line indexes in step.
func (d *Document) ReplaceRange(from, to int, repl string) {
	if from < 0 {
		from = 0
	}
	if to > d.size {
		to = d.size
	}
	if to < from {
		to = from
	}
	if from == to && repl == "" {
		return
	}

	pi, pj := d.pageOf(from), d.pageOf(to)
	head := d.pages[pi][:from-d.starts[pi]]
	tail := d.pages[pj][to-d.starts[pj]:]
	middle := head + repl + tail

	pages := make([]string, 0, len(d.pages)+1)
	pages = append(pages, d.pages[:pi]...)
	midPages, _ := chunkPages(middle)
	pages = append(pages, midPages...)
	pages = append(pages, d.pages[pj+1:]...)
	d.pages = pages

	d.size += len(repl) - (to - from)
	d.rebuildStarts()
	d.updateLines(from, to, repl)
}

// updateLines fixes the line index for a replacement of [from, to) by repl.
//
// The index is spliced in place: line starts before the edit are untouched, the
// ones the replaced text had are dropped, the newlines in repl become new starts
// and the starts after the edit shift by the size difference. Typing therefore
// costs one integer add per line below the caret rather than a copy of the whole
// index, which is what keeps a keystroke in a million-line file cheap.
func (d *Document) updateLines(from, to int, repl string) {
	delta := len(repl) - (to - from)
	keep := sort.SearchInts(d.lines, from+1) // line starts at or before from
	drop := sort.SearchInts(d.lines, to+1)   // line starts at or before to

	var added [16]int
	newStarts := added[:0]
	for i := 0; i < len(repl); i++ {
		if repl[i] == '\n' {
			newStarts = append(newStarts, from+i+1)
		}
	}

	oldLen := len(d.lines)
	grow := len(newStarts) - (drop - keep)
	if grow > 0 {
		d.lines = append(d.lines, make([]int, grow)...)
	}
	tailStart := keep + len(newStarts)
	tailLen := oldLen - drop
	if tailStart != drop {
		copy(d.lines[tailStart:tailStart+tailLen], d.lines[drop:oldLen])
	}
	for i := tailStart; i < tailStart+tailLen; i++ {
		d.lines[i] += delta
	}
	copy(d.lines[keep:], newStarts)
	d.lines = d.lines[:tailStart+tailLen]
}

// ReplaceAll swaps the whole document, for operations that rewrite everything.
func (d *Document) ReplaceAll(text string) {
	*d = *NewDocument(text)
}

// Text materialises the whole document. It is O(file) memory, so it is only for
// user actions that need the whole text (find and replace); the display and the
// save path never call it.
func (d *Document) Text() string {
	if len(d.pages) == 1 {
		return d.pages[0]
	}
	var b strings.Builder
	b.Grow(d.size)
	for _, p := range d.pages {
		b.WriteString(p)
	}
	return b.String()
}

// Snapshot returns a copy of the page table that stays valid while the document
// is edited. Pages are immutable once written — an edit builds a new page slice —
// so a save on a background goroutine can read a snapshot while the window keeps
// being edited.
func (d *Document) Snapshot() *Document {
	s := &Document{size: d.size}
	s.pages = append([]string(nil), d.pages...)
	s.starts = append([]int(nil), d.starts...)
	return s
}

// WriteTo streams the document to w page by page.
func (d *Document) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for _, p := range d.pages {
		n, err := io.WriteString(w, p)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// eachLine calls fn for every line of the document without materialising it, so
// a save can stream straight from the pages.
func (d *Document) eachLine(fn func(line string, hasNewline bool) error) error {
	carry := ""
	for _, p := range d.pages {
		s := p
		if carry != "" {
			s = carry + p
		}
		start := 0
		for {
			i := strings.IndexByte(s[start:], '\n')
			if i < 0 {
				break
			}
			if err := fn(s[start:start+i], true); err != nil {
				return err
			}
			start += i + 1
		}
		carry = s[start:]
	}
	return fn(carry, false)
}

// counts returns the character and word counts of the document, streaming the
// pages instead of building the text.
func (d *Document) counts() (chars, words int) {
	inWord := false
	_ = d.eachLine(func(line string, hasNewline bool) error {
		inWord = false // a line break separates words
		if hasNewline {
			chars++ // the line break is a character too
		}
		for _, r := range line {
			chars++
			if unicode.IsSpace(r) {
				inWord = false
			} else if !inWord {
				inWord = true
				words++
			}
		}
		return nil
	})
	return chars, words
}

// utf8Count counts the runes in s.
func utf8Count(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
