// Why: timings and fuzz runs are worthless when two of them share the
// machine, so tools/gate and tools/bench take one machine-wide lock first.
//
// What: Acquire, Release and Path of a Lock, and ErrBusy; the per-system
// tryLock and unlock live in lock_windows.go and lock_unix.go, and the lock
// file's default place (DefaultPath, WorkRoot) in workdir.go.
//
// How: an exclusive open of one file, polled once a second, which the
// operating system releases when the holder exits, so a crashed holder
// never leaves a stale lock.

// Package machinelock is the machine-wide lock that tools/gate and
// tools/bench hold while they measure or fuzz: an exclusive open of one file,
// which the operating system releases when the holding process ends, however
// it ends. The default file is per user, so it excludes the runs of one
// account; set $MK_WORK to a directory every user shares to exclude the
// runs of all accounts. It also resolves the work root the tools share
// (WorkRoot).
package machinelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrBusy is what tryLock returns while another process holds the lock.
var ErrBusy = errors.New("machine lock held by another process")

// Lock is a held machine lock.
type Lock struct {
	path string
	f    *os.File
}

// Acquire waits until it holds the lock at path, calling wait about once a
// minute with the time waited so far (wait may be nil). It returns any
// error other than the lock being held.
func Acquire(path string, wait func(waited time.Duration)) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	start := time.Now()
	next := time.Minute
	for {
		f, err := tryLock(path)
		if err == nil {
			l := &Lock{path: path, f: f}
			fmt.Fprintf(f, "pid %d since %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
			return l, nil
		}
		if !errors.Is(err, ErrBusy) {
			return nil, fmt.Errorf("machine lock %s: %w", path, err)
		}
		if w := time.Since(start); w >= next {
			if wait != nil {
				wait(w)
			}
			next += time.Minute
		}
		time.Sleep(time.Second)
	}
}

// Release gives the lock up; a nil or released Lock is a no-op.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlock(l.f)
	l.f = nil
	return err
}

// Path is the lock file.
func (l *Lock) Path() string { return l.path }
