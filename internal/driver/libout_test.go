// Why: a library bin is consumed by C hosts; its header, its shared and
// static outputs and its runtime's start and stop must work from C.
// What: TestLibHeader_CForm, TestLibOutput_LinuxShared, TestLibHost_Windows,
// TestLibOutput_ObjectCut and TestLibGate_Windows, over libSource and gateC.
// How: builds the sample library, reads the Linux .so's soname and dynamic
// symbols, and on Windows builds C hosts with the bundled clang that call
// it through the header, the import library, LoadLibrary and the .lib.

package driver

import (
	"bytes"
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// libSource is the sample library:
// pure computations through every C form of the set (an integer, a
// reference to an exported struct, a str), and one export with no C form.
const libSource = `import std.runtime
import std.time

/// A point in the plane.
export struct Point {
    export x: i32
    export y: i32
}

/// Twice doubles n.
export extern Twice(n: i32): i32 {
    return n * 2
}

/// Sum adds the coordinates of p.
export extern Sum(p: &Point): i64 {
    return i64(p.x) + i64(p.y)
}

export extern Len(s: str): int {
    return s.Len()
}

export Maybe(n: ?i32): i32 {
    return n else { return 0 }
}

Helper(n: i32): i32 {
    return n + 1
}

export extern Next(n: i32): i32 {
    return Helper(n)
}

/// Wait sleeps ms milliseconds on a timer, then answers ms + 1.
export extern Wait(ms: i32): i32 {
    io := runtime.TaskIo() else {
        return -1
    }
    io.Sleep(time.Millis(i64(ms))) else {
        return -2
    }
    return ms + 1
}
`

// libPackage writes the sample library package of kind and returns its
// directory.
func libPackage(t *testing.T, kind string) string {
	return writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"mlib"`,
			"bins": `[pkg.Bin(name: "mlib", root: ".", link_script: "", cpu: "", subsystem: "", kind: "` + kind + `", abi: "")]`}),
		"lib.mk": libSource,
	})
}

// buildLib builds the library at root into out and returns its path.
func buildLib(t *testing.T, root string, target types.Target, prof types.Profile, out string) string {
	t.Helper()
	s, err := NewSession(Config{Root: root, Target: target, Profile: prof, Tools: needTools(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(out + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	return paths[0]
}

// TestLibHeader_CForm: the header declares the exports with a C form,
// their /// comments and the struct they need; Maybe, an exported Mk
// function that is no extern, is not declared (an export extern with no C
// form is E1109 at check; TestLibExports_NoCFormRefused).
func TestLibHeader_CForm(t *testing.T) {
	dir := buildLib(t, libPackage(t, "static"), types.X86_64Linux, types.Dev, t.TempDir())
	h, err := os.ReadFile(filepath.Join(filepath.Dir(dir), "mlib.h"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#ifndef MK_MLIB_H", "#include <stdint.h>", "extern \"C\" {",
		"typedef struct MkStr {\n    const uint8_t *ptr;\n    int64_t len;\n} MkStr;",
		"typedef struct Point Point;\n", "/* A point in the plane. */\nstruct Point {\n    int32_t x;\n    int32_t y;\n};",
		"/* Twice doubles n. */\nint32_t Twice(int32_t n);", "int64_t Sum(const Point * p);", "int64_t Len(MkStr s);",
		"int32_t Next(int32_t n);",
	} {
		if !strings.Contains(string(h), want) {
			t.Errorf("header lacks %q:\n%s", want, h)
		}
	}
	if filepath.Base(dir) != "libmlib.a" {
		t.Errorf("static Linux library %s, want libmlib.a", filepath.Base(dir))
	}
	// A library has no process entry: a C host's crt1 defines _start, and
	// the archive's index names Twice but neither _start nor mk_start.
	idx := archiveIndex(t, dir)
	if !slices.Contains(idx, "Twice") || slices.Contains(idx, "_start") || slices.Contains(idx, "mk_start") {
		t.Errorf("archive index %q", idx)
	}
}

// TestLibOutput_LinuxShared: the Linux dynamic library is libmlib.so.1 with
// that soname, and its dynamic symbols are the package's exports only, at
// the version node MLIB_1: never helper, std's export extern functions or
// the runtime, nor Maybe, which has no C form.
func TestLibOutput_LinuxShared(t *testing.T) {
	so := buildLib(t, libPackage(t, "dynamic"), types.X86_64Linux, types.Dev, t.TempDir())
	if filepath.Base(so) != "libmlib.so.1" {
		t.Fatalf("library %s", so)
	}
	f, err := elf.Open(so)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if s, _ := f.DynString(elf.DT_SONAME); !slices.Equal(s, []string{"libmlib.so.1"}) {
		t.Errorf("soname %q", s)
	}
	syms, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	var defs []string
	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF && elf.ST_TYPE(s.Info) == elf.STT_FUNC {
			defs = append(defs, s.Name)
		}
	}
	slices.Sort(defs)
	if want := []string{"Len", "Next", "Sum", "Twice", "Wait", "mlib_mk_start", "mlib_mk_stop"}; !slices.Equal(defs, want) {
		t.Errorf("exported functions %q, want %q", defs, want)
	}
}

// hostC calls every C-form export through the header: 40 + 7 + 5 + 8 = 60.
const hostC = `#include "mlib.h"
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
void start(void) {
    Point p = {3, 4};
    MkStr s = {(const uint8_t *)"hello", 5};
    int64_t r = Twice(20) + Sum(&p) + Len(s) + Next(7);
    ExitProcess((unsigned)r);
}
`

// loadC loads the DLL by LoadLibraryA and calls Twice and Next.
const loadC = `#include "mlib.h"
typedef void *HMODULE;
__declspec(dllimport) HMODULE __stdcall LoadLibraryA(const char *name);
__declspec(dllimport) void *__stdcall GetProcAddress(HMODULE m, const char *name);
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
void start(void) {
    HMODULE m = LoadLibraryA("mlib.dll");
    if (!m) ExitProcess(1);
    int32_t (*twice)(int32_t) = (int32_t (*)(int32_t))GetProcAddress(m, "Twice");
    int32_t (*next)(int32_t) = (int32_t (*)(int32_t))GetProcAddress(m, "Next");
    if (!twice || !next || GetProcAddress(m, "Helper") || GetProcAddress(m, "MkThreadYield")) ExitProcess(2);
    ExitProcess((unsigned)(twice(20) + next(7)));
}
`

// clangHost compiles and links a freestanding Windows host (no CRT, no
// SDK: the gate's clean machine) against libs, in dir.
func clangHost(t *testing.T, tools *toolchain.Tools, dir, name, src string, libs ...string) string {
	t.Helper()
	c := filepath.Join(dir, name+".c")
	if err := os.WriteFile(c, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, name+".exe")
	args := append([]string{"--target=x86_64-pc-windows-msvc", "-ffreestanding", "-fno-stack-protector", "-nostdlib", "-fuse-ld=lld",
		"-Wall", "-Werror", "-std=c11", "-Wl,/entry:start", "-Wl,/subsystem:console", "-I" + dir, c, "-o", exe}, libs...)
	runTool(t, dir, tools.Clang, args...)
	return exe
}

func runTool(t *testing.T, dir, tool string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir = dir
	var e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &e, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %q: %v\n%s", filepath.Base(tool), args, err, e.String())
	}
}

// TestLibHost_Windows: a C program built with the bundled clang
// and no C runtime calls a Mk library's exports through its generated
// header, through the import library, through LoadLibrary, and linked
// statically with the .lib, in dev and release; the header compiles under
// -Wall -Werror as C11 and C++17. The runtime's start, a timer wait from
// two threads and the stop are TestLibGate_Windows's.
func TestLibHost_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: a Windows host runs the Windows leg; linux.sh runs Linux's")
	}
	tools := needTools(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			k32 := filepath.Join(t.TempDir(), "k32.lib")
			if err := tools.ImportLib(context.Background(), "kernel32.dll", []string{"ExitProcess", "GetProcAddress", "LoadLibraryA"}, "x64", k32); err != nil {
				t.Fatal(err)
			}
			dyn := t.TempDir()
			dll := buildLib(t, libPackage(t, "dynamic"), types.X86_64Windows, prof, dyn)
			if filepath.Base(dll) != "mlib.dll" {
				t.Fatalf("library %s", dll)
			}
			for _, std := range [][]string{{"-x", "c", "-std=c11"}, {"-x", "c++", "-std=c++17"}} {
				runTool(t, dyn, tools.Clang, append(append([]string{"--target=x86_64-pc-windows-msvc", "-ffreestanding", "-Wall", "-Werror", "-fsyntax-only"}, std...), filepath.Join(dyn, "mlib.h"))...)
			}
			exe := clangHost(t, tools, dyn, "host", hostC, filepath.Join(dyn, "mlib.lib"), k32)
			if _, _, code := runExe(t, types.X86_64Windows, exe, ""); code != 60 {
				t.Errorf("import-library host: exit %d, want 60", code)
			}
			exe = clangHost(t, tools, dyn, "load", loadC, k32)
			if _, _, code := runExe(t, types.X86_64Windows, exe, ""); code != 48 {
				t.Errorf("LoadLibrary host: exit %d, want 48", code)
			}
			st := t.TempDir()
			lib := buildLib(t, libPackage(t, "static"), types.X86_64Windows, prof, st)
			if filepath.Base(lib) != "mlib.lib" {
				t.Fatalf("library %s", lib)
			}
			// Deterministic: a second build elsewhere writes the same bytes.
			again := buildLib(t, libPackage(t, "static"), types.X86_64Windows, prof, t.TempDir())
			if readFile(t, lib) != readFile(t, again) {
				t.Error("two static builds differ")
			}
			// The static library carries std, the runtime and the system
			// import libraries: the host needs nothing else.
			exe = clangHost(t, tools, st, "host", hostC, lib)
			if _, _, code := runExe(t, types.X86_64Windows, exe, ""); code != 60 {
				t.Errorf("static host: exit %d, want 60", code)
			}
		})
	}
}

// TestLibOutput_ObjectCut: kind "object" is refused with E0202 at the
// manifest step (manifest bins[0].kind, reason invalid), never built as an
// executable. The check reads the code and facts, not the message's wording.
func TestLibOutput_ObjectCut(t *testing.T) {
	s, err := NewSession(Config{Root: libPackage(t, "object"), Target: types.X86_64Windows, Tools: needTools(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(t.TempDir() + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 || len(r.Report.Diags) != 1 {
		t.Fatalf("paths %v\n%s", paths, r.Report.Text(r.Sources))
	}
	if d := r.Report.Diags[0].Diag; d.Code != diag.E0202 || factText(d, "manifest") != "bins[0].kind" || factText(d, "reason") != "invalid" || !strings.Contains(d.Message, `"object"`) {
		t.Fatalf("paths %v\n%s", paths, r.Report.Text(r.Sources))
	}
}

// gateC is the host of the gate's library check: the runtime started with
// options, a function that waits on a timer called from the main thread and
// from a second thread at once, then a clean stop; a call after the stop is
// panic 16 when argument "after" is given (the host's command line holds
// it).
const gateC = `#include "mlib.h"
typedef void *HANDLE;
__declspec(dllimport) void __stdcall ExitProcess(unsigned code);
__declspec(dllimport) HANDLE __stdcall CreateThread(void *a, size_t s, unsigned long (__stdcall *f)(void *), void *p, unsigned long fl, unsigned long *id);
__declspec(dllimport) unsigned long __stdcall WaitForSingleObject(HANDLE h, unsigned long ms);
__declspec(dllimport) unsigned long long __stdcall GetTickCount64(void);
__declspec(dllimport) const unsigned short *__stdcall GetCommandLineW(void);
static volatile int32_t second;
static unsigned long __stdcall worker(void *p) { (void)p; second = Wait(200); return 0; }
void start(void) {
    MlibMkOptions o = {2, 0};
    if (mlib_mk_start(&o) != 0) ExitProcess(10);
    if (mlib_mk_start(0) != 0) ExitProcess(14);
    HANDLE h = CreateThread(0, 0, worker, 0, 0, 0);
    int32_t first = Wait(200);
    WaitForSingleObject(h, 0xFFFFFFFF);
    if (first != 201 || second != 201) ExitProcess(11);
    if (Twice(21) != 42) ExitProcess(15);
    if (mlib_mk_stop() != 0) ExitProcess(13);
    const unsigned short *c = GetCommandLineW();
    while (*c) {
        if (c[0] == 'a' && c[1] == 'f' && c[2] == 't') Wait(1);
        c++;
    }
    ExitProcess(0);
}
`

// TestLibGate_Windows is the gate's Libraries row on Windows: a C program
// built with the bundled clang (no CRT) loads the Mk library through its
// import library, starts its runtime, calls a function that waits on a
// 200 ms timer from its main thread and from a second thread at once (both
// answer 201, the sleep's success; no wall-clock assertion, which runs
// only under MK_TIMING), and stops the runtime cleanly, exiting 0; a call
// after the stop is panic 16 (exit 101). Dev and release.
func TestLibGate_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: a Windows host runs the Windows leg; linux.sh runs Linux's")
	}
	tools := needTools(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			k32 := filepath.Join(t.TempDir(), "k32.lib")
			if err := tools.ImportLib(context.Background(), "kernel32.dll", []string{"CreateThread", "ExitProcess", "GetCommandLineW",
				"GetTickCount64", "WaitForSingleObject"}, "x64", k32); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			buildLib(t, libPackage(t, "dynamic"), types.X86_64Windows, prof, dir)
			exe := clangHost(t, tools, dir, "gate", gateC, filepath.Join(dir, "mlib.lib"), k32)
			if _, errOut, code := runExe(t, types.X86_64Windows, exe, ""); code != 0 {
				t.Errorf("gate host: exit %d, stderr %q", code, errOut)
			}
			if _, errOut, code := runExe(t, types.X86_64Windows, exe, "", "after"); code != 101 || !strings.Contains(errOut, "suspend in callback") || !strings.Contains(errOut, "after the library's stop") {
				t.Errorf("a call after the stop: exit %d, stderr %q; want 101 and panic 16", code, errOut)
			}
			st := t.TempDir()
			lib := buildLib(t, libPackage(t, "static"), types.X86_64Windows, prof, st)
			exe = clangHost(t, tools, st, "gate", gateC, lib, k32)
			if _, errOut, code := runExe(t, types.X86_64Windows, exe, ""); code != 0 {
				t.Errorf("static gate host: exit %d, stderr %q", code, errOut)
			}
		})
	}
}
