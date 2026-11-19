// Why: an Mk library runs inside a C host's process; its runtime must not
// steal the host's faults, misroute calls or panic on a refused stop.
// What: tests of host overflow, fn-value thunks, refused stops and libc, with the C hosts sehC and refusedC and the
// package fvFiles.
// How: builds libraries and C hosts with the bundled clang and runs them:
// a library's vectored handler passes on a host's own stack overflow, a
// function value of a thunked export extern names the thunk in every
// module, a refused stop never panics a concurrent call, and a library
// with a system-libc dynamic link is E0202.

package driver

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// sehC is a host that uses SEH: a thread of the host overflows its own
// stack in its own code and recovers with __try/__except, before and
// after the Mk library's runtime started (argument "MKRT" starts it).
const sehC = `#include "mlib.h"
typedef void *HANDLE;
typedef unsigned long DWORD;
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
__declspec(dllimport) HANDLE __stdcall CreateThread(void *a, size_t s, DWORD (__stdcall *f)(void *), void *p, DWORD fl, DWORD *id);
__declspec(dllimport) DWORD __stdcall WaitForSingleObject(HANDLE h, DWORD ms);
__declspec(dllimport) const unsigned short *__stdcall GetCommandLineW(void);
__declspec(dllimport) HANDLE __stdcall GetStdHandle(DWORD n);
__declspec(dllimport) int __stdcall WriteFile(HANDLE h, const void *b, DWORD n, DWORD *w, void *o);
__declspec(dllimport) int __stdcall SetThreadStackGuarantee(DWORD *size);
static void out(const char *s) { DWORD n = 0, w; while (s[n]) n++; WriteFile(GetStdHandle((DWORD)-11), s, n, &w, 0); }
static volatile long long sink;
static long long host_recurse(long long n) {
    if (n == -1) return 0;
    volatile char pad[256];
    pad[n & 255] = (char)n;
    sink = pad[(n + 1) & 255];
    return host_recurse(n + 1) + pad[(n + 7) & 255];
}
static volatile int recovered;
static DWORD __stdcall worker(void *p) {
    (void)p;
    DWORD g = 65536;
    SetThreadStackGuarantee(&g);
    __try {
        sink = host_recurse(0);
    } __except (_exception_code() == 0xC00000FD ? 1 : 0) {
        recovered = 1;
    }
    return 0;
}
void start(void) {
    const unsigned short *c = GetCommandLineW();
    int lib = 0;
    for (; *c; c++) if (c[0] == 'M' && c[1] == 'K' && c[2] == 'R' && c[3] == 'T') lib = 1;
    if (lib) {
        if (mlib_mk_start(0) != 0) ExitProcess(10);
        if (Twice(2) != 4) ExitProcess(11);
        out("runtime started\n");
    }
    HANDLE h = CreateThread(0, 1 << 20, worker, 0, 0, 0);
    WaitForSingleObject(h, 0xFFFFFFFF);
    out(recovered ? "host recovered from its own stack overflow\n" : "not recovered\n");
    ExitProcess(recovered ? 0 : 3);
}
`

// TestHostOverflowPassedOn: a library's vectored handler passes on a
// stack overflow whose pc is outside the library's own code and whose stack
// pointer is on no stack Mk made, so a host that handles its own overflow
// with __try recovers with the runtime started, through the import library
// and with the static .lib, dev and release.
func TestHostOverflowPassedOn(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: a Windows host runs the Windows leg")
	}
	tools := needTools(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			imp := t.TempDir()
			k32 := filepath.Join(imp, "k32.lib")
			if err := tools.ImportLib(context.Background(), "kernel32.dll", []string{"CreateThread", "ExitProcess", "GetCommandLineW",
				"GetStdHandle", "SetThreadStackGuarantee", "WaitForSingleObject", "WriteFile"}, "x64", k32); err != nil {
				t.Fatal(err)
			}
			nt := filepath.Join(imp, "ntdll.lib")
			if err := tools.ImportLib(context.Background(), "ntdll.dll", []string{"__C_specific_handler"}, "x64", nt); err != nil {
				t.Fatal(err)
			}
			dyn := t.TempDir()
			buildLib(t, libPackage(t, "dynamic"), types.X86_64Windows, prof, dyn)
			st := t.TempDir()
			lib := buildLib(t, libPackage(t, "static"), types.X86_64Windows, prof, st)
			for _, h := range []struct {
				name, dir string
				libs      []string
			}{{"dynamic", dyn, []string{filepath.Join(dyn, "mlib.lib"), k32, nt}}, {"static", st, []string{lib, k32, nt}}} {
				// Unwind tables for every host function, so the system's
				// unwinder reaches the __except through host_recurse's
				// frames (without them the host cannot recover at all).
				h.libs = append(h.libs, "-funwind-tables")
				exe := clangHost(t, tools, h.dir, "seh", sehC, h.libs...)
				for _, args := range [][]string{nil, {"MKRT"}} {
					out, errOut, code := runExe(t, types.X86_64Windows, exe, "", args...)
					if code != 0 || !strings.Contains(out, "host recovered") {
						t.Errorf("%s host %v: exit %d, stdout %q, stderr %q; want the host's own recovery", h.name, args, code, out, errOut)
					}
				}
			}
		})
	}
}

// fvFiles is a package where a C thread (CreateThread) enters Same,
// an export extern in Main's module, then Other, one in another module,
// each through a function value taken in Main's module. Each answers 7
// only when it runs as a task (runtime.TaskIo is some).
var fvFiles = map[string]string{
	"pkg.mk": manifest(map[string]string{"name": `"fv"`, "bins": `[` + libBin("fv", "app", "", "") + `]`, "unsafe_modules": `["app", "w"]`}),
	"app/main.mk": `import std.runtime
import std.time
import std.raw
import w
import cb

/// Same is an export extern in Main's own module.
export extern Same(p: *u8): u32 {
    io := runtime.TaskIo() else {
        return 1
    }
    io.Sleep(time.Millis(20)) else {
        return 2
    }
    return 7
}

Run(f: fn(*u8): u32): u32 {
    var code: u32 = 99
    unsafe "kernel32's thread calls; code outlives the wait" {
        h := w.CreateThread(raw.Null[u8](), 0, f, raw.Null[u8](), 0, raw.Null[u32]())
        _ = w.WaitForSingleObject(h, 0xFFFF_FFFF)
        _ = w.GetExitCodeThread(h, raw.OfMut(mut code))
    }
    return code
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := Run(Same)
    b := Run(cb.Other)
    try Print(mut out, "same-module {a} other-module {b}\n", (a: a, b: b))
}
`,
	"cb/cb.mk": `import std.runtime
import std.time

/// Other is an export extern in another module than Main's.
export extern Other(p: *u8): u32 {
    io := runtime.TaskIo() else {
        return 1
    }
    io.Sleep(time.Millis(20)) else {
        return 2
    }
    return 7
}
`,
	"w/w.mk": `export extern CreateThread(a: *u8, s: uint, f: fn(*u8): u32, p: *u8, fl: u32, id: *u32): uint
export extern WaitForSingleObject(h: uint, ms: u32): u32
export extern GetExitCodeThread(h: uint, code: *u32): i32
`,
}

// TestFnValueNamesThunk: a function value of a thunked
// export extern is the thunk in every module, its own included, so a C
// thread that calls it runs it as a task: both answer 7, dev and
// release.
func TestFnValueNamesThunk(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: the probe calls kernel32's CreateThread")
	}
	tools := needTools(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			s, err := NewSession(Config{Root: writeTree(t, fvFiles), Profile: prof, Tools: tools, CacheDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "fv.exe"))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
			}
			out, errOut, code := runExe(t, types.X86_64Windows, paths[0], "")
			if code != 0 || strings.TrimSpace(out) != "same-module 7 other-module 7" {
				t.Errorf("exit %d, stdout %q, stderr %q; want same-module 7 other-module 7", code, out, errOut)
			}
		})
	}
}

// refusedC is a host whose stop is refused: one thread sits in Wait(4000),
// so every stop answers 1, while the main thread calls mlib_mk_stop in a
// loop and a second thread calls Twice; no call may panic.
const refusedC = `#include "mlib.h"
typedef void *HANDLE;
typedef unsigned long DWORD;
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
__declspec(dllimport) HANDLE __stdcall CreateThread(void *a, size_t s, DWORD (__stdcall *f)(void *), void *p, DWORD fl, DWORD *id);
__declspec(dllimport) unsigned long long __stdcall GetTickCount64(void);
__declspec(dllimport) void __stdcall Sleep(DWORD ms);
static volatile int in_long, long_done;
static volatile long long b_calls;
static DWORD __stdcall longcall(void *p) { (void)p; in_long = 1; int r = Wait(4000); long_done = r; return 0; }
static DWORD __stdcall caller(void *p) { (void)p; for (;;) { if (Twice(5) != 10) ExitProcess(21); b_calls++; } return 0; }
void start(void) {
    if (mlib_mk_start(0) != 0) ExitProcess(10);
    CreateThread(0, 0, longcall, 0, 0, 0);
    while (!in_long) {}
    Sleep(500);
    CreateThread(0, 0, caller, 0, 0, 0);
    while (b_calls < 100) {}
    unsigned long long t0 = GetTickCount64();
    while (GetTickCount64() - t0 < 1500 && !long_done) {
        if (mlib_mk_stop() != 1) ExitProcess(30);
    }
    ExitProcess(0);
}
`

// TestRefusedStopKeepsCalls: a stop refused because a call runs
// never panics a concurrent new call, which waits out the reverted Stopping
// state: 10 runs of 10 exit 0.
func TestRefusedStopKeepsCalls(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: a Windows host runs the Windows leg")
	}
	tools := needTools(t)
	k32 := filepath.Join(t.TempDir(), "k32.lib")
	if err := tools.ImportLib(context.Background(), "kernel32.dll", []string{"CreateThread", "ExitProcess", "GetTickCount64", "Sleep"}, "x64", k32); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	buildLib(t, libPackage(t, "dynamic"), types.X86_64Windows, types.Dev, dir)
	exe := clangHost(t, tools, dir, "refused", refusedC, filepath.Join(dir, "mlib.lib"), k32)
	pass := 0
	for i := 0; i < 10; i++ {
		_, errOut, code := runExe(t, types.X86_64Windows, exe, "")
		if code == 0 {
			pass++
		} else {
			t.Logf("run %d: exit %d, stderr %q", i, code, errOut)
		}
	}
	if pass != 10 {
		t.Errorf("%d of 10 runs passed", pass)
	}
}

// TestSystemLibcRefused: a library whose package has a dynamic links
// entry for x86_64-linux (system-libc mode) is E0202 when built for
// x86_64-linux, never an E0001 at the link.
func TestSystemLibcRefused(t *testing.T) {
	root := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"slc"`, "bins": `[` + libBin("slc", ".", "dynamic", "") + `]`,
			"links": `[pkg.Link(name: "libz.so.1", kind: "dynamic", path: "", target: "x86_64-linux")]`}),
		"lib.mk": "export extern Twice(n: i32): i32 {\n    return n * 2\n}\n",
	})
	b := libBuildIn(t, root, types.X86_64Linux, types.Dev, t.TempDir(), t.TempDir(), false)
	if b.err != nil {
		t.Fatal(b.err)
	}
	ds := b.r.Report.Diags
	if len(ds) != 1 || ds[0].Diag.Code != diag.E0202 || !strings.Contains(ds[0].Diag.Message, "system-libc") {
		t.Fatalf("want one E0202 naming system-libc mode:\n%s", b.r.Report.Text(b.r.Sources))
	}
}

// TestHeaderWording: the header says what a stop before any start
// does, and gives stack_reserve's floor.
func TestHeaderWording(t *testing.T) {
	dir := t.TempDir()
	buildLib(t, libPackage(t, "dynamic"), types.X86_64Windows, types.Dev, dir)
	h := readFile(t, filepath.Join(dir, "mlib.h"))
	for _, want := range []string{"A stop before any start does nothing and answers 0", "at least 256 KiB",
		"A call into the library after a stop is a panic (code 16)", "waits for a stop in progress"} {
		if !strings.Contains(h, want) {
			t.Errorf("the header lacks %q:\n%s", want, h)
		}
	}
}

// recurseSource is the sample library with an export that overflows its
// own stack in Mk code.
const recurseSource = libSource + `
/// Recurse overflows the stack in Mk code (an Mk fault the library owns).
export extern Recurse(n: i64): i64 {
    var a: [64]i64 = [0; 64]
    a[n % 64] = n
    return Recurse(n + 1) + a[(n + 3) % 64]
}
`

// mkOverflowC calls Recurse from the host's main thread: the call runs as
// a task of the library's runtime, whose overflow is Mk's.
const mkOverflowC = `#include "mlib.h"
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
void start(void) {
    ExitProcess((unsigned)Recurse(0));
}
`

// TestMkOverflowStillMk (the other direction of
// TestHostOverflowPassedOn): a stack overflow in the library's own
// code, on a task of its runtime, is still Mk's: 101 and "stack limit",
// dynamic and static, dev and release.
func TestMkOverflowStillMk(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: a Windows host runs the Windows leg")
	}
	tools := needTools(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			k32 := filepath.Join(t.TempDir(), "k32.lib")
			if err := tools.ImportLib(context.Background(), "kernel32.dll", []string{"ExitProcess"}, "x64", k32); err != nil {
				t.Fatal(err)
			}
			pkg := func(kind string) string {
				return writeTree(t, map[string]string{
					"pkg.mk": manifest(map[string]string{"name": `"mlib"`, "bins": `[` + libBin("mlib", ".", kind, "") + `]`}),
					"lib.mk": recurseSource,
				})
			}
			dyn := t.TempDir()
			buildLib(t, pkg("dynamic"), types.X86_64Windows, prof, dyn)
			st := t.TempDir()
			lib := buildLib(t, pkg("static"), types.X86_64Windows, prof, st)
			for _, h := range []struct {
				name, dir string
				libs      []string
			}{{"dynamic", dyn, []string{filepath.Join(dyn, "mlib.lib"), k32}}, {"static", st, []string{lib, k32}}} {
				exe := clangHost(t, tools, h.dir, "mkov", mkOverflowC, h.libs...)
				_, errOut, code := runExe(t, types.X86_64Windows, exe, "")
				if code != 101 || !strings.Contains(errOut, "stack limit") {
					t.Errorf("%s host: exit %d, stderr %q; want 101 and the stack report", h.name, code, errOut)
				}
			}
		})
	}
}
