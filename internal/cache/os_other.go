// Why: the store's retry and copy logic asks the system which errors are
// transient and which mean another file system.
// What: isTransient and isCrossDevice for POSIX systems.
// How: no error is transient (an open file never blocks a rename or a
// removal); EXDEV means PutFile must copy across file systems.

//go:build !windows

package cache

import (
	"errors"
	"syscall"
)

// isTransient is false on POSIX systems: an open file never blocks a rename
// over it or its removal.
func isTransient(err error) bool { return false }

// isCrossDevice reports whether a rename failed because the two paths are on
// different file systems.
func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
