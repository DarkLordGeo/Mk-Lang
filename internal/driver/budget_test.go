// Why: the build cache must stay under its size budget across many edits,
// or a long editing session fills the disk.
// What: TestCacheStaysUnderBudget and dirBytes, the size of a directory tree.
// How: fifty release builds after one-function edits into one small-budget
// cache, with the store's clock moved forward so eviction may act.

package driver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mk/internal/types"
)

// dirBytes is the size of every file under dir.
func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCacheStaysUnderBudget: fifty release builds, each after a different
// one-function edit, into one cache with a small budget. After each command
// (which ends with MaybeGC) the cache root, the ThinLTO directory included,
// is under the budget. The budget is the cache one cold build leaves plus
// 1/40 of it, and the store's clock advances three hours per command
// (CacheNow), past cache.Grace. Measured by this test with the default
// budget on a development machine: each edit adds about 10 KB (a pack's
// object, the module entries, the ThinLTO files), so without eviction the
// bound breaks after about 14 builds.
func TestCacheStaysUnderBudget(t *testing.T) {
	tools := needTools(t)
	builds := 50
	if testing.Short() {
		builds = 8
	}
	dir := writeTree(t, incPkg("x + 0"))
	cache := t.TempDir()
	cfg := Config{Root: dir, CacheDir: cache, Target: types.X86_64Windows, Profile: types.Release, Tools: tools}
	frontBuild(t, cfg, filepath.Join(t.TempDir(), "app.exe"))
	one := dirBytes(t, cache)
	budget := one + one/40
	cfg.CacheBudget = budget
	// Each command runs three hours after the last on the store's clock,
	// so the previous commands' entries are past cache.Grace (2 h) and
	// may be evicted; real time would keep every entry for two hours.
	var clock time.Time = time.Now()
	cfg.TestHooks.CacheNow = func() time.Time { return clock }
	lib := filepath.Join(dir, "lib", "lib.mk")
	src := incPkg("x + 0")["lib/lib.mk"]
	peak := int64(0)
	for i := 1; i <= builds; i++ {
		clock = clock.Add(3 * time.Hour)
		if err := os.WriteFile(lib, []byte(strings.Replace(src, "x + 0", fmt.Sprintf("x + %d", i), 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		frontBuild(t, cfg, filepath.Join(t.TempDir(), "app.exe"))
		got := dirBytes(t, cache)
		peak = max(peak, got)
		if got > budget {
			t.Fatalf("after build %d the cache holds %d bytes, over the budget of %d", i, got, budget)
		}
	}
	t.Logf("one cold release build: %d bytes; budget %d; peak after %d builds: %d", one, budget, builds, peak)
}
