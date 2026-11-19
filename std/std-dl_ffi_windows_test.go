// Why: TestFFI_Windows needs an independent loader to judge std.dl's answers.
// What: dlGoLoad for std-dl_ffi_test.go, Windows only.
// How: loads the path with Go's syscall.LoadLibrary and reports the result.

package std

import (
	"syscall"
	"testing"
)

// dlGoLoad loads path with Go's LoadLibrary and names the outcome as
// std.dl's driver line does: ok, NotFound, Invalid or Go's error text.
func dlGoLoad(t *testing.T, path string) string {
	h, err := syscall.LoadLibrary(path)
	switch {
	case err == nil:
		if _, perr := syscall.GetProcAddress(h, "GetFileVersionInfoSizeW"); perr != nil {
			t.Errorf("Go's GetProcAddress on %s: %v", path, perr)
		}
		if _, perr := syscall.GetProcAddress(h, "NoSuchExport"); perr == nil {
			t.Errorf("Go's GetProcAddress found NoSuchExport in %s", path)
		}
		_ = syscall.FreeLibrary(h)
		return "ok"
	case err == syscall.Errno(126):
		return "NotFound"
	case err == syscall.Errno(193):
		return "Invalid"
	default:
		return err.Error()
	}
}
