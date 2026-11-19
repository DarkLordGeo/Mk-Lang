// Why: assertions on wall-clock time fail on a busy machine, so the
// package's tests make them only when MK_TIMING=1, in the gate's timing
// pass (go run ./tools/gate), alone on a quiet machine.
// What: timingOff, timingRun, stdTimingBlocks and stdTimingBlock.
// How: stdTimingBlocks lists the std.rt and std.task test blocks whose
// verdict depends on time; TestStdRuntimeTests runs them always and judges
// them only under MK_TIMING=1.

package driver

import (
	"os"
	"slices"
	"strings"
)

// timingOff says why a wall-clock assertion was not made.
const timingOff = "MK_TIMING=1 not set"

// timingRun reports whether this run asserts on wall-clock time, MK_TIMING=1.
func timingRun() bool { return os.Getenv("MK_TIMING") == "1" }

// stdTimingBlocks are the std.rt and std.task test blocks whose verdict
// depends on wall-clock time (ratios, time bounds, a lateness flag, linger
// exits counted in a timed window). TestStdRuntimeTests still runs them,
// and judges them only under MK_TIMING=1. It is the twin of std's
// rtsTimingBlocks (std/timing_test.go); keep the two equal.
var stdTimingBlocks = []string{
	"RT-Bench-DeepChain", "RT-Bench-SleepDepth", "RT-Bench-CancelWaiters",
	"RT-DeepChain-Cancel", "RT-DeepChain-Fail", "RT-DeepChain-Join",
	"RT-R17-CancelLevel", "RT-R17-CancelNested", "RT-R17-FirstErrorWins",
	"RT-Signal-Cancel", "RT-Pool-CancelQueued", "RT-Io-CancelInFlight",
	"RT-F13-PoolCancelNoBleed", "RT-F13-PoolCancelEarly",
	"RT-MN-ReapClear", "RT-Linger-Ends",
}

// stdTimingBlock reports whether the block named name (its first word is
// its id) is one of stdTimingBlocks.
func stdTimingBlock(name string) bool {
	id, _, _ := strings.Cut(name, " ")
	return slices.Contains(stdTimingBlocks, id)
}
