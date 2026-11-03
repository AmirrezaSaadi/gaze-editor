# Changelog

All notable changes to Gaze are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases are published on the
[releases page](https://github.com/AmirrezaSaadi/gaze-editor/releases).

## [Unreleased]

Nothing yet.

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
