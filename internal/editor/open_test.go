package editor

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// pacedReader hands out its payload in small chunks with a delay between them, so
// a background read can be observed while it is still in flight.
type pacedReader struct {
	data  []byte
	step  int
	delay time.Duration

	mu  sync.Mutex
	pos int
}

func (p *pacedReader) Read(b []byte) (int, error) {
	p.mu.Lock()
	pos := p.pos
	p.mu.Unlock()
	if pos >= len(p.data) {
		return 0, io.EOF
	}
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	n := p.step
	if n > len(b) {
		n = len(b)
	}
	if pos+n > len(p.data) {
		n = len(p.data) - pos
	}
	copy(b, p.data[pos:pos+n])
	p.mu.Lock()
	p.pos += n
	p.mu.Unlock()
	return n, nil
}

func (p *pacedReader) Close() error { return nil }

// consumed is how far the reader has been read, for watching it stop.
func (p *pacedReader) consumed() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos
}

// failingReader reports an error instead of text.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errBinary }
func (failingReader) Close() error             { return nil }

// paced wraps text for a background open of the given size.
func paced(text string, step int, delay time.Duration) *pacedReader {
	return &pacedReader{data: []byte(text), step: step, delay: delay}
}

// fastProgress makes the progress throttle fire on every read.
func fastProgress(t *testing.T) {
	t.Helper()
	old := openProgressInterval
	openProgressInterval = time.Millisecond
	t.Cleanup(func() { openProgressInterval = old })
}

// A large open returns to the UI immediately: the old buffer is still there and
// the status line says what is happening, then the document appears.
func TestOpenLargeFileInBackground(t *testing.T) {
	fastProgress(t)
	e := newTestEditor(t)
	e.SetContent("old buffer", "")

	want := strings.Repeat("a line of the new file\n", 512)
	src := paced(want, 256, time.Millisecond)
	path := filepath.Join(t.TempDir(), "big.txt")

	e.startOpenReader(path, "big.txt", src, int64(len(src.data)), openLarge, nil)

	// The call came back while the read is still going.
	if got := e.text(); got != "old buffer" {
		t.Fatalf("buffer while loading = %q, want the old one", got)
	}
	if !strings.HasPrefix(e.status.Text, "opening big.txt") {
		t.Fatalf("status = %q, want it to name the file being opened", e.status.Text)
	}

	waitFor(t, "the open to finish", func() bool { return e.currentFile == path })
	if got := e.text(); got != want {
		t.Fatalf("buffer = %d bytes, want %d", len(got), len(want))
	}
	// The status line is set just after the buffer, so wait for it rather than
	// racing it.
	waitFor(t, "the opened status", func() bool {
		return strings.Contains(e.status.Text, "opened")
	})
}

// The status line reports how far the read has got.
func TestOpenLargeFileReportsProgress(t *testing.T) {
	fastProgress(t)
	e := newTestEditor(t)
	src := paced(strings.Repeat("x\n", 4096), 128, time.Millisecond)
	path := filepath.Join(t.TempDir(), "big.txt")

	seen := false
	e.startOpenReader(path, "big.txt", src, int64(len(src.data)), openLarge, nil)
	waitFor(t, "a progress update", func() bool {
		seen = seen || strings.Contains(e.status.Text, "%")
		return seen
	})
	waitFor(t, "the open to finish", func() bool { return e.currentFile == path })
}

// A second open wins: the first read is abandoned instead of installing itself
// over the newer buffer.
func TestOpenInBackgroundIsSuperseded(t *testing.T) {
	e := newTestEditor(t)
	slow := paced(strings.Repeat("first\n", 2048), 128, time.Millisecond)
	first := filepath.Join(t.TempDir(), "first.txt")

	e.startOpenReader(first, "first.txt", slow, int64(len(slow.data)), openLarge, nil)

	second := filepath.Join(t.TempDir(), "second.txt")
	if err := os.WriteFile(second, []byte("second buffer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.startOpen(second, openNormal, nil)

	if got := e.text(); got != "second buffer\n" {
		t.Fatalf("buffer = %q, want the second file", got)
	}
	// The abandoned read must stop instead of running to the end.
	time.Sleep(60 * time.Millisecond)
	at := slow.consumed()
	time.Sleep(80 * time.Millisecond)
	if slow.consumed() != at {
		t.Fatal("the superseded read kept reading after another file was opened")
	}
	if got := e.text(); got != "second buffer\n" {
		t.Fatalf("buffer = %q, the abandoned open installed itself", got)
	}
	if e.currentFile != second {
		t.Fatalf("current file = %q, want %q", e.currentFile, second)
	}
}

// Typing while a big file loads must not be thrown away silently.
func TestOpenInBackgroundAsksAboutEditsMadeMeanwhile(t *testing.T) {
	e := newTestEditor(t)
	e.SetContent("start", "")
	slow := paced(strings.Repeat("new\n", 4096), 128, time.Millisecond)
	path := filepath.Join(t.TempDir(), "big.txt")

	e.startOpenReader(path, "big.txt", slow, int64(len(slow.data)), openLarge, nil)
	test.Type(e.code, "edited ")

	waitFor(t, "the prompt", func() bool { return e.win.Canvas().Overlays().Top() != nil })
	if got := e.text(); !strings.HasPrefix(got, "edited ") {
		t.Fatalf("buffer = %q, want the typing kept while the question is open", got)
	}
	if e.currentFile == path {
		t.Fatal("the file was installed without asking")
	}
}

// A read that fails in the background is reported like any other open failure.
func TestOpenInBackgroundReportsErrors(t *testing.T) {
	e := newTestEditor(t)
	e.startOpenReader("/tmp/gaze-nope.txt", "nope.txt", failingReader{}, 10, openLarge, nil)

	waitFor(t, "the failure", func() bool { return e.status.Text == "open failed" })
	if e.currentFile == "/tmp/gaze-nope.txt" {
		t.Fatal("a failed read must not change the current file")
	}
}

// Small files are still read on the spot, and the post-open step runs after the
// buffer really holds them.
func TestOpenSmallFileIsInline(t *testing.T) {
	e := newTestEditor(t)
	path := filepath.Join(t.TempDir(), "small.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ran := false
	e.startOpen(path, openNormal, func() {
		ran = true
		if row, col := caret(e); row != 0 || col != 0 {
			t.Errorf("caret = %d:%d in the post-open step", row, col)
		}
	})

	if !ran {
		t.Fatal("a small file should open inline")
	}
	if got := e.text(); got != "one\ntwo\nthree\n" {
		t.Fatalf("buffer = %q", got)
	}
}

// The post-open step of a background open runs once the document is installed,
// which is what puts the caret at file:line:col from the command line.
func TestOpenInBackgroundRunsAfterInstall(t *testing.T) {
	e := newTestEditor(t)
	path := filepath.Join(t.TempDir(), "big.txt")
	text := strings.Repeat("line\n", 2048)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	e.startOpen(path, openLarge, func() {
		if got := e.doc.LineCount(); got != 2049 {
			t.Errorf("post-open step ran with %d lines, want 2049", got)
		}
		e.gotoLineCol(3, 0)
	})

	waitFor(t, "the caret jump", func() bool {
		row, _ := caret(e)
		return row == 2
	})
}

// openProgressText says what it can: a percentage when the size is known, bytes
// otherwise.
func TestOpenProgressText(t *testing.T) {
	cases := []struct {
		read, total int64
		want        string
	}{
		{0, 100, "0%"},
		{50, 100, "50%"},
		{100, 100, "100%"},
		{120, 100, "100%"},
		{4096, 0, "4.0 KB read"},
	}
	for _, c := range cases {
		if got := openProgressText(c.read, c.total); got != c.want {
			t.Fatalf("openProgressText(%d, %d) = %q, want %q", c.read, c.total, got, c.want)
		}
	}
}

// The abort error is internal: a reader whose open was superseded reports it so
// the caller can stop, and the open path ignores it rather than showing an error.
func TestProgressReaderAbortsWhenSuperseded(t *testing.T) {
	e := newTestEditor(t)
	cur := &e.loadSeq
	seq := cur.Add(1)
	pr := &progressReader{r: paced("hello, world", 4, 0), seq: cur, me: seq}

	buf := make([]byte, 16)
	if _, err := pr.Read(buf); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	cur.Add(1) // a newer open took over
	if _, err := pr.Read(buf); !errors.Is(err, errLoadAbandoned) {
		t.Fatalf("read after being superseded = %v, want errLoadAbandoned", err)
	}
}
