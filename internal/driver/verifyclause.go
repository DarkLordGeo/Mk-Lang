// Why: a `from` or `into` clause that E0601, E0602 or E0604 offers is proven
// for the function it edits, but it adds loans at the callers. A caller may
// then fail with an error no clause clears (an E0505 or E0503 where the new
// loan meets a longer-lived value), or its own clause may lead to one. Such
// a clause must not be labelled proven, and mk fix must not write it.
//
// What: provenClause, notProvenPrefix and whyNotProven, verifier.settleClause
// (the chain rule), clauseStepMoved and verifier.movedClauses.
//
// How: the clause is recompiled alone. A better report proves it. When its
// error only moved to the callers, their own proven clauses are applied too
// and the package rechecked, step by step, until the report is better than
// the one the clause came from (proven) or a step makes no such move (an
// alternative naming what the recheck reported). Every step spends one
// recheck of the report's bound; a chain the bound cuts is not proven.
package driver

import (
	"strings"

	"mk/internal/diag"
)

// notProvenPrefix starts the note of a clause fix offered as an
// alternative.
const notProvenPrefix = " (not proven: "

// provenClause reports whether fix f of d is a proven clause fix: a `from`
// or `into` that E0601, E0602 or E0604 offers.
func provenClause(d *diag.Diagnostic, f diag.Fix) bool {
	if f.Class != diag.Proven {
		return false
	}
	return d.Code == diag.E0601 || d.Code == diag.E0602 || d.Code == diag.E0604
}

// whyNotProven is the alternative's note for a proven clause fix whose
// chain ends in no progress: the diagnostics the last recheck reported that
// the base did not, at most three, by code and place.
func whyNotProven(after, base Result) string {
	type key struct {
		at  diagLine
		msg string
	}
	left := map[key]int{}
	for _, e := range base.Report.Diags {
		left[key{lineOf(base, e.Diag), e.Diag.Message}]++
	}
	var added []string
	more := 0
	for _, e := range after.Report.Diags {
		k := key{lineOf(after, e.Diag), e.Diag.Message}
		if left[k] > 0 {
			left[k]--
			continue
		}
		if len(added) == 3 {
			more++
			continue
		}
		p := after.Sources.Position(e.Diag.At.StartPos())
		added = append(added, e.Diag.Code.String()+" at "+p.Path+":"+itoaQ(int(p.Line)))
	}
	if len(added) == 0 {
		return notProvenPrefix + "recompiled, it did not improve the report)"
	}
	s := strings.Join(added, ", ")
	if more > 0 {
		s += " and " + itoaQ(more) + " more"
	}
	return notProvenPrefix + "recompiled, the package then reports " + s + ")"
}

// settleClause judges proven clause c, whose recheck alone is after (the
// overlay is back to the base). It is kept proven when after is better than
// the base, or when the moves its error makes to callers, each followed by
// the callers' own proven clauses, end in a report better than the base.
// Otherwise c becomes an alternative.
func (v *verifier) settleClause(c vcand, after Result) {
	saved := v.ov.snapshot()
	defer v.ov.restore(saved)
	v.ov.apply([]fixCand{{path: c.path, edits: c.d.Fixes[c.fix].Edits}})
	prev, moved := v.base, []*diag.Diagnostic{c.d}
	for {
		if better(after.Report, v.base.Report) {
			return
		}
		if !clauseStepMoved(after, prev, moved) {
			v.drop(c, whyNotProven(after, v.base))
			return
		}
		next, nd := v.movedClauses(after, prev, c.d.Code)
		if len(next) == 0 {
			v.drop(c, whyNotProven(after, v.base))
			return
		}
		if v.used >= v.budget {
			v.drop(c, notProvenPrefix+"its error moves to the callers, and their clauses were not recompiled within the bound of "+itoaQ(v.budget)+" rechecks per report)")
			return
		}
		v.used++
		v.ov.apply(next) // on top of the clauses so far: next's spans are after's
		n, ok := v.check()
		if !ok {
			v.drop(c, whyNoSession)
			return
		}
		prev, after, moved = after, n, nd
	}
}

// clauseStepMoved is mk fix's clauseMovedUp for a step of a chain: the
// moved diagnostics (all of one code, E0604 or E0602) are gone from their
// lines in after, no internal error appears, the phase is not earlier, and
// no diagnostic of another code is added.
func clauseStepMoved(after, before Result, moved []*diag.Diagnostic) bool {
	if len(moved) == 0 || internalCount(after.Report) > 0 || after.Report.Phase < before.Report.Phase {
		return false
	}
	code := moved[0].Code
	if code != diag.E0604 && code != diag.E0602 {
		return false
	}
	other := func(r Result) int {
		n := 0
		for _, e := range r.Report.Diags {
			if e.Diag.Code != code {
				n++
			}
		}
		return n
	}
	if other(after) > other(before) {
		return false
	}
	gone := map[diagLine]bool{}
	for _, d := range moved {
		gone[lineOf(before, d)] = true
	}
	for _, e := range after.Report.Diags {
		if gone[lineOf(after, e.Diag)] {
			return false
		}
	}
	return true
}

// movedClauses are the proven clause fixes of the diagnostics of code that
// after reports and before did not (by line and message): the callers the
// error moved to. Two callers in one function offer the same clause; it is
// applied once.
func (v *verifier) movedClauses(after, before Result, code diag.Code) ([]fixCand, []*diag.Diagnostic) {
	type key struct {
		at  diagLine
		msg string
	}
	old := map[key]int{}
	for _, e := range before.Report.Diags {
		old[key{lineOf(before, e.Diag), e.Diag.Message}]++
	}
	var cands []fixCand
	var ds []*diag.Diagnostic
	for _, e := range after.Report.Diags {
		d := e.Diag
		k := key{lineOf(after, d), d.Message}
		if old[k] > 0 {
			old[k]--
			continue
		}
		if d.Code != code {
			continue
		}
		ds = append(ds, d)
		for _, f := range d.Fixes {
			if !provenClause(d, f) {
				continue
			}
			if p := v.path(after, f); p != "" {
				cands = append(cands, fixCand{path: p, edits: f.Edits, d: d})
			}
			break
		}
	}
	return nonOverlapping(cands), ds
}
