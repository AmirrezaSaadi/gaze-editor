package editor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Downloaded language definitions.
//
// A definition is data, so downloading one is safe: it is JSON, it is validated
// before it is written, its patterns go through Go's linear-time regexp, and
// nothing in it is ever executed. What it can cost is colour, at worst.
//
// The files live in the same langs/ directory of the same GitHub repository that
// ships them, so a definition can be added or fixed without a release. The base
// URL is compiled in — a definition cannot point Gaze somewhere else — and the
// file name in the manifest has to be a plain name, so nothing can be written
// outside the languages directory.

// langBaseURL is where definitions are fetched from. It is a variable only so
// tests can point it at a local server.
var langBaseURL = "https://raw.githubusercontent.com/AmirrezaSaadi/gaze-editor/main/langs/"

// langInstallTimeout bounds a download, so a stalled connection cannot leave a
// dialog spinning forever.
const langInstallTimeout = 30 * time.Second

// maxManifestBytes caps the index of available languages.
const maxManifestBytes = 512 << 10

// langFileName is what a definition may be called: a plain name, no directory
// and no traversal.
var langFileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*\.json$`)

// languageDir is where downloaded definitions are kept. It is a variable so
// tests can use a temporary directory.
var languageDir = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gaze", "languages")
}

// manifest is the index of definitions available for download.
type manifest struct {
	Schema    int             `json:"schema"`
	Languages []manifestEntry `json:"languages"`
}

// manifestEntry is one downloadable definition.
type manifestEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Version int    `json:"version"`
	Bytes   int    `json:"bytes"`
	SHA256  string `json:"sha256"`
}

// httpClient is the client used for definitions: a timeout, and the proxy the
// environment asks for.
var httpClient = &http.Client{Timeout: langInstallTimeout}

// fetchManifest asks GitHub what is available.
func fetchManifest(ctx context.Context) (*manifest, error) {
	data, err := fetchLangFile(ctx, "index.json", maxManifestBytes)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("the language index is not readable: %v", err)
	}
	if m.Schema > grammarSchema {
		return nil, fmt.Errorf("the language index needs a newer Gaze (format %d)", m.Schema)
	}
	return &m, nil
}

// fetchLangFile downloads one file from the languages directory, refusing
// anything bigger than limit.
func fetchLangFile(ctx context.Context, name string, limit int64) ([]byte, error) {
	if !langFileName.MatchString(name) && name != "index.json" {
		return nil, fmt.Errorf("%q is not a language file", name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, langBaseURL+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", name, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %s", name, humanBytes(limit))
	}
	return data, nil
}

// installLanguage downloads and installs one definition from the manifest.
func (e *Editor) installLanguage(ctx context.Context, entry manifestEntry) error {
	dir := languageDir()
	if dir == "" {
		return fmt.Errorf("there is no configuration directory to install into")
	}
	data, err := fetchLangFile(ctx, entry.File, maxGrammarBytes)
	if err != nil {
		return err
	}
	if entry.SHA256 != "" {
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, entry.SHA256) {
			return fmt.Errorf("%s did not match the checksum in the index", entry.File)
		}
	}
	g, err := parseGrammar(data)
	if err != nil {
		return fmt.Errorf("%s: %v", entry.File, err)
	}
	g.path = filepath.Join(dir, entry.File)
	return writeLanguage(dir, entry.File, data, g)
}

// installLanguageFile reads a definition the user picked from disk and installs
// it, so a language can be added without the network at all.
func (e *Editor) installLanguageFile(path string) error {
	dir := languageDir()
	if dir == "" {
		return fmt.Errorf("there is no configuration directory to install into")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGrammarBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxGrammarBytes {
		return fmt.Errorf("%s is larger than %s", filepath.Base(path), humanBytes(maxGrammarBytes))
	}
	g, err := parseGrammar(data)
	if err != nil {
		return fmt.Errorf("%s: %v", filepath.Base(path), err)
	}
	// Do not quietly replace a definition that is already installed from a file:
	// two definitions claiming the same id would only confuse the matching.
	for _, prev := range loadLanguageDir() {
		if prev.id == g.id {
			return fmt.Errorf("%s is already installed — remove it first", prev.name)
		}
	}
	name := g.id + ".json"
	g.path = filepath.Join(dir, name)
	return writeLanguage(dir, name, data, g)
}

// writeLanguage writes a definition and makes the editor notice it.
func writeLanguage(dir, name string, data []byte, g *grammar) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}
	g.path = path
	return nil
}

// removeLanguage deletes an installed definition. Definitions compiled into the
// binary cannot be removed, only the ones that were added.
func removeLanguage(g *grammar) error {
	if g == nil || g.builtin || g.path == "" {
		return fmt.Errorf("%s is built into Gaze and cannot be removed", nameOf(g))
	}
	return os.Remove(g.path)
}

// nameOf is a nil-safe name for messages.
func nameOf(g *grammar) string {
	if g == nil {
		return "this language"
	}
	return g.name
}

// loadLanguageDir reads the definitions installed under the user's config
// directory. A broken file is skipped rather than failing the whole set.
func loadLanguageDir() []*grammar {
	dir := languageDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*grammar
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		g, err := parseGrammar(data)
		if err != nil {
			continue
		}
		g.path = path
		out = append(out, g)
	}
	return out
}

// languages is every definition Gaze can use: the ones compiled in, then the
// ones installed by the user. A user definition with the same id replaces the
// built-in one, so a fixed grammar can be shipped without waiting for a release.
func (e *Editor) languages() []*grammar {
	if e.langs != nil {
		return e.langs
	}
	all := append([]*grammar(nil), builtinGrammars()...)
	seen := map[string]bool{}
	for _, g := range all {
		seen[g.id] = true
	}
	for _, g := range loadLanguageDir() {
		if seen[g.id] {
			for i, prev := range all {
				if prev.id == g.id && prev.builtin {
					all[i] = g
					break
				}
			}
			continue
		}
		seen[g.id] = true
		all = append(all, g)
	}
	e.langs = all
	return all
}

// forgetLanguages drops the cached list after an install or a removal.
func (e *Editor) forgetLanguages() {
	e.langs = nil
	e.rebuildLanguageMenu()
	e.applySyntax()
}
