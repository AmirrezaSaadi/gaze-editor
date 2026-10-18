# packaging

## `gaze.desktop`

The freedesktop launcher installed by `make install`. It refers to the icon by
its theme name (`Icon=gaze`), so the icon has to be installed alongside it —
the `install` target in the top-level `Makefile` drops it into
`share/icons/hicolor/512x512/apps/gaze.png`.

## `icon.svg`

The source of truth for the Gaze icon: a rounded-square tile with a soft purple
aura, a white four-pointed star with a neon glow, and the `GAZE` wordmark.

`Icon.png` at the repository root is *rendered* from this file. That PNG is the
asset Fyne reads when packaging and the one the desktop entry points at — don't
edit it by hand.

### Re-rendering

```sh
make icon     # rsvg-convert -w 512 -h 512 -o Icon.png packaging/icon.svg
```

Two dependencies affect the result:

- **librsvg** (`rsvg-convert`). The artwork uses `feGaussianBlur` (a three-layer
  neon glow) plus linear and radial gradients. A rasteriser without filter
  support drops the glow silently, leaving a flat star.
- **Plus Jakarta Sans** (SIL Open Font License 1.1). The wordmark asks for
  weight 800 of the variable font. Without it, fontconfig substitutes the
  default sans — usually Noto Sans — and the letterforms change noticeably. The
  font is deliberately not vendored here; fetch it from Google Fonts if you
  need a byte-identical re-render.
