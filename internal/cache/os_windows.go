// Why: on Windows another process holding a file open makes renames and
// deletes fail for a while; the store must treat that as a miss or a retried
// write, never an error.
// What: isTransient and isCrossDevice for Windows, and the error codes they
// test.
// How: access denied, sharing and lock violations are transient;
// ERROR_NOT_SAME_DEVICE means PutFile must copy across volumes.

package cache

import (
	"errors"
	"syscall"
)

// Windows error codes the store treats specially.
const (
	errAccessDenied     syscall.Errno = 5  // ERROR_ACCESS_DENIED: a delete-pending file, or a rename over an open one
	errNotSameDevice    syscall.Errno = 17 // ERROR_NOT_SAME_DEVICE
	errSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
	errLockViolation    syscall.Errno = 33 // ERROR_LOCK_VIOLATION
)

// isTransient reports whether err is a sharing error another process causes
// by holding a file open, which goes away when it closes the file.
func isTransient(err error) bool {
	return errors.Is(err, errAccessDenied) || errors.Is(err, errSharingViolation) || errors.Is(err, errLockViolation)
}

// isCrossDevice reports whether a rename failed because the two paths are on
// different volumes.
func isCrossDevice(err error) bool { return errors.Is(err, errNotSameDevice) }
