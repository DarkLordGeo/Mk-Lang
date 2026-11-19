// Why: tools/gate, tools/bench and tools/dogfood must agree on where their
// work goes and where the machine lock lives, on any machine, with no setup.
//
// What: WorkEnv, WorkRoot (the shared work root), DefaultPath (the lock
// file every tool uses unless told otherwise) and CheckEnv (which refuses a
// relative $MK_WORK).
//
// How: WorkRoot is $MK_WORK when it is an absolute path, else mk-work under
// the user's cache directory (os.UserCacheDir), else mk-work under the temp
// directory. The lock file is machine.lock in that root, so it never
// follows a tool's own -work flag. A relative $MK_WORK would resolve against
// each process's own working directory and split the lock, so WorkRoot
// ignores it and CheckEnv, which each tool calls first, reports it. None of
// the functions creates anything.

package machinelock

import (
	"fmt"
	"os"
	"path/filepath"
)

// WorkEnv names the environment variable that sets the work root.
const WorkEnv = "MK_WORK"

// CheckEnv reports a $MK_WORK that is set but not an absolute path. The
// tools exit with it before they read any default.
func CheckEnv() error {
	if w := os.Getenv(WorkEnv); w != "" && !filepath.IsAbs(w) {
		return fmt.Errorf("$%s is %q: give an absolute path", WorkEnv, w)
	}
	return nil
}

// WorkRoot is the work root shared by the tools: $MK_WORK when absolute, else
// mk-work under the user's cache directory, else mk-work under the
// temporary directory. tools/gate and tools/dogfood work in it, and
// tools/bench in its bench subdirectory. It is a sibling of the compiler's
// own cache (<cache>/mk), never inside it.
func WorkRoot() string {
	return workRoot(os.Getenv, os.UserCacheDir, os.TempDir)
}

// DefaultPath is the machine lock file: machine.lock in WorkRoot. Every
// tool resolves the same file for the same user and environment, whatever
// work directory each one is given.
func DefaultPath() string {
	return filepath.Join(WorkRoot(), "machine.lock")
}

func workRoot(getenv func(string) string, cacheDir func() (string, error), tempDir func() string) string {
	if w := getenv(WorkEnv); w != "" && filepath.IsAbs(w) {
		return w
	}
	if d, err := cacheDir(); err == nil && d != "" {
		return filepath.Join(d, "mk-work")
	}
	return filepath.Join(tempDir(), "mk-work")
}
