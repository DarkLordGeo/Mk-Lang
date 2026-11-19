// Why: the time of a one-function edit build is the number an editing user
// feels, and a profile of it shows where that time goes.
// What: TestPerfMemory_EditProbe, a harness for the ELF one-function edit
// measured in process.
// How: runs only when MK_EDITPROBE_DIR names a package on disk with a bin
// (corpus.Generate(1, 100) with a Main); logs each edit build's wall time
// and stages through t.Log, optionally under a CPU profile, and asserts
// nothing about the times.

package driver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPerfMemory_EditProbe builds MK_EDITPROBE_DIR once for x86_64-linux
// into a fresh cache, then edits one call of part05/f04.mk's Entry
// MK_EDITPROBE_N times (default 8), each a new Session as `mk build` makes,
// and logs each build's wall time and stages. With MK_EDITPROBE_PROF set,
// the edit builds run under a CPU profile written there.
func TestPerfMemory_EditProbe(t *testing.T) {
	dir := os.Getenv("MK_EDITPROBE_DIR")
	if dir == "" {
		t.Skip("MK_EDITPROBE_DIR is not set")
	}
	n := 8
	if v, err := strconv.Atoi(os.Getenv("MK_EDITPROBE_N")); err == nil && v > 0 {
		n = v
	}
	target, _, ok := TargetNamed("x86_64-linux")
	if !ok {
		t.Fatal("no x86_64-linux target")
	}
	cache := t.TempDir()
	out := filepath.Join(t.TempDir(), "bin")
	file := filepath.Join(dir, "part05", "f04.mk")
	orig, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer os.WriteFile(file, orig, 0o644)
	build := func() (time.Duration, string) {
		var tm bytes.Buffer
		t0 := time.Now()
		s, err := NewSession(Config{Root: dir, CacheDir: cache, Target: target, GenView: true, Timings: &tm})
		if err != nil {
			t.Fatal(err)
		}
		res, _, err := s.Build(out)
		el := time.Since(t0)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Report.Diags) > 0 {
			t.Fatalf("build reported %d diagnostics: %+v", len(res.Report.Diags), res.Report.Diags[0])
		}
		_ = res.WriteTimings(&tm)
		return el, tm.String()
	}
	el, _ := build()
	t.Logf("cold build %v", el.Round(time.Millisecond))
	if p := os.Getenv("MK_EDITPROBE_PROF"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			t.Fatal(err)
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}
	for i := 1; i <= n; i++ {
		src := strings.Replace(string(orig), "    t += F0193(n, t % 13)", fmt.Sprintf("    t += F0193(n, t %% %d)", 13+i), 1)
		if src == string(orig) {
			t.Fatal("the edit line is not in part05/f04.mk")
		}
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		el, tm := build()
		var stages []string
		for _, l := range strings.Split(tm, "\n") {
			if strings.HasPrefix(l, "stage ") {
				stages = append(stages, strings.TrimPrefix(l, "stage "))
			}
		}
		t.Logf("edit %d: %v; %s", i, el.Round(100*time.Microsecond), strings.Join(stages, ", "))
	}
}
