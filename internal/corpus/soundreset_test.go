// Why: S89 is the generator's form of a backing allocator reset under an
// arena that was reset itself; its hole must stay rejected and its correct
// form accepted and clean, or a run of it shows nothing.
//
// What: TestSoundGen_ResetForms and resetProgram.
//
// How: every (make, view, hole) form of resetText, alone in a program whose
// Run calls it, is checked on the shared session; a correct form is also
// run on four run seeds, one of them failing allocations, in the dynamic
// checker.

package corpus

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/driver"
)

// resetProgram is a program of S89's form f alone: its header item and
// import, the function, and a Run that folds it.
func resetProgram(f resetForm) string {
	var b strings.Builder
	if resetViews(f.view, f.lit).mem {
		b.WriteString("import std.mem\n\n")
	}
	if resetMakes[f.make].newIn {
		b.WriteString(headerItems()[itemNewIn].text + "\n")
	}
	b.WriteString(resetText("Rb1", f))
	b.WriteString("\nexport Run(k: i64, a: &Arena): i64 {\n    var h: i64 = 0\n    h = h *% 31 +% Rb1(a)\n    h = h *% 31 +% k\n    return h\n}\n")
	return b.String()
}

// TestSoundGen_ResetForms: each hole form of S89 is rejected, with loan
// codes only; each correct form is accepted and runs clean.
func TestSoundGen_ResetForms(t *testing.T) {
	s := soundSess(t)
	for mk := range resetMakes {
		for view := range nResetViews {
			for _, hole := range []bool{false, true} {
				f := resetForm{make: mk, view: view, hole: hole, lit: "7"}
				if view%2 == 1 {
					f.pre = "3"
				}
				f.after = !hole && mk != 1
				text := resetProgram(f)
				name := fmt.Sprintf("make %d view %d hole %v", mk, view, hole)
				unit := fmt.Sprintf("sound/reset%d", soundUnitN.Add(1))
				out := s.CheckUnit(driver.UnitInput{Name: unit, Files: []driver.UnitFile{{Path: "sound.mk", Text: text}}})
				var codes []string
				for _, e := range out.Report.Diags {
					codes = append(codes, e.Diag.Code.String())
				}
				if hole {
					if len(codes) == 0 {
						t.Errorf("%s: accepted, want a loan rejection; runs: %s\n%s", name, resetRuns(s, unit), text)
					}
					for _, c := range codes {
						if !loanCode(c) {
							t.Errorf("%s: %s, want loan codes only (%v)\n%s", name, c, codes, text)
						}
					}
					t.Logf("%s: %v", name, codes)
				} else if len(codes) > 0 {
					t.Errorf("%s: rejected %v, want accepted\n%s", name, codes, text)
				} else if r := resetRuns(s, unit); r != "" {
					t.Errorf("%s: %s\n%s", name, r, text)
				}
				s.Forget([]string{unit})
			}
		}
	}
}

// resetRuns runs unit's Run on four run seeds, the last failing page
// allocations, and is "" when every run completes with no violation.
func resetRuns(s *driver.CorpusSession, unit string) string {
	var bad []string
	for i, k := range RunSeeds(0x5289, 4) {
		fail := uint32(0)
		if i == 3 {
			fail = 3
		}
		r := SoundRunOn(s, SoundRunSpec{Unit: unit, Fn: "Run", Args: []uint64{k}, Tag: &SoundTag{Seed: k, FailAlloc: fail}})
		if r.ErrKind != "" || r.Skipped != "" || len(r.Violations) > 0 {
			bad = append(bad, fmt.Sprintf("run %#x: err %q %s, skipped %q, violations %v", k, r.ErrKind, r.ErrText, r.Skipped, r.Violations))
		}
	}
	return strings.Join(bad, "; ")
}
