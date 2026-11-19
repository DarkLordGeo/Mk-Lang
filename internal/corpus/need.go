// Why: a unit whose stages are not all wired yet must be pending, not failed,
// so the corpus has to know which compiler stages each error code needs.
//
// What: NeedOf (the stage set a code needs), GroupOf and the group policy
// for codes two passes emit, EmittedIn, TwoGroup, and unitNeed.
//
// How: a code needs its group's stages (phaseStages), plus mono or gen for
// the codes those stages emit. A code of several groups waits for its last
// group, except where groupPolicy pins it: E0101 to parse, E0905 to types.
// Stage needs are sets, never a prefix of the Stage order.

package corpus

import (
	"slices"

	"mk/internal/diag"
	"mk/internal/driver"
)

// The groups a code is emitted in are diag.Groups. Which of them a header-less
// unit waits for is this package's policy, stated here with its reasons:
//
//   - A code of several groups waits for its last group (the later emitter),
//     because only that emitter's absence is a failure once every group is
//     wired.
//   - E0101 waits for its default group, parse: its second emitter
//     (resolve's E0101 in names) reports forms the design blocks do not
//     hold, and waiting for names would hold every E01xx block pending on
//     std. A conformance unit asks for the other group with "// needs:".
//     E0609 has types as its default and its forms in syntax (parse), check
//     (types) and borrow (flow); it follows the first rule and waits for
//     flow, where borrow reports the capture forms.
//   - E0905 waits for types: it takes the group of the error it wraps
//     so its rows run from parse to flow, and gen, which emits it,
//     lands with the types stages (needIn adds StageGen).
var groupPolicy = map[diag.Code]diag.Phase{
	diag.E0101: diag.PhaseParse,
	diag.E0905: diag.PhaseTypes,
}

// TwoGroup reports whether a header-less unit waits for a group other than
// c's default one: c is reported in several groups (diag.MultiGroup) and the
// group policy does not pin it to its default.
func TwoGroup(c diag.Code) bool { return GroupOf(c) != diag.DefaultPhase(c) }

// EmittedIn reports whether some pass emits c in group p: one of
// diag.Groups(c), or its default group. A "// needs: p" header applies to
// exactly the codes for which it holds.
func EmittedIn(c diag.Code, p diag.Phase) bool {
	return p == diag.DefaultPhase(c) || slices.Contains(diag.Groups(c), p)
}

// GroupOf is the group whose stages must be wired before c's absence is a
// failure: the group policy's for E0101 and E0905, else the last of
// diag.Groups(c) (diag.DefaultPhase for a code of one group).
func GroupOf(c diag.Code) diag.Phase {
	if p, ok := groupPolicy[c]; ok {
		return p
	}
	gs := diag.Groups(c)
	return slices.Max(gs)
}

// phaseStages is the stage set a group needs.
func phaseStages(p diag.Phase) driver.StageSet {
	s := driver.StageSet(0).With(driver.StageLoad, driver.StageParse)
	if p >= diag.PhaseNames {
		s = s.With(driver.StageResolve)
	}
	if p >= diag.PhaseTypes {
		s = s.With(driver.StageSignatures, driver.StageBodies)
	}
	if p >= diag.PhaseFlow {
		s = s.With(driver.StageFlow)
	}
	return s
}

// allStages is every stage of the pipeline.
func allStages() driver.StageSet {
	var s driver.StageSet
	for st := driver.StageNone + 1; st <= driver.StageNative; st++ {
		s = s.With(st)
	}
	return s
}

// NeedOf is the set of stages that must be wired before c's absence is a
// failure rather than pending: its group's stages (GroupOf), plus
// StageInstances for E0708 (mono) and StageGen for E0903, E0905, E0906 and
// E0910 (gen); E0001 needs every stage. Sets, never a prefix of the Stage order:
// StageGen sorts before StageSignatures.
func NeedOf(c diag.Code) driver.StageSet {
	return needIn(c, GroupOf(c))
}

// needIn is NeedOf with c's group given.
func needIn(c diag.Code, g diag.Phase) driver.StageSet {
	switch c {
	case diag.E0001:
		return allStages()
	case diag.E0708:
		return phaseStages(diag.PhaseTypes).With(driver.StageInstances)
	case diag.E0903, diag.E0905, diag.E0906, diag.E0910: // E0910 from Gen.Input too
		return phaseStages(diag.PhaseTypes).With(driver.StageGen)
	}
	return phaseStages(g)
}

// okNeed is an ok unit's need: stages 1 to 7b and the instance walk.
func okNeed() driver.StageSet {
	return phaseStages(diag.PhaseFlow).With(driver.StageInstances)
}

// unitNeed is the unit's need: the union of its codes'
// needs for reject, okNeed for ok, load and parse for parse; and the latest
// group among them, which the unit must have been judged through
// (BlockResult.Through). A conformance unit's "// needs:" header names the
// group of every code it is emitted in (EmittedIn), E0101's names form
// included; Units refuses a header that applies to none of the codes.
func unitNeed(u *Unit) (driver.StageSet, diag.Phase) {
	switch u.Verdict {
	case VerdictOK:
		return okNeed(), diag.PhaseFlow
	case VerdictParse:
		return phaseStages(diag.PhaseParse), diag.PhaseParse
	}
	var s driver.StageSet
	last := diag.PhaseParse
	for _, c := range u.Codes {
		g := GroupOf(c)
		if u.Meta.Needs != diag.PhaseInternal && EmittedIn(c, u.Meta.Needs) {
			g = u.Meta.Needs
		}
		s |= needIn(c, g)
		if c == diag.E0001 {
			g = diag.PhaseFlow // E0001 needs a clean run through flow
		}
		last = max(last, g)
	}
	return s, last
}
