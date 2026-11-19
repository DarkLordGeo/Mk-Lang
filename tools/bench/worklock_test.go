// Why: bench and the gate exclude each other only if bench's default lock
// is machine.lock in the shared work root, whatever its own -work is.
// What: TestDefaultLockAndWork.
// How: read the defaults that publish -h prints under set $MK_WORK and
// $MK_BENCH_WORK, and check a relative $MK_WORK is refused.

package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mk/tools/machinelock"
)

// TestDefaultLockAndWork: -lock defaults to DefaultPath under $MK_WORK,
// -work to $MK_BENCH_WORK when set and to bench under $MK_WORK when not.
// flag prints string defaults quoted, so the paths are compared quoted.
func TestDefaultLockAndWork(t *testing.T) {
	root, bw := t.TempDir(), t.TempDir()
	t.Setenv(machinelock.WorkEnv, root)
	for _, tc := range []struct{ benchWork, work string }{
		{"", filepath.Join(root, "bench")},
		{bw, bw},
	} {
		t.Setenv("MK_BENCH_WORK", tc.benchWork)
		var out, errb strings.Builder
		run([]string{"publish", "-h"}, &out, &errb)
		help := errb.String()
		lock := filepath.Join(root, "machine.lock")
		if lock != machinelock.DefaultPath() || !strings.Contains(help, "(default "+strconv.Quote(lock)+")") {
			t.Errorf("MK_BENCH_WORK=%q: -lock default is not %q:\n%s", tc.benchWork, lock, help)
		}
		if !strings.Contains(help, "(default "+strconv.Quote(tc.work)+")") {
			t.Errorf("MK_BENCH_WORK=%q: -work default is not %q:\n%s", tc.benchWork, tc.work, help)
		}
	}
	t.Setenv(machinelock.WorkEnv, "rel")
	var out, errb strings.Builder
	if code := run([]string{"list"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "absolute") {
		t.Errorf("relative %s: exit %d, %s", machinelock.WorkEnv, code, errb.String())
	}
}
