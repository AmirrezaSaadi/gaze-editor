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

// Opening a big file means reading hundreds of megabytes and indexing every
// line. Doing that on the UI goroutine freezes the window for seconds, so a
// large file is read on its own goroutine while the old buffer stays usable, and
// the status line (with the progress bar beside it) reports how far along the read
// is. Small files are still read
// inline: it takes a moment and avoids a frame of nothing happening.

// openProgressInterval throttles the progress updates, so the status line is not
// rewritten for every read.
var openProgressInterval = 200 * time.Millisecond

// errLoadAbandoned aborts a background read that a newer open has superseded.
var errLoadAbandoned = errors.New("gaze: open abandoned")

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
// be nil) on the UI goroutine. A file big enough to make the read noticeable is
// read in the background; anything else is read inline.
func (e *Editor) startOpen(path string, mode openMode, after func()) {
	name := filepath.Base(path)
	seq := e.loadSeq.Add(1) // any load still in flight is now stale

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

// readInBackground streams a large file on its own goroutine and installs it when
// the read is done, unless a newer open has taken over in the meantime.
// loadBarWidth is how much room the progress bar takes in the status row.
const loadBarWidth = 130

// showLoadBar puts the progress bar in the status row, left of the status text.
func (e *Editor) showLoadBar() {
	if e.loadBar == nil || e.statusBox == nil {
		return
	}
	e.loadBar.SetValue(0)
	bar := container.NewCenter(container.NewGridWrap(fyne.NewSize(loadBarWidth, 10), e.loadBar))
	row := container.NewBorder(nil, nil, bar, nil, e.status)
	e.statusBox.Objects = []fyne.CanvasObject{row}
	e.statusBox.Refresh()
}

// setLoadBar moves the progress bar, given how much has been read.
func (e *Editor) setLoadBar(read, total int64) {
	if e.loadBar == nil {
		return
	}
	switch {
	case total <= 0:
		e.loadBar.SetValue(0)
	case read < 0:
		e.loadBar.SetValue(0)
	case read >= total:
		e.loadBar.SetValue(1)
	default:
		e.loadBar.SetValue(float64(read) / float64(total))
	}
}

// hideLoadBar takes the progress bar back out of the status row.
func (e *Editor) hideLoadBar() {
	if e.loadBar == nil || e.statusBox == nil {
		return
	}
	if len(e.statusBox.Objects) == 1 && e.statusBox.Objects[0] == e.status {
		return
	}
	e.statusBox.Objects = []fyne.CanvasObject{e.status}
	e.statusBox.Refresh()
}

// loadBarShown reports whether the progress bar is in the status row, for tests.
func (e *Editor) loadBarShown() bool {
	return e.statusBox != nil && len(e.statusBox.Objects) == 1 && e.statusBox.Objects[0] != e.status
}

func (e *Editor) readInBackground(seq uint64, path, name string, body io.ReadCloser, size int64, mode openMode, after func()) {
	editsAtRequest := e.editSeq
	// The wrap width is widget state, so it is read here, on the UI goroutine.
	cells, wrap := e.code.wrapCells(), e.wrap
	e.setStatus("opening " + name + "…")
	if size > 0 {
		e.showLoadBar()
	}

	go func() {
		defer body.Close()
		// Progress updates are queued, so one written just before the read ends
		// can land afterwards: finished stops it from overwriting the result.
		var finished atomic.Bool
		pr := &progressReader{
			r:     &limitReader{r: body, limit: MaxOpenBytes},
			seq:   &e.loadSeq,
			me:    seq,
			total: size,
			report: func(read, total int64) {
				fyne.Do(func() {
					if finished.Load() || e.loadSeq.Load() != seq {
						return
					}
					e.setStatus("opening " + name + " — " + openProgressText(read, total))
					e.setLoadBar(read, total)
				})
			},
		}
		doc, eol, enc, err := openDocument(pr)
		// Index the rows here rather than on the UI goroutine: for a file with
		// millions of lines this is the largest part of the install.
		var ix docIndex
		if err == nil && wrap {
			_, indentWidth := detectIndent(doc.TextRange(0, indentScanLimit))
			ix = docIndex{rows: buildRowIndex(doc, cells, indentWidth, wrap), cells: cells}
		}
		fyne.Do(func() {
			finished.Store(true)
			e.hideLoadBar()
			if e.loadSeq.Load() != seq {
				return // superseded by another open, or the editor is closing
			}
			if errors.Is(err, errLoadAbandoned) {
				return
			}
			if err != nil {
				e.reportOpenError(err, name)
				return
			}
			e.installWhenIdle(seq, editsAtRequest, path, doc, eol, enc, mode, ix, after)
		})
	}()
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
