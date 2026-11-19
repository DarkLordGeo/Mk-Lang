// Why: a watching process must get Ctrl+C on Windows even when it started
// with Ctrl+C disabled, and IOCP's ReissueAccept handoff must not race.
// What: TestRtIo_CtrlIgnored, TestRtIo_ReissueRace and the helper
// process TestRtIo_CtrlHelper; the Windows half of rt-io_ignoredsignal_test.go.
// How: the probe gets a hidden console of its own (CREATE_NEW_CONSOLE) and
// inherits this process's Ctrl+C state, set by SetConsoleCtrlHandler(NULL,
// TRUE or FALSE) just before CreateProcess; this test binary, started again
// with MK_RTIO55_HELPER, attaches to that console and raises Ctrl+C with
// GenerateConsoleCtrlEvent. CREATE_NEW_PROCESS_GROUP with CREATE_NEW_CONSOLE
// does not disable Ctrl+C (measured: under PowerShell the unwatched control
// died of STATUS_CONTROL_C_EXIT; Git Bash starts go test with it disabled).

//go:build windows

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	rtio55NewConsole   = 0x00000010 // CREATE_NEW_CONSOLE
	rtio55NewGroup     = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	rtio55Detached     = 0x00000008 // DETACHED_PROCESS
	rtio55ControlCExit = 0xC000013A // STATUS_CONTROL_C_EXIT
)

// TestRtIo_CtrlHelper is the helper process, not a test: started with
// MK_RTIO55_HELPER=pid, it leaves its own console, attaches to pid's,
// ignores the event itself and raises Ctrl+C for the whole console. Exit 0
// when the event was raised.
func TestRtIo_CtrlHelper(t *testing.T) {
	v := os.Getenv("MK_RTIO55_HELPER")
	if v == "" {
		return
	}
	p, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad MK_RTIO55_HELPER", v)
		os.Exit(2)
	}
	k := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = k.NewProc("FreeConsole").Call()
	if r, _, err := k.NewProc("AttachConsole").Call(uintptr(p)); r == 0 {
		fmt.Fprintln(os.Stderr, "AttachConsole:", err)
		os.Exit(3)
	}
	_, _, _ = k.NewProc("SetConsoleCtrlHandler").Call(syscall.NewCallback(func(uint32) uintptr { return 1 }), 1)
	if r, _, err := k.NewProc("GenerateConsoleCtrlEvent").Call(0, 0); r == 0 {
		fmt.Fprintln(os.Stderr, "GenerateConsoleCtrlEvent:", err)
		os.Exit(4)
	}
	os.Exit(0)
}

// rtio55Raise raises Ctrl+C on pid's console through the helper.
func rtio55Raise(t *testing.T, pid int) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestRtIo_CtrlHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), fmt.Sprintf("MK_RTIO55_HELPER=%d", pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		// The probe may have ended already; its output says what it did.
		t.Logf("ctrl helper: %v\n%s", err, out)
	}
}

// rtio55CtrlState sets this process's Ctrl+C state, which the next child
// inherits: disabled (SetConsoleCtrlHandler(NULL, TRUE)) or enabled.
func rtio55CtrlState(t *testing.T, disabled bool) {
	t.Helper()
	arg := uintptr(0)
	if disabled {
		arg = 1
	}
	if r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, arg); r == 0 {
		t.Fatalf("SetConsoleCtrlHandler(NULL, %d): %v", arg, err)
	}
}

// rtio55Run starts exe mode on a console of its own, with Ctrl+C disabled
// at start when disabled (inherited from this process, and in a new process
// group as well), raises Ctrl+C every 100 ms from "ready" until it exits (at
// most 20 s), and returns its output and exit code. It leaves this process
// with Ctrl+C enabled.
func rtio55Run(t *testing.T, exe, mode string, disabled bool) (string, uint32) {
	t.Helper()
	cmd := exec.Command(exe, mode)
	flags := uint32(rtio55NewConsole)
	if disabled {
		flags |= rtio55NewGroup
	}
	rtio55CtrlState(t, disabled)
	defer rtio55CtrlState(t, false)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	pr, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var out strings.Builder
	deadline := time.After(20 * time.Second)
	ready, open := false, true
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for open {
		select {
		case l, ok := <-lines:
			if !ok {
				open = false
				break
			}
			out.WriteString(l + "\n")
			if l == "ready" {
				ready = true
			}
		case <-tick.C:
			if ready {
				rtio55Raise(t, cmd.Process.Pid)
			}
		case <-deadline:
			_ = cmd.Process.Kill()
			open = false
		}
	}
	err = cmd.Wait()
	code := uint32(0)
	if ee, ok := err.(*exec.ExitError); ok {
		code = uint32(ee.ExitCode())
	} else if err != nil {
		t.Fatal(err)
	}
	if errb.Len() > 0 {
		out.WriteString("stderr: " + errb.String())
	}
	return out.String(), code
}

// TestRtIo_CtrlIgnored: a probe started with Ctrl+C disabled (a new
// process group) that watches Interrupt still gets it (got >= 1, exit 0);
// the old SignalWatch left it disabled (got 0 after 10 s). Two controls
// show the harness is real: unwatched in a new group, Ctrl+C is ignored
// (survived, exit 0); unwatched and not in a new group, with Ctrl+C
// enabled in this process, Ctrl+C kills it (STATUS_CONTROL_C_EXIT). And a
// watching probe with Ctrl+C enabled at start gets it too.
func TestRtIo_CtrlIgnored(t *testing.T) {
	exe := buildRTIO55(t, "")
	out, code := rtio55Run(t, exe, "watch", true)
	got := rtio55Lines(out)
	if code != 0 || got["watch"] != "0" || got["got"] == "" || got["got"] == "0" {
		t.Errorf("watch, Ctrl+C disabled at start: exit %#x; want watch 0, got >= 1, exit 0\n%s", code, out)
	}
	// A process with no console at all (DETACHED_PROCESS, as a service
	// runs) can still watch: SetConsoleCtrlHandler(NULL, FALSE) succeeds
	// without one.
	det := exec.Command(exe, "once")
	det.SysProcAttr = &syscall.SysProcAttr{CreationFlags: rtio55Detached}
	if o, err := det.Output(); err != nil || rtio55Lines(string(o))["watch"] != "0" {
		t.Errorf("once, detached: %v; want watch 0\n%s", err, o)
	}
	out, code = rtio55Run(t, exe, "plain", true)
	if code != 0 || !strings.Contains(out, "survived") {
		t.Errorf("plain, Ctrl+C disabled: exit %#x; want survived, exit 0\n%s", code, out)
	}
	// The two runs below start with Ctrl+C enabled, as an interactive
	// console has it (rtio55Run sets this process's state, which the child
	// inherits). Nothing raises Ctrl+C on this process's own console.
	out, code = rtio55Run(t, exe, "watch", false)
	got = rtio55Lines(out)
	if code != 0 || got["watch"] != "0" || got["got"] == "" || got["got"] == "0" {
		t.Errorf("watch, Ctrl+C enabled at start: exit %#x; want watch 0, got >= 1, exit 0\n%s", code, out)
	}
	out, code = rtio55Run(t, exe, "plain", false)
	if code != rtio55ControlCExit || strings.Contains(out, "survived") {
		t.Errorf("plain, Ctrl+C enabled: exit %#x; want STATUS_CONTROL_C_EXIT before survived\n%s", code, out)
	}
}

// TestRtIo_ReissueRace: ReissueAccept's handoff on IOCP (test points 5
// and 10, two reaper threads), 40 rounds, three runs: the op is never
// handed out inside the reissuer's window, exactly once after it, and the
// handoff happened in at least half the rounds.
func TestRtIo_ReissueRace(t *testing.T) {
	exe := buildRTIO55(t, "")
	for run := 0; run < 3; run++ {
		r := runNative(t, exe, "", "race")
		if r.code != 0 || rtio55Lines(r.stdout)["reissue"] != "0" {
			t.Errorf("run %d: exit %d; want reissue 0 (else the failed step)\nstdout:\n%s\nstderr:\n%s", run, r.code, r.stdout, r.stderr)
		}
	}
}
