// Why: a program's peak memory must not include the size of the tools/bench
// process that started it.
// What: TestAppPeakMemoryBigParent.
// How: grows the test process to 128 MiB, runs a 16 MiB helper and checks
// that its peak reads far under 128 MiB.

package main

import (
	"os"
	"runtime"
	"runtime/debug"
	"testing"
)

// TestAppPeakMemoryBigParent: a program's peak is its own, whatever the size
// of the tools/bench process that starts it. The test
// process first makes 128 MiB of its own resident, then runs the 16 MiB
// helper (16, so on Linux it clears the fallback spawner's floor widely),
// whose peak must read far under that. On Linux without the spawner,
// ru_maxrss carries the starter's high-water mark (Go's CLONE_VM child shares
// its address space until exec), so this would read over 128 MiB; on Windows
// a new process has its own address space, and this passing on
// mem_windows.go as it is shows Windows has no such fault. The file uses only
// runProgram and the size helper, so it also runs on a tree without the
// spawner.
func TestAppPeakMemoryBigParent(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("peak memory is read on Windows and Linux only")
	}
	const ballast = 128 << 20
	b := make([]byte, ballast)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	env := append(runEnv(os.Environ()), "BENCH_HELPER_APP=size")
	_, _, small, err := runProgram(os.Args[0], []string{"16"}, env)
	runtime.KeepAlive(b)
	debug.FreeOSMemory() // b is dead here: give the 128 MiB back for the tests after this one

	if err != nil {
		t.Fatal(err)
	}
	t.Logf("16 MiB program from a parent with 128 MiB resident: peak RSS %d bytes (%.1f MiB)", small.RSS, float64(small.RSS)/(1<<20))
	if small.RSS == 0 || small.RSS >= ballast/2 {
		t.Fatalf("peak RSS of the 16 MiB program %d, want above 0 and under %d (the parent holds %d)", small.RSS, ballast/2, ballast)
	}
}
