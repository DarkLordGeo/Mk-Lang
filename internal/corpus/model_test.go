// Why: the executable model of the loan rules and the compiler must agree
// on every model case.
// What: TestModelCases and modelProblem over tests/conformance/model/.
// How: judges each unit through stages 1 to 7 in parity.txt's order;
// TestModelParity ties each verdict to parity.txt.
//
// TestModelCases judges every tests/conformance/model/
// unit through stages 1 to 7 with the test prelude and std, no code
// generation, in parity.txt's order (R cases, then H), and is the
// judged half of TestModelParity: a model unit that passes has the verdict
// its file states, which TestModelParity (presence part) ties to
// parity.txt's Go verdict. All 146 must match.

package corpus

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

// modelProblem is the expectation of one judged model unit, or "": a FAIL
// is always a problem (the Go verdict differs from parity.txt's); a PENDING
// is accepted only while something outside the unit explains it: a stage
// avail lacks ("needs <stage>"), a std module std does not provide yet
// ("std: std.<m> undeclared"), or a std that holds the run to an earlier
// phase than flow ("std: ..." while through is before flow). Every other
// PENDING, and every PENDING once the stages through flow are wired and
// units are judged through flow, except the module case, is a problem.
func modelProblem(u UnitResult, avail driver.StageSet, through diag.Phase) string {
	switch u.Status {
	case StatusPass:
		return ""
	case StatusFail:
		return fmt.Sprintf("%s: FAIL %s", u.Unit.Key, u.Reason)
	}
	if st, ok := strings.CutPrefix(u.Reason, "needs Stage"); ok {
		for _, s := range allStages().Stages() {
			if strings.EqualFold(s.String(), st) {
				if avail.Has(s) {
					return fmt.Sprintf("%s: PENDING %s, but the stage is wired", u.Unit.Key, u.Reason)
				}
				return ""
			}
		}
		return fmt.Sprintf("%s: PENDING on an unknown stage: %s", u.Unit.Key, u.Reason)
	}
	if m, ok := strings.CutPrefix(u.Reason, "std: std."); ok && strings.HasSuffix(m, " undeclared") {
		return ""
	}
	if strings.HasPrefix(u.Reason, "std: ") && through < diag.PhaseFlow {
		return ""
	}
	return fmt.Sprintf("%s: PENDING %s, with every stage of the unit wired and std judged through %s", u.Unit.Key, u.Reason, through)
}

func TestModelProblem(t *testing.T) {
	u := &Unit{Key: "tests/conformance/model/R1.Moves.mk", Verdict: VerdictReject}
	at := func(st Status, reason string) UnitResult {
		return UnitResult{Unit: u, Judgement: Judgement{Status: st, Reason: reason}}
	}
	cases := []struct {
		r       UnitResult
		avail   driver.StageSet
		through diag.Phase
		bad     bool
	}{
		{at(StatusPass, ""), stagesNative, diag.PhaseFlow, false},
		{at(StatusFail, "expected [x], emitted nothing"), stagesNative, diag.PhaseFlow, true},
		{at(StatusFail, "internal error: boom"), stagesTypes, diag.PhaseTypes, true},
		{at(StatusPending, "needs StageFlow"), stagesTypes, diag.PhaseTypes, false},
		{at(StatusPending, "needs StageInstances"), stagesTypes.With(driver.StageFlow), diag.PhaseFlow, false},
		{at(StatusPending, "needs StageInstances"), stagesNative, diag.PhaseFlow, true},
		{at(StatusPending, "needs StageGen"), stagesNative, diag.PhaseFlow, false},
		{at(StatusPending, "needs StageGen"), stagesAll, diag.PhaseFlow, true},
		{at(StatusPending, "needs StageNowhere"), stagesNative, diag.PhaseFlow, true},
		{at(StatusPending, "std: std.sync undeclared"), stagesNative, diag.PhaseFlow, false},
		{at(StatusPending, "std: Cell undeclared"), stagesNative, diag.PhaseFlow, true},
		{at(StatusPending, "std: E0402 std/core/x.mk:1:1 leaked"), stagesNative, diag.PhaseTypes, false},
		{at(StatusPending, "std: E0402 std/core/x.mk:1:1 leaked"), stagesNative, diag.PhaseFlow, true},
		{at(StatusPending, "std: judged through types only"), stagesNative, diag.PhaseTypes, false},
	}
	for i, c := range cases {
		if got := modelProblem(c.r, c.avail, c.through); (got != "") != c.bad {
			t.Errorf("case %d (%s %q, through %s): problem %q, want bad=%v", i, c.r.Status, c.r.Reason, c.through, got, c.bad)
		}
	}
}

func TestModelCases(t *testing.T) {
	root := repoRoot(t)
	r, err := Run(Options{Conformance: true, Only: conformanceDir + "/model/*"})
	if err != nil {
		t.Fatal(err)
	}
	byCase := map[string]UnitResult{}
	for _, u := range r.Units {
		if u.Unit.Meta.Case == "" {
			t.Errorf("%s: no case header", u.Unit.Key)
			continue
		}
		byCase[u.Unit.Meta.Case] = u
	}
	text, err := readText(filepath.Join(root, filepath.FromSlash(conformanceDir), "model", "parity.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var match, pending, fail int
	for _, l := range splitLines(text) {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue // TestModelParity reports a malformed line
		}
		u, ok := byCase[f[0]]
		if !ok {
			continue // and a missing file
		}
		switch u.Status {
		case StatusPass:
			match++
		case StatusFail:
			fail++
		default:
			pending++
			t.Logf("%s (%s): PENDING %s", f[0], f[2], u.Reason)
		}
		if p := modelProblem(u, r.Avail, r.Std.Through); p != "" {
			t.Errorf("%s (parity.txt Go verdict %s): %s", f[0], f[2], p)
		}
	}
	t.Logf("%d model units, judged through %s: %d match parity.txt, %d FAIL, %d PENDING (the test wants %d matching)",
		len(r.Units), r.Std.Through, match, fail, pending, modelCases)
}
