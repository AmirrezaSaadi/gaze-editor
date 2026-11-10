# Changelog

All notable changes to Gaze are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases are published on the
[releases page](https://github.com/AmirrezaSaadi/gaze-editor/releases).

## [Unreleased]

Nothing yet.

## [1.11.0] - 2026-11-10

Typing that knows about code, and comments you can toggle.

### Added

- **Smart typing** (View → Smart Typing, on by default, persisted). Enter keeps
  the indentation of the line it starts on, opens one level deeper after `{`, `(`
  or `[` and after a block colon (`def f():`, `case 1:`, YAML), and pushes a
  closing bracket that sat at the caret onto its own line — so a one-line `{}`
  splits into a block. A caret inside the indentation breaks below it instead of
  repeating the prefix. Backspace in the margin removes a whole indentation level.
  Tab and Shift+Tab over a selection indent or outdent every line of it as one
  undoable edit.
- **Auto-close brackets** (View → Auto-close Brackets, on by default, persisted).
  An opener inserts its pair and leaves the caret between the halves; an opener
  typed over a single-line selection wraps the selection; a closing bracket typed
  where its partner already sits steps over it; backspace inside an empty pair
  removes both; Enter inside a fresh pair splits it. A quote after a word is left
  alone, so an apostrophe stays an apostrophe.
- **Toggle Comment** — `Ctrl+/` (Edit → Toggle Comment) comments out the selected
  lines, or the caret's line, and takes it off again when every line that holds
  anything is already commented. Indentation is kept, blank lines are left blank,
  and it is one undo step. The delimiters come from the language definition — they
  were already there for the highlighter, `parseGrammar` simply dropped them — so
  a language without line comments gets its block comment wrapped around the
  block, and a language with no comment syntax at all says so.
- **Bracket match highlight** — the bracket at the caret (or the one it just moved
  past) and its partner are marked with a soft box drawn behind the glyphs. Other
  kinds of bracket are skipped when searching, so nesting reads correctly, and the
  search stops after 100,000 characters: a pair further apart is not looked for, so
  a keystroke in a huge buffer never waits on it.

### Changed

- Indent and Outdent (`Ctrl+]` / `Ctrl+[`) move the caret with its text instead of
  dropping it at the start of the line.
- Undo restores the selection the edit was made over, not just the caret.

### Performance

Measured on a 200k-line buffer, per keystroke: a plain character 0.18 ms, an opener
with its pair 0.17 ms, a backspace over an indentation level 0.65 ms, a repaint with
a bracket match 0.03–0.15 ms. A break costs what a break without the indentation
costs — the line-index update, not the indentation, is the price. Commands that know
where the caret belongs (`editAt`) apply the change and place the caret in one undo
step and one repaint, which is what made the pair cost a third of what it was.

### Tests

- `typing_test.go` (auto-indent, tabs, braces, colon, plain mode, backspace over a
  level and over a tab, block indent/outdent, undo of a block edit, a break in a
  2000-line buffer), `comment_test.go` (one line, indentation, a block, mixed
  commented/uncommented, the line-start rule, block comments, no syntax, the caret,
  an empty line), `brackets_test.go` (both directions, the caret just past, nesting,
  across lines, no match, the scan bound, selection and focus, box placement on
  plain and wrapped rows, drawing order) and the auto-close cases.

## [1.10.1] - 2026-11-09

Two focus and pointer bugs, the first ones reported from a Windows build.

### Fixed

- **The editor stopped taking keystrokes after a dialog closed.** A dialog takes
  keyboard focus and does not hand it back, which left the window with nothing
  focused at all: typing went nowhere, so a file opened through File → Open (or a
  new buffer confirmed through a prompt) could not be edited. Focus now returns to
  the editing surface when a document is installed, when one of the editor's own
  dialogs closes, and when the text is clicked — and a keystroke that arrives with
  nothing focused is still delivered to the editor instead of being dropped.
- **A modifier could stay stuck down.** A key released while another window had
  focus never reached the widget, so Ctrl or Alt could look held and swallow every
  keystroke. Regaining focus clears them.
- **The pointer over the scrollbar showed the text caret.** The bar is drawn by
  the editor, so the whole widget answered with the editing cursor; over the bar
  (while it is drawn) it is the default arrow now.

### Tests

- `focus_test.go`: the cursor over the bar and over the text, typing with nothing
  focused, a dialog keeping the keyboard while it is up, focus returning when it
  closes, clicking the text, stuck modifiers, and focus after an open.

## [1.10.0] - 2026-11-09

Syntax highlighting, from definitions that are data.

### Added

- **Highlighting for 15 languages**: Go, Rust, C, C++, Python, JavaScript,
  TypeScript, shell, JSON, TOML, YAML, Markdown, INI/conf, Dockerfile and
  Makefile. The language is matched by extension and then by whole file name, and
  named in the status readout.
- **View → Syntax Highlighting** (persisted), **View → Language** to override the
  choice for the open file, and **View → Languages…** to manage definitions.
- **Downloadable definitions.** The `langs/` directory is both embedded in the
  binary and served from GitHub, so *Languages… → Check for Downloads* can install
  a definition that the shipped build predates — no new release needed — and
  *Install from File…* installs one with no network at all. Downloads are checked
  against the checksum in `langs/index.json`, written atomically to
  `~/.config/gaze/languages/`, and an installed definition replaces a built-in
  one with the same id.
- Definitions are **JSON only**: keywords, types, builtins, line and block
  comments, string rules (with escapes and may-span-lines), numbers, and anchored
  regexes for line prefixes. Nothing in one can run, its patterns go through Go's
  linear-time `regexp`, its file name must be a plain name, and its body is capped
  at 256 KB — a definition is a colour, at worst.

### Changed

- The renderer draws a row as **runs of cells** instead of one string: a run is a
  cell offset, a colour and its text, still capped at 8 runs per row and still
  pooled, so scrolling allocates nothing. Tab expansion moved into that pass, so
  `lineDisplay` is gone.
- The editor's status line is now the middle of the bottom bar rather than its
  left edge, so a long message truncates with an ellipsis instead of running into
  the readout on the right; "opened …" names the file by its base name when the
  path is long.
- Highlighting state is an `int32` per line, filled forwards from the last known
  state and dropped from the edited line down, so typing in a 100,000-line file
  re-lexes the lines below the caret, not the file.

### Performance

- Lexing the visible window (35 lines, 9 runs a line) costs ~20 µs; text objects
  go from ~35 to ~250 for a crowded screenful, and a full repaint from ~0.4 ms to
  ~0.9 ms. Typing is unchanged (~0.5 ms).
- The binary is ~130 KB larger (the embedded definitions plus the lexer). No new
  module dependency: `net/http` was already linked in by Fyne.
- A language with block comments is only highlighted up to 100,000 lines;
  definitions without multi-line constructs work at any size, because a line is
  then self-contained.

## [1.9.0] - 2026-11-09

Big files are no longer copied into memory, and a load shows its progress.

### Added

- **Memory-mapped opening.** From 8 MiB up, a file with plain LF line endings is
  mapped read-only instead of read: the document's pages become string views of
  the mapping, so the text costs address space and page cache instead of heap,
  and untouched pages are never touched at all. A 1 GB / 21.9M-line file opens in
  ~2.4 s using ~260 MB (against 9.5 s and 1.3 GB before), and that is mostly the
  line index. A 2 GB file opens in ~3.9 s using ~370 MB. Typing stays at the
  same ~0.4 ms: an edit copies only the 64 KB page it lands in.
- **A progress bar in the status row**, at the bottom left, filling as a large
  file loads. It appears only for a background (large-file) open, keeps its own
  label empty because the status text beside it carries the percentage, and takes
  no room in the layout when nothing is loading.
- `MaxOpenBytes` is now 2 GiB, because the limit is the line index rather than
  the file: 4 bytes a line, and 8 more if wrapping is on.
- Word wrap is not offered above 8M lines (`wrapIndexMaxLines`), where the
  visual-row index would cost more than the wrapping is worth. The View menu
  shows the effective state, and toggling it there says why.

### Changed

- A CRLF, BOM, UTF-16 or binary file still goes to the streaming reader, which
  normalises and names it as it copies; the mapping is only used when the bytes
  can be used exactly as they are.
- The line index is filled as line starts go by (`lineIndexBuilder`) instead of
  being collected in an `[]int` first. The old shape needed 8 bytes a line on top
  of the 4 the index keeps, which for a 2 GB file was half a gigabyte of
  temporary allocation.
- Open and close documentation says what a file now costs: mapping, line index,
  and where the size limit comes from.

### Fixed

- `Document.ReplaceRange` clamps a start offset past the end of the document
  instead of slicing out of bounds. Reachable when edits are applied to offsets
  measured before earlier edits moved the text.

## [1.8.0] - 2026-11-09

Opening a big file no longer freezes the window.

### Added

- **Background opening.** A file big enough for the read to be noticed (16 MiB
  and up) is streamed on its own goroutine: the call returns in under a
  millisecond, the status line reports progress ("opening big.txt — 43%"), and
  the buffer you had stays readable and editable. A 256 MB file reads in ~1.2 s
  with the UI free the whole time.
- **Edits made while a file loads are protected.** The synchronous open made
  that impossible; now, if the buffer changed while the file was being read,
  Gaze asks before replacing it instead of silently dropping the typing.
- **A second open supersedes the first.** Each open request is numbered, and a
  read whose number is no longer current aborts at its next chunk, so a file
  cannot install itself over a newer one. Quitting abandons in-flight reads too.

### Changed

- Small files are still read inline — it takes no time and avoids a frame of
  nothing happening — so only the opens that used to freeze take the new path.
- The visual-row index of a background-loaded document is built in the reader
  goroutine (`buildRowIndex`) instead of on the UI goroutine, which is most of
  what remained of the install pause: ~0.4 s for 256 MB instead of ~0.9 s.
- The post-open step (the `file:line:col` caret jump, and session restore's
  caret) now runs after the document is really installed, including when a
  recovery snapshot is offered first — it used to run against the old buffer.

## [1.7.0] - 2026-11-04

Files of a few hundred megabytes now open in seconds instead of being turned
away, and a keystroke in one costs a fraction of a millisecond.

### Added

- **A 1 GiB open limit** (was 256 MiB), with the size dialog and the refusal
  message saying what an open actually costs in memory.

### Changed

- **Opening streams the file into the page store.** `docBuilder` fills 64 KB
  pages, normalises line endings and records line starts as the bytes arrive,
  so the file is never held as one string and the peak is the file plus its
  index: a 256 MB file opens in ~2 s with ~340 MB of heap, a 1 GB file in ~9 s
  with ~1.3 GB. Runs without a carriage return or NUL are copied in bulk.
- **UTF-16 is decoded while reading** through a streaming `utf16Reader`, so a
  large UTF-16 file no longer needs a full byte slice and a full rune slice
  before it can be shown. `readDocument` (stdin, tests) shares the same sniffer.
- **Line starts are indexed in blocks of 4096** (a block base plus an int32),
  so typing shifts one base per block instead of one offset per line: a
  keystroke in a 5.5M-line file went from 12 ms to ~0.1 ms.
- Text metrics are cached per theme size instead of calling `fyne.MeasureText`
  on every query, the wrap index walks pages directly and skips lines that
  cannot wrap (2.4 s -> 0.4 s for 5.5M lines), and the wrap index is patched
  rather than rebuilt when an edit changes the line count.
- Case-insensitive find compares ASCII bytes against a folded query instead of
  calling `strings.EqualFold` at every position (17 s -> 1.6 s for a full pass
  over 256 MB), and the wrap-around search is skipped when the search already
  started at the top of the file.

### Fixed

- The wrap index was wrong after an edit that joined two lines: the joined
  line's own row count has to be added, not the row count of the line that was
  swallowed.
- `decodeUTF16` (and the whole-buffer read it needed) is gone; unpaired
  surrogates still become U+FFFD and a truncated UTF-16 stream is still
  reported rather than silently dropped.

## [1.6.0] - 2026-11-03

`widget.Entry` is gone: Gaze draws and edits every file itself, so a 40 MB log
and a shopping list now behave identically — same wrap, same undo, same counts,
same crash recovery.

### Added

- **Word wrap in large-file mode.** The code view wraps a line into visual rows
  itself (breaking after the last space that fits, splitting words that are
  wider than the window) and keeps a visual-row index so scrolling, the
  scrollbar, the caret and mouse hits all work in row space. The index is
  patched in place after an edit instead of being rebuilt, so typing stays cheap
  in a 600k-line file.
- **Live word and character counts for large files.** The count streams the
  document's pages off the UI goroutine, debounced after typing pauses, and the
  readout simply omits the numbers until the first count arrives.
- **Crash recovery for large files.** A snapshot is streamed page by page from
  the document, so an unsaved 40 MB buffer survives a crash; snapshots of big
  documents are taken every 2 minutes rather than every 30 seconds.

### Changed

- **One editing surface.** `editor.entry`, the normal/large area switch and the
  snapshot-based undo stack are gone. `setContentAs` always builds a `Document`,
  and large-file mode now only decides which chrome is kept (save-time trim, a
  short undo history) instead of which widget edits the text.
- Undo, redo, select all, clipboard, the line commands and find/replace all run
  through the code view: one history entry per command, typed runs coalesced,
  and each entry remembers the dirty flag so undoing back to the loaded state
  clears the "•" marker again.
- Saves always stream the document snapshot through the same encoding, EOL and
  trim path; the save-time trim is mirrored back into the buffer as one undoable
  edit rather than a `SetText` rewrite.
- The status readout is cached, so a keystroke no longer forces a window relayout.

### Fixed

- Loading a document while a save was in flight could let that save's trim
  rewrite land in the new buffer; the request now carries an edit sequence.
- Zooming or switching theme did not re-measure the editing surface unless the
  file was large.
- Undo after a save-time trim restored the untrimmed text as a separate step
  that could not be redone.

## [1.5.0] - 2026-11-02

Large files now get their own editor surface, and `widget.Entry` is out of the
picture for them entirely.

### Added

- `codeView`, a canvas-rendered editing widget: it draws only the lines on
  screen (~35 `canvas.Text` objects), keeps the caret as a document position and
  edits the `Document` directly. Because it implements `fyne.Scrollable`, Fyne
  clips it, and because it draws its own scrollbar it needs no `Scroll`
  container.
- Full editing in large-file mode: typing, Enter/Backspace/Delete, Tab and
  Shift+Tab, arrows/Home/End/PageUp/PageDown with Shift selection, Ctrl
  word-wise and document-wise movement, click to place the caret, drag to
  select (with scrolling past the edges), double-click to select a word, cut /
  copy / paste / select-all, and horizontal scrolling for long lines.
- A real undo history for large files: edits record the text they replaced, so
  undo and redo work across the whole document and consecutive typing is undone
  as one run. The line operations and replace-all are single steps.
- The line operations, find/replace, go-to-line and session restore are
  re-expressed against the document rather than a string window.

### Changed

- Scrolling redraws a page in ~0.3 ms instead of refilling a 114-line window in
  ~25 ms: no more hiccup at window boundaries, and no boundaries to hit.
- The line index is spliced in place on an edit instead of being rebuilt, so a
  keystroke costs one add per line below the caret rather than a 4.8 MB copy.
  A keystroke in a 600k-line file dropped from 4.3 ms to 1.4 ms.
- A 24 MB / 600k-line file opens in ~80 ms with ~5 MB of heap (was ~150 ms).
- The window title and the status readout are only updated when their text
  actually changes, so keystrokes no longer force a chrome relayout for nothing.

### Removed

- The windowed `Entry` viewport from 1.4.0 (and its 40-line margin, refills and
  window-bounded selection), replaced by `codeView`.

## [1.4.0] - 2026-11-02

Large files are now **editable**, not just openable.

### Added

- Windowed document store (`Document`): the buffer lives in 64 KB pages with a
  byte-offset line index, so an edit touches only the pages it overlaps and line
  lookup is a binary search. A 24 MB, 600k-line file opens in ~150 ms with
  ~5 MB of heap — the same file in `widget.Entry` needs hundreds of MB and
  minutes of layout.
- A viewport widget that shows one window of the document (visible lines plus a
  40-line margin) in a `widget.Entry` and draws its own scrollbar. Scrolling
  inside the window is a `Move`; the window is refilled only when it runs out.
  Because the viewport implements `fyne.Scrollable`, Fyne clips it for free.
- Large-file editing now covers everything: typing, caret movement that follows
  off either end of the window, go-to-line, indent/outdent, move/duplicate line,
  find & replace across the whole file, and saving the whole file.

### Changed

- Saving a large-file document streams page by page through the same encoding,
  line-ending and trim path as a normal save, so it never builds a copy.
- `finishSave` decides whether the buffer moved on using an edit sequence number
  instead of comparing strings, which is what makes it work for both modes.

### Fixed

- Refilling the window no longer discards edits that had not reached the document
  yet: a refill commits the window first.
- Caret callbacks no longer re-enter the refill (`SetText` moves the caret, which
  used to refill again).
- Font zoom and window resizes rebuild the window, so no blank strip appears
  below the last line and jump targets stay on screen.

## [1.3.0] - 2026-11-01

### Added

- Open size limits. Fyne lays out the whole buffer, so a big enough file used to
  exhaust memory and take the window down with it. Files over 16 MiB now ask
  before opening and load in **large-file mode** (word wrap, live word/character
  counts, undo snapshots and crash recovery off); files over 256 MiB are refused
  with an explanation. Oversized stdin is refused the same way.

### Changed

- Saving streams the encoding, line-ending translation and whitespace trim
  straight into the temp file. For a 48 MB document that is ~0.1 MB of allocation
  instead of ~47 MB per save.
- Crash-recovery snapshots are streamed the same way rather than copying the
  buffer into a byte slice first.
- Indentation detection only looks at the first 64 KiB of a file, and trimming
  trailing whitespace builds one copy instead of two.

### Fixed

- Opening a large file no longer splits the entire buffer into a `[]string` just
  to guess its indentation (16 bytes of header per line, before Fyne had laid out
  a single character).
- A file that grows past the limit between the size check and the read is refused
  instead of being silently truncated into the buffer — truncation would have
  looked like a complete document and saved back short.

## [1.2.3] - 2026-10-28

### Added

- Crash recovery for untitled buffers. Buffers with no file were never
  snapshotted, so they were exactly the ones a crash could lose; they are now
  written to a draft slot and offered back at the next launch.

### Changed

- Saves run on a background goroutine and so do recovery snapshots, so a slow
  disk or network mount no longer freezes the window. Edits typed while a save
  is in flight are kept and leave the document dirty.
- Word and character counts are recounted once typing pauses for large buffers,
  instead of on every keystroke.
- `debug.FreeOSMemory` only runs for buffers over 8 MiB, since it stops the
  world.
- Status messages clear themselves after a few seconds.
- Saving warns when the file was changed on disk by another program since it was
  opened, instead of overwriting it silently.

### Fixed

- A column on the command line (`gaze file:10:5`) put the caret one character to
  the right. The column is 1-based, like the line.
- **Regex search treated the search offset as the start of the buffer.** `^`
  matched mid-file and `\b` fired inside a word, so find could skip matches and
  replace could rewrite text that was not a match at all.
- **Discarding unsaved changes left the recovery snapshot behind**, so the edits
  the user threw away were offered back the next time the same file was opened.
- Replace All jumped the caret to the top of the buffer; it now stays where it
  was.
- Trimming trailing whitespace on save discarded the undo history. The rewrite
  is now undoable like any other whole-buffer command.
- Esc moved focus into the editor even when the find bar was closed, stealing
  the keystroke from whatever had focus.
- `gaze -v` reported a hard-coded version and could disagree with the tag it was
  built from. The version is now stamped in with `-X main.version` by both the
  Makefile and the release workflow.

## [1.2.2] - 2026-10-20

### Added

- Encoding awareness. UTF-8, UTF-8 with a byte-order mark, and UTF-16 (LE and
  BE) are detected, edited as UTF-8 internally, and written back in their
  original form. The status bar names the encoding when it is not plain UTF-8.
- Light and dark theme screenshots in the README.

### Fixed

- **Saving a UTF-16 file destroyed it.** Files were read as raw bytes and written
  straight back, so UTF-16 content loaded as garbage and the mangled buffer was
  then saved over the original. UTF-16 files now round-trip intact.
- Binary data, UTF-32, and UTF-16 without a byte-order mark are refused with an
  explanation instead of being opened and corrupted on save.
- **Whole-buffer commands could not be undone.** `widget.Entry` clears its undo
  stack on `SetText`, and the line operations and find & replace all rewrite the
  buffer that way. Gaze now keeps a bounded snapshot stack around those rewrites
  and consults Entry's stack first, so typing still undoes natively and in the
  right order.
- Undoing back to the just-loaded state clears the unsaved-changes marker again.

## [1.2.1] - 2026-10-19

### Added

- `LICENSE`, MIT.
- Application icon: `packaging/icon.svg` as the source, with `Icon.png` rendered
  from it, installed by `make install` and shipped in the Linux archive. The
  desktop entry now refers to it instead of a stock theme icon.
- `make icon` to re-render the icon from source.

### Changed

- App ID is now `io.github.amirrezasaadi.gaze-editor`, replacing
  `com.gaze.editor`, which implied ownership of a domain the project does not
  own.

  **This moves the settings directory.** Preferences and the crash-recovery
  cache live under `~/.config/<app-id>/` and are not migrated, so upgrading
  gives you a fresh configuration.

## [1.2.0] - 2026-10-19

Initial public release.

- Monospaced editing surface with Light and Dark themes
- Atomic saves, crash-recovery autosave snapshots, session restore
- Recent files, and drag & drop to open
- Find & replace with wrap-around, match case, regular expressions with `$1`
  capture groups, and a persisted search history
- Go to line, and `file:line:col` on the command line
- Line operations: indent/outdent, move line up/down, duplicate line
- Line-ending awareness (LF, CRLF, CR) and optional trim of trailing whitespace
  on save
- Word wrap, font zoom, persisted preferences
- Live status bar
- Linux, Windows and macOS builds

[Unreleased]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.3...v1.3.0
[1.2.3]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.2...v1.2.3
[1.2.2]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/AmirrezaSaadi/gaze-editor/releases/tag/v1.2.0
