// Why: std-dl_ffi_test.go calls dlGoLoad on every OS.
// What: dlGoLoad off Windows.
// How: a stub; TestFFI_Windows skips before it asks Go's loader anything.

//go:build !windows

package std

import "testing"

func dlGoLoad(t *testing.T, path string) string {
	t.Fatalf("dlGoLoad(%s): Go's LoadLibrary is Windows only", path)
	return ""
}
