// Why: on Windows the stat hint detects a recreated file by its creation
// time, which NTFS tunnelling can carry over.
// What: setCreationLater for Windows.
// How: a recreated file outside the 15-second tunnelling window has a new
// creation time; the helper sets one an hour later.

package cache

import (
	"os"
	"syscall"
	"testing"
)

// setCreationLater gives the file at p a creation time one hour after the one
// in old, as a recreate outside NTFS's 15-second tunnelling window would; a
// recreate inside it inherits the old creation time.
func setCreationLater(t *testing.T, p string, old os.FileInfo) {
	t.Helper()
	d, ok := old.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		t.Fatal("no Win32FileAttributeData")
	}
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	const fileWriteAttributes = 0x100
	h, err := syscall.CreateFile(name, fileWriteAttributes, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(h)
	ct := syscall.NsecToFiletime(d.CreationTime.Nanoseconds() + 3600e9)
	if err := syscall.SetFileTime(h, &ct, nil, nil); err != nil {
		t.Fatal(err)
	}
}
