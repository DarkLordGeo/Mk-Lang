// Why: the std-missing_*_test.go files test Spin, RwLock, Select, Rng, Dir
// and Path as Mk blocks and as whole programs, and share their setup.
// What: missStd (a std copy and cache per test) and build and run helpers.
// How: programs are built over the copy and compared with Go models.

package std

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// missStd makes a temporary root holding a std copy and a cache, and
// returns the root and the std directory. MK_STDMISSING_SRC names another
// std tree to copy instead of this directory (a snapshot without modules
// that are mid-edit).
func missStd(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	var stdDir string
	if alt := os.Getenv("MK_STDMISSING_SRC"); alt != "" {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(alt)); err != nil {
			t.Fatal(err)
		}
	} else {
		stdDir = copyStd(t, root, nil)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	return root, stdDir
}

// missBuild writes src as main.mk under a fresh std copy and builds it
// with flags; a missing toolchain skips the test.
func missBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	root, stdDir := missStd(t)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if strings.Contains(out, "no usable LLVM") {
		t.Skipf("needs the LLVM toolchain: %s", out)
	}
	if status != 0 {
		t.Fatalf("mk build: status %d\n%s", status, out)
	}
	return exe
}

// missRun runs exe in dir with env added and returns its stdout; a
// non-zero exit fails the test.
func missRun(t *testing.T, exe, dir string, env ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\nstderr:\n%s", exe, err, errb.String())
	}
	return strings.ReplaceAll(out.String(), "\r\n", "\n")
}
