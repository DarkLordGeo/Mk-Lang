// Why: mk check opens the build cache, so without care these tests would write
// .mk-cache directories into tests/programs and tests/conformance.
// What: TestMain and runWithCache.
// How: when MK_CACHE is unset, points it at a temporary directory for the
// whole package run and removes it afterwards.

package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives the package's tests (and the mk processes they start) one
// cache directory of their own when MK_CACHE is unset: mk check opens the
// store, and a test that checks a path inside the repository
// (tests/programs, tests/conformance) with no Config.CacheDir would otherwise
// write <that dir>/.mk-cache. Tests that need a fresh cache pass
// Config.CacheDir (check's helper does).
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
