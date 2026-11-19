// Why: codegen's native tests compile IR, link CRT-free executables and run
// them on Windows and, through wsl.exe, on Linux.
// What: helpers (needLLVM, clangC, linkExe, runExe, wslClangRun, supportLL)
// and the skip rules (notRun, skipRunner, wslGuard).
// How: LLVM tools run as processes on IR, found by MK_LLVM, the install
// location, then PATH (codegen's tests cannot import toolchain). A missing
// tool or a WSL that does not answer skips with a message, and fails under
// MK_GATE=1. Each process has a deadline, so a wedged WSL costs seconds.

package codegen

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mk/internal/types"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// sharedDir holds build products several tests reuse (sharedTempDir); it
// is made on first use and removed by TestMain.
var (
	sharedMu  sync.Mutex
	sharedDir string
)

func TestMain(m *testing.M) {
	code := m.Run()
	sharedMu.Lock()
	if sharedDir != "" {
		os.RemoveAll(sharedDir)
	}
	sharedMu.Unlock()
	os.Exit(code)
}

// sharedTempDir is a new directory name under sharedDir that outlives the
// calling test.
func sharedTempDir(t *testing.T, name string) string {
	t.Helper()
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if sharedDir == "" {
		d, err := os.MkdirTemp("", "mk-codegen-test-")
		if err != nil {
			t.Fatal(err)
		}
		sharedDir = d
	}
	d := filepath.Join(sharedDir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

var (
	binOnce sync.Once
	binDir  string
)

// llvmBin is the directory holding clang, or "" when there is none.
func llvmBin() string {
	binOnce.Do(func() {
		var dirs []string
		if d := os.Getenv("MK_LLVM"); d != "" {
			dirs = append(dirs, d)
		}
		if runtime.GOOS == "windows" {
			dirs = append(dirs, `C:\Program Files\LLVM\bin`)
		} else {
			for v := 21; v >= 18; v-- {
				dirs = append(dirs, fmt.Sprintf("/usr/lib/llvm-%d/bin", v))
			}
		}
		for _, d := range dirs {
			if _, err := os.Stat(tool(d, "clang")); err == nil {
				binDir = d
				return
			}
		}
		if p, err := exec.LookPath("clang"); err == nil {
			binDir = filepath.Dir(p)
		}
	})
	return binDir
}

func tool(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// needLLVM skips the test when clang is missing.
func needLLVM(t *testing.T) string {
	t.Helper()
	d := llvmBin()
	if d == "" {
		t.Skip("clang not found (MK_LLVM, C:\\Program Files\\LLVM\\bin, PATH); the gate fails on this skip")
	}
	return d
}

func runTool(t *testing.T, argv ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = waitDelay
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%q: %v\n%s", argv, err, out)
	}
	return string(out)
}

// clangC compiles one .ll text to an object for target at opt ("-O0".."-O2",
// plus any further flags separated by spaces, "-O2 -flto=thin -ffunction-sections").
func clangC(t *testing.T, target types.Target, dir, name, ll, opt string) string {
	t.Helper()
	bin := needLLVM(t)
	src := filepath.Join(dir, name+".ll")
	if err := os.WriteFile(src, []byte(ll), 0o644); err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(dir, name+".o")
	args := append([]string{tool(bin, "clang"), "--target=" + target.LLVMTriple, "-c"}, strings.Fields(opt)...)
	runTool(t, append(args, "-Wno-override-module", "-fverify-intermediate-code", "-o", obj, src)...)
	return obj
}

// linkOpts are the extra link settings of one test executable.
type linkOpts struct {
	stack   uint64   // COFF /stack reserve; 0 is the default
	kernel  []string // kernel32 names the harness imports
	release bool
	// lto links ThinLTO bitcode objects at -O3; cache is its cache
	// directory ("" for none), as toolchain passes them.
	lto   bool
	cache string
	// noref keeps unreferenced sections on COFF (/opt:noref): lld-link
	// discards them by default when there is no /debug.
	noref bool
}

// linkExe links objects CRT-free: lld-link -nodefaultlib with mk_start as
// the entry and a kernel32 import library made by llvm-lib, or ld.lld
// -static with _start.
func linkExe(t *testing.T, target types.Target, dir string, objs []string, o linkOpts) string {
	t.Helper()
	bin := needLLVM(t)
	if target.OS == types.OSWindows {
		args := []string{tool(bin, "lld-link"), "/entry:mk_start", "/subsystem:console", "/nodefaultlib", "/Brepro",
			"/out:" + filepath.Join(dir, "t.exe")}
		if o.stack != 0 {
			args = append(args, fmt.Sprintf("/stack:%d", o.stack))
		}
		if o.release {
			args = append(args, "/opt:ref,icf")
		}
		if o.noref {
			args = append(args, "/opt:noref")
		}
		if o.lto {
			args = append(args, "/opt:lldlto=3")
			if o.cache != "" {
				args = append(args, "/lldltocache:"+o.cache)
			}
		}
		args = append(args, objs...)
		if len(o.kernel) > 0 {
			def := filepath.Join(dir, "kernel32.def")
			text := "LIBRARY kernel32.dll\nEXPORTS\n" + strings.Join(o.kernel, "\n") + "\n"
			if err := os.WriteFile(def, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			lib := filepath.Join(dir, "kernel32.lib")
			runTool(t, tool(bin, "llvm-lib"), "/def:"+def, "/machine:x64", "/out:"+lib)
			args = append(args, lib)
		}
		runTool(t, args...)
		return filepath.Join(dir, "t.exe")
	}
	exe := filepath.Join(dir, "t")
	args := []string{tool(bin, "ld.lld"), "-static", "-e", "_start", "-o", exe}
	if o.release {
		args = append(args, "--gc-sections")
	}
	if o.lto {
		args = append(args, "--lto-O3")
		if o.cache != "" {
			args = append(args, "--thinlto-cache-dir="+o.cache)
		}
	}
	runTool(t, append(args, objs...)...)
	return exe
}

// hostRunner is the command prefix that runs target's executables here, or
// a skip reason.
func hostRunner(target types.Target) ([]string, string) {
	switch {
	case target.Arch != types.ArchX64 || runtime.GOARCH != "amd64":
		return nil, "no runner for " + target.Name + " on this host"
	case target.OS == types.OSWindows && runtime.GOOS == "windows":
		return nil, ""
	case target.OS == types.OSLinux && runtime.GOOS == "linux":
		return nil, ""
	case target.OS == types.OSLinux && runtime.GOOS == "windows":
		if !linuxRun() {
			return nil, linuxOff
		}
		p, why := wslRunner()
		if why != "" {
			return nil, why
		}
		return []string{p, "-e"}, ""
	}
	return nil, "no runner for " + target.Name + " on this host"
}

// waitDelay bounds the wait for a killed process's pipes: a wsl.exe client
// killed at its deadline can leave the pipe open in the Linux side.
const waitDelay = 5 * time.Second

// wslProbe is how long `wsl.exe -e true` may take before WSL counts as not
// answering (it answers in well under a second when it is up).
const wslProbe = 30 * time.Second

var (
	wslOnce sync.Once
	wslMu   sync.Mutex
	wslExe  string
	wslWhy  string
)

// wslRunner is the path of a wsl.exe that answered `wsl.exe -e true` within
// wslProbe, or a reason starting "needs wsl.exe". It probes once per test
// binary, so a WSL that is down when the tests start costs one probe; one
// that stops answering later costs the one command that hits its deadline
// (wslDead), and every later WSL test stops at once.
func wslRunner() (string, string) {
	wslOnce.Do(func() {
		wslMu.Lock()
		defer wslMu.Unlock()
		wslGuard()
		p, err := exec.LookPath("wsl.exe")
		if err != nil {
			wslWhy = "needs wsl.exe: not found"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), wslProbe)
		defer cancel()
		cmd := exec.CommandContext(ctx, p, "-e", "true")
		cmd.WaitDelay = waitDelay
		if out, err := cmd.CombinedOutput(); err != nil {
			wslWhy = fmt.Sprintf("needs wsl.exe: `wsl.exe -e true` did not answer within %v: %v %q", wslProbe, err, out)
			return
		}
		wslExe = p
	})
	wslMu.Lock()
	defer wslMu.Unlock()
	return wslExe, wslWhy
}

// wslDead records that a WSL command ran past its deadline, so wslRunner
// gives every later caller a reason instead of another wait.
func wslDead(ctx context.Context, argv []string) {
	if ctx.Err() != context.DeadlineExceeded {
		return
	}
	wslMu.Lock()
	defer wslMu.Unlock()
	if wslWhy == "" {
		wslWhy = fmt.Sprintf("needs wsl.exe: %q ran past its deadline, so WSL counts as not answering for the rest of this test binary", argv)
	}
}

// wslClangPath is WSL's LLVM 21 clang. /usr/bin/clang may be older, below
// the LLVM 21 floor, so the versioned directory is
// named, as toolchain.DefaultDirs("linux") finds it first.
const wslClangPath = "/usr/lib/llvm-21/bin/clang"

// wslClangRun compiles ll for x86_64-linux with WSL's clang 21 at opt, the
// verifier on, with a deadline.
func wslClangRun(t *testing.T, ll, opt string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("WSL clang runs only from a Windows host (on Linux, clangC uses the host clang)")
	}
	if !linuxRun() {
		t.Skip(linuxOff)
	}
	wsl, why := wslRunner()
	if why != "" {
		notRun(t, "%s", why)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wsl, "-e", wslClangPath, "--target=x86_64-unknown-linux-gnu", "-x", "ir", "-c", opt,
		"-fverify-intermediate-code", "-Wno-override-module", "-o", "/dev/null", "-")
	cmd.WaitDelay = waitDelay
	cmd.Stdin = strings.NewReader(ll)
	if out, err := cmd.CombinedOutput(); err != nil {
		wslDead(ctx, cmd.Args)
		t.Fatalf("WSL clang %s: %v\n%s", opt, err, out)
	}
}

// gateRun reports whether this is the gate's run (tools/gate sets MK_GATE=1).
func gateRun() bool { return os.Getenv("MK_GATE") == "1" }

// linuxOff is the skip reason of a leaf that would start wsl.exe while
// MK_LINUX=1 is not set. It does not start with "needs ", so skipRunner
// skips it even under MK_GATE=1: the gate runs the Linux leaves in their
// own pass, under the WSL lock.
const linuxOff = "MK_LINUX=1 not set"

// linuxRun reports whether this run may start wsl.exe, MK_LINUX=1.
func linuxRun() bool { return os.Getenv("MK_LINUX") == "1" }

// wslGuard panics when a wsl.exe is about to start without MK_LINUX=1: a
// leaf that reaches it missed its linuxRun check.
//
// With MK_LINUX=1 it also panics unless the run holds the WSL lock
// (tools/wsl-lock.ps1 sets MK_WSL_LOCKED=1), so a Linux run outside the
// lock cannot happen by accident; MK_WSL_UNLOCKED_OK=1 allows one on
// purpose.
func wslGuard() {
	if !linuxRun() {
		panic("wsl.exe started without MK_LINUX=1; the leaf must skip with " + linuxOff)
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" && os.Getenv("MK_WSL_UNLOCKED_OK") != "1" {
		panic("wsl.exe started with MK_LINUX=1 outside the WSL lock (MK_WSL_LOCKED is not 1): run under pwsh -NoProfile -File tools/wsl-lock.ps1, or set MK_WSL_UNLOCKED_OK=1")
	}
}

// notRun stops a test that cannot run here because clang, lld or WSL is
// missing or does not answer: a SKIP, or under MK_GATE=1 a failure with the
// same text.
func notRun(t testing.TB, format string, args ...any) {
	t.Helper()
	if gateRun() {
		t.Fatalf("MK_GATE=1, not run: "+format, args...)
	}
	t.Skipf(format, args...)
}

// skipRunner stops a test hostRunner gave a reason for: a missing or silent
// WSL ("needs ...") is notRun; a target with no runner on this host (an
// aarch64 target, or Windows from Linux) is a plain skip.
func skipRunner(t testing.TB, why string) {
	t.Helper()
	if strings.HasPrefix(why, "needs ") {
		notRun(t, "%s", why)
	}
	t.Skip(why)
}

// wslPath is a Windows path as WSL sees it.
func wslPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) > 1 && p[1] == ':' {
		p = "/mnt/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// runExe runs exe (through a shell line when sh is not empty, Linux only:
// sh is a prefix such as "ulimit -s 512;") and returns stdout and the exit
// status.
func runExe(t *testing.T, target types.Target, exe, sh string) (string, int) {
	t.Helper()
	prefix, why := hostRunner(target)
	if why != "" {
		skipRunner(t, why)
	}
	path := exe
	if len(prefix) > 0 {
		path = wslPath(exe)
	}
	argv := append(append([]string(nil), prefix...), path)
	if sh != "" {
		argv = append(append([]string(nil), prefix...), "sh", "-c", sh+" exec "+path)
	}
	if len(prefix) > 0 {
		wslGuard()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && len(prefix) > 0 {
		wslDead(ctx, argv)
	}
	if stderr.Len() > 0 {
		t.Logf("stderr: %s", stderr.String())
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("%q: %v", argv, err)
		}
		code = shellExitCode(ee.ProcessState)
	}
	return stdout.String(), code
}

// shellExitCode is the exit status of a finished process as a shell
// reports it: 128 + the signal number for a process a signal killed (139
// for SIGSEGV, 132 for SIGILL), which is what wsl.exe relays, and the exit
// code otherwise. Go's ExitCode reports -1 for a signal.
func shellExitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

// runTargets are the targets native tests build and run.
var runTargets = []types.Target{types.X86_64Windows, types.X86_64Linux}

// supportLL is the support pack of target with no unit and no env.
func supportLL(t *testing.T, target types.Target) string {
	t.Helper()
	var b bytes.Buffer
	if err := EmitSupport(&Unit{}, nil, Config{Target: target}, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// harnessOS is the IR of h_exit(i32) and h_write(ptr, i64) for target's OS:
// ExitProcess and WriteFile through kernel32, or raw exit_group and write.
func harnessOS(target types.Target) (string, []string) {
	if target.OS == types.OSWindows {
		return `
declare void @ExitProcess(i32) noreturn nounwind
declare ptr @GetStdHandle(i32) nounwind
declare i32 @WriteFile(ptr, ptr, i32, ptr, ptr) nounwind

define internal void @h_exit(i32 %c) noreturn nounwind {
  call void @ExitProcess(i32 %c)
  unreachable
}

define internal void @h_write(ptr %b, i64 %n) nounwind {
  %h = call ptr @GetStdHandle(i32 -11)
  %w = alloca i32, align 4
  %n32 = trunc i64 %n to i32
  %r = call i32 @WriteFile(ptr %h, ptr %b, i32 %n32, ptr %w, ptr null)
  ret void
}
`, []string{"ExitProcess", "GetStdHandle", "WriteFile"}
	}
	return `
define internal void @h_exit(i32 %c) noreturn nounwind {
  %c64 = sext i32 %c to i64
  %r = call i64 asm sideeffect "syscall", "={rax},{rax},{rdi},~{rcx},~{r11},~{memory}"(i64 231, i64 %c64)
  unreachable
}

define internal void @h_write(ptr %b, i64 %n) nounwind {
  %r = call i64 asm sideeffect "syscall", "={rax},{rax},{rdi},{rsi},{rdx},~{rcx},~{r11},~{memory}"(i64 1, i64 1, ptr %b, i64 %n)
  ret void
}
`, nil
}

// llBytes is a c"..." constant body.
func llBytes(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			s.WriteByte(c)
		} else {
			fmt.Fprintf(&s, "\\%02X", c)
		}
	}
	return s.String()
}

func mkdir(d string) error { return os.MkdirAll(d, 0o755) }
