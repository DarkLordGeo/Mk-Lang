// Why: os.Chtimes goes through UnixNano, which wraps past 2262, so it cannot set
// the far-off times the stat tests need.
// What: sd8SetModified on Windows, for std-defects_stattime_test.go.
// How: computes the FILETIME from the Unix seconds and calls SetFileTime.

package std

import (
	"syscall"
	"time"
)

func sd8SetModified(p string, at time.Time) error {
	ticks := (at.Unix()+11644473600)*10000000 + int64(at.Nanosecond()/100)
	ft := syscall.Filetime{LowDateTime: uint32(ticks), HighDateTime: uint32(ticks >> 32)}
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(name, syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	return syscall.SetFileTime(h, nil, nil, &ft)
}
