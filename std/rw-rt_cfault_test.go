// Why: on Windows, Mk's vectored fault handler must leave a C object's own
// __try faults to C, yet still report every fault in Mk code as Mk's.
// What: TestRwRt_CFaultOwnHandler over the bins "cfault" (C called from tasks)
// and "cfault5" (no task: externs called on Main's stack).
// How: a C source faults inside its own __try (__C_specific_handler from
// std.libc); the handler tells C frames from Mk's by unwind entries and std.rt's
// function table.
//
// Each bin also runs a C fault with no handler (bare), Mk's own fault (mk),
// and a fault in an Mk function that C calls back inside its own __try (cb).
// Before the in_c filter every "try" run exited 101; before the frame walk a
// "try" run with no task exited 101, a "cb" run printed "cb -7" (C swallowed
// Mk's fault), and a "bare" run with tasks ended with 0xC0000005 and no report.

package std

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var cfaultFiles = map[string]string{
	"pkg.mk": `const Package = pkg.Package(
    name: "cfault",
    bins: [pkg.Bin(name: "cfault", root: "app", link_script: "", cpu: "", subsystem: "console"),
        pkg.Bin(name: "cfault5", root: "k5", link_script: "", cpu: "", subsystem: "console")],
    deps: [],
    c_sources: [pkg.CSource(path: "lib/cfault.c", flags: [])],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: ["ext"],
    links: [],
)
`,
	"lib/cfault.c": `/* A C object that faults inside its own __try and handles the fault
   itself; std.libc supplies the language handler its __try needs. */

/* Not inlined, so the read is a call inside the __try and the __except
   is kept without -fasync-exceptions. */
__declspec(noinline) int cfault_read(volatile int *p) { return *p; }

/* 7, from the __except below. */
int cfault_try(void) {
    int r = 0;
    __try {
        r = cfault_read((volatile int *)16);
    } __except (1) {
        r = 7;
    }
    return r;
}

/* Faults with no handler of its own. */
int cfault_bare(void) { return cfault_read((volatile int *)16); }

/* Calls f, an Mk function, inside its own __try: -7 if anything faults. */
int cfault_call(int (*f)(void)) {
    int r = 0;
    __try {
        r = f();
    } __except (1) {
        r = -7;
    }
    return r;
}
`,
	"ext/ext.mk": `import std.raw

extern cfault_try(): i32
extern cfault_bare(): i32
extern cfault_call(f: fn(): i32): i32

export Try(): i32 {
    unsafe "cfault_try is the package's C; it handles its own fault" {
        return cfault_try()
    }
}

export Bare(): i32 {
    unsafe "cfault_bare is the package's C; it faults with no handler" {
        return cfault_bare()
    }
}

export MkFault(): i32 {
    unsafe "a read of address 16, on purpose" {
        return raw.Read(raw.FromAddr[i32](16))
    }
}

// CFaultCb is the callback: Mk's own fault, run by C inside its __try.
export extern CFaultCb(): i32 {
    unsafe "a read of address 16, on purpose" {
        return raw.Read(raw.FromAddr[i32](16))
    }
}

export Callback(): i32 {
    unsafe "cfault_call is the package's C; it calls CFaultCb" {
        return cfault_call(CFaultCb)
    }
}
`,
	"app/main.mk": `import ext
import std.atomic

// Run calls the C function that faults inside its own __try 20 times and
// adds each result to sum.
Run(sum: &atomic.AtomicInt) {
    for _ in 0..20 {
        _ = sum.Add(i64(ext.Try()))
    }
}

// Cb calls the C function that calls back into Mk, from a task.
Cb(c: &atomic.AtomicInt) {
    _ = c.Add(i64(ext.Callback()))
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    mode := sys.Env("CFAULT_MODE") else { "try" }
    if mode == "bare" {
        b := ext.Bare()
        try Print(mut out, "bare {b}\n", (b: b))
        return
    }
    if mode == "mk" {
        m := ext.MkFault()
        try Print(mut out, "mk {m}\n", (m: m))
        return
    }
    if mode == "cb" {
        c := atomic.AtomicInt.New(0)
        scope s := sys.io.Scope() {
            s.go Cb(&c)
        }
        try Print(mut out, "cb {c}\n", (c: c.Load()))
        return
    }
    r := ext.Try()
    sum := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go Run(&sum)
        }
    }
    try Print(mut out, "try {r} {n}\n", (r: r, n: sum.Load()))
}
`,
	"k5/main.mk": `import ext

// No spawn and no scope: a program with no task (K-5).
export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    mode := sys.Env("CFAULT_MODE") else { "try" }
    if mode == "bare" {
        b := ext.Bare()
        try Print(mut out, "bare {b}\n", (b: b))
        return
    }
    if mode == "mk" {
        m := ext.MkFault()
        try Print(mut out, "mk {m}\n", (m: m))
        return
    }
    if mode == "cb" {
        c := ext.Callback()
        try Print(mut out, "cb {c}\n", (c: c))
        return
    }
    r := ext.Try()
    try Print(mut out, "try {r}\n", (r: r))
}
`,
}

// cfaultRun runs exe with env added and returns its output and exit code.
func cfaultRun(t *testing.T, exe string, env ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%v: killed after 60 s\n%s", env, out)
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n"), code
}

// cfaultReported: exit 101 with the fault report of the read of address 16,
// followed by a site when site is true.
func cfaultReported(out string, code int, site bool) bool {
	want := "panic: invalid memory access at 0x0000000000000010"
	if site {
		want += " at "
	}
	return code == 101 && strings.Contains(out, want)
}

func TestRwRt_CFaultOwnHandler(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("__try and the vectored handler are Windows'")
	}
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, text := range cfaultFiles {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, profile := range [][]string{nil, {"--release"}} {
		name := "dev"
		if profile != nil {
			name = "release"
		}
		exes := map[string]string{}
		for _, bin := range []string{"cfault", "cfault5"} {
			exe := filepath.Join(dir, bin+".exe")
			args := append(append([]string{"build"}, profile...), "-bin", bin, "-o", exe, ".")
			out, status := mkCmd(t, dir, stdDir, args...)
			if status != 0 {
				if strings.Contains(out, "no usable LLVM") {
					t.Skipf("needs the LLVM toolchain: %s", out)
				}
				t.Fatalf("mk %v: exit %d\n%s", args, status, out)
			}
			exes[bin] = exe
		}
		// The C code's own __except runs on every carrier count: the
		// handler passes the fault on and lowers the stale StackLimit,
		// without which the unwind fails with STATUS_BAD_STACK on a
		// carrier other than 0.
		for _, c := range []int{1, 2, 4, 16} {
			for run := 0; run < 5; run++ {
				out, code := cfaultRun(t, exes["cfault"], fmt.Sprintf("MK_CARRIERS=%d", c), fmt.Sprintf("MK_TEST_SEED=%d", run+1))
				if code != 0 || out != "try 7 1120\n" {
					t.Errorf("%s, %d carriers, run %d: exit %#x, output %q; want exit 0 and %q", name, c, run, uint32(code), out, "try 7 1120\n")
				}
			}
		}
		// No task: the extern runs on Main's stack with no in_c flag; the walk
		// still finds the C frame's handler.
		if out, code := cfaultRun(t, exes["cfault5"]); code != 0 || out != "try 7\n" {
			t.Errorf("%s, K-5: exit %#x, output %q; want exit 0 and %q", name, uint32(code), out, "try 7\n")
		}
		for _, c := range []int{1, 4, 16} {
			carriers := fmt.Sprintf("MK_CARRIERS=%d", c)
			// A fault in Mk's own code is still reported, with
			// its site (ext/ext.mk, or app/main.mk where release inlines
			// MkFault).
			if out, code := cfaultRun(t, exes["cfault"], "CFAULT_MODE=mk", carriers); !cfaultReported(out, code, true) {
				t.Errorf("%s, mk mode, %d carriers: exit %#x, output %q; want 101 and the report with its site", name, c, uint32(code), out)
			}
			// C code with no handler of its own: no frame between it and
			// mk_call_system handles it, so it is Mk's to report.
			if out, code := cfaultRun(t, exes["cfault"], "CFAULT_MODE=bare", carriers); !cfaultReported(out, code, false) {
				t.Errorf("%s, bare mode, %d carriers: exit %#x, output %q; want 101 and the report", name, c, uint32(code), out)
			}
			// Mk's own fault in a callback that C runs inside its __try is
			// reported at the callback, not swallowed by the C handler.
			if out, code := cfaultRun(t, exes["cfault"], "CFAULT_MODE=cb", carriers); !cfaultReported(out, code, true) {
				t.Errorf("%s, cb mode, %d carriers: exit %#x, output %q; want 101 and the report with its site", name, c, uint32(code), out)
			}
		}
		for _, mode := range []string{"mk", "bare", "cb"} {
			if out, code := cfaultRun(t, exes["cfault5"], "CFAULT_MODE="+mode); !cfaultReported(out, code, mode != "bare") {
				t.Errorf("%s, K-5, %s mode: exit %#x, output %q; want 101 and the report", name, mode, uint32(code), out)
			}
		}
	}
}
