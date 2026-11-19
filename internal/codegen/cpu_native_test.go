// Why: cpu features must hold on the real machine: what simd.Cpu() reports
// and what a cpu function's generic callees are compiled with.
// What: TestCpuNative (the reported set is closed under the table's
// implications), TestInheritNative (a generic instance created by a call
// from a cpu function gets its features), TestBaselineIgnoresCpuFlag.
// How: mk runs as a process (simdMkExe) on small programs on this machine.

package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

// TestCpuNative: Has("sse2") is true on every x86_64 machine, and
// every feature Cpu() reports has each feature its row implies reported too
// (3.4's table, through CPUClosure), at dev and release.
func TestCpuNative(t *testing.T) {
	needLLVM(t)
	if runtime.GOARCH != "amd64" {
		t.Skip("the x86_64 feature table; this host is " + runtime.GOARCH)
	}
	var names []string
	var src strings.Builder
	src.WriteString("import std.simd\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    c := simd.Cpu()\n")
	for _, f := range types.CPUFeatures() {
		if f.Arch != types.ArchX64 {
			continue
		}
		i := len(names)
		names = append(names, f.Name)
		fmt.Fprintf(&src, "    f%d := c.Has(%q)\n    try Print(mut out, \"%s {f%d}\\n\", (f%d))\n", i, f.Name, f.Name, i, i)
	}
	src.WriteString("}\n")
	exe := simdMkExe(t)
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"run"}
			if prof == "release" {
				args = append(args, "--release")
			}
			cmd := exec.Command(exe, append(args, "main.mk")...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("mk %v: %v\n%s", args, err, out)
			}
			has := map[string]bool{}
			for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				f := strings.Fields(ln)
				if len(f) != 2 || (f[1] != "true" && f[1] != "false") {
					t.Fatalf("line %q of:\n%s", ln, out)
				}
				has[f[0]] = f[1] == "true"
			}
			if len(has) != len(names) {
				t.Fatalf("%d features printed, want %d:\n%s", len(has), len(names), out)
			}
			if !has["sse2"] || !has["x86-64-v2"] {
				t.Errorf("sse2 %v, x86-64-v2 %v: every 1.0 x86_64 host has both", has["sse2"], has["x86-64-v2"])
			}
			for _, n := range names {
				if !has[n] {
					continue
				}
				set, _ := types.CPUClosure(types.ArchX64, []string{n})
				for _, m := range set {
					if !has[m] {
						t.Errorf("Cpu() has %s but not %s, which it implies", n, m)
					}
				}
			}
			t.Logf("%s", strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", "; "))
		})
	}
}

// TestInheritNative: testdata/simd/inherit's SumN[8] is called from
// Sum8 (cpu "x86-64-v3") and from Sum8Base. At dev nothing is inlined, so
// the 8-lane add appears as a 256-bit vpaddd only if mono made SumN[8] an
// instance of its own with avx2 and codegen called it from Sum8 (Sum8's own
// body has no vector operation). TestDifferentialNative runs the same
// package and compares the evaluator with the run.
func TestInheritNative(t *testing.T) {
	bin := needLLVM(t)
	if runtime.GOARCH != "amd64" {
		t.Skip("x86_64 instructions; this host is " + runtime.GOARCH)
	}
	exe := simdMkExe(t)
	dir := t.TempDir()
	for _, n := range []string{"main.mk", "pkg.mk"} {
		b, err := os.ReadFile(filepath.Join("testdata", "simd", "inherit", n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "inherit")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command(exe, "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mk build: %v\n%s", err, b)
	}
	dis := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", out)
	n := 0
	for _, ln := range strings.Split(dis, "\n") {
		if strings.Contains(ln, "vpaddd") && strings.Contains(ln, "ymm") {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("no 256-bit vpaddd in the dev build: SumN[8] was not compiled with Sum8's features")
	}
}

// TestBaselineIgnoresCpuFlag: the unsafe-call rule judges a cpu
// call against the target's baseline and never --cpu's, so the same source
// draws the same E1101 under every --cpu (mk check takes no --cpu at all).
func TestBaselineIgnoresCpuFlag(t *testing.T) {
	needLLVM(t)
	src := `Fast(x: i32): i32 cpu "avx2" {
    return x
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Fast(1)
    try Print(mut out, "{r}\n", (r))
}
`
	exe := simdMkExe(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var first string
	for _, cpu := range []string{"", "x86-64-v3", "x86-64-v4", "native"} {
		args := []string{"build", "--json", "-o", filepath.Join(dir, "out.exe")}
		if cpu != "" {
			args = append(args, "--cpu", cpu)
		}
		cmd := exec.Command(exe, append(args, "main.mk")...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("--cpu %q: built a call of a cpu function outside unsafe", cpu)
		}
		s := string(out)
		if !strings.Contains(s, `"E1101"`) || !strings.Contains(s, "cpu-call") {
			t.Fatalf("--cpu %q: want E1101 cpu-call, got:\n%s", cpu, s)
		}
		if first == "" {
			first = s
		} else if s != first {
			t.Errorf("--cpu %q changed the diagnostics:\n%s\nwant:\n%s", cpu, s, first)
		}
	}
}
