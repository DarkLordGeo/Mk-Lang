// Why: regression tests for code generation defects found in atomics, E1109,
// frame pointers and stack overflow reports.
// What: TestUnorderedLoopLinks, _E1109KernelHooks, _E1109AnotherBinsRoot,
// _CorpusFramePointers, _CLIDefaultsInDriverTests, _HelloBudgetCLIDefault and
// _OverflowSiteFramePointers.
// How: builds small packages with mk for Windows and x86_64-linux, in dev and
// release, and checks that they link, run and report as expected.

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// unordArrays is two 64-element array literals of New(a) and New(0) of
// the atomic type t.
func unordArrays(t string, a string) (string, string) {
	var x, y []string
	for range 64 {
		x = append(x, t+".New("+a+")")
		y = append(y, t+".New(0)")
	}
	return "[" + strings.Join(x, ", ") + "]", "[" + strings.Join(y, ", ") + "]"
}

func unordFixedMain() string {
	a, b := unordArrays("atomic.Atomic[u64]", "7")
	return `import std.atomic

CopyFixed(dst: &[64]atomic.Atomic[u64], src: &[64]atomic.Atomic[u64]) {
    for i in 0..64 {
        dst[i].Store(src[i].Load(atomic.Unordered), atomic.Unordered)
    }
}

export Main(sys: &System): WriteError!void {
    var a: [64]atomic.Atomic[u64] = ` + a + `
    var b: [64]atomic.Atomic[u64] = ` + b + `
    CopyFixed(&b, &a)
    s := b[3].Load(atomic.Relaxed)
    try sys.Print("{s}\n", (s,))
}
`
}

func unordRawK() string {
	a, b := unordArrays("raw.Atomic[u64]", "3")
	return `import std.raw

export Raw(n: int): u64 {
    _ = n
    var a: [64]raw.Atomic[u64] = ` + a + `
    var b: [64]raw.Atomic[u64] = ` + b + `
    for i in 0..64 {
        b[i].StoreOrd(a[i].LoadOrd(5), 5)
    }
    return b[1].Load()
}
`
}

const unordRawMain = `import k

export Main(sys: &System): WriteError!void {
    s := k.Raw(sys.args.Len() + 60)
    try sys.Print("{s}\n", (s,))
}
`

const unordRawPkg = `const Package = pkg.Package(
    name: "urawfix",
    bins: [pkg.Bin(name: "urawfix", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["k"],
    links: [],
)
`

// TestUnorderedLoopLinks: atomic.Unordered (order 5) is lowered as
// monotonic, so a counted copy loop of Unordered loads and stores links in
// release. LLVM's loop idiom would otherwise turn it into a call to
// __llvm_memcpy_element_unordered_atomic_8, which no Mk link provides.
func TestUnorderedLoopLinks(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"fixed", map[string]string{"main.mk": unordFixedMain()}, "7\n"},
		{"rawfix", map[string]string{"main.mk": unordRawMain, "pkg.mk": unordRawPkg, "k/k.mk": unordRawK()}, "3\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, c.files)
			for _, prof := range [][]string{nil, {"--release"}} {
				exe := filepath.Join(dir, c.name+strings.Join(prof, "")+".exe")
				if st, out, errOut := mk(append(append([]string{"build"}, prof...), "-o", exe, dir)...); st != 0 {
					t.Fatalf("mk build %v: status %d\n%s%s", prof, st, out, errOut)
				}
				// The host's own build runs natively (Windows, or Linux on
				// the Linux leg).
				got, err := exec.Command(exe).Output()
				if err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != c.want {
					t.Errorf("%s %v: %v, output %q, want %q", c.name, prof, err, got, c.want)
				}
				elf := filepath.Join(dir, c.name+strings.Join(prof, "")+".elf")
				args := append(append([]string{"build", "--target", "x86_64-linux"}, prof...), "-o", elf, dir)
				if st, out, errOut := mk(args...); st != 0 {
					t.Errorf("mk %v: status %d\n%s%s", args, st, out, errOut)
				}
			}
		})
	}
}

// e1109Pkg is a package of a dynamic library at its root and a second bin
// at root second, an executable.
func e1109Pkg(name, second string) string {
	return `const Package = pkg.Package(
    name: "` + name + `",
    bins: [pkg.Bin(name: "lib", root: ".", link_script: "", cpu: "", subsystem: "console", kind: "dynamic"), pkg.Bin(name: "` + second + `", root: "` + second + `", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`
}

// TestE1109KernelHooks: a package with a hosted dynamic library at "."
// and a freestanding kernel bin under it builds the kernel for
// x86_64-freestanding: no library is built for that target, so its
// MkPanicHook (`never`, no C form) and MkEntry are not judged by E1109.
func TestE1109KernelHooks(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pkg.mk": e1109Pkg("e1109k", "kernel"),
		"lib.mk": "export extern Add(a: i32, b: i32): i32 {\n    return a + b\n}\n",
		"kernel/main.mk": `import std.free

export extern MkPanicHook(info: &free.PanicInfo): never {
    for {
    }
}

export extern MkEntry() {
    for {
    }
}
`,
	})
	for _, prof := range [][]string{nil, {"--release"}} {
		out := filepath.Join(dir, "k"+strings.Join(prof, "")+".elf")
		args := append(append([]string{"build", "--bin", "kernel", "--target", "x86_64-freestanding"}, prof...), "-o", out, dir)
		if st, o, e := mk(args...); st != 0 {
			t.Errorf("mk %v: status %d\n%s%s", args, st, o, e)
		}
	}
}

// TestE1109AnotherBinsRoot: on a hosted target the library's
// own export externs are judged (Bad, E1109), but an executable bin whose
// root lies under the library's is not part of the library: its Count
// takes a slice, and the executable builds and runs.
func TestE1109AnotherBinsRoot(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"pkg.mk": e1109Pkg("hosted", "app"),
		"lib.mk": "export extern Add(a: i32, b: i32): i32 {\n    return a + b\n}\n\nexport extern Bad(x: []u8): i32 {\n    return Trunc[i32](x.Len())\n}\n",
		"app/main.mk": `export extern Count(x: []u8): i32 {
    return Trunc[i32](x.Len())
}

export Main(sys: &System): WriteError!void {
    r := Count("ab".AsBytes())
    try sys.Print("{r}\n", (r,))
}
`,
	})
	st, o, e := mk("check", dir)
	text := o + e
	if st == 0 || strings.Count(text, "E1109 no-c-form") != 1 || !strings.Contains(text, "lib.mk:5:") || strings.Contains(text, "main.mk") {
		t.Errorf("mk check: status %d, want one E1109 at lib.mk's Bad and none in app/main.mk:\n%s", st, text)
	}
	// The executable alone: its root is not the library's.
	writeTree(t, dir, map[string]string{"lib.mk": "export extern Add(a: i32, b: i32): i32 {\n    return a + b\n}\n"})
	exe := filepath.Join(dir, "app.exe")
	if st, o, e := mk("build", "--bin", "app", "-o", exe, dir); st != 0 {
		t.Fatalf("mk build --bin app: status %d\n%s%s", st, o, e)
	}
	if runtime.GOOS == "windows" {
		if got, err := exec.Command(exe).Output(); err != nil || strings.TrimSpace(string(got)) != "2" {
			t.Errorf("app: %v, output %q, want 2", err, got)
		}
	}
}

// TestCorpusFramePointers: mk corpus builds with mk build's
// frame-pointer setting, the default or MK_FRAME_POINTERS.
func TestCorpusFramePointers(t *testing.T) {
	var c corpusFlags
	t.Setenv("MK_FRAME_POINTERS", "")
	if cfg := c.driverConfig(io.Discard); cfg.FramePointers != framePointersDefault("") {
		t.Errorf("corpus FramePointers %v, want the CLI default for the host %v", cfg.FramePointers, framePointersDefault(""))
	}
	for _, v := range []string{"on", "off"} {
		t.Setenv("MK_FRAME_POINTERS", v)
		if cfg := c.driverConfig(io.Discard); cfg.FramePointers != (v == "on") {
			t.Errorf("MK_FRAME_POINTERS=%s: corpus FramePointers %v", v, cfg.FramePointers)
		}
	}
}

// TestCLIDefaultsInDriverTests: internal/driver's hello budget test
// builds with this command's frame-pointer default for each target (its
// cliFramePointersOff names the one target whose default is off).
func TestCLIDefaultsInDriverTests(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "driver", "hellosize_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("const cliFramePointersOff = %q\n", framePointersOffTarget)
	if !strings.Contains(strings.ReplaceAll(string(b), "\r\n", "\n"), want) {
		t.Errorf("internal/driver/hellosize_test.go has no %q", want)
	}
	if framePointersDefault(framePointersOffTarget) || !framePointersDefault("x86_64-linux") {
		t.Errorf("framePointersDefault: %s %v, x86_64-linux %v; want off and on", framePointersOffTarget,
			framePointersDefault(framePointersOffTarget), framePointersDefault("x86_64-linux"))
	}
}

// TestHelloBudgetCLIDefault: release hello built by mk build with no
// flag (frame pointers as the CLI defaults them) is at most 65 KiB on
// x86_64-windows and x86_64-linux (the budget of internal/driver's
// hellosize_test.go).
func TestHelloBudgetCLIDefault(t *testing.T) {
	t.Setenv("MK_FRAME_POINTERS", "")
	hello := filepath.Join("..", "..", "tests", "programs", "hello.mk")
	for _, target := range []string{"x86_64-windows", "x86_64-linux"} {
		out := filepath.Join(t.TempDir(), "hello-"+target)
		if st, o, e := mk("build", "--release", "--target", target, "-o", out, hello); st != 0 {
			t.Fatalf("mk build %s: status %d\n%s%s", target, st, o, e)
		}
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() > 66560 {
			t.Errorf("release hello for %s is %d bytes with the CLI default, over the 66,560-byte budget", target, fi.Size())
		}
		t.Logf("release hello for %s: %d bytes", target, fi.Size())
	}
}

// TestOverflowSiteFramePointers: with frame pointers on, a main-task
// overflow whose faulting pc is inside a memory function (Deep's pad is
// zeroed by memset) is reported at the Mk caller's site, as an access fault
// is (FaultOwner passes a memory or probe row at frame 0 on):
// tests/programs/panics/stack_main.mk (pad 256) and pad 264, dev and release.
// Both settings are built: the CLI's default for the host (off on
// x86_64-windows, on elsewhere) and MK_FRAME_POINTERS=on.
func TestOverflowSiteFramePointers(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "tests", "programs", "panics", "stack_main.mk"))
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"", "on"} {
		t.Setenv("MK_FRAME_POINTERS", env)
		for _, pad := range []string{"256", "264"} {
			dir := t.TempDir()
			text := strings.ReplaceAll(strings.ReplaceAll(string(src), "\r\n", "\n"), "256", pad)
			writeTree(t, dir, map[string]string{"main.mk": text})
			for _, prof := range [][]string{nil, {"--release"}} {
				got := runPanic(t, dir, "sm"+pad+env+strings.Join(prof, ""), prof, "MK_BACKTRACE=0")
				if want := "panic: stack limit at main.mk:5:1\n"; got != want {
					t.Errorf("MK_FRAME_POINTERS=%q pad %s %v: stderr %q, want %q", env, pad, prof, got, want)
				}
			}
		}
	}
}
