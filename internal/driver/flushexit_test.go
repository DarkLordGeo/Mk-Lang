// Why: a program whose last buffered write fails must not exit 0 as if
// its output had been written.
// What: TestFailedLastFlushExits1.
// How: runs hello world with stdout on a handle opened for reading only and
// checks stderr and the exit status.

package driver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mk/internal/types"
)

// TestFailedLastFlushExits1: hello world run with stdout on a handle
// opened for reading only. Its one Print fills the buffer without a write,
// so the write happens in the last flush when Main returns, and fails: the
// process reports it on stderr and exits 1 instead of 0. Windows only: the
// Linux leg would start wsl.exe, and a redirected read-only handle is the
// same rule there.
func TestFailedLastFlushExits1(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows leg runs the program natively")
	}
	dir := t.TempDir()
	exe := buildProgram(t, filepath.Join(programs, "hello.mk"), types.X86_64Windows, types.Dev, dir)
	ro := filepath.Join(dir, "ro.txt")
	if err := os.WriteFile(ro, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(ro) // read only: WriteFile on it fails
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	var e bytes.Buffer
	cmd.Stdout, cmd.Stderr = f, &e
	err = cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	if code != 1 || !strings.Contains(e.String(), "error: WriteError on stdout") {
		t.Fatalf("exit %d, stderr %q; want exit 1 and the WriteError line", code, e.String())
	}
}
