// Why: on Windows the stat hint needs a change identity that the directory
// listing already returns, since a per-file open would cost what it saves.
// What: changeIdentity for Windows: the creation time.
// How: the directory enumeration carries it; Go's syscall exposes no change
// time here.

package cache

import (
	"os"
	"syscall"
)

// changeIdentity returns the file's creation time. A delete and recreate
// changes it outside the tunnelling window (15 seconds by default); an
// in-place write does not, which the racy rule and --rehash cover.
func changeIdentity(fi os.FileInfo) [2]uint64 {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return [2]uint64{uint64(d.CreationTime.Nanoseconds()), 0}
	}
	return [2]uint64{}
}
