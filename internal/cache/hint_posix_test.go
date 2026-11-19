// Why: the stat hint's recreate test needs a host-specific helper.
// What: setCreationLater for POSIX systems, where it does nothing.
// How: on POSIX a recreated file already has a new inode and change time.

//go:build !windows

package cache

import (
	"os"
	"testing"
)

// setCreationLater does nothing here: a recreate already has a new inode and
// ctime on Linux.
func setCreationLater(t *testing.T, p string, old os.FileInfo) {}
