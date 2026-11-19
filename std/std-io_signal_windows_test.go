// Why: Ctrl+C and Ctrl+Break reach only the processes of one console, so
// the signal test needs a console of its own on Windows.
// What: lib146Windows and the helper process TestSTDIO_SignalHelper, the
// Windows half of std-io_signal_test.go.
// How: the program runs on a console of its own (CREATE_NEW_CONSOLE, window
// hidden), and a helper, this test binary started again with
// MK_STDIO_SIGHELPER, attaches to that console and raises Ctrl+C or
// Ctrl+Break there with GenerateConsoleCtrlEvent, as a user pressing the
// keys would. Only the processes of that console receive it.

//go:build windows

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	stdioCreateNewConsole = 0x00000010
	stdioCtrlC            = 0 // CTRL_C_EVENT: Interrupt
	stdioCtrlBreak        = 1 // CTRL_BREAK_EVENT: Terminate
	stdioControlCExit     = 0xC000013A
)

// TestSTDIO_SignalHelper is the signal test's helper process, not a test: started
// with MK_STDIO_SIGHELPER=pid:event, it leaves its own console, attaches
// to pid's, ignores the event itself and raises it for the whole console.
// Exit 0 when the event was raised.
func TestSTDIO_SignalHelper(t *testing.T) {
	v := os.Getenv("MK_STDIO_SIGHELPER")
	if v == "" {
		// Not a test: it passes at once unless started as the helper
		// (a Skip would show in every run as a skipped test).
		return
	}
	pid, ev, ok := strings.Cut(v, ":")
	p, err1 := strconv.Atoi(pid)
	e, err2 := strconv.Atoi(ev)
	if !ok || err1 != nil || err2 != nil {
		fmt.Fprintln(os.Stderr, "bad MK_STDIO_SIGHELPER", v)
		os.Exit(2)
	}
	k := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = k.NewProc("FreeConsole").Call()
	if r, _, err := k.NewProc("AttachConsole").Call(uintptr(p)); r == 0 {
		fmt.Fprintln(os.Stderr, "AttachConsole:", err)
		os.Exit(3)
	}
	// Its own handler, called before Go's (handlers run last added
	// first), takes the event, so the helper survives Ctrl+Break too.
	_, _, _ = k.NewProc("SetConsoleCtrlHandler").Call(syscall.NewCallback(func(uint32) uintptr { return 1 }), 1)
	if r, _, err := k.NewProc("GenerateConsoleCtrlEvent").Call(uintptr(e), 0); r == 0 {
		fmt.Fprintln(os.Stderr, "GenerateConsoleCtrlEvent:", err)
		os.Exit(4)
	}
	os.Exit(0)
}

// stdioRaise raises event on pid's console through the helper.
func stdioRaise(t *testing.T, pid, event int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestSTDIO_SignalHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), fmt.Sprintf("MK_STDIO_SIGHELPER=%d:%d", pid, event))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		// The program may have ended already; the judge says what it did.
		t.Logf("signal helper: %v\n%s", err, out)
	}
}

// lib146Windows runs each case: start the program on its own console,
// wait for "ready", raise the event every 100 ms until "got" shows (at
// most 8 s), raise it once more, and wait for the exit (20 s at most).
func lib146Windows(t *testing.T, exe string) {
	// A process that ignores Ctrl+C (SetConsoleCtrlHandler(NULL, TRUE), as
	// Git Bash does for the go test it starts) passes that to its children,
	// and Windows then drops Ctrl+C before any handler. Watching Interrupt
	// clears it (std.sys's EnableCtrlC), so the "int" case runs
	// twice: started with Ctrl+C disabled (inherited from this process) and
	// enabled. "term" (Ctrl+Break is never ignored) and "none" (the default
	// action must fire) start enabled. This process is left enabled.
	ctrlState := func(disabled bool) {
		arg := uintptr(0)
		if disabled {
			arg = 1
		}
		if r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, arg); r == 0 {
			t.Fatalf("SetConsoleCtrlHandler(NULL, %d): %v", arg, err)
		}
	}
	defer ctrlState(false)
	for _, c := range []struct {
		arg      string
		event    int
		disabled bool
	}{{"int", stdioCtrlC, true}, {"int", stdioCtrlC, false}, {"term", stdioCtrlBreak, false}, {"none", stdioCtrlC, false}} {
		ctrlState(c.disabled)
		cmd := exec.Command(exe, c.arg)
		cmd.Dir = filepath.Dir(exe)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: stdioCreateNewConsole, HideWindow: true}
		pr, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var errb bytes.Buffer
		cmd.Stderr = &errb
		err = cmd.Start()
		ctrlState(false)
		if err != nil {
			t.Fatal(err)
		}
		lines := make(chan string, 16)
		var out strings.Builder
		go func() {
			sc := bufio.NewScanner(pr)
			for sc.Scan() {
				lines <- sc.Text()
			}
			close(lines)
		}()
		wait := func(want string, d time.Duration) bool {
			deadline := time.After(d)
			for {
				select {
				case l, ok := <-lines:
					if !ok {
						return false
					}
					out.WriteString(l + "\n")
					if l == want {
						return true
					}
				case <-deadline:
					return false
				}
			}
		}
		pid := cmd.Process.Pid
		early := false
		if wait("ready", 10*time.Second) {
			// WaitSignal must not return before a signal: nothing is raised yet.
			if c.arg != "none" && wait("got", 300*time.Millisecond) {
				early = true
			}
			got := false
			for i := 0; i < 80 && !got && c.arg != "none"; i++ {
				stdioRaise(t, pid, c.event)
				got = wait("got", 100*time.Millisecond)
			}
			stdioRaise(t, pid, c.event)
		}
		// The rest of the output, up to the end of the pipe (os/exec: read
		// it all before Wait).
		deadline := time.After(20 * time.Second)
	drain:
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					break drain
				}
				out.WriteString(l + "\n")
			case <-deadline:
				break drain
			}
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		var werr error
		select {
		case werr = <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			werr = <-done
		}
		r := nativeRun{stdout: out.String(), stderr: errb.String()}
		if early {
			r.stderr += "got before any signal\n"
		}
		if ee, ok := werr.(*exec.ExitError); ok {
			r.code = ee.ExitCode()
		} else if werr != nil {
			t.Fatal(werr)
		}
		if c.arg == "none" {
			if uint32(r.code) != stdioControlCExit || r.stdout != "ready\n" {
				t.Errorf("unwatched Ctrl+C: exit %#x, stdout %q, stderr %q; want STATUS_CONTROL_C_EXIT and only ready", uint32(r.code), r.stdout, r.stderr)
			}
			continue
		}
		what := c.arg
		if c.disabled {
			what += " (started with Ctrl+C disabled)"
		}
		lib146Judge(t, what, r)
	}
}
