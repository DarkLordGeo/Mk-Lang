// Why: on a warm cache, a canonical round of mk fix --std std.core
// --canonical must still see the subject's bodies when the subject is in
// the prelude closure, which resolveClosure loads before the build's own
// pipeline.
// What: TestCanonWarm_StdCoreSubject and its helper canonStdCore.
// How: rewrites a for-in candidate in a std.core file of a std copy, cold and
// after a check has warmed the cache, and expects the same rewrite.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonWarm_StdCoreSubject: a for-in candidate in a std.core file of a
// std copy is rewritten by mk fix --canonical --std=std.core after a check
// has warmed the cache, as it is cold.
func TestCanonWarm_StdCoreSubject(t *testing.T) {
	t.Parallel()
	for _, warmFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "warm"}[warmFirst], func(t *testing.T) {
			t.Parallel()
			canonStdCore(t, warmFirst)
		})
	}
}

func canonStdCore(t *testing.T, warmFirst bool) {
	const coreFile = `struct CountWarm {
    n: int
}

CountWarm.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

export SumWarm(k: int): int {
    var total := 0
    var it := CountWarm(n: k)
    for x := it.Next() {
        total += x
    }
    return total
}
`
	dir := copyStd(t, map[string]string{"core/canonwarm.mk": coreFile}, nil)
	cache := t.TempDir()
	cfg := Config{StdTests: "std.core", StdDir: dir, CacheDir: cache}
	warm, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if r := warm.Check(); len(r.Report.Diags) != 0 {
		t.Fatalf("std copy does not check clean:\n%s", diagLines(r))
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{Canonical: true})
	if err != nil || res.Stopped != "clean" {
		t.Fatalf("mk fix --canonical --std=std.core: %v %q", err, res.Stopped)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "core", "canonwarm.mk"))
	if !strings.Contains(string(got), "    for x in CountWarm(n: k) {\n") {
		t.Errorf("warm cache: the for-in round did not run (rounds %+v):\n%s", res.Rounds, got)
	}
}
