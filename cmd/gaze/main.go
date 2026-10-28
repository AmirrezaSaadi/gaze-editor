// Command gaze is a tiny, fast, keyboard-friendly text editor built on Fyne.
//
//	usage: gaze [file[:line[:col]]]
//	       gaze -        read a buffer from standard input
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"fyne.io/fyne/v2/app"

	"gaze/internal/editor"
)

// version is the build version. Release builds override it with
// -ldflags "-X main.version=$VERSION" so the binary cannot drift from the tag
// it was cut from; the literal below is what `go run` and `go test` report.
var version = "1.2.3"

func main() {
	// Keep the resident set small: let the collector run eagerly rather than
	// growing a large idle heap.
	debug.SetGCPercent(50)

	var (
		file      string
		line, col int
		stdin     bool
		tooMany   bool
		printHelp bool
		printVer  bool
	)

	for _, arg := range os.Args[1:] {
		switch {
		case arg == "-h" || arg == "--help":
			printHelp = true
		case arg == "-v" || arg == "--version":
			printVer = true
		case arg == "-":
			stdin = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "gaze: unknown option %q\n", arg)
			usage(os.Stderr)
			os.Exit(2)
		case file == "":
			file, line, col = parseLocation(arg)
		default:
			tooMany = true
		}
	}

	switch {
	case printHelp:
		usage(os.Stdout)
		return
	case printVer:
		fmt.Println("gaze " + version)
		return
	}

	// No file and no "-": read stdin when it is a pipe or regular file.
	if file == "" && !stdin {
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			stdin = true
		}
	}

	if tooMany {
		fmt.Fprintln(os.Stderr, "gaze: only one file can be opened; ignoring the rest")
	}

	var initial string
	if stdin {
		var sb strings.Builder
		if _, err := io.Copy(&sb, os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "gaze: reading stdin: %v\n", err)
			os.Exit(1)
		}
		initial = sb.String()
	}

	// Reverse-DNS app ID. Fyne derives the settings directory from it
	// (see fyne's storageRoot), so changing it moves the user's preferences.
	a := app.NewWithID("io.github.amirrezasaadi.gaze-editor")
	w := a.NewWindow("Gaze")

	ed := editor.New(a, w)
	ed.BuildUI()

	switch {
	case stdin:
		ed.SetContent(initial, "")
		ed.SetStatus("stdin — unsaved")
	case file != "":
		if _, err := os.Stat(file); err != nil {
			ed.SetContent("", file)
			ed.SetStatus("new file — " + file)
		} else {
			ed.OpenPathAt(file, line, col)
		}
	default:
		if !ed.RestoreSession() {
			// Nothing to reopen: an untitled buffer from a crashed session may
			// still be waiting in the recovery cache.
			ed.OfferDraftRecovery()
		}
	}

	w.CenterOnScreen()
	w.ShowAndRun()
}

// parseLocation splits an argument of the form path[:line[:col]] into its
// parts, trying the longest existing path first so paths containing colons are
// still handled. Trailing numeric components are also stripped when nothing
// exists yet, so `gaze newfile.txt:10` works for a file about to be created.
func parseLocation(arg string) (path string, line, col int) {
	if fileExists(arg) {
		return arg, 0, 0
	}
	parts := strings.Split(arg, ":")
	for k := len(parts) - 1; k >= 1; k-- {
		candidate := strings.Join(parts[:k], ":")
		if !fileExists(candidate) {
			continue
		}
		if n, m, ok := trailNumbers(parts[k:]); ok {
			return candidate, n, m
		}
	}
	// Nothing exists: still peel off a trailing :line[:col] so a file that is
	// about to be created can be positioned too.
	if len(parts) >= 3 {
		if n, m, ok := trailNumbers(parts[len(parts)-2:]); ok {
			return strings.Join(parts[:len(parts)-2], ":"), n, m
		}
	}
	if len(parts) >= 2 {
		if n, _, ok := trailNumbers(parts[len(parts)-1:]); ok {
			return strings.Join(parts[:len(parts)-1], ":"), n, 0
		}
	}
	return arg, 0, 0
}

// trailNumbers interprets up to two numeric components as line and column.
func trailNumbers(nums []string) (line, col int, ok bool) {
	if len(nums) == 0 || len(nums) > 2 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(nums[0])
	if err != nil {
		return 0, 0, false
	}
	line = n
	if len(nums) == 2 {
		m, err := strconv.Atoi(nums[1])
		if err != nil {
			return 0, 0, false
		}
		col = m
	}
	return line, col, true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `gaze — a tiny text editor

usage: gaze [options] [file[:line[:col]]]
       gaze -              read from standard input

options:
  -h, --help              show this help and exit
  -v, --version           print the version and exit
  -                       read a buffer from standard input

A single optional file path is opened at startup (optionally positioned at a
line/column). With no file, the previous session is restored; a piped stdin is
loaded instead.

keyboard:
  Ctrl+N/O/S  new / open / save      Ctrl+F  find & replace
  Ctrl+Z/Y    undo / redo            Ctrl+G  go to line
  Ctrl+=/-/0  zoom in / out / reset  Ctrl+Q  quit
  Ctrl+] / [        indent / outdent
  Ctrl+Shift+Up/Down  move line       Ctrl+Shift+D  duplicate line
  Alt+Up/Down       find history      Esc  close find bar
`)
}
