// Why: std-defects_stattime_test.go sets file times through sd8SetModified.
// What: sd8SetModified off Windows, with os.Chtimes.
// How: the native leg is skipped off Windows; the Linux leg sets times with touch in WSL.

//go:build !windows

package std

import (
	"os"
	"time"
)

func sd8SetModified(p string, at time.Time) error {
	return os.Chtimes(p, at, at)
}
