package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLocation(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "notes.txt")
	colon := filepath.Join(dir, "weird:name.txt")
	for _, p := range []string{plain, colon} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		arg  string
		path string
		line int
		col  int
	}{
		{plain, plain, 0, 0},
		{plain + ":10", plain, 10, 0},
		{plain + ":10:5", plain, 10, 5},
		{colon, colon, 0, 0},
		{colon + ":7", colon, 7, 0},
		{dir + "/missing.txt:3", dir + "/missing.txt", 3, 0},
		{dir + "/missing.txt:3:9", dir + "/missing.txt", 3, 9},
		{dir + "/missing.txt", dir + "/missing.txt", 0, 0},
	}
	for _, c := range cases {
		path, line, col := parseLocation(c.arg)
		if path != c.path || line != c.line || col != c.col {
			t.Errorf("parseLocation(%q) = (%q,%d,%d), want (%q,%d,%d)",
				c.arg, path, line, col, c.path, c.line, c.col)
		}
	}
}
