// Why: the driver must tell a tool that crashed from one that never started.
// What: TestToolErrorOnSignal, Linux only.
// How: runs a child killed by a signal, and this binary killing itself.

package toolchain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestToolErrorOnSignal: a tool that started and died by a signal
// is ExitNoStatus with the signal named, never ExitNotStarted, so the
// driver sees a crash and not "could not start". Linux only: on Windows a
// killed process has an exit status.
func TestToolErrorOnSignal(t *testing.T) {
	_, err := run(context.Background(), NameLDLLD, "/bin/sh", []string{"-c", "kill -SEGV $$"}, "")
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("err = %#v, want *ToolError", err)
	}
	if te.ExitCode != ExitNoStatus || !te.Started() || te.Cancelled || te.Signal != "segmentation fault" {
		t.Errorf("ToolError = %#v, want ExitNoStatus and signal segmentation fault", te)
	}
	if msg := te.Error(); strings.Contains(msg, "could not start") || !strings.Contains(msg, "segmentation fault") {
		t.Errorf("Error() = %q", msg)
	}

	// The same for this test binary killing itself (SIGKILL).
	_, err = helper(t, context.Background(), "kill")
	if !errors.As(err, &te) || te.ExitCode != ExitNoStatus || te.Signal != "killed" || te.Cancelled {
		t.Errorf("self-kill: %#v", err)
	}
}
