# Gaze — a tiny, fast text editor

[![CI](https://github.com/AmirrezaSaadi/gaze-editor/actions/workflows/ci.yml/badge.svg)](https://github.com/AmirrezaSaadi/gaze-editor/actions/workflows/ci.yml)

A small, keyboard-first text editor built with [Fyne](https://fyne.io).
Monospaced editing surface, stock **Light** and **Dark** themes, and only the
day-to-day features you actually reach for — no plugin system, no bloat.

## Screenshots

| Light theme | Dark theme |
| :---: | :---: |
| [![Gaze editing a file in the light theme](screenshots/light.png)](screenshots/light.png) | [![Gaze editing a file in the dark theme](screenshots/dark.png)](screenshots/dark.png) |

## Features

- **File** — New, Open, Save, Save As, native dialogs, unsaved-changes guard
- **Atomic save** — writes a synced temp file and renames it over the target,
  preserving permissions (and symlinks) so a crash cannot truncate your file
- **Autosave recovery** — dirty buffers are snapshotted to the cache dir and
  offered back after a crash, untitled buffers included
- **Syntax highlighting** for 15 languages out of the box — Go, Rust, C/C++,
  Python, JavaScript, TypeScript, shell, JSON, TOML, YAML, Markdown, INI,
  Dockerfile, Makefile — from data-only definitions: keywords, strings,
  comments, numbers and a few line patterns. More can be installed from inside
  the editor (*View → Languages…*), and a definition is just a JSON file
- **One editing surface for every file size** — the text lives in a 64 KB page
  store with a block-indexed line table, and Gaze draws the lines on screen
  itself, so cost tracks the screen rather than the file. Wrap, selection,
  - **One editing surface for every file size** — the text lives in a 64 KB page
  store with a block-indexed line table, and Gaze draws the lines on screen
  itself, so cost tracks the screen rather than the file. Wrap, selection,
  undo, find/replace across the whole file, crash recovery and saving behave
  the same at any size: a 256 MB / 5.5M-line file opens in about 2 s using
  ~340 MB of memory, and a keystroke in it costs ~0.1 ms. Over 16 MiB Gaze
  asks first, and over 2 GiB it refuses
- **Big files are memory-mapped** — from 8 MiB up, a file with plain LF endings
  is mapped instead of copied, so its bytes stay in the page cache and cost
  almost no heap: a 1 GB / 21.9M-line file opens in ~2.4 s using ~260 MB (mostly
  the line index), and a 2 GB one in ~3.9 s using ~370 MB. Typing in it costs the
  same ~0.4 ms as anywhere else, and untouched pages are never read at all
- **Big files open in the background** — a file large enough to take a moment is
  read on its own goroutine, so the window stays usable: a thin progress bar
  fills at the bottom left while the status line names the file, and you can
  keep reading (or typing in) the buffer you had. If you changed that buffer
  while it loaded, Gaze asks before replacing it
- **Save guard** — a save warns when the file was changed on disk since it was
  opened, rather than overwriting it silently
- **Session restore** — reopen the last file and caret position on a bare launch
- **Drag & drop** a file onto the window to open it
- **Recent files** — persisted list (max 10) under *File → Open Recent*
- **Find & replace** — next with wrap-around, match-case, **regex** (with `$1`
  capture groups), and a persisted **search history** (`Alt+Up` / `Alt+Down`);
  works across a whole large file, not just the visible window, and while the bar
  is open the scrollbar carries a tick per matching line, so you can see where a
  query lives in the file without jumping through it
- **Go to line** — jump straight to a line number, or `file:line:col` on the CLI
- **Smart typing** — a break keeps the indentation of the line it starts on, opens
  a level after `{`, `(`, `[` or a block colon, and pushes a bracket that sat at the
  caret onto its own line; backspace in the margin removes one indentation level;
  Tab and Shift+Tab indent or outdent every selected line (persisted, on by
  default)
- **Brackets** — typing an opener inserts its pair (or wraps the selection in it),
  typing a closer whose partner is already there steps over it, backspace inside an
  empty pair removes both, and Enter inside a fresh pair splits it into a block;
  the bracket at the caret and its partner are marked with a box, and the search
  for a partner gives up after 100,000 characters so no keystroke waits on it
  (pairing has its own persisted preference)
- **Toggle comment** — `Ctrl+/` comments out the selected lines, or the caret's
  line, and takes the comments off again; the delimiters come from the language
  definition, so a language without line comments gets its block comment instead
- **Line operations** — indent/outdent, move line up/down, duplicate line, toggle
  comment, sort a block of lines (either way), remove duplicate lines, and change
  the case of the selection or the caret's line
- **Trim trailing whitespace on save** (optional, persisted)
- **Line-ending aware** — CRLF/CR files are normalised for editing and restored
  on save; the convention is shown in the status bar
- **Encoding aware** — UTF-8, UTF-8 with BOM and UTF-16 (LE/BE) are detected and
  written back in their original form; binary files and UTF-32 are refused
  instead of being corrupted on save
- **Undo / redo** covering typing *and* the whole-buffer commands (line
  operations, replace all), **cut / copy / paste**, **select all**
- **Word wrap** toggle (persisted), word-aware, at any file size
- **Font zoom** in/out/reset (persisted)
- **Tooltips** — hover any toolbar or find-bar button to see what it does
- Live status bar: `line:col · words · chars · EOL · indent · zoom` — counted in
  the background for big files, so the numbers never block typing
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
    autosave.go           # crash-recovery snapshots (streamed from the document)
    document.go           # 64 KB page store, line index, windowed reads
    codeview.go           # the editing surface: drawing, caret, selection, undo
    encoding.go           # UTF-8/UTF-16 detection and byte-order marks
    text.go               # rune/offset/text helpers, line ops, EOL + indent
    tooltip.go            # hover tooltips for toolbar/find-bar buttons
  internal/theme/theme.go # Light/Dark selection, font zoom, persistence
  Icon.png                # app icon, rendered from packaging/icon.svg
  packaging/gaze.desktop  # freedesktop launcher for `make install`
  packaging/icon.svg      # icon artwork; `make icon` re-renders Icon.png
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

## Syntax highlighting

Write the file, and Gaze colours it: the language is matched by extension, then
by whole file name (`Dockerfile`, `Makefile`), and the name is shown in the
status readout so you can see what it picked. *View → Syntax Highlighting*
turns it off, *View → Language* overrides the choice for the file in front of
you, and *View → Languages…* lists what is installed and what can be fetched.

The colours are the token colours of GitHub's own themes, chosen once per theme
variant, so they read on both the light and the dark background.

**Language definitions are data, not code.** A language is a JSON file:

```json
{
  "schema": 1,
  "id": "go",
  "name": "Go",
  "extensions": ["go"],
  "filenames": [],
  "line_comment": ["//"],
  "block_comment": [{"start": "/*", "end": "*/"}],
  "strings": [{"start": "\"", "end": "\"", "escape": "\\"},
              {"start": "`", "end": "`", "multiline": true}],
  "keywords": ["func", "return", "if", "for"],
  "types": ["int", "string"],
  "builtins": ["len", "append"],
  "numbers": true,
  "line_rules": [{"pattern": "^#", "class": "keyword"}]
}
```

`class` is one of `keyword`, `type`, `builtin`, `string`, `number`, `comment`.
A `line_rules` pattern colours the prefix it matches, which is how Markdown
headings, YAML keys, Makefile targets and C preprocessor lines are handled.
Patterns go through Go's `regexp`, which is linear-time, so a definition cannot
make the editor hang.

The 15 definitions Gaze ships with are embedded in the binary from `langs/`, and
that same directory is served from GitHub, so a definition can be corrected
without a new release:

- **Languages… → Check for Downloads** lists what the repository has that you do
  not, with checksums in `langs/index.json`; **Install** writes it to
  `~/.config/gaze/languages/`.
- **Install from File…** takes a definition you wrote yourself, no network at all.
- An installed definition with the same `id` as a built-in one replaces it; the
  Remove button appears only for the ones you added.

Nothing is downloaded until you ask, the base URL is compiled in, a definition's
file name must be a plain name (no paths), the body is capped at 256 KB, and the
checksum from the index is verified before it is written. Nothing in a
definition is ever executed.

**Cost.** Highlighting is a pure function of a line and the state it starts in, so
the CPU cost is the lines on screen: lexing 35 lines is about 20 µs. The visible
cost is object count — a coloured line is drawn as up to 8 runs of text instead
of one, so a full repaint of the window goes from ~0.4 ms to ~0.9 ms, and typing
stays where it was (~0.5 ms). Object memory is ~84 KB for a screenful.

Highlighting is exact for a document up to 100,000 lines. Above that, a language
that has no multi-line constructs (JSON, INI, logs) is still exact, because a line
is then self-contained; one that does (C, Go, Python) is left plain rather than
guessed, since knowing the state at line 4,000,000 would mean lexing four million
lines first.

## Size & memory notes

- No duplicate buffer: dirty state is a flag, not a second copy of the file.
- Only ~35 `canvas.Text` objects exist no matter how big the file is: scrolling
  moves them, so cost tracks the screen, not the document. Re-drawing a page
  costs ~0.3 ms.
- A file of 8 MiB or more with plain LF line endings is memory-mapped, and the
  pages of the document are string views of the mapping (`unsafe.String`, no
  copy), so the text costs address space rather than heap and the kernel decides
  which parts stay resident. Anything else — CRLF, a BOM, UTF-16, a binary blob —
  is streamed into pages instead, because it needs normalising or naming as it
  is read. A 256 MB LF file is the same document either way.
- A mapping is never released: undo history, snapshots and in-flight saves hold
  substrings of it, and there is no way to know when the last one goes. On a
  64-bit machine the address space is free; the pages are the page cache's.
- Saves stream the document's pages straight to disk.
- Line starts are stored in blocks of 4096, so typing shifts one base per block
  instead of one offset per line: ~0.1 ms per keystroke in a 5.5M-line file.
  A line break still rebuilds that index, which costs ~35 ms there.
- Word and character counts of a big document are computed off the UI goroutine
  (debounced), and its crash-recovery snapshots are streamed page by page.
- `runtime/debug.FreeOSMemory` runs after open/save so the resident set does
  not creep up as files are cycled.
- `debug.SetGCPercent(50)` keeps the idle heap small.
- Highlighting adds up to 8 text objects per visible line (a screenful is ~250
  instead of ~35) and about 130 KB to the binary, which is the embedded
  definitions and the lexer. No new dependencies: `net/http`, needed to fetch a
  definition, was already linked in by Fyne.
- The UI is deliberately slim: a five-button toolbar, a two-row find bar and a
  compact status line — no extra chrome, no placeholder text.

## Known limitations (Fyne)

- **No IME composition.** Gaze's editing surface is drawn by Gaze, so a
  composing input method (CJK, some keyboard layouts) cannot attach to it the way
  it attaches to `widget.Entry`; typed runes are inserted directly.
- Tabs are expanded to the editor's indent width for display, so a file that
  mixes tabs and spaces lines up by column, not by tab stop.
- Markdown is approximated: headings, lists, quotes, rules and fences get a
  colour, but inline emphasis (`**bold**`, `*italic*`) and link targets are not
  styled, and nothing is reflowed.
- A language with block comments is not highlighted past 100,000 lines (see
  above); the language name is then left out of the readout rather than shown
  over plain text.
- **Find/replace materialises the document once per command**, because `^`, `$`
  and `$1` need the whole text. It is released as soon as the command finishes.
- The line commands (indent, move, duplicate) act on the caret's line; there is
  no block indent over a multi-line selection yet.
- Large-file mode (over 16 MiB) keeps trailing whitespace: trimming on save
  would have to rewrite the whole document to mirror it back into the buffer.
- Files over 2 GiB are refused rather than loaded. The limit is the line index
  (4 bytes a line, plus 8 more when wrapping), not the text: a 2 GiB file with
  44M lines needs ~370 MB for it.
- Word wrap is not offered above 8M lines, where the visual-row index would cost
  more than reflowing a file that size is worth; the menu reports it as off.
- A memory-mapped file is read through the kernel, so if another program
  truncates it while Gaze has it open, reading past the new end can fault.
- Opening a very large file still stops the window for a moment while the parsed
  document is installed (~0.4 s for 256 MB, ~1.2 s for 1 GB); the reading itself
  happens in the background with a progress readout.
- A second open while one is loading wins: the first read is abandoned rather
  than finishing and installing itself.
- BOM-less UTF-16 is detected and refused with an explanatory message rather
  than decoded, because guessing the byte order of an unmarked file is not safe.

## Changes

See [CHANGELOG.md](CHANGELOG.md) for what changed in each release.

## License

MIT — see [LICENSE](LICENSE).
