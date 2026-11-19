// Why: a panic report is far more useful with the Mk frames that led to it,
// and a crash dump must not drag dbghelp.dll into every executable.
// What: tests of backtraces, runtime.WriteDump, a freestanding kernel
// build, a panic hook that writes a dump, and StackSize.Bytes.
// How: builds small programs with mk, runs them to a panic and reads the
// report. A panic three Mk frames deep prints those frames innermost first
// (Windows by RtlVirtualUnwind, Linux by the frame chain); MK_BACKTRACE=0
// leaves the one report line.

package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// btMain calls First, Second and Third through a table indexed by a value
// the optimizer cannot know (the argument count), and each uses the
// result of its call, so no frame is inlined or a tail call in release.
const btMain = `Next(i: int): fn(int): int {
    fs: [3]fn(int): int = [First, Second, Third]
    return fs[i]
}

First(n: int): int {
    return Next(n + 1)(n + 1) * 2 + 1
}

Second(n: int): int {
    return Next(n + 1)(n + 1) * 3 + 1
}

Third(n: int): int {
    if n == 2 {
        Panic("three deep")
    }
    return n
}

export Main(sys: &System): WriteError!void {
    n := sys.args.Len() - 1
    r := Next(n)(n) + 1
    try sys.Print("{r}\n", (r,))
}
`

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runPanic builds dir with args and runs it with env added, returning its
// standard error; the exit status must be 101.
func runPanic(t *testing.T, dir, name string, args []string, env ...string) string {
	t.Helper()
	exe := filepath.Join(dir, name+".exe")
	st, out, errOut := mk(append(append([]string{"build"}, args...), "-o", exe, dir)...)
	if st != 0 {
		t.Fatalf("mk build %v: status %d\n%s%s", args, st, out, errOut)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 101 {
		t.Fatalf("%s %v: %v, want exit 101\n%s", name, args, err, stderr.String())
	}
	return strings.ReplaceAll(stderr.String(), "\r\n", "\n")
}

func TestBacktrace(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.mk": btMain})
	const first = "panic: three deep at main.mk:16:9\n"
	// The backtrace is on by default and MK_BACKTRACE=0 turns it
	// off. Windows walks the unwind entries with frame pointers on and off
	// (rbp a frame register when on, which the walk's own context must
	// carry); Linux walks the frame chain, and with them off says so.
	for _, prof := range [][]string{nil, {"--release"}} {
		for _, fp := range []string{"--frame-pointers=on", "--frame-pointers=off"} {
			args := append(append([]string(nil), prof...), fp)
			name := strings.Join(args, "")
			got := runPanic(t, dir, "bt"+name, args, "MK_BACKTRACE=")
			if runtime.GOOS == "linux" && fp == "--frame-pointers=off" {
				if got != first+"backtrace: frame pointers were off\n" {
					t.Errorf("%v: stderr %q, want the report's line and the frame-pointers line", args, got)
				}
			} else if !strings.HasPrefix(got, first+"backtrace:\n  Third (main.mk)\n  Second (main.mk)\n  First (main.mk)\n  Main (main.mk)\n") {
				t.Errorf("%v: stderr\n%s\nwant the report's line, then Third, Second, First and Main", args, got)
			}
			if plain := runPanic(t, dir, "plain"+name, args, "MK_BACKTRACE=0"); plain != first {
				t.Errorf("%v with MK_BACKTRACE=0: stderr %q, want %q", args, plain, first)
			}
		}
	}
}

const dumpMain = `import std.runtime

export Main(sys: &System): WriteError!void {
    match runtime.WriteDump(sys.args[1]) {
        ok(_) { try sys.Print("ok\n", ()) }
        err(_) { try sys.Print("err\n", ()) }
    }
}
`

// TestWriteDump: the minidump's header (MDMP, version 0xA793) and its
// stream directory, each stream inside the file, with the thread, module
// and system-information streams; dbghelp.dll is loaded at run time, not
// imported. Linux's form is cut: there WriteDump fails with Unsupported.
func TestWriteDump(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.mk": dumpMain})
	exe := filepath.Join(dir, "dump.exe")
	if st, out, errOut := mk("build", "-o", exe, dir); st != 0 {
		t.Fatalf("mk build: status %d\n%s%s", st, out, errOut)
	}
	path := filepath.Join(dir, "d.dmp")
	out, err := exec.Command(exe, path).Output()
	if err != nil {
		t.Fatalf("%s: %v", exe, err)
	}
	if runtime.GOOS != "windows" {
		if string(out) != "err\n" {
			t.Errorf("WriteDump off Windows printed %q, want err (Unsupported)", out)
		}
		return
	}
	if string(out) != "ok\n" {
		t.Fatalf("WriteDump printed %q", out)
	}
	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	libs, _ := f.ImportedLibraries()
	f.Close()
	for _, l := range libs {
		if strings.EqualFold(l, "dbghelp.dll") {
			t.Errorf("the executable imports %s", l)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 32 || string(b[:4]) != "MDMP" || binary.LittleEndian.Uint16(b[4:]) != 0xA793 {
		t.Fatalf("no minidump header: % x", b[:min(32, len(b))])
	}
	n := int(binary.LittleEndian.Uint32(b[8:]))
	at := int(binary.LittleEndian.Uint32(b[12:]))
	if n == 0 || at+12*n > len(b) {
		t.Fatalf("stream directory: %d streams at %d, file %d bytes", n, at, len(b))
	}
	seen := map[uint32]bool{}
	for i := 0; i < n; i++ {
		e := b[at+12*i:]
		typ, size, rva := binary.LittleEndian.Uint32(e), binary.LittleEndian.Uint32(e[4:]), binary.LittleEndian.Uint32(e[8:])
		if typ != 0 && int(rva)+int(size) > len(b) {
			t.Errorf("stream %d (type %d): %d bytes at %d, past the file's %d", i, typ, size, rva, len(b))
		}
		seen[typ] = true
	}
	for _, want := range []uint32{3, 4, 7} { // ThreadList, ModuleList, SystemInfo
		if !seen[want] {
			t.Errorf("no stream of type %d in %v", want, seen)
		}
	}
}

// TestFreestandingKernelBuilds: tests/freestanding/kernel builds for
// x86_64-freestanding in dev and release. std.mem's intrinsic tests must not
// import std.runtime, which has no freestanding module (every freestanding
// build that reached std.mem would be E0202), so the Cycles block lives in
// std.runtime's own tests.
func TestFreestandingKernelBuilds(t *testing.T) {
	for _, prof := range [][]string{nil, {"--release"}} {
		out := filepath.Join(t.TempDir(), "kernel.elf")
		args := append(append([]string{"build", "--target", "x86_64-freestanding"}, prof...), "-o", out, filepath.Join("..", "..", "tests", "freestanding", "kernel"))
		if st, o, e := mk(args...); st != 0 {
			t.Errorf("mk %v: status %d\n%s%s", args, st, o, e)
		}
	}
}

const hookDumpMain = `import std.runtime

export extern MkPanicHook(info: &runtime.PanicInfo) {
    match runtime.WriteDump("hook.dmp") {
        ok(_) { }
        err(_) { }
    }
}

export Main(sys: &System): void {
    Panic("dump me")
}
`

// TestPanicHookWritesDump: a program's MkPanicHook may call
// runtime.WriteDump; the panic still reports and exits 101, and the dump it
// wrote is a minidump (Windows).
func TestPanicHookWritesDump(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("SKIP: WriteDump's Linux form is cut (Unsupported)")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.mk": hookDumpMain})
	exe := filepath.Join(dir, "hook.exe")
	if st, out, errOut := mk("build", "-o", exe, dir); st != 0 {
		t.Fatalf("mk build: status %d\n%s%s", st, out, errOut)
	}
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MK_BACKTRACE=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 101 || !strings.HasPrefix(stderr.String(), "panic: dump me at main.mk:") {
		t.Fatalf("%v, stderr %q; want exit 101 and the report", err, stderr.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "hook.dmp"))
	if err != nil || len(b) < 32 || string(b[:4]) != "MDMP" {
		t.Fatalf("hook.dmp: %v, %d bytes", err, len(b))
	}
}

// TestStackSizeBytes: StackSize.Bytes(n) runs a task on a slot of n
// rounded up to a power of two, from its class's own region reserved on first
// use: 8 MiB holds a 6 MiB recursion a 1 MiB Default slot could not, 100,000
// bytes gives a 128 KiB slot, 64 KiB overflows a 400 KiB recursion as a stack
// limit in its task, and more than 512 MiB panics at the scope; in dev and
// release.
func TestStackSizeBytes(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.mk": stackBytesMain})
	for _, prof := range [][]string{nil, {"--release"}} {
		exe := filepath.Join(dir, "ss"+strings.Join(prof, "")+".exe")
		if st, out, errOut := mk(append(append([]string{"build"}, prof...), "-o", exe, dir)...); st != 0 {
			t.Fatalf("mk build %v: status %d\n%s%s", prof, st, out, errOut)
		}
		for _, c := range []struct {
			arg, out, err string
			code          int
		}{
			{"", "1501 11 true true\n", "", 0},
			{"small", "", "panic: stack limit in task", 101},
			{"huge", "", "panic: StackSize.Bytes is above 512 MiB", 101},
		} {
			var args []string
			if c.arg != "" {
				args = []string{c.arg}
			}
			cmd := exec.Command(exe, args...)
			cmd.Env = append(os.Environ(), "MK_BACKTRACE=0")
			var so, se bytes.Buffer
			cmd.Stdout, cmd.Stderr = &so, &se
			err := cmd.Run()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			if code != c.code || so.String() != c.out || !strings.HasPrefix(se.String(), c.err) {
				t.Errorf("%v %q: exit %d, stdout %q, stderr %q; want %d, %q, %q", prof, c.arg, code, so.String(), se.String(), c.code, c.out, c.err)
			}
		}
	}
}

const stackBytesMain = `import std.atomic
import std.runtime

Deep(n: int): int {
    var buf: [4096]u8 = [0; 4096]
    buf[n % 4096] = 1
    if n == 0 {
        return int(buf[0])
    }
    return Deep(n - 1) + int(buf[n % 4096])
}

Work(n: int, out: &atomic.AtomicInt, room: &atomic.AtomicInt) {
    b := runtime.StackBounds()
    _ = room.Add(Trunc[i64](b.hi - b.lo))
    _ = out.Add(i64(Deep(n)))
}

export Main(sys: &System): WriteError!void {
    r := atomic.AtomicInt.New(0)
    big := atomic.AtomicInt.New(0)
    if sys.args.Len() > 1 {
        size := if sys.args[1] == "small" { 65536 } else { (1 << 29) + 1 }
        scope u := sys.io.ScopeWith(Limits(tasks: 1, stack: StackSize.Bytes(n: size))) {
            u.go Work(100, &r, &big)
        }
        return
    }
    scope s := sys.io.ScopeWith(Limits(tasks: 4, stack: StackSize.Bytes(n: 8 << 20))) {
        s.go Work(1500, &r, &big)
    }
    q := atomic.AtomicInt.New(0)
    small := atomic.AtomicInt.New(0)
    scope t := sys.io.ScopeWith(Limits(tasks: 4, stack: StackSize.Bytes(n: 100_000))) {
        t.go Work(10, &q, &small)
    }
    try sys.Print("{a} {b} {c} {d}\n", (a: r.Load(), b: q.Load(), c: big.Load() > 6 << 20, d: small.Load() < 128 << 10))
}
`
