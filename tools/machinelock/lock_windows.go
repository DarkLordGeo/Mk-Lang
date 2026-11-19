// Why: on Windows the machine lock must vanish with its holder, however the
// holder ends.
//
// What: tryLock and unlock for Windows.
//
// How: CreateFile with share mode 0, so a second open fails with a sharing
// violation (ErrBusy) until the holder's handle is closed, which the system
// does when the process ends.

package machinelock

import (
	"errors"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION.
const errSharingViolation syscall.Errno = 32

func tryLock(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errSharingViolation) {
			return nil, ErrBusy
		}
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func unlock(f *os.File) error { return f.Close() }
