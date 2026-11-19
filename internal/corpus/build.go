// Why: with --build the corpus also builds and runs ok units, and a build or
// a run must never turn a failing unit into a passing one, nor fail a unit
// for anything but what the build or the run itself shows.
//
// What: JudgeBuild, which applies a build's outcome to a unit's JudgeUnit
// judgement.
//
// How: a unit fails for an E0001 or E0000 in the report, for a passing ok
// unit the driver did not build, or for a Main that panicked, was killed at
// the 10 s timeout, or could not start. A Main that returns an error, or a
// unit built but not run because it reaches a bodiless prelude function
// (RunSkipped), passes.

package corpus

import (
	"fmt"

	"mk/internal/diag"
	"mk/internal/driver"
)

// buildNoCause starts JudgeBuild rule 2's reason.
const buildNoCause = "build: the unit was not built and the report names no cause"

// runTimeout is the driver's bound on one unit's run, for the reason text
// only.
const runTimeout = "10 s"

// JudgeBuild applies --build's outcome to j, the unit's JudgeUnit
// judgement. It is pure. With mode UnitCheck, and for any verdict other
// than ok (ToUnitInput builds only ok units), j is returned unchanged.
//
//  1. A run happened (r.Ran) and Main panicked (status 101), was killed at
//     the timeout, or did not start (status -1): FAIL, a PENDING j
//     included, because the driver builds only a clean unit judged through
//     flow, so the failure is the program's or the toolchain's, not a
//     missing stage's. A FAIL j keeps its own reason, so a compile cause is
//     never masked by a run.
//  2. j is PASS, the unit was judged through flow (the driver builds
//     nothing earlier), and r.Built is false: FAIL. The driver built
//     nothing and reported no cause. A toolchain fault reaches the judge
//     as an E0001 (an E0000 or E0001 from the build part appears in the
//     report; rule 1b of JudgeUnit), so this is the driver's fault.
//  3. Otherwise j.
func JudgeBuild(u *Unit, r driver.BlockResult, mode driver.UnitMode, j Judgement) Judgement {
	if mode == driver.UnitCheck || u.Verdict != VerdictOK {
		return j
	}
	fail := func(format string, args ...any) Judgement {
		j.Status, j.Reason = StatusFail, fmt.Sprintf(format, args...)
		return j
	}
	if r.Ran && j.Status != StatusFail { // rule 1
		switch {
		case r.TimedOut:
			return fail("run: Main did not finish within %s and was killed", runTimeout)
		case r.RunStatus == 101:
			return fail("run: Main panicked (exit 101)")
		case r.RunStatus < 0:
			return fail("run: Main could not be started")
		}
	}
	if j.Status == StatusPass && r.Through >= diag.PhaseFlow && !r.Built { // rule 2
		return fail("%s (no E0000 or E0001 either: the driver's fault; mk version --llvm <dir> shows a toolchain fault)", buildNoCause)
	}
	return j
}
