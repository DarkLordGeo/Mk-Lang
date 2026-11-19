// Why: a junction's print name can be a decoy, so rt.ReadLink must read the
// substitute name, the path the I/O manager follows.
// What: TestRt_ReadLinkSubstitute, on Windows.
// How: Go's os.Readlink is the oracle; the decoy junction is made with
// FSCTL_SET_REPARSE_POINT, and links absolute and relative where allowed.

//go:build windows

package std

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

// rt8MakeJunction turns the empty directory link into a junction whose
// substitute name is \??\target and whose print name is print.
func rt8MakeJunction(link, target, print string) error {
	if err := os.Mkdir(link, 0o755); err != nil {
		return err
	}
	p, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	const openReparse = 0x00200000
	h, err := syscall.CreateFile(p, syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|openReparse, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	sub := utf16.Encode([]rune(`\??\` + target))
	pr := utf16.Encode([]rune(print))
	// MOUNT_POINT_REPARSE_BUFFER: four u16 (substitute offset and length,
	// print offset and length), then both names, each NUL-terminated.
	path := append(append(append(append([]uint16{}, sub...), 0), pr...), 0)
	data := make([]byte, 8+2*len(path))
	binary.LittleEndian.PutUint16(data[0:], 0)
	binary.LittleEndian.PutUint16(data[2:], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(data[4:], uint16(2*(len(sub)+1)))
	binary.LittleEndian.PutUint16(data[6:], uint16(2*len(pr)))
	for i, u := range path {
		binary.LittleEndian.PutUint16(data[8+2*i:], u)
	}
	buf := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint32(buf[0:], 0xA0000003) // IO_REPARSE_TAG_MOUNT_POINT
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(data)))
	copy(buf[8:], data)
	var got uint32
	const setReparse = 0x000900A4 // FSCTL_SET_REPARSE_POINT
	return syscall.DeviceIoControl(h, setReparse, &buf[0], uint32(len(buf)), nil, 0, &got, nil)
}

// TestRt_ReadLinkSubstitute: a junction whose print name is a decoy (any user
// who can write an empty directory can make one with FSCTL_SET_REPARSE_POINT)
// must read as its substitute name; symbolic links, where the account may make
// them, absolute and relative, must read as os.Readlink reads them.
func TestRt_ReadLinkSubstitute(t *testing.T) {
	rtsNative(t)
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rt8MakeJunction(filepath.Join(dir, "j"), real, `C:\decoy`); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	j, err := os.Readlink(filepath.Join(dir, "j"))
	if err != nil {
		t.Fatal(err)
	}
	if j != real {
		t.Fatalf("oracle: os.Readlink of the junction is %q, want %q", j, real)
	}
	want["j"] = j
	// Symbolic links need the privilege or developer mode: checked where
	// the account may make them.
	if os.Symlink(real, filepath.Join(dir, "abs")) == nil {
		want["abs"], _ = os.Readlink(filepath.Join(dir, "abs"))
	}
	if os.Symlink(`real\sub`, filepath.Join(dir, "rel")) == nil {
		want["rel"], _ = os.Readlink(filepath.Join(dir, "rel"))
	}
	for _, profile := range []string{"dev", "release"} {
		exe, all := rtsBuild(t, profile)
		var found bool
		for _, b := range all {
			if !strings.HasPrefix(b.name, "RT8-Oracle-ReadLink") {
				continue
			}
			found = true
			r := rtsRun(t, exe, b.index, 60*time.Second, "MK_RT8_READLINK="+dir)
			rtsJudge(t, b, r, "")
			got := map[string]string{}
			for _, line := range strings.Split(r.stderr, "\n") {
				f := strings.Fields(line)
				if len(f) != 3 || f[0] != "R" || f[2] == "-" {
					continue
				}
				v, err := hex.DecodeString(f[2])
				if err != nil {
					t.Fatalf("%s: bad line %q", profile, line)
				}
				got[f[1]] = string(v)
			}
			for name, w := range want {
				if got[name] != w {
					t.Errorf("%s: rt.ReadLink(%s) = %q, os.Readlink = %q", profile, name, got[name], w)
				}
			}
		}
		if !found {
			t.Fatal("no RT8-Oracle-ReadLink block")
		}
	}
}
