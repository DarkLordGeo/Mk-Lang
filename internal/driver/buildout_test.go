// Why: mk build -o names a file or a directory, and doc/tools.md shows
// `mk build --release -o out/`; a directory must never be the rename's
// target (Windows answers "Access is denied").
// What: TestBuildOutDir: -o naming an existing directory, and -o ending in a
// separator naming none yet, each write <dir>/<bin>[.exe].
// How: two Builds of one file with the host's toolchain; nothing runs.

package driver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildOutDir(t *testing.T) {
	tools := needTools(t)
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(nutshellProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	exe := "main" + exeSuffix(HostTarget())
	existing := t.TempDir()
	fresh := filepath.Join(t.TempDir(), "out")
	for _, c := range []struct{ out, dir string }{
		{existing, existing},
		{fresh + string(filepath.Separator), fresh},
	} {
		s, err := NewSession(Config{Root: file, Tools: tools, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(c.out)
		if err != nil || len(r.Report.Diags) > 0 {
			t.Fatalf("mk build -o %s: %v\n%s", c.out, err, r.Report.Text(r.Sources))
		}
		want := filepath.Join(c.dir, exe)
		if len(paths) != 1 || filepath.Clean(paths[0]) != want {
			t.Errorf("mk build -o %s wrote %v, want %s", c.out, paths, want)
		}
		if fi, err := os.Stat(want); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("mk build -o %s: %s is not a file: %v", c.out, want, err)
		}
	}
}
