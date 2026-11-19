// Why: the package must build on systems with neither Windows nor Unix
// file locking.
// What: tryLock, which always fails, and unlock.
// How: tryLock returns an error, so Acquire never waits forever.

//go:build !windows && !unix

package machinelock

import (
	"errors"
	"os"
)

func tryLock(string) (*os.File, error) {
	return nil, errors.New("no machine lock on this operating system")
}

func unlock(f *os.File) error { return f.Close() }
