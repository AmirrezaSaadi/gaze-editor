// Package langs holds the syntax definitions Gaze ships with.
//
// The directory doubles as the download source: the same files are served from
// https://raw.githubusercontent.com/AmirrezaSaadi/gaze-editor/main/langs/, so a
// definition can be added or fixed without cutting a release, and Gaze can fetch
// it from inside the editor (View → Languages…).
package langs

import (
	"embed"
	"io/fs"
)

//go:embed *.json
var files embed.FS

// Files is the embedded set of definitions, index.json included.
func Files() fs.FS { return files }
