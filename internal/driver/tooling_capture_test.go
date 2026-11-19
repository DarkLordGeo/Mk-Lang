// Why: capturing function literals cross mono's instances, the environment
// arguments, eval and code generation, and only whole programs exercise all
// of them together.
// What: capturePrograms, TestCaptureProgramsRun,
// TestCaptureContractPanicsAtDev and TestCompileTimeCapture.
// How: builds and runs each program of testdata/capture at dev and release
// with buildRunCapture and compares its exact standard output.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// capturePrograms are the capturing-literal programs, built and run through
// the driver at dev and release: mono's instances, the environment arguments
// and code generation end to end, which no lang package's tests reach.
// testdata/capture holds them; want is the exact standard output.
var capturePrograms = []struct {
	name string
	want string
}{
	{"g9b", "249\n"},
	{"g9c", "261\n"},
	{"g9d", "324 272\n"},
	{"gen5", "72\n"},
	{"gen6", "11\n"},
	{"p1", "true\n"},
	{"ens", "9\n"},
	{"inv", "2\n"},
	{"fl3", "6\n"},
	{"lsort", "4 7 2 9 \n"},
}

func TestCaptureProgramsRun(t *testing.T) {
	needTools(t)
	target := HostTarget()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			cache := t.TempDir()
			for _, p := range capturePrograms {
				t.Run(p.name, func(t *testing.T) {
					got, _, code := buildRunCapture(t, target, prof, cache, p.name)
					if code != 0 || got != p.want {
						t.Errorf("%s at %s prints %q (exit %d), want %q", p.name, prof, got, code, p.want)
					}
				})
			}
		})
	}
}

// TestCaptureContractPanicsAtDev: a capturing literal in a requires
// predicate that is false panics at dev with the contract's position.
func TestCaptureContractPanicsAtDev(t *testing.T) {
	needTools(t)
	target := HostTarget()
	out, errOut, code := buildRunCapture(t, target, types.Dev, t.TempDir(), "p1bad")
	if code == 0 || !strings.Contains(out+errOut, "panic: contract: requires at p1bad.mk:11:2") {
		t.Errorf("p1bad at dev: exit %d, stdout %q, stderr %q; want the requires panic", code, out, errOut)
	}
}

// TestCompileTimeCapture: a capturing literal in code the evaluator runs
// at compile time (a const initializer). eval resolves a direct call whose
// const operand is a capturing literal by mono's instance; without that the
// build is E0000 "3 arguments to std.core.Slice:Sort, which has 2
// parameters".
func TestCompileTimeCapture(t *testing.T) {
	needTools(t)
	target := HostTarget()
	got, _, code := buildRunCapture(t, target, types.Dev, t.TempDir(), "ct")
	if code != 0 || got != "4 4\n" {
		t.Errorf("ct prints %q (exit %d), want %q", got, code, "4 4\n")
	}
}

func buildRunCapture(t *testing.T, target types.Target, prof types.Profile, cache, name string) (string, string, int) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "capture", name+".mk"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, name+".mk")
	if err := os.WriteFile(file, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Root: file, Target: target, Profile: prof, CacheDir: cache}
	cfg.Tools = needTools(t)
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(dir, name+exeSuffix(target)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build %s at %s:\n%s", name, prof, r.Report.Text(r.Sources))
	}
	return runExe(t, target, paths[0], "")
}
