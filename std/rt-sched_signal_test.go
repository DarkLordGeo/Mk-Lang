// Why: a signal wait must work against signals the OS really delivers, which
// the scheduler-side tests (std/task/tests_signal.mk) cannot send.
// What: the program builder and runner, and TestRtSched_LinuxSignal (SIGINT,
// SIGTERM, SIGHUP in WSL at 1 and 8 carriers, io_uring and epoll).
// How: a program in the cookbook's shutdown shape waits in a watcher task
// that cancels the scope, prints "ready" and then "stopped", and exits 0.

package std

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rtsSignalProgram is tests/programs/tasks/signal.mk waiting for sig.
func rtsSignalProgram(sig string) string {
	return fmt.Sprintf(`import std.proc
import std.time

Watch(s: &Scope, io: Io) {
    proc.WaitSignal(io, proc.Signal.%[1]s) else {
        return
    }
    s.Cancel()
}

// Prime's wait installs the watch; Main cancels it after 100 ms, so the
// signal's default action is gone before "ready" is printed.
Prime(io: Io) {
    proc.WaitSignal(io, proc.Signal.%[1]s) else {
        return
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    scope p := sys.io.Scope() {
        p.go Prime(sys.io)
        sys.io.Sleep(time.Millis(100)) else {}
        p.Cancel()
    }
    try Print(mut out, "ready\n", ())
    try out.Flush()
    scope s := sys.io.Scope() {
        s.go Watch(&s, sys.io)
        for {
            sys.io.Sleep(time.Millis(100)) else {
                break
            }
        }
    }
    try Print(mut out, "stopped\n", ())
}
`, sig)
}

// rtsBuildSignal builds the program for sig on target into root.
func rtsBuildSignal(t *testing.T, root, sig, target string) string {
	t.Helper()
	stdDir := copyStd(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(rtsSignalProgram(sig)), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "sig_"+strings.ToLower(sig))
	if target == "x86_64-windows" {
		exe += ".exe"
	}
	if out, st := mkCmd(t, root, stdDir, "build", "--release", "--target", target, "-o", exe, "main.mk"); st != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", st, out)
	}
	return exe
}

// rtsSignalRun starts cmd, waits for "ready", calls send with its pid and
// returns its standard output and exit status; 20 s bound.
func rtsSignalRun(t *testing.T, cmd *exec.Cmd, send func(pid int) error) (string, int) {
	t.Helper()
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(20*time.Second, func() { cmd.Process.Kill() })
	defer timer.Stop()
	r := bufio.NewReader(pipe)
	first, _ := r.ReadString('\n')
	// The program's watch is installed before "ready" (rtsSignalProgram),
	// but its watcher reads the count's baseline after it, so a signal sent
	// in between is not a delivery for that wait: send every 200 ms until
	// the program ends (a watched signal never ends it by itself).
	done := make(chan struct{})
	sent := make(chan int, 1)
	var sendErr error
	if first == "ready\n" {
		go func() {
			n := 0
			for {
				if err := send(cmd.Process.Pid); err == nil {
					n++
				} else if n == 0 {
					sendErr = err
				}
				select {
				case <-done:
					sent <- n
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
		}()
	} else {
		sent <- 0
	}
	rest, _ := r.ReadString(0)
	err = cmd.Wait()
	close(done)
	if n := <-sent; n == 0 {
		t.Fatalf("no signal sent: %v (first line %q)", sendErr, first)
	} else if n > 1 {
		t.Logf("%d sends", n)
	}
	st := 0
	if ee, ok := err.(*exec.ExitError); ok {
		st = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if st != 0 {
		t.Logf("stderr:\n%s", stderr.String())
	}
	return first + rest, st
}

// TestRtSched_LinuxSignal sends SIGINT, SIGTERM and SIGHUP to the program
// waiting for Interrupt, Terminate and Hangup on x86_64-linux in WSL, at 1
// and 8 carriers, on io_uring and epoll, in one wsl.exe client (run it under
// tools/wsl-lock.ps1 with go test -p 1).
func TestRtSched_LinuxSignal(t *testing.T) {
	rtsNeedWSL(t)
	root := t.TempDir()
	sigs := map[string]string{"Interrupt": "INT", "Terminate": "TERM", "Hangup": "HUP"}
	var sh strings.Builder
	fmt.Fprintf(&sh, "cd %q\n", rtsWSLPath(root))
	for _, sig := range []string{"Interrupt", "Terminate", "Hangup"} {
		dir := filepath.Join(root, sig)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		exe := rtsBuildSignal(t, dir, sig, "x86_64-linux")
		for _, io := range []string{"", "epoll"} {
			for _, n := range []int{1, 8} {
				// Wait for "ready" (at most 10 s), then send every 200 ms until
				// "stopped" (at most 50 times; rtsSignalRun says why), and wait
				// for the end. timeout bounds a hang and passes INT, TERM and
				// HUP on to the program.
				fmt.Fprintf(&sh, `rm -f out.txt err.txt
MK_IO=%s MK_CARRIERS=%d timeout -s KILL 30 %q > out.txt 2> err.txt & p=$!
i=0; while ! grep -q ready out.txt 2>/dev/null && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done
j=0; while ! grep -q stopped out.txt && [ $j -lt 50 ]; do kill -%s $p 2>/dev/null; j=$((j+1)); sleep 0.2; done
wait $p; st=$?
printf '%%s io=%%s carriers=%%s sends=%%s exit=%%s out=%%s\n' %s '%s' %d $j $st "$(tr '\n' ',' < out.txt)"
`, io, n, rtsWSLPath(exe), sigs[sig], sig, io, n)
			}
		}
	}
	wslGuard()
	out, err := exec.Command("wsl.exe", "-e", "sh", "-c", sh.String()).Output()
	if err != nil {
		t.Fatalf("wsl.exe: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 12 {
		t.Fatalf("want 12 runs, have:\n%s", out)
	}
	for _, l := range lines {
		t.Log(l)
		if !strings.HasSuffix(l, "exit=0 out=ready,stopped,") {
			t.Errorf("%s", l)
		}
	}
}
