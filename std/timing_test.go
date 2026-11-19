// Why: wall-clock assertions fail on a busy machine, so they must run only when asked for.
// What: the MK_TIMING switch for package std's tests.
// How: a time assertion runs only with MK_TIMING=1 (the gate's timing pass, alone on a quiet machine); otherwise
// the test logs what it measured, and a mixed runtime block or rt-io probe verdict is logged, not judged.

package std

import (
	"os"
	"slices"
	"strings"
)

// timingOff says why a wall-clock assertion was not made.
const timingOff = "MK_TIMING=1 not set"

// timingRun reports whether this run asserts on wall-clock time, MK_TIMING=1.
func timingRun() bool { return os.Getenv("MK_TIMING") == "1" }

// rtsTimingBlocks are the runtime test blocks (std/rt, std/task) whose
// verdict depends on wall-clock time: the RT-Bench ratios, the time bounds
// on cancels, deep chains and pool rounds, a 300 ms lateness flag, and
// RT-Linger-Ends, which counts linger exits within a timed window. The
// twin list for mk test --std is internal/driver's stdTimingBlocks; keep
// the two equal.
var rtsTimingBlocks = []string{
	"RT-Bench-DeepChain", "RT-Bench-SleepDepth", "RT-Bench-CancelWaiters",
	"RT-DeepChain-Cancel", "RT-DeepChain-Fail", "RT-DeepChain-Join",
	"RT-R17-CancelLevel", "RT-R17-CancelNested", "RT-R17-FirstErrorWins",
	"RT-Signal-Cancel", "RT-Pool-CancelQueued", "RT-Io-CancelInFlight",
	"RT-F13-PoolCancelNoBleed", "RT-F13-PoolCancelEarly",
	"RT-MN-ReapClear", "RT-Linger-Ends",
}

// rtsTimingBlock reports whether the block named name (its first word is
// its id) is one of rtsTimingBlocks.
func rtsTimingBlock(name string) bool {
	id, _, _ := strings.Cut(name, " ")
	return slices.Contains(rtsTimingBlocks, id)
}

// rtioTimingCases are the rt-io probe cases with a step that fails when a
// wait took too long (a wake after 1 s, an interrupt after 50 ms, a mean
// high-resolution wait over 5 ms, and the like); their value is judged
// only under MK_TIMING=1. Steps that fail when a wait ended too early are
// correctness checks and stay judged with the rest of their case only
// under MK_TIMING=1 too, since a case reports one number.
var rtioTimingCases = []string{"wake", "sigrace", "interrupt", "hrdirty", "wakekeep", "wakelate", "pushall", "semlong", "epollms"}

// rtioJudged reports whether case c's value is judged in this run.
func rtioJudged(c string) bool {
	return timingRun() || !slices.Contains(rtioTimingCases, c)
}
