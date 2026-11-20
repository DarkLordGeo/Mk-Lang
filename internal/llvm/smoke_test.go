// Why: printed IR is only right if clang accepts it, on every target.
// What: TestLLVMSyntaxSmoke, TestWSLEnvForwardsSymbolization and the clang
// harness (clangCommand, compile, smokeTriples) other tests use.
// How: clang 21 compiles each module with the IR verifier on, natively for the
// four hosted targets and in WSL for x86_64-linux; a missing clang or WSL is a
// skip.

package llvm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"mk/internal/toolchain"
)

// compiler is one clang: a host binary, or WSL's clang fed on stdin.
type compiler struct {
	name string
	argv []string // command prefix; the IR is read from stdin ("-")
}

func (c compiler) run(stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runDeadline)
	defer cancel()
	cmd := clangCommand(ctx, c.argv[0], append(append(c.argv[1:len(c.argv):len(c.argv)], args...), "-")...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("%s ran past its %v deadline: %w", c.name, runDeadline, err)
		out.WriteString("\n" + err.Error())
	}
	return out.String(), err
}

// runDeadline bounds one clang run; probeDeadline the first WSL call (a WSL
// that is up answers in well under a second).
const (
	runDeadline   = 120 * time.Second
	probeDeadline = 30 * time.Second
	waitDelay     = 5 * time.Second
)

// clangCommand is a bounded clang command whose crash handler does not
// symbolize. TestLLVMVerifierOn makes clang abort on purpose ("Broken module
// found"), and WSL's clang 18 can hang in llvm-symbolizer after that abort;
// LLVM_DISABLE_SYMBOLIZATION=1 avoids it. So the variable is set here and
// forwarded into WSL through WSLENV (an existing WSLENV is kept), and the
// run has a deadline.
func clangCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if strings.EqualFold(filepath.Base(name), "wsl.exe") {
		wslGuard()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = waitDelay
	cmd.Env = append(os.Environ(), "LLVM_DISABLE_SYMBOLIZATION=1", "WSLENV="+wslEnv(os.Getenv("WSLENV")))
	return cmd
}

// linuxOff is the skip reason of a leaf that would start wsl.exe while
// MK_LINUX=1 is not set; tools/gate runs those leaves in their own pass,
// under the WSL lock.
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

// wslEnv is WSLENV with LLVM_DISABLE_SYMBOLIZATION/u added once.
func wslEnv(cur string) string {
	for _, e := range strings.Split(cur, ":") {
		if name, _, _ := strings.Cut(e, "/"); name == "LLVM_DISABLE_SYMBOLIZATION" {
			return cur
		}
	}
	if cur == "" {
		return "LLVM_DISABLE_SYMBOLIZATION/u"
	}
	return cur + ":LLVM_DISABLE_SYMBOLIZATION/u"
}

// wslClangPath is WSL's LLVM 21 clang: /usr/bin/clang may be older than
// LLVM 21, the oldest LLVM Mk supports.
const wslClangPath = "/usr/lib/llvm-21/bin/clang"

var (
	hostOnce, wslOnce sync.Once
	hostClang         *compiler
	wslClang          *compiler
	hostWhy, wslWhy   string
)

func findHostClang(t *testing.T) compiler {
	t.Helper()
	hostOnce.Do(func() {
		// The clang mk itself would use. PATH alone is wrong on Linux:
		// Ubuntu's /usr/bin/clang is LLVM 18, which does not parse range().
		tl, err := toolchain.Discover(toolchain.Options{})
		if err != nil {
			hostWhy = err.Error() + "; the gate fails on this skip"
			return
		}
		hostClang = &compiler{name: "clang", argv: []string{tl.Clang}}
	})
	if hostClang == nil {
		t.Skip(hostWhy)
	}
	return *hostClang
}

func findWSLClang(t *testing.T) compiler {
	t.Helper()
	if runtime.GOOS == "windows" && !linuxRun() {
		t.Skip(linuxOff)
	}
	wslOnce.Do(func() {
		if runtime.GOOS != "windows" {
			wslWhy = "WSL clang runs only from a Windows host"
			return
		}
		wslGuard()
		wsl, err := exec.LookPath("wsl.exe")
		if err != nil {
			wslWhy = "wsl.exe not found; the gate fails on this skip"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), probeDeadline)
		defer cancel()
		out, err := clangCommand(ctx, wsl, "-e", wslClangPath, "--version").CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			wslWhy = fmt.Sprintf("`wsl.exe -e clang --version` did not answer within %v; the gate fails on this skip", probeDeadline)
			return
		}
		if err != nil {
			wslWhy = "clang not found in WSL (" + strings.TrimSpace(string(out)) + "); the gate fails on this skip"
			return
		}
		wslClang = &compiler{name: "wsl clang 21", argv: []string{wsl, "-e", wslClangPath}}
	})
	if wslClang == nil {
		t.Skip(wslWhy)
	}
	return *wslClang
}

// smokeTriples are Mk's four hosted targets (types.Targets()).
var smokeTriples = []target{
	{name: "x86_64-windows", triple: "x86_64-pc-windows-msvc", arch: "x86_64", os: "windows"},
	{name: "x86_64-linux", triple: "x86_64-unknown-linux-gnu", arch: "x86_64", os: "linux"},
	{name: "aarch64-linux", triple: "aarch64-unknown-linux-gnu", arch: "aarch64", os: "linux"},
	{name: "aarch64-macos", triple: "arm64-apple-macosx11.0.0", arch: "aarch64", os: "darwin"},
}

// resolve fills tg's triple and data layout with what c prints for a module
// that has neither (the way toolchain reads them).
func resolve(t *testing.T, c compiler, tg target) target {
	t.Helper()
	out, err := c.run([]byte("define void @f() {\n  ret void\n}\n"),
		"--target="+tg.triple, "-Wno-override-module", "-S", "-emit-llvm", "-x", "ir", "-o", "-")
	if err != nil {
		t.Fatalf("%s: probing %s: %v\n%s", c.name, tg.triple, err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "target datalayout = "); ok {
			tg.layout = strings.Trim(v, `"`)
		}
		if v, ok := strings.CutPrefix(line, "target triple = "); ok {
			tg.triple = strings.Trim(v, `"`)
		}
	}
	if tg.layout == "" {
		t.Fatalf("%s: no data layout for %s in\n%s", c.name, tg.triple, out)
	}
	return tg
}

// compile runs c on m at -O0 and -O2 with the verifier on and requires a
// clean exit with no output; it returns the output of a failing run.
func compile(c compiler, tg target, m *Module) (string, bool) {
	var buf bytes.Buffer
	if _, err := m.WriteTo(&buf); err != nil {
		return err.Error(), false
	}
	devnull := os.DevNull
	if strings.HasPrefix(c.name, "wsl ") { // inside WSL, NUL would be a file in the package directory
		devnull = "/dev/null"
	}
	for _, opt := range []string{"-O0", "-O2"} {
		out, err := c.run(buf.Bytes(), "--target="+tg.triple, "-fverify-intermediate-code", "-Werror",
			"-fno-crash-diagnostics", opt, "-c", "-x", "ir", "-o", devnull)
		if err != nil || strings.TrimSpace(out) != "" {
			return c.name + " " + tg.name + " " + opt + ": " + strings.TrimSpace(out), false
		}
	}
	return "", true
}

func TestLLVMSyntaxSmoke(t *testing.T) {
	host := findHostClang(t)
	for _, tg := range smokeTriples {
		t.Run("clang/"+tg.name, func(t *testing.T) {
			t.Parallel()
			r := resolve(t, host, tg)
			if out, ok := compile(host, r, buildEvery(r)); !ok {
				t.Error(out)
			}
		})
	}
	t.Run("wsl-clang/x86_64-linux", func(t *testing.T) {
		t.Parallel()
		w := findWSLClang(t)
		r := resolve(t, w, smokeTriples[1])
		if out, ok := compile(w, r, buildEvery(r)); !ok {
			t.Error(out)
		}
	})
}

// TestWSLEnvForwardsSymbolization: every clang run disables symbolization,
// and WSL sees the variable, since a WSL clang that crashes (as
// TestLLVMVerifierOn makes it) can hang in llvm-symbolizer. A
// fast symbolizer would hide a broken WSLENV, so this asks WSL directly.
func TestWSLEnvForwardsSymbolization(t *testing.T) {
	for _, c := range []struct{ cur, want string }{
		{"", "LLVM_DISABLE_SYMBOLIZATION/u"},
		{"GOPATH/l", "GOPATH/l:LLVM_DISABLE_SYMBOLIZATION/u"},
		{"LLVM_DISABLE_SYMBOLIZATION/u:X", "LLVM_DISABLE_SYMBOLIZATION/u:X"},
		{"X:LLVM_DISABLE_SYMBOLIZATION", "X:LLVM_DISABLE_SYMBOLIZATION"},
	} {
		if got := wslEnv(c.cur); got != c.want {
			t.Errorf("wslEnv(%q) = %q, want %q", c.cur, got, c.want)
		}
	}
	t.Run("wsl", func(t *testing.T) {
		w := findWSLClang(t)
		ctx, cancel := context.WithTimeout(context.Background(), runDeadline)
		defer cancel()
		out, err := clangCommand(ctx, w.argv[0], "-e", "sh", "-c", "echo \"[$LLVM_DISABLE_SYMBOLIZATION]\"").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "[1]" {
			t.Errorf("WSL sees LLVM_DISABLE_SYMBOLIZATION as %q (err %v), want [1]", strings.TrimSpace(string(out)), err)
		}
	})
}
