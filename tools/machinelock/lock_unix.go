// Why: on Unix the machine lock must vanish with its holder, however the
// holder ends.
// What: tryLock and unlock for Unix.
// How: flock(LOCK_EX|LOCK_NB) on the file, released by the kernel when the
// descriptor closes; EWOULDBLOCK is ErrBusy.

//go:build unix

package machinelock

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func unlock(f *os.File) error { return f.Close() }
