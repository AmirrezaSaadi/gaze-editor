package editor

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// ShowLanguages opens the languages dialog: what is installed, what can be
// downloaded from the project's repository, and a way to install a definition
// straight from a file.
//
// Downloads are never automatic. Nothing is fetched until a button is pressed,
// and the URL is fixed in the binary, so the dialog cannot be pointed anywhere
// else by a definition or by a file it reads.
func (e *Editor) ShowLanguages() {
	if e.win == nil {
		return
	}
	installed := container.NewVBox()
	available := container.NewVBox()
	note := widget.NewLabel("")
	note.Wrapping = fyne.TextWrapWord

	var refreshInstalled func()
	var setAvailable func(*manifest)

	refreshInstalled = func() {
		installed.RemoveAll()
		for _, g := range e.languages() {
			installed.Add(e.installedLanguageRow(g, refreshInstalled))
		}
		installed.Refresh()
	}
	refreshInstalled()

	content := container.NewVBox(
		widget.NewLabelWithStyle("Installed", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		installed,
		widget.NewLabelWithStyle("Available to download", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		available,
	)

	// The list of downloads is fetched when the dialog opens, so a new definition
	// shows up without a new release of the editor.
	setAvailable = func(m *manifest) {
		available.RemoveAll()
		have := map[string]bool{}
		for _, g := range e.languages() {
			have[g.id] = true
		}
		n := 0
		for _, entry := range m.Languages {
			if have[entry.ID] {
				continue
			}
			available.Add(e.availableLanguageRow(entry, note, setAvailable, refreshInstalled))
			n++
		}
		if n == 0 {
			available.Add(widget.NewLabel("Everything in the index is already installed."))
		}
		available.Refresh()
	}
	fetch := func() {
		note.SetText("Looking for languages to download…")
		available.RemoveAll()
		available.Add(widget.NewLabel("…"))
		available.Refresh()
		go func() {
			m, err := fetchManifest(context.Background())
			fyne.Do(func() {
				if err != nil {
					available.RemoveAll()
					available.Add(widget.NewLabel("Could not reach the language index."))
					available.Refresh()
					note.SetText(err.Error() + " — you can still install a definition from a file.")
					return
				}
				note.SetText("")
				setAvailable(m)
			})
		}()
	}

	installFile := func() {
		dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, e.win)
				return
			}
			if r == nil {
				return
			}
			defer r.Close()
			if err := e.installLanguageFile(r.URI().Path()); err != nil {
				note.SetText("Could not install it: " + err.Error())
				return
			}
			e.forgetLanguages()
			refreshInstalled()
			note.SetText("Installed " + r.URI().Name() + ".")
		}, e.win)
	}

	buttons := container.NewHBox(
		widget.NewButton("Check for Downloads", fetch),
		widget.NewButton("Install from File…", installFile),
	)
	body := container.NewBorder(nil, container.NewVBox(note, buttons), nil, nil,
		container.NewVScroll(container.NewVBox(content, widget.NewSeparator())))
	body.Resize(fyne.NewSize(560, 420))

	d := dialog.NewCustom("Languages", "Close", body, e.win)
	d.Resize(fyne.NewSize(560, 460))
	d.Show()
	fetch()
}

// installedLanguageRow is one line of the installed list.
func (e *Editor) installedLanguageRow(g *grammar, refresh func()) fyne.CanvasObject {
	where := "built in"
	if !g.builtin && g.path != "" {
		where = "installed"
	}
	detail := where
	if exts := grammarExtensions(g); exts != "" {
		detail += " · " + exts
	}
	text := container.NewVBox(
		widget.NewLabel(g.name),
		widget.NewLabelWithStyle(detail, fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
	)
	use := widget.NewButton("Use", func() { e.UseLanguage(g) })
	actions := container.NewHBox(use)
	if !g.builtin {
		g := g
		actions.Add(widget.NewButton("Remove", func() {
			if err := removeLanguage(g); err != nil {
				return
			}
			e.forgetLanguages()
			refresh()
		}))
	}
	return container.NewBorder(nil, nil, nil, actions, text)
}

// availableLanguageRow is one line of the download list.
func (e *Editor) availableLanguageRow(entry manifestEntry, note *widget.Label, setAvailable func(*manifest), refresh func()) fyne.CanvasObject {
	text := container.NewVBox(
		widget.NewLabel(entry.Name),
		widget.NewLabelWithStyle(fmt.Sprintf("version %d · %s", entry.Version, humanBytes(int64(entry.Bytes))),
			fyne.TextAlignLeading, fyne.TextStyle{Italic: true}),
	)
	install := widget.NewButton("Install", func() {
		note.SetText("Downloading " + entry.Name + "…")
		go func() {
			err := e.installLanguage(context.Background(), entry)
			fyne.Do(func() {
				if err != nil {
					note.SetText("Could not install " + entry.Name + ": " + err.Error())
					return
				}
				e.forgetLanguages()
				refresh()
				note.SetText(entry.Name + " installed — it is used the next time a matching file is opened.")
				// Drop it from the list of what is available.
				if m, merr := fetchManifest(context.Background()); merr == nil {
					fyne.Do(func() { setAvailable(m) })
				}
			})
		}()
	})
	return container.NewBorder(nil, nil, nil, install, text)
}

// grammarExtensions describes what a definition claims, for the dialog.
func grammarExtensions(g *grammar) string {
	var parts []string
	parts = append(parts, g.filenames...)
	for _, e := range g.exts {
		parts = append(parts, "."+e)
	}
	return strings.Join(parts, ", ")
}
