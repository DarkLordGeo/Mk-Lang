// Why: on Linux a stat hint can see edits that restore the mtime, because
// ctime changes on every write.
// What: changeIdentity for Linux: ctime and the inode.
// How: both come from the Lstat that DirEntry.Info does, so no extra system
// call is made.

package cache

import (
	"os"
	"syscall"
)

// changeIdentity returns the file's ctime (nanoseconds) and inode. Any write,
// a chmod and an mtime restored by utimes all change ctime.
func changeIdentity(fi os.FileInfo) [2]uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return [2]uint64{uint64(st.Ctim.Nano()), st.Ino}
	}
	return [2]uint64{}
}
