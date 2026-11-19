// Why: many std tests need a whole Mk program built natively and run, and
// must not hang the machine or start WSL by accident.
// What: mkCmd, buildNative, runNative, runProgram and wantRun, the Linux gate
// (linuxRun, linuxGate, wslGuard) and the time bounds mkTimeout and runTimeout.
// How: writes the program over a copy of std, runs `mk build`, runs the
// executable and judges its output and exit code; skips without clang.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// nativeRun is one program's result.
type nativeRun struct {
	stdout string
	stderr string
	code   int
	took   time.Duration // the run of the executable only
}

// linuxOff is the skip reason of a leaf that would start wsl.exe while
// MK_LINUX=1 is not set. It is a
// plain skip, also under MK_GATE=1: the gate runs the Linux leaves in their
// own pass, under the WSL lock.
const linuxOff = "MK_LINUX=1 not set"

// linuxRun reports whether this run may start wsl.exe, MK_LINUX=1.
func linuxRun() bool { return os.Getenv("MK_LINUX") == "1" }

// linuxGate skips t unless MK_LINUX=1. Every Linux leaf calls it before
// its WSL probe, so the skip comes before any MK_GATE=1 failure.
func linuxGate(t testing.TB) {
	t.Helper()
	if !linuxRun() {
		t.Skip(linuxOff)
	}
}

// wslGuard panics when a wsl.exe is about to start without MK_LINUX=1: a
// leaf that reaches it missed its linuxGate.
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

// mkCmd runs mk with args in dir, MK_STD pointing at stdDir, and returns its
// combined output and exit status. mk test or mk run of x86_64-linux
// starts wsl.exe in the mk child, so it is guarded too.
func mkCmd(t testing.TB, dir, stdDir string, args ...string) (string, int) {
	t.Helper()
	if len(args) > 0 && (args[0] == "test" || args[0] == "run") &&
		(slices.Contains(args, "x86_64-linux") || slices.Contains(args, "--target=x86_64-linux")) {
		wslGuard()
	}
	ctx, cancel := context.WithTimeout(context.Background(), mkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, mkExe(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MK_STD="+stdDir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("mk %s: killed after %s (a hang holds the WSL lock for every other run)\n%s", strings.Join(args, " "), mkTimeout, out.String())
	}
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n"), status
}

// buildNative copies std with fixtures, writes src as main.mk and builds it
// (with extra build flags, for instance --release); it returns the
// executable's path. A failed build fails the test, except a missing
// toolchain, which skips it.
func buildNative(t *testing.T, fixtures map[string]string, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" && !slices.Contains(flags, "x86_64-linux") {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// mkTimeout bounds one mk command and runTimeout one run of a built program.
// A program that loops for ever (a table probe that never meets an empty
// slot, a scheduler that never wakes) would otherwise hold the WSL lock for
// every other run; std-coll's collRun uses the same bound.
const (
	mkTimeout  = 300 * time.Second
	runTimeout = 120 * time.Second
)

// runNative runs exe with stdin and args in its own directory, and fails the
// test if it runs longer than runTimeout (the process is killed).
func runNative(t *testing.T, exe, stdin string, args ...string) nativeRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run()
	r := nativeRun{stdout: out.String(), stderr: errb.String(), took: time.Since(start)}
	if ctx.Err() != nil {
		t.Fatalf("%s: killed after %s\nstdout:\n%s\nstderr:\n%s", filepath.Base(exe), runTimeout, r.stdout, r.stderr)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

// runProgram builds src over today's std and runs it with no input.
func runProgram(t *testing.T, src string) nativeRun {
	t.Helper()
	return runNative(t, buildNative(t, nil, src), "")
}

// wantRun fails the test unless r exited with code and printed stdout.
func wantRun(t *testing.T, r nativeRun, code int, stdout string) {
	t.Helper()
	if r.code != code || r.stdout != stdout {
		t.Errorf("exit %d, want %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s", r.code, code, r.stdout, stdout, r.stderr)
	}
}
