// Why: a Linux run through wsl.exe that times out must leave no process
// inside the WSL VM; orphans pile up and hold ports.
// What: TestTimedOutLinuxRunLeavesNoProcess with its control
// subtest, hangProgram, wslProcesses and wslKillAll.
// How: runs a program that ignores the usual signals, times it out, and
// looks for it with pgrep inside WSL.
//
// Cancelling the run once killed wsl.exe only: the Linux program kept
// running (an orphaned ./spawn_set ran 5 h 45 min), and the runner's retry
// started a second copy beside it.
//
// On WSL Ubuntu, killing wsl.exe does end a plain busy loop inside the VM,
// but not a program that watches Hangup, Terminate and Interrupt
// (std.proc.WaitSignal, as the runtime's poller does for any signal a task
// waits on): that one lives on. So the control and the test both use such a
// program.

package corpus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// hangProgram never ends: three tasks wait for Hangup, Terminate and
// Interrupt in a loop, so each signal's default action is gone
// (std.proc.WaitSignal) and only SIGKILL ends it, as for any
// program that watches signals.
const hangProgram = `import std.proc

Watch(io: Io, sig: proc.Signal) {
    for {
        proc.WaitSignal(io, sig) else {
            return
        }
    }
}

export Main(sys: &System) {
    scope s := sys.io.Scope() {
        s.go Watch(sys.io, proc.Signal.Hangup)
        s.go Watch(sys.io, proc.Signal.Terminate)
        s.go Watch(sys.io, proc.Signal.Interrupt)
    }
}
`

// wslProcesses lists the processes inside WSL whose command line holds
// pattern (pgrep -af), or "" when there are none.
func wslProcesses(t *testing.T, pattern string) string {
	t.Helper()
	wslGuard()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wsl.exe", "-e", "pgrep", "-af", pattern).Output()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSpace(string(out))
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return ""
	}
	t.Fatalf("wsl.exe -e pgrep -af %s: %v", pattern, err)
	return ""
}

// wslKillAll kills every process inside WSL whose command line holds
// pattern: only this test's own programs, whose names are unique.
func wslKillAll(pattern string) {
	wslGuard()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "wsl.exe", "-e", "pkill", "-KILL", "-f", pattern).Run()
}

func TestTimedOutLinuxRunLeavesNoProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Log("a Linux host runs the program directly, and the context kills it; no WSL VM to leave it in")
		return
	}
	tools := needLLVM(t)
	dir := t.TempDir()
	name := fmt.Sprintf("hangkill%d", time.Now().UnixNano()%1_000_000_000)
	if err := os.WriteFile(filepath.Join(dir, name+".mk"), []byte(hangProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	r := testRunner(t, dir, tools)
	exe, err := r.Build(name+".mk", "x86_64-linux", "dev")
	if err != nil {
		t.Fatal(err)
	}
	needWSLRun(t, "x86_64-linux", true)
	defer wslKillAll(name)

	// Control: the old command line, wsl.exe with no bound inside the VM,
	// killed from the Windows side. pgrep must see the program live on, so
	// the check below can see one.
	t.Run("control", func(t *testing.T) {
		argv, err := runArgv("x86_64-linux", filepath.Dir(exe), exe, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = filepath.Dir(exe)
		cmd.WaitDelay = 5 * time.Second
		// As runOnce runs it: stdin from a reader, stdout and stderr to
		// buffers, so all three are pipes.
		var out, errOut bytes.Buffer
		cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(nil), &out, &errOut
		_ = cmd.Run()
		if ps := wslProcesses(t, name); ps == "" {
			t.Errorf("killing wsl.exe ended the Linux program too; the premise of CORPUS-REV-2 no longer holds on this WSL")
		} else {
			t.Logf("killing wsl.exe left: %s", ps)
		}
		wslKillAll(name)
		if ps := wslProcesses(t, name); ps != "" {
			t.Fatalf("pkill did not end the control's program: %s", ps)
		}
	})

	start := time.Now()
	got, err := r.Run(ProgramCase{Program: name + ".mk", Timeout: 2 * time.Second}, "x86_64-linux", "dev")
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !got.TimedOut || got.Exit != -1 {
		t.Errorf("a program that loops forever: exit %d, timed out %v; want a timeout", got.Exit, got.TimedOut)
	}
	if ps := wslProcesses(t, name); ps != "" {
		t.Errorf("after Runner.Run timed out (%v), WSL still runs: %s", took.Round(time.Millisecond), ps)
	}
	if limit := 2 * (2*time.Second + wslGrace); took > limit {
		t.Errorf("Runner.Run took %v, over two attempts' bound %v", took, limit)
	}
	t.Logf("two attempts of 2 s timed out in %v, no process left", took.Round(time.Millisecond))
}
