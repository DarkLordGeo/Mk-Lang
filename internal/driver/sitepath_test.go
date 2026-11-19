// Why: a panic site's file is package-relative, so a report reads the same
// on every machine, while a diagnostic cites a command-line file as given.
// What: sitePathOf and TestSitePathPackageRelative.
// How: loads one file in single-file mode with the root given absolute and
// with a directory, and compares codegen's Env.Path with the SourceMap's.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sitePathOf is the path codegen's Env gives the SourceMap file at sm.
func sitePathOf(t *testing.T, w *world, sm string) string {
	t.Helper()
	f, ok := w.sm.ByPath(sm)
	if !ok {
		t.Fatalf("no source file %q", sm)
	}
	return cgEnv{w}.Path(f.ID)
}

// TestSitePathPackageRelative: in single-file mode the package root
// is the file's directory, so a site cites the file's name whether the root
// was given absolute or with a directory; the SourceMap, which diagnostics
// read, keeps the path as given. A package file is cited by its path under
// the root, a std file as std/<dir>/<file>.mk, a corpus unit's file by its
// unit-relative path.
func TestSitePathPackageRelative(t *testing.T) {
	stdDir := fakeStd(t)
	dir := writeTree(t, map[string]string{"sub/one.mk": "G() {\n}\n"})

	abs := filepath.Join(dir, "sub", "one.mk")
	s, err := NewSession(Config{Root: abs, StdDir: stdDir})
	if err != nil {
		t.Fatal(err)
	}
	s.Check()
	given := filepath.ToSlash(abs)
	if got := sitePathOf(t, s.w, given); got != "one.mk" {
		t.Errorf("absolute root: site path %q, want one.mk", got)
	}
	if f := s.w.sm.Files(); !strings.HasSuffix(given, "/sub/one.mk") || len(f) == 0 {
		t.Fatalf("source map %d files, given %q", len(f), given)
	}
	if got := sitePathOf(t, s.w, "std/fs/fs.mk"); got != "std/fs/fs.mk" {
		t.Errorf("std file: site path %q", got)
	}

	wd, _ := os.Getwd()
	t.Chdir(dir)
	defer t.Chdir(wd)
	s, err = NewSession(Config{Root: "sub" + string(filepath.Separator) + "one.mk", StdDir: stdDir})
	if err != nil {
		t.Fatal(err)
	}
	s.Check()
	if got := sitePathOf(t, s.w, "sub/one.mk"); got != "one.mk" {
		t.Errorf("relative root: site path %q, want one.mk", got)
	}

	pkg := writeTree(t, map[string]string{"pkg.mk": manifest(nil), "main.mk": "Main() {\n}\n", "lib/l.mk": "export L() {\n}\n"})
	s, err = NewSession(Config{Root: pkg, StdDir: stdDir})
	if err != nil {
		t.Fatal(err)
	}
	s.Check()
	if got := sitePathOf(t, s.w, "lib/l.mk"); got != "lib/l.mk" {
		t.Errorf("package file: site path %q", got)
	}

	c, err := NewCorpusSession(Config{StdDir: stdDir})
	if err != nil {
		t.Fatal(err)
	}
	c.CheckUnit(UnitInput{Name: "u#1", Files: []UnitFile{{"block.mk", "G() {\n}\n"}}})
	if got := sitePathOf(t, c.w, "u#1/block.mk"); got != "block.mk" {
		t.Errorf("corpus unit: site path %q, want block.mk", got)
	}
}
