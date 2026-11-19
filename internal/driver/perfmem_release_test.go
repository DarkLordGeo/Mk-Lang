// Why: releasing the front end's output after the check (Config.ReleaseFront,
// and GenView, which turns it on and which cmd/mk sets for check, build,
// run and test) saves memory, but a released field read later by native,
// testexec, prop or the deferred walk would change the output or crash.
// What: TestPerfMemory_ReleaseSameExe, over Session.Build and Session.Test.
// How: builds and tests incPkg with and without the release, cold and warm,
// with 1 and 8 workers, and compares the bytes of the executables and
// objects. The fields released are f.ast, m.info, m.units, cmod.byItem and
// cmod.files.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mk/internal/cache"
)

// TestPerfMemory_ReleaseSameExe: mk build of incPkg with the front end
// released gives the executable and objects of a build that keeps it, with
// the cache off at 1 and 8 workers, cold and warm into one cache, and with
// GenView; mk test over testsSource and mk test --prop over propClampHole
// report the same results with and without the release.
func TestPerfMemory_ReleaseSameExe(t *testing.T) {
	tools := needTools(t)
	t.Run("build", func(t *testing.T) {
		dir := writeTree(t, incPkg("x + 1"))
		out := t.TempDir()
		_, want, wantObjs := incBuild(t, Config{Root: dir, Tools: tools, NoCache: true}, filepath.Join(out, "keep"))
		cacheDir := t.TempDir()
		for _, c := range []struct {
			name string
			cfg  Config
		}{
			{"no cache, 1 worker", Config{Root: dir, Tools: tools, NoCache: true, ReleaseFront: true, Jobs: 1}},
			{"no cache, 8 workers", Config{Root: dir, Tools: tools, NoCache: true, ReleaseFront: true, Jobs: 8}},
			{"cold", Config{Root: dir, Tools: tools, CacheDir: cacheDir, ReleaseFront: true}},
			{"warm", Config{Root: dir, Tools: tools, CacheDir: cacheDir, ReleaseFront: true}},
			{"warm, 1 worker", Config{Root: dir, Tools: tools, CacheDir: cacheDir, ReleaseFront: true, Jobs: 1}},
			{"gen view", Config{Root: dir, Tools: tools, NoCache: true, GenView: true}},
		} {
			if strings.HasPrefix(c.name, "warm") {
				// A warm mk build reuses the stored link and links
				// no objects; without the stored executables it links the
				// cached objects, which are what this compares.
				if err := os.RemoveAll(cache.DirPath(cacheDir, cache.BinDir)); err != nil {
					t.Fatal(err)
				}
			}
			_, got, gotObjs := incBuild(t, c.cfg, filepath.Join(out, strings.ReplaceAll(c.name, " ", "_")))
			if !bytes.Equal(got, want) {
				t.Errorf("%s: executable differs from the build that keeps the front end", c.name)
			}
			if !objsEqual(gotObjs, wantObjs) {
				t.Errorf("%s: objects differ from the build that keeps the front end", c.name)
			}
		}
		// The comparison means something only if Build released: the
		// modules' ASTs and resolve Info are gone after it.
		s, err := NewSession(Config{Root: dir, Tools: tools, NoCache: true, ReleaseFront: true})
		if err != nil {
			t.Fatal(err)
		}
		if r, _, err := s.Build(filepath.Join(out, "probe")); err != nil || len(r.Report.Diags) > 0 {
			t.Fatalf("build: %v", err)
		}
		released := 0
		for _, m := range s.pkgB.mods {
			if m.path == "lib" || m.path == "app" {
				if m.info != nil || len(m.files) == 0 || m.files[0].ast != nil {
					t.Errorf("module %s kept its Info or AST after a released Build", m.path)
				}
				released++
			}
		}
		if released != 2 {
			t.Errorf("found %d of the modules lib and app", released)
		}
	})
	t.Run("test", func(t *testing.T) {
		dir := testPkg(t, map[string]string{"t.mk": testsSource})
		root := filepath.Join(dir, "t.mk")
		want := results(mkTest(t, Config{Root: root}, TestOptions{}))
		for _, cfg := range []Config{
			{Root: root, ReleaseFront: true, Jobs: 1},
			{Root: root, ReleaseFront: true, Jobs: 8},
			{Root: root, GenView: true},
		} {
			if got := results(mkTest(t, cfg, TestOptions{})); got != want {
				t.Errorf("release (jobs %d, gen view %v):\n%s\nwant (no release):\n%s", cfg.Jobs, cfg.GenView, got, want)
			}
		}
		// Session.Test releases too: the test module's AST and Info are
		// gone after it, so the comparison above is not vacuous.
		s, err := NewSession(Config{Root: root, Tools: tools, CacheDir: t.TempDir(), ReleaseFront: true, TestTimeout: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		if r, rep, err := s.Test(TestOptions{}); err != nil || rep == nil || len(r.Report.Diags) > 0 {
			t.Fatalf("mk test: %v", err)
		}
		found := false
		for _, m := range s.pkgB.mods {
			if m.path == "main" {
				found = true
				if m.info != nil || len(m.files) == 0 || m.files[0].ast != nil {
					t.Error("module main kept its Info or AST after a released Test")
				}
			}
		}
		if !found {
			t.Error("no module main in the test build")
		}
	})
	t.Run("prop", func(t *testing.T) {
		dir := testPkg(t, map[string]string{"t.mk": propClampHole})
		root := filepath.Join(dir, "t.mk")
		norm := func(s string) string { return strings.ReplaceAll(s, dir, "<root>") }
		_, want := propTest(t, nil, Config{Root: root, Tools: tools}, TestOptions{})
		if !strings.Contains(want, "Clamp(1, 0, 0)") {
			t.Fatalf("the no-release run did not find the hole: %s", want)
		}
		for _, cfg := range []Config{
			{Root: root, Tools: tools, ReleaseFront: true, Jobs: 1},
			{Root: root, Tools: tools, ReleaseFront: true, Jobs: 8},
			{Root: root, Tools: tools, GenView: true},
		} {
			// A fresh cache each run (propTest's default), so no run
			// replays another's corpus.
			if _, got := propTest(t, nil, cfg, TestOptions{}); norm(got) != norm(want) {
				t.Errorf("release (jobs %d, gen view %v):\n%s\nwant (no release):\n%s", cfg.Jobs, cfg.GenView, got, want)
			}
		}
	})
}
