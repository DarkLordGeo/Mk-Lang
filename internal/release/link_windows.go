// Why: on Windows a symbolic link needs administrator rights or developer
// mode, so <root>/current is a directory junction instead.
//
// What: MakeLink, setLink, setMountPoint and mountPointBuffer.
//
// How: current is a directory junction, which needs no administrator rights,
// made with CreateFile and DeviceIoControl(FSCTL_SET_REPARSE_POINT) from
// the syscall package, the reparse buffer built with encoding/binary (no
// unsafe import).

package release

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	ioReparseTagMountPoint   = 0xA0000003
	fsctlSetReparsePoint     = 0x000900A4
	fileFlagOpenReparsePoint = 0x00200000
	fileFlagBackupSemantics  = 0x02000000
)

// mountPointBuffer is a REPARSE_DATA_BUFFER for a mount point (a junction)
// to the absolute directory target: the tag, the data length, a reserved
// word, the substitute name's and the print name's offsets and lengths in
// bytes, then the two names in UTF-16, each ending in a NUL. The substitute
// name is the NT path \??\<target>.
func mountPointBuffer(target string) []byte {
	subst := utf16(`\??\` + target)
	pr := utf16(target)
	pathBytes := 2*len(subst) + 2*len(pr) // both with their NULs
	var b []byte
	b = binary.LittleEndian.AppendUint32(b, ioReparseTagMountPoint)
	b = binary.LittleEndian.AppendUint16(b, uint16(8+pathBytes))
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint16(b, 0)                        // substitute name offset
	b = binary.LittleEndian.AppendUint16(b, uint16(2*(len(subst)-1))) // its length, no NUL
	b = binary.LittleEndian.AppendUint16(b, uint16(2*len(subst)))     // print name offset
	b = binary.LittleEndian.AppendUint16(b, uint16(2*(len(pr)-1)))    // its length, no NUL
	for _, u := range append(subst, pr...) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}

// utf16 is s in UTF-16 with a terminating NUL.
func utf16(s string) []uint16 {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		panic(err) // a path holding a NUL never reaches here (MakeLink checks)
	}
	return u
}

// MakeLink makes link, which must not exist, a junction to the absolute
// directory target.
func MakeLink(link, target string) error {
	t, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(t); err != nil || !fi.IsDir() {
		return fmt.Errorf("release: junction target %s is not a directory", t)
	}
	if err := os.Mkdir(link, 0o755); err != nil {
		return err
	}
	if err := setMountPoint(link, t); err != nil {
		os.Remove(link)
		return fmt.Errorf("release: making the junction %s: %v", link, err)
	}
	return nil
}

func setMountPoint(link, target string) error {
	p, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	if _, err := syscall.UTF16FromString(target); err != nil {
		return err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		fileFlagOpenReparsePoint|fileFlagBackupSemantics, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	buf := mountPointBuffer(target)
	var n uint32
	return syscall.DeviceIoControl(h, fsctlSetReparsePoint, &buf[0], uint32(len(buf)), nil, 0, &n, nil)
}

// setLink points the junction link at the absolute directory target. An
// existing junction is retargeted in place by FSCTL_SET_REPARSE_POINT, so
// there is no moment without current; an empty directory (what a crash
// inside MakeLink leaves) gets the reparse point; with nothing there a
// junction is made. root is unused on Windows.
func setLink(root, link, target string) error {
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		return fmt.Errorf("release: junction target %s is not a directory", target)
	}
	fi, err := os.Lstat(link)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return MakeLink(link, target)
	case err != nil:
		return err
	case fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0:
		if err := setMountPoint(link, target); err != nil {
			return fmt.Errorf("release: retargeting the junction %s: %v", link, err)
		}
		return nil
	case fi.IsDir():
		if des, err := os.ReadDir(link); err == nil && len(des) == 0 {
			return setMountPoint(link, target)
		}
	}
	return fmt.Errorf("release: %s is not a link", link)
}
