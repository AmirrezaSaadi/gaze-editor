package editor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// langServer serves a languages directory over HTTP and points the editor at it.
type langServer struct {
	*httptest.Server
	files map[string]string
}

func newLangServer(t *testing.T, files map[string]string) *langServer {
	t.Helper()
	ls := &langServer{files: files}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/langs/")
		data, ok := ls.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, data)
	}))
	t.Cleanup(ls.Close)

	oldURL, oldDir := langBaseURL, languageDir
	langBaseURL = ls.URL + "/langs/"
	dir := t.TempDir() // the same directory for the whole test
	languageDir = func() string { return dir }
	t.Cleanup(func() { langBaseURL, languageDir = oldURL, oldDir })
	return ls
}

// entryFor builds a manifest entry for a definition, checksum included.
func entryFor(id, name, data string) manifestEntry {
	sum := sha256.Sum256([]byte(data))
	return manifestEntry{
		ID: id, Name: name, File: id + ".json", Version: 1,
		Bytes: len(data), SHA256: hex.EncodeToString(sum[:]),
	}
}

func manifestJSON(entries ...manifestEntry) string {
	m := manifest{Schema: 1, Languages: entries}
	data, _ := json.Marshal(m)
	return string(data)
}

const testGrammar = `{"schema":1,"id":"braces","name":"Braces","extensions":["brc"],"keywords":["open","close"]}`

func TestFetchManifest(t *testing.T) {
	newLangServer(t, map[string]string{"index.json": manifestJSON(entryFor("braces", "Braces", testGrammar))})
	m, err := fetchManifest(context.Background())
	if err != nil {
		t.Fatalf("fetchManifest: %v", err)
	}
	if len(m.Languages) != 1 || m.Languages[0].ID != "braces" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestFetchManifestRejectsBadIndex(t *testing.T) {
	cases := map[string]string{
		"not json":    "{",
		"newer":       `{"schema":9,"languages":[]}`,
		"too large":   `{"schema":1,"languages":[` + strings.Repeat(" ", maxManifestBytes) + `]}`,
		"missing":     "",
		"server down": "",
	}
	for name, body := range cases {
		files := map[string]string{}
		if body != "" {
			files["index.json"] = body
		}
		newLangServer(t, files)
		if _, err := fetchManifest(context.Background()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Installing downloads the definition, checks it against the checksum in the
// index and writes it where the editor will find it.
func TestInstallLanguage(t *testing.T) {
	newLangServer(t, map[string]string{
		"index.json":  manifestJSON(entryFor("braces", "Braces", testGrammar)),
		"braces.json": testGrammar,
	})
	e := newTestEditor(t)
	e.forgetLanguages()
	if got := grammarFor("x.brc", e.languages()); got != nil {
		t.Fatalf("%v was already installed", got)
	}

	m, err := fetchManifest(context.Background())
	if err != nil {
		t.Fatalf("fetchManifest: %v", err)
	}
	if err := e.installLanguage(context.Background(), m.Languages[0]); err != nil {
		t.Fatalf("installLanguage: %v", err)
	}
	e.forgetLanguages()

	g := grammarFor("x.brc", e.languages())
	if g == nil || g.name != "Braces" || g.builtin {
		t.Fatalf("installed language = %+v", g)
	}
	if _, err := os.Stat(g.path); err != nil {
		t.Fatalf("the definition was not written: %v", err)
	}
	if !strings.HasPrefix(g.path, languageDir()) {
		t.Fatalf("installed outside the languages directory: %s", g.path)
	}
}

func TestInstallLanguageRejectsBadChecksum(t *testing.T) {
	entry := entryFor("braces", "Braces", testGrammar)
	entry.SHA256 = strings.Repeat("0", 64)
	newLangServer(t, map[string]string{
		"index.json":  manifestJSON(entry),
		"braces.json": testGrammar,
	})
	e := newTestEditor(t)
	if err := e.installLanguage(context.Background(), entry); err == nil {
		t.Fatal("a definition that did not match its checksum was installed")
	}
	if _, err := os.Stat(filepath.Join(languageDir(), "braces.json")); err == nil {
		t.Fatal("a rejected definition was written anyway")
	}
}

// The file name in an index is not a path: nothing may be written outside the
// languages directory, and no other URL may be fetched.
func TestInstallLanguageRejectsPaths(t *testing.T) {
	for _, name := range []string{"../evil.json", "/etc/passwd", "sub/dir.json", "braces.txt", ""} {
		entry := manifestEntry{ID: "braces", Name: "Braces", File: name, Version: 1}
		newLangServer(t, map[string]string{
			"index.json":   manifestJSON(entry),
			"braces.json":  testGrammar,
			"../evil.json": testGrammar,
		})
		e := newTestEditor(t)
		if err := e.installLanguage(context.Background(), entry); err == nil {
			t.Errorf("%q was accepted as a definition name", name)
		}
	}
}

// A definition that is not a definition is refused before it reaches the disk.
func TestInstallLanguageRejectsBrokenGrammar(t *testing.T) {
	broken := `{"schema":1,"id":"braces"}`
	newLangServer(t, map[string]string{
		"index.json":  manifestJSON(entryFor("braces", "Braces", broken)),
		"braces.json": broken,
	})
	e := newTestEditor(t)
	entry := entryFor("braces", "Braces", broken)
	if err := e.installLanguage(context.Background(), entry); err == nil {
		t.Fatal("a definition with no extensions was installed")
	}
}

// Installing from a file works with no network at all, and the editor picks it up
// as soon as it is there.
func TestInstallLanguageFile(t *testing.T) {
	old := languageDir
	dir := t.TempDir()
	languageDir = func() string { return dir }
	t.Cleanup(func() { languageDir = old })

	e := newTestEditor(t)
	src := writeTestFile(t, "braces.json", testGrammar)
	if err := e.installLanguageFile(src); err != nil {
		t.Fatalf("installLanguageFile: %v", err)
	}
	e.forgetLanguages()
	g := grammarFor("a.brc", e.languages())
	if g == nil || g.name != "Braces" {
		t.Fatalf("language = %v", g)
	}
	if g.builtin {
		t.Fatal("a file-installed definition is marked as built in")
	}

	// Installing the same id again is refused rather than quietly replacing the
	// first copy: two definitions with one id would only confuse the matching.
	if err := e.installLanguageFile(src); err == nil {
		t.Fatal("the same definition was installed twice")
	}
	entries, err := os.ReadDir(languageDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d files after installing the same definition twice", len(entries))
	}

	// And it can be removed again.
	e.forgetLanguages()
	if err := removeLanguage(g); err != nil {
		t.Fatalf("remove: %v", err)
	}
	e.forgetLanguages()
	if got := grammarFor("a.brc", e.languages()); got != nil {
		t.Fatalf("%v survived removal", got)
	}
}

func TestRemoveBuiltinIsRefused(t *testing.T) {
	g := grammarByID(t, "go")
	if err := removeLanguage(g); err == nil {
		t.Fatal("a built-in language was removed")
	}
	if err := removeLanguage(nil); err == nil {
		t.Fatal("removing nil succeeded")
	}
}

// A broken file in the languages directory is skipped, not fatal: one bad
// download must not take the others with it.
func TestLoadLanguageDirSkipsBroken(t *testing.T) {
	dir := t.TempDir()
	old := languageDir
	languageDir = func() string { return dir }
	t.Cleanup(func() { languageDir = old })

	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte(testGrammar), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadLanguageDir()
	if len(got) != 1 || got[0].id != "braces" {
		t.Fatalf("loaded %d definitions: %+v", len(got), got)
	}
}

// The dialog opens and the index is fetched off the UI goroutine; a failure to
// reach GitHub shows a message rather than blocking the window.
func TestLanguagesDialogOpens(t *testing.T) {
	newLangServer(t, map[string]string{
		"index.json": manifestJSON(
			entryFor("braces", "Braces", testGrammar),
			entryFor("go", "Go", testGrammar),
		),
		"braces.json": testGrammar,
	})
	e := newTestEditor(t)
	e.forgetLanguages()
	e.ShowLanguages()
	if n := len(e.win.Canvas().Overlays().List()); n == 0 {
		t.Fatal("the languages dialog did not open")
	}
	// The fetch is asynchronous: give it a moment so a panic inside it would be
	// caught by the test rather than the user.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	e.win.Canvas().Overlays().Top().Hide()
}

// An index that cannot be reached is reported, not fatal.
func TestLanguagesDialogSurvivesOffline(t *testing.T) {
	oldURL, oldDir := langBaseURL, languageDir
	dir := t.TempDir()
	langBaseURL = "http://127.0.0.1:1/langs/"
	languageDir = func() string { return dir }
	t.Cleanup(func() { langBaseURL, languageDir = oldURL, oldDir })

	e := newTestEditor(t)
	e.forgetLanguages()
	e.ShowLanguages()
	time.Sleep(100 * time.Millisecond)
	e.win.Canvas().Overlays().Top().Hide()
}
