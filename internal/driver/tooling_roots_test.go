// Why: a function with a const value parameter has no instance without its
// const arguments, so treating it as a root would ask mono for something
// that cannot exist.
// What: TestRootsLeaveOutConstParamFunctions.
// How: builds a package with such a function and checks it is neither a
// limit root nor a build root, while its callers are.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRootsLeaveOutConstParamFunctions(t *testing.T) {
	dir := t.TempDir()
	src := `Rep(const n: int): int {
    return n * 2
}

Twice(x: int): int {
    return x + x
}

export Main(sys: &System) {
    _ = Rep(3) + Twice(1)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), CacheDir: filepath.Join(dir, ".cache")})
	if err != nil {
		t.Fatal(err)
	}
	r := s.Check()
	if len(r.Report.Diags) > 0 {
		t.Fatalf("check: %s", r.Report.Text(r.Sources))
	}
	keys := func(name string, ids []string) {
		t.Helper()
		if slices.Contains(ids, "main.Rep") {
			t.Errorf("%s roots hold main.Rep (const parameter): %q", name, ids)
		}
		for _, want := range []string{"main.Main", "main.Twice"} {
			if !slices.Contains(ids, want) {
				t.Errorf("%s roots miss %s: %q", name, want, ids)
			}
		}
	}
	var limit []string
	for _, rt := range s.w.limitRoots(s.pkgB) {
		limit = append(limit, s.w.items.Get(rt.Item).Key.String())
	}
	keys("limit", limit)
	var build []string
	for _, rt := range s.w.buildRoots("main", s.pkgB.mods, true, false) {
		build = append(build, s.w.items.Get(rt.Item).Key.String())
	}
	keys("build", build)
}
