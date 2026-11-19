// Why: wall-clock assertions fail on a loaded machine, so they run only on
// request.
// What: timingRun, the MK_TIMING switch for package lower's tests.
// How: an assertion on wall-clock time runs only when MK_TIMING=1, in the
// gate's timing pass (go run ./tools/gate), alone on a quiet machine.
// Without it the test still does its functional part and logs what it
// measured.

package lower

import "os"

// timingOff says why a wall-clock assertion was not made.
const timingOff = "MK_TIMING=1 not set"

// timingRun reports whether this run asserts on wall-clock time, MK_TIMING=1.
func timingRun() bool { return os.Getenv("MK_TIMING") == "1" }
