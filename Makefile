BINARY := gaze
PKG    := ./cmd/gaze

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

.PHONY: run build release upx test vet tidy install uninstall clean size

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
	@echo "installed $(BINDIR)/$(BINARY)"

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(APPDIR)/$(BINARY).desktop

size: build
	@ls -l $(BINARY)
