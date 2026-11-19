// Why: a memory ordering nobody explains is one nobody can check; each
// atomic must name the happens-before edge it creates.
// What: TestRwRt_AtomicsNameTheirEdge over sys (each target), sync, atomic,
// free, time and mmio; std/rt-sched_atomics_test.go covers std/rt and std/task.
// How: rtsAtomicScan over each module's non-test files wants a comment naming
// the edge within rtsAtomicWindo lines above every atomic operation.

package std

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// rwRtEdgeDirs are the directories this test scans, each as one set (an
// atomic declared in one module is used through its own module's code).
var rwRtEdgeDirs = []string{"sys/linux", "sys/windows", "sys/nutshell", "sync", "atomic", "free", "time", "mmio", "runtime", "vm", "sim"}

func TestRwRt_AtomicsNameTheirEdge(t *testing.T) {
	total := 0
	for _, dir := range rwRtEdgeDirs {
		m, err := filepath.Glob(filepath.Join(filepath.FromSlash(dir), "*.mk"))
		if err != nil {
			t.Fatal(err)
		}
		var files []string
		for _, f := range m {
			if !strings.HasPrefix(filepath.Base(f), "tests_") {
				files = append(files, f)
			}
		}
		sort.Strings(files)
		text := map[string][]string{}
		for _, f := range files {
			text[f] = rtsReadLines(t, f)
		}
		sites, missing := rtsAtomicScan(files, text)
		for _, s := range missing {
			t.Errorf("atomic operation without an edge comment within %d lines: %s", rtsAtomicWindo, s)
		}
		t.Logf("%s: %d atomic sites, %d without an edge comment", dir, sites, len(missing))
		total += sites
	}
	if total < 200 {
		t.Fatalf("found only %d atomic sites: the scan is broken", total)
	}
}
