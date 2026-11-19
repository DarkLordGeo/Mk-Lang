// Why: atomic.Spin yields through a weak MkThreadYield that only pauses; a
// hosted program must link std.rt's real OS yield (std/rt/yield.mk) instead.
// What: TestRt_SpinYieldHookLinked and checkYieldHook, Windows only.
// How: reads the PDB of a dev and a release build with llvm-pdbutil (skips
// without it, which is not a pass); std.task.Start keeps the hook reachable.
//
// The public MkThreadYield must sit at std.rt's procedure, not the weak
// default's; or, where LTO inlined the hook (release), SpinLock's inlinee
// MkThreadYield holds the inlinee ThreadYield, std.rt's body.

package std

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestRt_SpinYieldHookLinked(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the PDB check is Windows'; Linux runs nm in linux.sh")
	}
	pdbutil, err := exec.LookPath("llvm-pdbutil")
	if err != nil {
		if p := `C:\Program Files\LLVM\bin\llvm-pdbutil.exe`; fileExists(p) {
			pdbutil = p
		} else {
			t.Skip("llvm-pdbutil not found")
		}
	}
	root, stdDir := missStd(t)
	src := "import std.atomic\n\nexport Main(sys: &System) {\n    s := atomic.Spin[int].New(0)\n    var g := s.Lock()\n    g.Store(1)\n    g.Unlock()\n}\n"
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			dir := filepath.Join(root, prof)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"build", "-debug", "-o", filepath.Join(dir, "spin.exe"), "main.mk"}
			if prof == "release" {
				args = append([]string{"build", "-release"}, args[1:]...)
			}
			if out, status := mkCmd(t, dir, stdDir, args...); status != 0 {
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				t.Fatalf("mk build: status %d\n%s", status, out)
			}
			pdbs, _ := filepath.Glob(filepath.Join(dir, "*.pdb"))
			if len(pdbs) != 1 {
				t.Fatalf("want one PDB beside the build, got %v", pdbs)
			}
			pub, err := exec.Command(pdbutil, "dump", "-publics", pdbs[0]).CombinedOutput()
			if err != nil {
				t.Fatalf("llvm-pdbutil -publics: %v\n%s", err, pub)
			}
			syms, err := exec.Command(pdbutil, "dump", "-symbols", pdbs[0]).CombinedOutput()
			if err != nil {
				t.Fatalf("llvm-pdbutil -symbols: %v\n%s", err, syms)
			}
			checkYieldHook(t, string(pub), string(syms))
		})
	}
}

var (
	yieldPublic  = regexp.MustCompile("`MkThreadYield`\\s+flags = function, addr = ([0-9:]+)")
	yieldWeak    = regexp.MustCompile("`\\.weak\\.MkThreadYield\\.[^`]*`\\s+flags = function, addr = ([0-9:]+)")
	yieldProc    = regexp.MustCompile("S_GPROC32 \\[size = \\d+\\] `std::rt::MkThreadYield`\\s+parent = \\d+, end = \\d+, addr = ([0-9:]+)")
	yieldInlined = regexp.MustCompile(`(\d+) \| S_INLINESITE \[size = \d+\]\s+inlinee = 0x[0-9A-F]+ \(MkThreadYield\)`)
)

// checkYieldHook: the public MkThreadYield is std.rt's procedure, never the
// weak default; or, with no public symbol, the hook was inlined and its
// inlinee is ThreadYield (the weak default is one pause and calls nothing).
func checkYieldHook(t *testing.T, pub, syms string) {
	t.Helper()
	public := yieldPublic.FindStringSubmatch(pub)
	weak := yieldWeak.FindStringSubmatch(pub)
	proc := yieldProc.FindStringSubmatch(syms)
	if public == nil {
		for _, m := range yieldInlined.FindAllStringSubmatch(syms, -1) {
			if strings.Contains(syms, "(ThreadYield), parent = "+m[1]+",") {
				return
			}
		}
		t.Fatalf("no public MkThreadYield and no inlined std.rt hook whose inlinee is ThreadYield (weak default: %q)", weak)
	}
	if proc == nil || public[1] != proc[1] {
		t.Errorf("MkThreadYield resolves to %s, std.rt's hook is at %v (the weak default: %q)", public[1], proc, weak)
	}
	if weak != nil && weak[1] == public[1] {
		t.Errorf("MkThreadYield resolves to the weak default at %s", weak[1])
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
