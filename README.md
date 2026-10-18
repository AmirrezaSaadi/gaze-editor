# Gaze — a tiny, fast text editor

A small, keyboard-first text editor built with [Fyne](https://fyne.io).
Monospaced editing surface, stock **Light** and **Dark** themes, and only the
day-to-day features you actually reach for — no plugin system, no bloat.

## Features

- **File** — New, Open, Save, Save As, native dialogs, unsaved-changes guard
- **Atomic save** — writes a synced temp file and renames it over the target,
  preserving permissions (and symlinks) so a crash cannot truncate your file
- **Autosave recovery** — dirty buffers are snapshotted to the cache dir and
  offered back after a crash
- **Session restore** — reopen the last file and caret position on a bare launch
- **Drag & drop** a file onto the window to open it
- **Recent files** — persisted list (max 10) under *File → Open Recent*
- **Find & replace** — next with wrap-around, match-case, **regex** (with `$1`
  capture groups), and a persisted **search history** (`Alt+Up` / `Alt+Down`)
- **Go to line** — jump straight to a line number, or `file:line:col` on the CLI
- **Line operations** — indent/outdent, move line up/down, duplicate line
- **Trim trailing whitespace on save** (optional, persisted)
- **Line-ending aware** — CRLF/CR files are normalised for editing and restored
  on save; the convention is shown in the status bar
- **Undo / redo**, **cut / copy / paste**, **select all**
- **Word wrap** toggle (persisted)
- **Font zoom** in/out/reset (persisted)
- **Tooltips** — hover any toolbar or find-bar button to see what it does
- Live status bar: `line:col · words · chars · EOL · indent · zoom`
- Window size, theme and preferences persisted between runs

## Command line

```
gaze [options] [file[:line[:col]]]

  -h, --help        show help
  -v, --version     print version
  -                 read a buffer from standard input
```

Opening a file, positioning at a line, or piping into `gaze` is first-class:

```sh
gaze notes.txt
gaze main.go:120          # open at line 120
gaze main.go:120:8        # …and column 8
gaze draft.md             # a path that does not exist yet is created on save
git diff | gaze           # piped stdin is loaded automatically
```

## Shortcuts

| Action                     | Shortcut                                |
| -------------------------- | --------------------------------------- |
| New / Open / Save          | `Ctrl+N` / `Ctrl+O` / `Ctrl+S`          |
| Save As                    | `Ctrl+Shift+S`                          |
| Find & replace             | `Ctrl+F`                                |
| Find history               | `Alt+Up` / `Alt+Down`                   |
| Go to line                 | `Ctrl+G`                                |
| Close find bar             | `Esc`                                   |
| Undo / Redo                | `Ctrl+Z` / `Ctrl+Y`                     |
| Cut / Copy / Paste         | `Ctrl+X` / `Ctrl+C` / `Ctrl+V`          |
| Select all                 | `Ctrl+A`                                |
| Indent / Outdent           | `Ctrl+]` / `Ctrl+[`                     |
| Move line up / down        | `Ctrl+Shift+Up` / `Ctrl+Shift+Down`     |
| Duplicate line             | `Ctrl+Shift+D`                          |
| Zoom in / out / reset      | `Ctrl+=` / `Ctrl+-` / `Ctrl+0`          |
| Quit                       | `Ctrl+Q`                                |

## Layout

```
Gaze/
  cmd/gaze/main.go        # CLI parsing (file:line:col) + entry point
  internal/editor/
    editor.go             # window wiring, slim toolbar, menus, status, theme
    fileops.go            # file I/O (atomic), recent files, session, geometry
    find.go               # find & replace (literal + regex), history, go-to-line
    autosave.go           # crash-recovery snapshots
    text.go               # rune/offset/text helpers, line ops, EOL + indent
    tooltip.go            # hover tooltips for toolbar/find-bar buttons
  internal/theme/theme.go # Light/Dark selection, font zoom, persistence
  packaging/gaze.desktop  # freedesktop launcher for `make install`
```

## Run / build / install

```sh
make run                        # go run ./cmd/gaze
make run ARGS="notes.txt"       # …with a file argument
make build                      # stripped, size-optimised binary -> ./gaze
make release                    # build, then UPX-compress if upx is installed
make install                    # -> ~/.local/bin + ~/.local/share/applications
make test                       # go test ./...
make vet                        # go vet ./...
```

`make build` uses `-trimpath`, `-ldflags "-s -w -buildid="` and the
`no_emoji,no_animations` tags. `no_emoji` drops Fyne's bundled 4 MB emoji font
(the system one is used instead) and `no_animations` skips cursor-blink and tap
animations, saving a little more code and CPU. This keeps the binary around
**20 MB** instead of the ~33 MB a default `go build` produces; what remains is
the Go runtime plus Fyne's GL driver and bundled text fonts.

## Size & memory notes

- No duplicate buffer: dirty state is a flag, not a second copy of the file.
- File loads stream through a `strings.Builder` and saves stream the string
  straight to disk, avoiding full-size `[]byte` round-trips.
- `runtime/debug.FreeOSMemory` runs after open/save so the resident set does
  not creep up as files are cycled.
- `debug.SetGCPercent(50)` keeps the idle heap small.
- The UI is deliberately slim: a five-button toolbar, a two-row find bar and a
  compact status line — no extra chrome, no placeholder text.

## Known limitations (Fyne)

- `widget.Entry` exposes no range-edit API, so **find/replace and the line
  operations rebuild the buffer via `SetText`**; this clears the entry's undo
  history for that operation.
- Selection is unexported, so commands act on the caret's line rather than a
  selection.
