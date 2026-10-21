# Changelog

All notable changes to Gaze are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases are published on the
[releases page](https://github.com/AmirrezaSaadi/gaze-editor/releases).

## [Unreleased]

Nothing yet.

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

[Unreleased]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.2...HEAD
[1.2.2]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/AmirrezaSaadi/gaze-editor/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/AmirrezaSaadi/gaze-editor/releases/tag/v1.2.0
