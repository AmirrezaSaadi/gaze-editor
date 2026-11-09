package editor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
)

// Opening a big file means reading hundreds of megabytes and indexing every line.
// Doing that on the UI goroutine freezes the window for seconds, so a large file
// is opened on its own goroutine while the old buffer stays usable, with the
// progress bar in the status row showing how far along it is. Small files are
// still read inline: it takes a moment and avoids a frame of nothing happening.
//
// A file big enough to be worth not reading is memory-mapped instead (see map.go):
// the pages of the document then point into the mapping, so the bytes stay in the
// page cache and the open only costs the line index.

// openProgressInterval throttles the progress updates, so the status line is not
// rewritten for every read.
var openProgressInterval = 200 * time.Millisecond

// errLoadAbandoned aborts a background read that a newer open has superseded.
var errLoadAbandoned = errors.New("gaze: open abandoned")

// loadBarWidth is how much room the progress bar takes in the status row.
const loadBarWidth = 130

// loadState is what a background open needs to report itself: the sequence number
// that decides whether it is still the current one, the name and size for the
// status line, and the wrap width, which is widget state and so has to be read on
// the UI goroutine before the work starts.
type loadState struct {
	seq   uint64
	name  string
	size  int64
	cells int
	wrap  bool
	done  atomic.Bool
}

// progressReader reports how much of a reader has been consumed, and gives up
// when the load it belongs to is no longer the current one.
type progressReader struct {
	r      io.Reader
	seq    *atomic.Uint64
	me     uint64
	total  int64
	n      int64
	last   time.Time
	report func(read, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	if p.seq.Load() != p.me {
		return 0, errLoadAbandoned
	}
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.report != nil && (p.last.IsZero() || time.Since(p.last) >= openProgressInterval) {
		p.last = time.Now()
		p.report(p.n, p.total)
	}
	return n, err
}

// startOpen reads path and installs it as the buffer, then runs after (which may
// be nil) on the UI goroutine. A file big enough to make the work noticeable is
// mapped or read in the background; anything else is read inline.
func (e *Editor) startOpen(path string, mode openMode, after func()) {
	name := filepath.Base(path)
	seq := e.loadSeq.Add(1) // any load still in flight is now stale

	if mode.large() {
		size := sizeOf(path)
		if size >= mapMinBytes {
			if region, err := mapPath(path, size); err == nil {
				e.scanInBackground(seq, path, name, region, size, mode, after)
				return
			}
			// Mapping is not available here (a network file system, a 32-bit
			// build, an exhausted address space): the streaming read still works.
		}
	}

	f, err := os.Open(path)
	if err != nil {
		e.reportOpenError(err, name)
		return
	}
	if !mode.large() {
		defer f.Close()
		e.readInline(seq, path, name, f, sizeOf(path), mode, after)
		return
	}
	e.readInBackground(seq, path, name, f, sizeOf(path), mode, after)
}

// startOpenReader is startOpen for a reader handed over by the open dialog, which
// knows the URI but not necessarily a stat-able path.
func (e *Editor) startOpenReader(path, name string, body io.ReadCloser, size int64, mode openMode, after func()) {
	// A dialog reader is usually just an open file, so the path-based open (and
	// its mapping) can be used; the reader is closed here in that case.
	if mode.large() && path != "" && size >= mapMinBytes {
		if _, err := os.Stat(path); err == nil {
			body.Close()
			e.startOpen(path, mode, after)
			return
		}
	}
	seq := e.loadSeq.Add(1)
	if !mode.large() {
		defer body.Close()
		e.readInline(seq, path, name, body, size, mode, after)
		return
	}
	e.readInBackground(seq, path, name, body, size, mode, after)
}

// readInline reads and installs a small file on the calling goroutine.
func (e *Editor) readInline(seq uint64, path, name string, body io.Reader, size int64, mode openMode, after func()) {
	doc, eol, enc, err := openDocument(&limitReader{r: body, limit: MaxOpenBytes})
	if errors.Is(err, errLoadAbandoned) || e.loadSeq.Load() != seq {
		return
	}
	if err != nil {
		e.reportOpenError(err, name)
		return
	}
	e.hideLoadBar()
	e.installWhenIdle(seq, e.editSeq, path, doc, eol, enc, mode, docIndex{}, after)
}

// beginLoad marks the start of a background open: the status line names the file
// and the progress bar appears beside it.
func (e *Editor) beginLoad(seq uint64, name string, size int64) *loadState {
	st := &loadState{seq: seq, name: name, size: size, cells: e.code.wrapCells(), wrap: e.wrap}
	e.setStatus("opening " + name + "…")
	if size > 0 {
		e.showLoadBar()
	}
	return st
}

// report moves the progress bar and the status line. It may be called from any
// goroutine, and does nothing once the load has been delivered or superseded.
func (e *Editor) report(st *loadState, read int64) {
	fyne.Do(func() {
		if st.done.Load() || e.loadSeq.Load() != st.seq {
			return
		}
		e.setStatus("opening " + st.name + " — " + openProgressText(read, st.size))
		e.setLoadBar(read, st.size)
	})
}

// end stops a load from reporting: its last progress update may still be queued,
// and it must not land after the result.
func (e *Editor) end(st *loadState) {
	st.done.Store(true)
	e.hideLoadBar()
}

// prepare builds what can be built off the UI goroutine before the document is
// installed: the visual-row index, which for a file with millions of lines is the
// largest part of the install.
func (e *Editor) prepare(st *loadState, doc *Document) docIndex {
	if doc == nil || !st.wrap || !e.wrapAllowed(doc) {
		return docIndex{}
	}
	_, indentWidth := detectIndent(doc.TextRange(0, indentScanLimit))
	return docIndex{rows: buildRowIndex(doc, st.cells, indentWidth, st.wrap), cells: st.cells}
}

// deliver installs a document prepared on a background goroutine, unless a newer
// open has taken over in the meantime.
func (e *Editor) deliver(st *loadState, editsAtRequest uint64, path string, doc *Document, eol, enc string, err error, mode openMode, ix docIndex, after func()) {
	fyne.Do(func() {
		e.end(st)
		if e.loadSeq.Load() != st.seq {
			return
		}
		if errors.Is(err, errLoadAbandoned) {
			return
		}
		if err != nil {
			e.reportOpenError(err, st.name)
			return
		}
		e.installWhenIdle(st.seq, editsAtRequest, path, doc, eol, enc, mode, ix, after)
	})
}

// scanInBackground indexes a mapped file on its own goroutine. Nothing is copied:
// the document's pages are views of the mapping, so the work is the two passes
// over the bytes that check them and record the line starts.
func (e *Editor) scanInBackground(seq uint64, path, name string, region []byte, size int64, mode openMode, after func()) {
	editsAtRequest := e.editSeq
	st := e.beginLoad(seq, name, size)

	go func() {
		if !mappable(region) {
			// The file needs normalising (CRLF, a BOM) or naming (binary,
			// UTF-16), which a mapping cannot do. Hand the same load to the
			// streaming reader, mapping and all still in the background.
			unmapFile(region)
			f, err := os.Open(path)
			if err != nil {
				e.deliver(st, editsAtRequest, path, nil, "", "", err, mode, docIndex{}, after)
				return
			}
			e.readStream(st, editsAtRequest, path, f, mode, after)
			return
		}
		e.report(st, size/3)
		doc := indexMapped(region)
		e.report(st, size*2/3)
		ix := e.prepare(st, doc)
		e.deliver(st, editsAtRequest, path, doc, eolLF, encUTF8, nil, mode, ix, after)
	}()
}

// readInBackground streams a large file on its own goroutine and installs it when
// the read is done, unless a newer open has taken over in the meantime.
func (e *Editor) readInBackground(seq uint64, path, name string, body io.ReadCloser, size int64, mode openMode, after func()) {
	editsAtRequest := e.editSeq
	st := e.beginLoad(seq, name, size)
	go e.readStream(st, editsAtRequest, path, body, mode, after)
}

// readStream copies a file into a document, normalising and checking it as it
// goes, and reports progress as it reads.
func (e *Editor) readStream(st *loadState, editsAtRequest uint64, path string, body io.ReadCloser, mode openMode, after func()) {
	defer body.Close()
	pr := &progressReader{
		r:      &limitReader{r: body, limit: MaxOpenBytes},
		seq:    &e.loadSeq,
		me:     st.seq,
		total:  st.size,
		report: func(read, total int64) { e.report(st, read) },
	}
	doc, eol, enc, err := openDocument(pr)
	ix := e.prepare(st, doc)
	e.deliver(st, editsAtRequest, path, doc, eol, enc, err, mode, ix, after)
}

// showLoadBar puts the progress bar at the left of the status row.
func (e *Editor) showLoadBar() {
	if e.loadBar == nil || e.loadSlot == nil {
		return
	}
	e.loadBar.SetValue(0)
	bar := container.NewCenter(container.NewGridWrap(fyne.NewSize(loadBarWidth, 10), e.loadBar))
	e.loadSlot.Objects = []fyne.CanvasObject{bar}
	e.loadSlot.Refresh()
}

// setLoadBar moves the progress bar, given how much has been read.
func (e *Editor) setLoadBar(read, total int64) {
	if e.loadBar == nil {
		return
	}
	switch {
	case total <= 0, read < 0:
		e.loadBar.SetValue(0)
	case read >= total:
		e.loadBar.SetValue(1)
	default:
		e.loadBar.SetValue(float64(read) / float64(total))
	}
}

// hideLoadBar takes the progress bar back out of the status row.
func (e *Editor) hideLoadBar() {
	if e.loadSlot == nil || len(e.loadSlot.Objects) == 0 {
		return
	}
	e.loadSlot.Objects = nil
	e.loadSlot.Refresh()
}

// loadBarShown reports whether the progress bar is in the status row, for tests.
func (e *Editor) loadBarShown() bool {
	return e.loadSlot != nil && len(e.loadSlot.Objects) > 0
}

// installWhenIdle installs a loaded document, first asking when the buffer was
// edited while it was loading: the synchronous open used to make that impossible,
// and nobody expects their typing to vanish.
func (e *Editor) installWhenIdle(seq, editsAtRequest uint64, path string, doc *Document, eol, enc string, mode openMode, ix docIndex, after func()) {
	if e.dirty && e.editSeq != editsAtRequest {
		name := filepath.Base(path)
		dialog.ShowConfirm("Changes made while opening",
			name+" finished loading, but this buffer has changes you made while it was being read.\n\n"+
				"Discard them and show "+name+"?",
			func(ok bool) {
				if e.loadSeq.Load() != seq {
					return
				}
				if !ok {
					e.setStatus("open cancelled — " + name)
					return
				}
				e.installDocument(path, doc, eol, enc, mode, ix, after)
			}, e.win)
		return
	}
	e.installDocument(path, doc, eol, enc, mode, ix, after)
}

// installDocument puts a freshly read document in the buffer and runs after.
func (e *Editor) installDocument(path string, doc *Document, eol, enc string, mode openMode, ix docIndex, after func()) {
	e.loadDocumentAs(path, doc, eol, enc, mode, ix, after)
}

// openProgressText describes how far a read has got, in per cent when the size is
// known and in bytes otherwise.
func openProgressText(read, total int64) string {
	if total <= 0 {
		return humanBytes(read) + " read"
	}
	pct := int(read * 100 / total)
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return fmt.Sprintf("%d%%", pct)
}

// sizeOf reports the size of path, or 0 when it cannot be stat'ed.
func sizeOf(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
