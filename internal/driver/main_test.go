// Why: mk check opens the build store, so a test that checks a path inside the
// repository without its own cache would leave a .mk-cache directory there.
// What: TestMain, which runs the package's tests with one shared cache.
// How: when MK_CACHE is unset, runWithCache points it at a temporary directory
// for the whole run and removes the directory afterwards.

package driver

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives the package's tests one cache directory of their own
// when MK_CACHE is unset: mk check opens the store, and a test
// that checks a path inside the repository (tests/programs, tests/conformance)
// with no Config.CacheDir would otherwise write <that dir>/.mk-cache.
// Tests that need a fresh cache pass Config.CacheDir (check's helper does).
func TestMain(m *testing.M) {
	os.Exit(runWithCache(m))
}

func runWithCache(m *testing.M) int {
	if os.Getenv("MK_CACHE") == "" {
		dir, err := os.MkdirTemp("", "mk-driver-test-cache-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		os.Setenv("MK_CACHE", dir)
	}
	return m.Run()
}
