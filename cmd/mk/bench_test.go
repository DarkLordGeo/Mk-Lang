// Why: published benchmark numbers must come from a quiet, timed run, so mk
// bench refuses --out unless MK_TIMING=1 is set.
// What: TestBenchOutRefusedWithoutMKTiming, plus argument errors of runBench.
// How: calls runBench with an empty environment and checks the exit status and
// stderr. The benchmark runs themselves are internal/driver's
// TestBenchRunsBenchmarksOnly.

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchOutRefusedWithoutMKTiming(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r.json")
	var so, se bytes.Buffer
	getenv := func(string) string { return "" }
	if st := runBench([]string{"--out", out, "x"}, &so, &se, getenv); st != 2 || !strings.Contains(se.String(), "needs MK_TIMING=1") {
		t.Errorf("--out without MK_TIMING: status %d, stderr %q", st, se.String())
	}
	se.Reset()
	if st := runBench([]string{"a", "b", "c"}, &so, &se, getenv); st != 2 {
		t.Errorf("three arguments: status %d", st)
	}
	se.Reset()
	if st := runBench([]string{"--baseline", filepath.Join(t.TempDir(), "missing.json")}, &so, &se, getenv); st != 2 || !strings.Contains(se.String(), "--baseline") {
		t.Errorf("a missing baseline: status %d, stderr %q", st, se.String())
	}
}
