package editor

import (
	"bytes"
	"io"
	"sort"
	"strings"
	"unicode"
	"unsafe"
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
// The line index stores the byte offset of every line start in blocks, so line
// lookup is a search over blocks and an edit only has to shift the block bases
// after it. That keeps a keystroke in a multi-million-line document cheap instead
// of costing one integer add per line in the file.
type Document struct {
	pages  []string   // contiguous byte ranges, each at most docPageSize (last may be shorter)
	starts []int      // byte offset of each page in document space
	lines  *lineIndex // byte offset of each line start in document space
	size   int

	// region is the memory mapping the pages are views of, when the document was
	// opened from a mapped file. The pages of a mapped document point into it
	// instead of holding copies, which is what makes a multi-gigabyte file cheap
	// to open: only the pages actually read are in memory, and the kernel brings
	// them back from the page cache rather than the heap.
	//
	// The mapping is never released. Undo history, snapshots and in-flight saves
	// hold substrings of these pages, and Go gives no way to learn when the last
	// of them is dropped, so unmapping would risk reading freed memory. Address
	// space is what it costs, which is free on a 64-bit machine.
	region []byte
}

// mapped reports whether the document's pages point into a file mapping.
func (d *Document) mapped() bool { return d != nil && d.region != nil }

// pageView is a string over b without copying. It is valid for as long as the
// mapping it comes from is alive, which for a document is the whole process.
func pageView(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// indexMapped builds a document over a mapped file. The bytes are already text
// with LF line endings — mappedFile checked that — so nothing is copied or
// rewritten: the pages are views of the mapping and the line index is built by
// walking it.
func indexMapped(region []byte) *Document {
	d := &Document{region: region, size: len(region)}
	var lines lineIndexBuilder
	lines.add(0)
	for off := 0; ; {
		k := bytes.IndexByte(region[off:], '\n')
		if k < 0 {
			break
		}
		off += k + 1
		lines.add(off)
	}
	d.lines = lines.finish()

	n := (len(region) + docPageSize - 1) / docPageSize
	if n == 0 {
		d.pages, d.starts = []string{""}, []int{0}
		return d
	}
	d.pages = make([]string, n)
	d.starts = make([]int, n)
	for i := 0; i < n; i++ {
		off := i * docPageSize
		end := off + docPageSize
		if end > len(region) {
			end = len(region)
		}
		d.pages[i] = pageView(region[off:end])
		d.starts[i] = off
	}
	return d
}

// NewDocument builds a document over text. The pages are substrings of text, so
// nothing is copied.
func NewDocument(text string) *Document {
	d := &Document{size: len(text)}
	d.pages, d.starts = chunkPages(text)
	d.rebuildLines()
	return d
}

// docBuilder builds a Document from a stream, so a file bigger than the heap can
// spare is never held as one string first: bytes go into page-sized buffers as
// they arrive, line endings are normalised on the way, and the line index is
// appended to as the newlines go by.
//
// The zero byte is rejected: Gaze would open a binary blob and write it back
// mangled, and counting the NULs here also lets a BOM-less UTF-16 file be named
// as such instead of called binary.
type docBuilder struct {
	pages []string
	lines lineIndexBuilder
	size  int
	page  []byte

	pendCR         bool // the last byte was \r, so a following \n is part of it
	sawCR, sawCRLF bool
	nuls, nulEven  int
}

func newDocBuilder() *docBuilder {
	b := &docBuilder{}
	b.lines.add(0)
	return b
}

// Write normalises line endings and appends the bytes to the current page. Whole
// runs without a carriage return or a NUL are copied in bulk: a per-byte loop over
// a few hundred megabytes is the difference between opening a big file in a
// moment and watching it crawl.
func (b *docBuilder) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		if b.pendCR {
			b.pendCR = false
			if p[0] == '\n' {
				b.sawCRLF = true
				p = p[1:]
				continue // the \r already ended this line
			}
		}
		i := indexSpecial(p)
		if i < 0 {
			b.appendBytes(p)
			break
		}
		if i > 0 {
			b.appendBytes(p[:i])
		}
		switch p[i] {
		case '\r':
			b.sawCR = true
			b.pendCR = true
			b.append('\n')
		case 0:
			b.nuls++
			if (b.size+len(b.page))%2 == 0 {
				b.nulEven++
			}
			b.append(0)
		}
		p = p[i+1:]
	}
	return total, nil
}

// indexSpecial is the offset of the first byte that needs normalising or
// rejecting, or -1.
func indexSpecial(p []byte) int {
	cr := bytes.IndexByte(p, '\r')
	nul := bytes.IndexByte(p, 0)
	switch {
	case cr < 0:
		return nul
	case nul < 0 || cr < nul:
		return cr
	default:
		return nul
	}
}

// appendBytes copies a run of ordinary bytes into the page, recording the line
// starts it contains.
func (b *docBuilder) appendBytes(p []byte) {
	for len(p) > 0 {
		n := docPageSize - len(b.page)
		if n > len(p) {
			n = len(p)
		}
		base := len(b.page)
		b.page = append(b.page, p[:n]...)
		p = p[n:]
		seg := b.page[base:]
		for off := 0; ; {
			k := bytes.IndexByte(seg[off:], '\n')
			if k < 0 {
				break
			}
			b.lines.add(b.size + base + off + k + 1)
			off += k + 1
		}
		if len(b.page) >= docPageSize {
			b.flush()
		}
	}
}

// append stores one byte, recording a line start and cutting a page when the
// buffer is full.
func (b *docBuilder) append(c byte) {
	b.page = append(b.page, c)
	if c == '\n' {
		b.lines.add(b.size + len(b.page))
	}
	if len(b.page) >= docPageSize {
		b.flush()
	}
}

// flush turns the pending bytes into a page. The string conversion copies, so the
// buffer can be reused for the next page.
func (b *docBuilder) flush() {
	if len(b.page) == 0 {
		return
	}
	b.pages = append(b.pages, string(b.page))
	b.size += len(b.page)
	b.page = b.page[:0]
}

// finish seals the document, or reports why the stream is not text.
func (b *docBuilder) finish() (*Document, error) {
	b.flush()
	if b.nuls > 0 {
		if looksUTF16Counts(b.size, b.nulEven, b.nuls-b.nulEven) {
			return nil, errUTF16NoBOM
		}
		return nil, errBinary
	}
	d := &Document{size: b.size, pages: b.pages, lines: b.lines.finish()}
	if len(d.pages) == 0 {
		d.pages = []string{""}
	}
	d.rebuildStarts()
	return d, nil
}

// eolStyle is the line-ending convention the stream used, with the same rule as
// normalizeEOL: CRLF if any line ends that way, CR if the file has lone CRs, LF
// otherwise.
func (b *docBuilder) eolStyle() string {
	switch {
	case !b.sawCR:
		return eolLF
	case b.sawCRLF:
		return eolCRLF
	default:
		return eolCR
	}
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

// lineBlock is how many lines share one base offset in the line index.
const lineBlock = 4096

// lineIndex holds the byte offset of every line start in two levels: the offset
// of line i is base[i/lineBlock] plus a value that fits in an int32.
//
// The two levels are what keep typing cheap in a huge file. Typing a character
// moves every later line start by one byte; with a flat array that is one add per
// line (millions of them per keystroke), while here it is one add per block plus
// at most lineBlock entries.
type lineIndex struct {
	rel  []int32 // offset within the block
	base []int64 // absolute offset of the first line of each block
}

// lineIndexBuilder fills a lineIndex as line starts go by, which keeps the peak
// memory of a big open at what the index actually needs: collecting the offsets in
// an []int first would cost eight bytes per line on top of the four the index
// keeps, and for a file with tens of millions of lines that is the difference
// between opening and swapping.
type lineIndexBuilder struct {
	rel  []int32
	base []int64
}

// add records the absolute byte offset of the next line start.
func (b *lineIndexBuilder) add(abs int) {
	if len(b.rel)%lineBlock == 0 {
		b.base = append(b.base, int64(abs))
	}
	b.rel = append(b.rel, int32(int64(abs)-b.base[len(b.base)-1]))
}

// finish seals the index.
func (b *lineIndexBuilder) finish() *lineIndex {
	if len(b.rel) == 0 {
		b.add(0)
	}
	return &lineIndex{rel: b.rel, base: b.base}
}

// newLineIndex builds an index from absolute line starts.
func newLineIndex(starts []int) *lineIndex {
	x := &lineIndex{
		rel:  make([]int32, len(starts)),
		base: make([]int64, (len(starts)+lineBlock-1)/lineBlock),
	}
	for b := range x.base {
		x.base[b] = int64(starts[b*lineBlock])
	}
	for i := range x.rel {
		x.rel[i] = int32(starts[i]) - int32(x.base[i/lineBlock])
	}
	return x
}

// count is the number of line starts, which is also the number of lines.
func (x *lineIndex) count() int { return len(x.rel) }

// at is the byte offset where line i starts, or -1 past the last line (the caller
// substitutes the document size).
func (x *lineIndex) at(i int) int {
	if i < 0 {
		return 0
	}
	if i >= len(x.rel) {
		return -1
	}
	return int(x.base[i/lineBlock] + int64(x.rel[i]))
}

// upTo is how many line starts are at or before off, which is one more than the
// index of the line containing off.
func (x *lineIndex) upTo(off int) int {
	if len(x.rel) == 0 {
		return 0
	}
	b := sort.Search(len(x.base), func(k int) bool { return x.base[k] > int64(off) }) - 1
	if b < 0 {
		return 0
	}
	lo := b * lineBlock
	hi := lo + lineBlock
	if hi > len(x.rel) {
		hi = len(x.rel)
	}
	base := x.base[b]
	j := sort.Search(hi-lo, func(k int) bool { return base+int64(x.rel[lo+k]) > int64(off) })
	return lo + j
}

// set points line i at off, which must lie within line i's block.
func (x *lineIndex) set(i, off int) {
	x.rel[i] = int32(off) - int32(x.base[i/lineBlock])
}

// shiftFrom moves every line start at or after index from by delta.
func (x *lineIndex) shiftFrom(from, delta int) {
	if delta == 0 || from >= len(x.rel) {
		return
	}
	if from < 0 {
		from = 0
	}
	b := from / lineBlock
	hi := (b + 1) * lineBlock
	if hi > len(x.rel) {
		hi = len(x.rel)
	}
	for i := from; i < hi; i++ {
		x.rel[i] += int32(delta)
	}
	for k := b + 1; k < len(x.base); k++ {
		x.base[k] += int64(delta)
	}
}

// splice replaces the line starts [keep, drop) with starts and moves the tail by
// delta. The line count changed, so every later entry may belong to a different
// block and the index is rebuilt: O(lines), which is why it only happens when a
// line is broken or joined, not while typing.
func (x *lineIndex) splice(keep, drop int, starts []int, delta int) {
	oldRel, oldBase := x.rel, x.base
	count := len(oldRel) + len(starts) - (drop - keep)
	rel := make([]int32, count)
	base := make([]int64, (count+lineBlock-1)/lineBlock)

	// The loop is written out rather than routed through a closure: a line break
	// in a multi-million-line document runs it millions of times.
	i := 0
	for ; i < keep; i++ {
		if i%lineBlock == 0 {
			base[i/lineBlock] = oldBase[i/lineBlock] + int64(oldRel[i])
		}
		rel[i] = oldRel[i]
	}
	for _, off := range starts {
		if i%lineBlock == 0 {
			base[i/lineBlock] = int64(off)
		}
		rel[i] = int32(off) - int32(base[i/lineBlock])
		i++
	}
	for ; i < count; i++ {
		j := i - keep - len(starts) + drop
		abs := oldBase[j/lineBlock] + int64(oldRel[j]) + int64(delta)
		if i%lineBlock == 0 {
			base[i/lineBlock] = abs
		}
		rel[i] = int32(abs) - int32(base[i/lineBlock])
	}
	x.rel, x.base = rel, base
}

// rebuildLines recomputes the whole line index, for a document that was built
// from pages rather than streamed.
func (d *Document) rebuildLines() {
	starts := make([]int, 0, len(d.pages)+d.size/48)
	starts = append(starts, 0)
	base := 0
	for _, p := range d.pages {
		for i := 0; i < len(p); i++ {
			if p[i] == '\n' {
				starts = append(starts, base+i+1)
			}
		}
		base += len(p)
	}
	d.lines = newLineIndex(starts)
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
func (d *Document) LineCount() int { return d.lines.count() }

// LineStart returns the byte offset where line i starts (0-based).
func (d *Document) LineStart(i int) int {
	if i <= 0 {
		return 0
	}
	if off := d.lines.at(i); off >= 0 {
		return off
	}
	return d.size
}

// LineText returns line i without its newline.
func (d *Document) LineText(i int) string {
	if i < 0 || i >= d.lines.count() {
		return ""
	}
	return strings.TrimSuffix(d.slice(d.LineStart(i), d.LineStart(i+1)), "\n")
}

// LineOf returns the 0-based line containing a byte offset.
func (d *Document) LineOf(off int) int {
	return d.lines.upTo(off) - 1
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
	lines := d.lines.count()
	if startLine >= lines {
		startLine = lines - 1
	}
	end := startLine + count
	if end > lines {
		end = lines
	}
	if end < startLine {
		end = startLine
	}
	to := d.size
	if end < lines {
		to = d.LineStart(end)
	}
	return d.slice(d.LineStart(startLine), to), end - startLine
}

// ReplaceRange replaces the bytes in [from, to) with repl and keeps the page and
// line indexes in step.
func (d *Document) ReplaceRange(from, to int, repl string) {
	if from < 0 {
		from = 0
	}
	if from > d.size {
		from = d.size
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
	keep := d.lines.upTo(from) // line starts at or before from
	drop := d.lines.upTo(to)   // line starts at or before to

	var added [16]int
	newStarts := added[:0]
	for i := 0; i < len(repl); i++ {
		if repl[i] == '\n' {
			newStarts = append(newStarts, from+i+1)
		}
	}

	if len(newStarts) == drop-keep {
		// The line count did not change (typing, or a replacement that keeps the
		// breaks): rewrite the starts that moved, then shift the tail by delta.
		for k, off := range newStarts {
			d.lines.set(keep+k, off)
		}
		d.lines.shiftFrom(drop, delta)
		return
	}
	d.lines.splice(keep, drop, newStarts, delta)
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
