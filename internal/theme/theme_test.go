package theme

import (
	"reflect"
	"testing"

	ftheme "fyne.io/fyne/v2/theme"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		NameLight:   NameLight,
		NameDark:    NameDark,
		"":          NameDark,
		"nonsense":  NameDark,
		"stargazer": NameDark,
		"starGAZEr": NameDark,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewThemes(t *testing.T) {
	if reflect.TypeOf(New(NameDark)) != reflect.TypeOf(ftheme.DarkTheme()) {
		t.Error("dark theme should be Fyne's stock dark theme")
	}
	if reflect.TypeOf(New(NameLight)) != reflect.TypeOf(ftheme.LightTheme()) {
		t.Error("light theme should be Fyne's stock light theme")
	}
}

func TestClampZoom(t *testing.T) {
	if got := clampZoom(0); got != ZoomMin {
		t.Errorf("clampZoom(0) = %v, want %v", got, ZoomMin)
	}
	if got := clampZoom(99); got != ZoomMax {
		t.Errorf("clampZoom(99) = %v, want %v", got, ZoomMax)
	}
	if got := clampZoom(1.5); got != 1.5 {
		t.Errorf("clampZoom(1.5) = %v, want 1.5", got)
	}
}
