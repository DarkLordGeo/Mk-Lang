// Why: console reads can be tested only in a console no other process uses.
// What: TestRtIo_WaitEdgesWindows, the Windows run of rt-io_waitedges_test.go's cases.
// How: CREATE_NO_WINDOW gives the probe a hidden console of its own; ConRead
// types into it and reads it back (sys.ConsoleTestType refuses a shared one).

package std

import (
	"bytes"
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// createNoWindow is CREATE_NO_WINDOW: a new console, no window.
const createNoWindow = 0x08000000

// TestRtIo_WaitEdgesWindows: EpollMs (point 6 does nothing on IOCP; the waits
// must still behave), SemBig, and ConRead in the probe's own console.
func TestRtIo_WaitEdgesWindows(t *testing.T) {
	t.Parallel()
	exe := buildRTIO52(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	rtio52Judge(t, stdout.String(), "1")
}
