// Why: profilers and debuggers walk the frame chain, which only works if
// every function keeps its frame pointer.
// What: TestFramePointerWalk and TestFramePointerSwitch.
// How: with --frame-pointers=on, fpWalkMain walks the chain from ten calls
// deep to Main on Linux (on Windows x64, codegen's
// TestFramePointerPrologue checks the prologue); the flag wins over
// MK_FRAME_POINTERS, which wins over the target's default.

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// fpWalkMain recurses ten levels, each holding a local whose address the
// next level reads after the call returns, so no level is a tail call or
// a loop at -O3 and each keeps its own frame.
const fpWalkMain = `import k

Down(n: int, up: &int): int {
    if n == 0 {
        return k.Walk() + *up - *up
    }
    here := n
    r := Down(n - 1, &here)
    return r + *up - *up + here - n
}

export Main(sys: &System): WriteError!void {
    top := 0
    d := Down(10, &top)
    try sys.Print("{d}\n", (d,))
}
`

// fpWalkK reads the frame pointer by asm and follows the chain while each
// saved pointer lies above the last and inside the running stack.
const fpWalkK = `import std.raw
import std.runtime

export Walk(): int {
    b := runtime.StackBounds()
    var fp: uint = 0
    var n := 0
    unsafe "reads the frame pointer and the saved ones below it on this stack" {
        asm("FPREAD", r: out(reg) fp)
        for _ in 0..200 {
            if fp < b.lo || fp + 16 > b.hi {
                break
            }
            next := raw.Read(raw.FromAddr[uint](fp))
            if next <= fp {
                break
            }
            n += 1
            fp = next
        }
    }
    return n
}
`

func fpWalk(t *testing.T, dir string, args []string, env string) int {
	t.Helper()
	exe := filepath.Join(dir, "w"+strconv.Itoa(len(args))+env+".exe")
	t.Setenv("MK_FRAME_POINTERS", env)
	st, out, errOut := mk(append(append([]string{"build"}, args...), "-o", exe, dir)...)
	if st != 0 {
		t.Fatalf("mk build %v: status %d\n%s%s", args, st, out, errOut)
	}
	b, err := exec.Command(exe).Output()
	if err != nil {
		ents, _ := os.ReadDir(dir)
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("%s: %v (%v)", exe, err, names)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("%s printed %q", exe, b)
	}
	return n
}

func TestFramePointerWalk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Win64's rbp is rsp plus an offset: the walk uses RtlVirtualUnwind; TestFramePointerPrologue covers Windows")
	}
	read := "mov {r}, rbp"
	if runtime.GOARCH == "arm64" {
		read = "mov {r}, x29"
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.mk": fpWalkMain,
		"k/k.mk":  strings.Replace(fpWalkK, "FPREAD", read, 1),
		"pkg.mk": `const Package = pkg.Package(
    name: "fpwalk",
    bins: [pkg.Bin(name: "fpwalk", root: ".", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["k"],
    links: [],
)
`,
	}
	for name, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Walk, then ten Downs, then Main: at least twelve frames.
	for _, c := range []struct {
		args []string
		env  string
	}{{[]string{"--frame-pointers=on"}, ""}, {[]string{"--release", "--frame-pointers=on"}, ""}, {[]string{"--release"}, "on"}} {
		if n := fpWalk(t, dir, c.args, c.env); n < 12 {
			t.Errorf("%v MK_FRAME_POINTERS=%q: the walk crossed %d frames, want at least 12", c.args, c.env, n)
		}
	}
}

// TestFramePointerSwitch: MK_FRAME_POINTERS sets the default, the flag
// wins over it, and the default is the build target's: off for
// x86_64-windows, on for every other target, whatever the host, so a
// cross-build gets its target's default; mk test and mk check (compileFlags
// alone) read it too.
func TestFramePointerSwitch(t *testing.T) {
	for _, name := range []string{"x86_64-windows", "x86_64-linux", "aarch64-linux", "aarch64-macos", "x86_64-freestanding", "x86_64-nutshell", "aarch64-nutshell"} {
		if got, want := framePointersDefault(name), name != "x86_64-windows"; got != want {
			t.Errorf("framePointersDefault(%s) = %v, want %v", name, got, want)
		}
	}
	if got, want := framePointersDefault(""), runtime.GOOS != "windows"; got != want {
		t.Errorf("framePointersDefault(host) = %v, want %v", got, want)
	}
	for _, target := range []string{"", "x86_64-windows", "x86_64-linux"} {
		def := framePointersDefault(target)
		for _, c := range []struct {
			env, flag string
			want      bool
		}{{"", "", def}, {"on", "", true}, {"off", "", false}, {"on", "off", false}, {"off", "on", true}, {"", "on", true}, {"", "off", false}} {
			t.Setenv("MK_FRAME_POINTERS", c.env)
			var nf nativeFlags
			fs := newNativeFlagSet("build", io.Discard, &nf)
			args := []string{}
			if target != "" {
				args = append(args, "--target", target)
			}
			if c.flag != "" {
				args = append(args, "--frame-pointers="+c.flag)
			}
			if err := fs.Parse(args); err != nil {
				t.Fatal(err)
			}
			cfg, ok := nf.config(".", io.Discard)
			if !ok || cfg.FramePointers != c.want {
				t.Errorf("--target %q MK_FRAME_POINTERS=%q --frame-pointers=%q: ok %v, FramePointers %v, want %v", target, c.env, c.flag, ok, cfg.FramePointers, c.want)
			}
			if c.flag != "" {
				continue
			}
			// mk test's and mk check's flags: the variable, else the default.
			var tf testFlags
			tfs := newTestFlagSet(io.Discard, &tf)
			targs := []string{}
			if target != "" {
				targs = append(targs, "--target", target)
			}
			if err := tfs.Parse(targs); err != nil {
				t.Fatal(err)
			}
			if cfg, ok := tf.config(".", io.Discard); !ok || cfg.FramePointers != c.want {
				t.Errorf("mk test --target %q MK_FRAME_POINTERS=%q: ok %v, FramePointers %v, want %v", target, c.env, ok, cfg.FramePointers, c.want)
			}
		}
	}
	dir := t.TempDir()
	if st, _, errOut := mk("build", "--frame-pointers=maybe", dir); st == 0 || !strings.Contains(errOut, "--frame-pointers") {
		t.Errorf("--frame-pointers=maybe: status %d stderr %q", st, errOut)
	}
	t.Setenv("MK_FRAME_POINTERS", "yes")
	if st, _, errOut := mk("check", dir); st == 0 || !strings.Contains(errOut, "MK_FRAME_POINTERS") {
		t.Errorf("MK_FRAME_POINTERS=yes: status %d stderr %q", st, errOut)
	}
}
