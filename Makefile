BINARY := gaze
PKG    := ./cmd/gaze
ICON   := Icon.png

# no_emoji drops Fyne's bundled 4 MB emoji font (the system one is used
# instead); no_animations skips the cursor-blink and button tap animations,
# saving a little more code, CPU and memory.
# -s -w strips symbols and DWARF; -trimpath and an empty -buildid keep builds
# reproducible and small.
TAGS    := no_emoji,no_animations
GOFLAGS := -trimpath -tags $(TAGS)
LDFLAGS := -s -w -buildid=

# Where `make install` drops things (XDG-ish, no root needed).
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
APPDIR  ?= $(PREFIX)/share/applications
ICONDIR ?= $(PREFIX)/share/icons/hicolor/512x512/apps

.PHONY: run build release upx icon test vet tidy install uninstall clean size

# make run ARGS="notes.txt"  (ARGS is optional)
run:
	go run $(GOFLAGS) $(PKG) $(ARGS)

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "built ./$(BINARY) — $$(du -h $(BINARY) | cut -f1)"

# release additionally compresses the binary with UPX when it is installed.
release: build upx

upx:
	@if command -v upx >/dev/null 2>&1; then \
		upx --best --lzma $(BINARY); \
	else \
		echo "upx not installed; skipping compression"; \
	fi

# Re-render $(ICON) from packaging/icon.svg. Needs rsvg-convert, and Plus
# Jakarta Sans installed for the wordmark to match the committed PNG — see
# packaging/README.md before regenerating.
icon:
	@command -v rsvg-convert >/dev/null 2>&1 || { \
		echo "error: rsvg-convert not found (try: apt install librsvg2-bin)"; exit 1; }
	rsvg-convert -w 512 -h 512 -o $(ICON) packaging/icon.svg
	@echo "rendered $(ICON) — $$(du -h $(ICON) | cut -f1)"

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

# Install the binary and a freedesktop .desktop entry into ~/.local.
install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	install -d $(DESTDIR)$(APPDIR)
	install -m 0644 packaging/$(BINARY).desktop $(DESTDIR)$(APPDIR)/$(BINARY).desktop
	install -d $(DESTDIR)$(ICONDIR)
	install -m 0644 $(ICON) $(DESTDIR)$(ICONDIR)/$(BINARY).png
	@echo "installed $(BINDIR)/$(BINARY)"

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(APPDIR)/$(BINARY).desktop
	rm -f $(DESTDIR)$(ICONDIR)/$(BINARY).png

size: build
	@ls -l $(BINARY)
