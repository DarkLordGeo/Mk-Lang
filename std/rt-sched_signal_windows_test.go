// Why: the Windows signal tests call kernel32 through syscall.NewLazyDLL, so
// they live apart from rt-sched_signal_test.go and GOOS=linux go vet passes.
// What: TestRtSched_SignalWindows (Ctrl+C and Ctrl+Break at 1 and 8 carriers)
// and TestRtSched_SignalHelper, its child-process helper.
// How: the helper runs the program on its own console and generates the
// console event there, the only way to deliver CTRL_C_EVENT to one process.

package std

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// TestRtSched_SignalHelper is not a test: run with RTS_SIGNAL_EXE set, it
// runs that program on a console of its own and sends it Ctrl+C, the only
// way to deliver CTRL_C_EVENT to one process: it clears its own inherited
// "ignore Ctrl+C" attribute first (a process created in a new process group
// gets it, and children inherit it, so the go test process may have it), so
// the program starts without it; then it attaches to the program's console,
// ignores the event itself and generates it. It prints the program's exit
// status and output on one line.
func TestRtSched_SignalHelper(t *testing.T) {
	exe := os.Getenv("RTS_SIGNAL_EXE")
	if exe == "" || runtime.GOOS != "windows" {
		t.Skip("helper process only")
	}
	k := syscall.NewLazyDLL("kernel32.dll")
	ctrl := k.NewProc("SetConsoleCtrlHandler")
	if r, _, err := ctrl.Call(0, 0); r == 0 {
		t.Fatalf("SetConsoleCtrlHandler(NULL, FALSE): %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = os.Environ()
	// CREATE_NEW_CONSOLE, hidden.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x10, HideWindow: true}
	attached := false
	out, st := rtsSignalRun(t, cmd, func(pid int) error {
		if !attached {
			k.NewProc("FreeConsole").Call()
			if r, _, err := k.NewProc("AttachConsole").Call(uintptr(pid)); r == 0 {
				return fmt.Errorf("AttachConsole(%d): %v", pid, err)
			}
			if r, _, err := ctrl.Call(0, 1); r == 0 {
				return fmt.Errorf("SetConsoleCtrlHandler(NULL, TRUE): %v", err)
			}
			attached = true
		}
		if r, _, err := k.NewProc("GenerateConsoleCtrlEvent").Call(0, 0); r == 0 {
			return fmt.Errorf("GenerateConsoleCtrlEvent: %v", err)
		}
		return nil
	})
	fmt.Printf("RTS-SIGNAL exit=%d out=%q\n", st, out)
}

// TestRtSched_SignalWindows delivers Ctrl+C (Interrupt, through
// TestRtSched_SignalHelper in a process of its own) and Ctrl+Break
// (Terminate; GenerateConsoleCtrlEvent to the program's new process group),
// each to the program waiting for that signal, at 1 and 8 carriers.
func TestRtSched_SignalWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	for _, sig := range []string{"Interrupt", "Terminate"} {
		t.Run(sig, func(t *testing.T) {
			exe := rtsBuildSignal(t, t.TempDir(), sig, "x86_64-windows")
			for _, n := range []int{1, 8} {
				env := append(os.Environ(), fmt.Sprintf("MK_CARRIERS=%d", n))
				var out string
				var st int
				if sig == "Interrupt" {
					h := exec.Command(os.Args[0], "-test.run=^TestRtSched_SignalHelper$", "-test.count=1", "-test.v")
					h.Env = append(env, "RTS_SIGNAL_EXE="+exe)
					b, err := h.CombinedOutput()
					i := strings.Index(string(b), "RTS-SIGNAL exit=")
					if err != nil || i < 0 {
						t.Fatalf("carriers=%d: helper: %v\n%s", n, err, b)
					}
					fmt.Sscanf(string(b[i:]), "RTS-SIGNAL exit=%d out=%q", &st, &out)
				} else {
					cmd := exec.Command(exe)
					cmd.Env = env
					cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
					out, st = rtsSignalRun(t, cmd, func(pid int) error {
						r, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent").Call(1, uintptr(pid))
						if r == 0 {
							return fmt.Errorf("GenerateConsoleCtrlEvent: %v", err)
						}
						return nil
					})
				}
				if st != 0 || out != "ready\nstopped\n" {
					t.Fatalf("carriers=%d: exit %d, stdout %q", n, st, out)
				}
			}
		})
	}
}
