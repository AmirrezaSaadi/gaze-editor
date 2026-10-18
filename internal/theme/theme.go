// Package theme provides Gaze's selectable Light and Dark themes, font zoom,
// and persistence of both. The stock Fyne themes are used directly; a thin
// wrapper scales font sizes for zoom without disturbing layout metrics.
package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	ftheme "fyne.io/fyne/v2/theme"
)

const (
	// NameLight is the stock Fyne light theme.
	NameLight = "light"
	// NameDark is the stock Fyne dark theme (the default).
	NameDark = "dark"

	prefTheme = "theme"
	prefZoom  = "zoom"

	// ZoomMin and ZoomMax bound the font zoom factor.
	ZoomMin = 0.6
	ZoomMax = 2.5

	zoomStep = 0.1
)

// Names is the ordered list of themes offered to users.
var Names = []string{NameLight, NameDark}

// Normalize maps any stored/unknown theme name onto a known theme.
func Normalize(name string) string {
	if name == NameLight {
		return NameLight
	}
	return NameDark
}

// New returns the Fyne theme for name. Unknown names fall back to dark.
func New(name string) fyne.Theme {
	if Normalize(name) == NameLight {
		return ftheme.LightTheme()
	}
	return ftheme.DarkTheme()
}

// scaleTheme multiplies text metrics of a base theme by factor. Colours, icons
// and structural sizes are left untouched so widgets stay aligned.
type scaleTheme struct {
	base   fyne.Theme
	factor float32
}

func (t *scaleTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return t.base.Color(n, v)
}

func (t *scaleTheme) Font(s fyne.TextStyle) fyne.Resource { return t.base.Font(s) }

func (t *scaleTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.base.Icon(n) }

func (t *scaleTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case ftheme.SizeNameText,
		ftheme.SizeNameHeadingText,
		ftheme.SizeNameSubHeadingText,
		ftheme.SizeNameCaptionText,
		ftheme.SizeNameLineSpacing:
		return t.base.Size(n) * t.factor
	}
	return t.base.Size(n)
}

// Manager keeps the active theme and zoom in sync with the running app and the
// persisted preferences.
type Manager struct {
	app  fyne.App
	name string
	zoom float32
}

// NewManager loads the persisted theme and zoom (defaults: dark, 100%) and
// applies them.
func NewManager(a fyne.App) *Manager {
	m := &Manager{
		app:  a,
		name: Normalize(a.Preferences().StringWithFallback(prefTheme, NameDark)),
		zoom: clampZoom(float32(a.Preferences().FloatWithFallback(prefZoom, 1))),
	}
	m.apply()
	return m
}

func (m *Manager) apply() {
	t := New(m.name)
	if m.zoom != 1 {
		t = &scaleTheme{base: t, factor: m.zoom}
	}
	m.app.Settings().SetTheme(t)
}

// Current reports the active theme name.
func (m *Manager) Current() string { return m.name }

// Zoom reports the active font zoom factor.
func (m *Manager) Zoom() float32 { return m.zoom }

// Set switches to name (unknown names fall back to dark) and persists it.
func (m *Manager) Set(name string) {
	m.name = Normalize(name)
	m.app.Preferences().SetString(prefTheme, m.name)
	m.apply()
}

// Toggle flips between light and dark.
func (m *Manager) Toggle() {
	if m.name == NameLight {
		m.Set(NameDark)
	} else {
		m.Set(NameLight)
	}
}

// Next returns the name of the theme after the current one, wrapping around.
func (m *Manager) Next() string {
	for i, n := range Names {
		if n == m.name {
			return Names[(i+1)%len(Names)]
		}
	}
	return Names[0]
}

// SetZoom applies and persists a font zoom factor, clamped to the supported range.
func (m *Manager) SetZoom(z float32) {
	m.zoom = clampZoom(z)
	m.app.Preferences().SetFloat(prefZoom, float64(m.zoom))
	m.apply()
}

// ZoomIn increases the font zoom by one step.
func (m *Manager) ZoomIn() { m.SetZoom(m.zoom + zoomStep) }

// ZoomOut decreases the font zoom by one step.
func (m *Manager) ZoomOut() { m.SetZoom(m.zoom - zoomStep) }

// ResetZoom restores the font zoom to 100%.
func (m *Manager) ResetZoom() { m.SetZoom(1) }

func clampZoom(z float32) float32 {
	if z < ZoomMin {
		return ZoomMin
	}
	if z > ZoomMax {
		return ZoomMax
	}
	return z
}
