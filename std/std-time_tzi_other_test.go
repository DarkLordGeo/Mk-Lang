// Why: the Zone.Local tests use Windows zone records that exist only on Windows.
// What: stubs of the helpers in std-time_tzi_windows_test.go.
// How: off Windows there are no zone records, and the tests that need them skip.

//go:build !windows

package std

import (
	"os/exec"
	"testing"
)

func time2ZoneRecords(key string, first, last int) ([]time2Tzi, time2Tzi, bool) {
	return nil, time2Tzi{}, false
}

func time2HostRecords(first, last int) ([]time2Tzi, time2Tzi, bool) {
	return nil, time2Tzi{}, false
}

func time2PeakCommit(t *testing.T, cmd *exec.Cmd) uint64 {
	t.Skip("peak commit is read on Windows only")
	return 0
}
