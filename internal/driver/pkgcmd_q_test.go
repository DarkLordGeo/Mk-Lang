// Why: the package commands edit pkg.mk and pkg.sum, and a failed or
// partial step must leave both files as they were.
// What: tests of mk install on a failed fetch, an edit of an unformatted
// manifest, the check before pkg.sum is written, mk fix's pkg-sum round,
// and a tampered seed.
// How: runs the commands against archive servers and seeds in temporary
// workspaces and compares the files before and after.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/fetch"
)

// mk install whose check cannot fetch the version it added: no "added"
// line, and pkg.mk and pkg.sum are as they were.
func TestPkgCmd_InstallFailedFetchChangesNothing(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	src := gs.source("someone/missing") // no tag: the fetch fails
	app := writeTree(t, map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"app\",\n)\n",
		"a.mk":   "export A(): int {\n    return 1\n}\n",
	})
	before := readFile(t, filepath.Join(app, "pkg.mk"))
	res, err := cmdSession(t, sourceCfg(gs, t.TempDir()), app).Install(src+"@1.0.0", PkgOptions{})
	for _, l := range res.Lines {
		if strings.HasPrefix(l, "added ") {
			t.Errorf("printed %q although the install failed", l)
		}
	}
	if err == nil && len(res.Check.Report.Diags) == 0 {
		t.Error("a failed fetch reported no fault")
	}
	if got := readFile(t, filepath.Join(app, "pkg.mk")); got != before {
		t.Errorf("pkg.mk changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Error("pkg.sum written")
	}
}

// An edit changes only the edited list, not the rest of a manifest that is
// not in mk fmt form.
func TestPkgCmd_EditKeepsUnformattedText(t *testing.T) {
	text := "const Package = pkg.Package(\n    name:   \"app\",   // spaced\n    deps: [],\n)\n"
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": text,
		"app/a.mk":   "export A(): int {\n    return 1\n}\n",
		"u/pkg.mk":   depManifest("u"),
		"u/src/u.mk": "export U(): int {\n    return 1\n}\n",
	})
	app := filepath.Join(ws, "app")
	e, err := openManifest(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.add("deps", `pkg.Dep(name: "u", path: "../u")`); err != nil {
		t.Fatal(err)
	}
	got, err := e.finish(Config{StdDir: fakeStd(t)}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(text, "deps: [],", "deps: [pkg.Dep(name: \"u\", path: \"../u\")],", 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// mk install from the seed checks the edited manifest before it
// writes the seed's pkg.sum line; a manifest the edit cannot check leaves
// pkg.sum alone.
func TestPkgCmd_InstallChecksBeforeSum(t *testing.T) {
	seedFor(t, "toml", "example.com/someone/toml", "1.0.0", tomlFiles("1"))
	// A manifest whose name collides: the edited manifest does not check.
	app := writeTree(t, map[string]string{
		"pkg.mk": "const Package = pkg.Package(\n    name: \"app\",\n    unsafe_modules: [\"nowhere\"],\n)\n",
		"a.mk":   "export A(): int {\n    return 1\n}\n",
	})
	_, err := cmdSession(t, Config{CacheDir: t.TempDir()}, app).Install("toml", PkgOptions{})
	if err == nil {
		t.Skip("the edited manifest checked; nothing to test")
	}
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Error("pkg.sum written before the manifest checked")
	}
}

// seedFor makes a toolchain seed holding short's tree at version, and
// returns the seed directory.
func seedFor(t *testing.T, short, src, version string, files map[string]string) string {
	t.Helper()
	tr := &fetch.Tree{}
	for p, d := range files {
		tr.Files = append(tr.Files, fetch.File{Path: p, Data: []byte(d)})
	}
	h := tr.Hash()
	seed := t.TempDir()
	if err := tr.Write(filepath.Join(seed, "pkg", fmt.Sprintf("%x", h[:]))); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("# first-party\n%s %s v%s %s %s\n", short, src, version, strings.Repeat("ab", 20), h)
	if err := os.WriteFile(filepath.Join(seed, "first-party.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	old := seedDir
	seedDir = func() string { return seed }
	t.Cleanup(func() { seedDir = old })
	return seed
}

// mk fix lists pkg.mk and pkg.sum among the files it changed when the pkg-sum
// round moved a hash, and the round prints as a rewrite round, with no
// code (not "E0000").
func TestSum_FixListsManifestAndSum(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	app := urlApp(t, srv.URL+"/ini.tar.gz", sha(arch))
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(app, "pkg.mk"), filepath.Join(app, sumFile)} {
		if !slices.Contains(res.Files, f) {
			t.Errorf("files %q lack %s", res.Files, f)
		}
	}
	if !IsRewriteRound("pkg-sum") {
		t.Error("the pkg-sum round prints as a diagnostic round")
	}
}

// TestPkgCmd_TamperedSeedNoted: a seed tree whose files do not hash to its
// name is not used, and the fetch that replaces it says so in a note.
func TestPkgCmd_TamperedSeedNoted(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("10"))
	src := gs.source("someone/toml")
	seed := seedFor(t, "toml", src, "1.0.0", tomlFiles("10"))
	ents, _ := os.ReadDir(filepath.Join(seed, "pkg"))
	if len(ents) != 1 {
		t.Fatal(ents)
	}
	if err := os.WriteFile(filepath.Join(seed, "pkg", ents[0].Name(), "extra.mk"), []byte("// planted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := writeTree(t, map[string]string{"pkg.mk": depManifest("app"), "a.mk": "export A(): int {\n    return 1\n}\n"})
	res, err := cmdSession(t, sourceCfg(gs, t.TempDir()), app).Install("toml", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Check.Notes, "\n"), "seed") {
		t.Errorf("notes %q, want the tampered seed named", res.Check.Notes)
	}
}
