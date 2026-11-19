// Why: every LLVM tool runs through one process runner, whose environment
// and error kinds the driver's messages depend on.
// What: TestHelperProcess (the child), TestRunPassesScrubbedEnv,
// TestToolErrorOnCancel and TestToolErrorExitStatus.
// How: the child process is this test binary, chosen by the word after "--"
// (the scrubbed environment cannot carry a marker), so every test here runs
// on both hosts with no LLVM.

package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is the child: it does nothing in an ordinary run.
func TestHelperProcess(t *testing.T) {
	i := slices.Index(os.Args, "--")
	if i < 0 || i+1 >= len(os.Args) {
		return
	}
	switch os.Args[i+1] {
	case "env":
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
	case "sleep":
		time.Sleep(20 * time.Second)
	case "kill":
		p, err := os.FindProcess(os.Getpid())
		if err == nil {
			p.Kill()
		}
		time.Sleep(20 * time.Second)
	case "fail":
		fmt.Fprintln(os.Stderr, "helper failed")
		os.Exit(5)
	}
	os.Exit(0)
}

// helper runs this test binary as a tool doing what.
func helper(t *testing.T, ctx context.Context, what string) ([]byte, error) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return run(ctx, NameClang, self, []string{"-test.run=^TestHelperProcess$", "--", what}, "")
}

// TestRunPassesScrubbedEnv: the tool sees exactly the environment of
// scrubbedEnv, whatever the host holds.
func TestRunPassesScrubbedEnv(t *testing.T) {
	t.Setenv("MK_LLVM", `x`)
	t.Setenv("LIB", `y`)
	out, err := helper(t, context.Background(), "env")
	if err != nil {
		t.Fatalf("helper: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(out), "\r", ""), "\n"), "\n")
	want := scrubbedEnv(runtime.GOOS, os.Getenv)
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("child environment =\n%q\nwant\n%q", got, want)
	}
	if !slices.Contains(got, "SOURCE_DATE_EPOCH=0") {
		t.Errorf("no SOURCE_DATE_EPOCH=0 in %q", got)
	}
	for _, kv := range got {
		if strings.HasPrefix(kv, "MK_LLVM=") || strings.HasPrefix(kv, "LIB=") {
			t.Errorf("host variable leaked: %q", kv)
		}
	}
}

// TestToolErrorOnCancel: a tool stopped by its context is marked
// Cancelled and wraps the context's error. It carries no tool exit status,
// so the driver cannot take it for a tool rejecting IR (E0000). On Windows
// the killed child reports exit status 1, which must not show through.
func TestToolErrorOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := helper(t, ctx, "sleep")
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("cancelled tool ran %v", d)
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("err = %#v, want *ToolError", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !te.Cancelled {
		t.Errorf("err = %#v, want Cancelled wrapping DeadlineExceeded", te)
	}
	if te.ExitCode != ExitNoStatus || !te.Started() || te.Signal != "" {
		t.Errorf("ExitCode = %d, Signal = %q, want ExitNoStatus and no signal", te.ExitCode, te.Signal)
	}
	if msg := te.Error(); !strings.Contains(msg, "cancelled") || strings.Contains(msg, "could not start") {
		t.Errorf("Error() = %q", msg)
	}

	// A context done before the start: cancelled, and no process ran.
	done, stop := context.WithCancel(context.Background())
	stop()
	_, err = helper(t, done, "sleep")
	if !errors.As(err, &te) || !te.Cancelled || !errors.Is(err, context.Canceled) || te.Started() {
		t.Errorf("pre-cancelled: %#v", err)
	}
}

// TestToolErrorExitStatus: an ordinary failure keeps its exit status and
// stderr, and is neither cancelled nor signaled.
func TestToolErrorExitStatus(t *testing.T) {
	_, err := helper(t, context.Background(), "fail")
	var te *ToolError
	if !errors.As(err, &te) || te.ExitCode != 5 || te.Cancelled || te.Signal != "" || !strings.Contains(te.Stderr, "helper failed") {
		t.Fatalf("err = %#v", err)
	}
	if msg := te.Error(); msg != "clang exited with status 5: helper failed" {
		t.Errorf("Error() = %q", msg)
	}
}
