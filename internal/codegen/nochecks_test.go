// Why: the cost of overflow and bounds checks is measured on tests/bench by
// building with them off, against the budgets of at most 5% and 3% median.
// What: TestBudgetNoChecksFlags over the test-only
// Config.TestNoOverflowChecks and TestNoBoundsChecks (codegen.go).
// How: without them the IR is the checked IR; with them a checked + becomes
// `add nsw` (signed) or `add` (unsigned) with no with.overflow call, and
// lower's PanicBounds trap becomes unreachable, so clang -O2 deletes the
// compare.

package codegen

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func TestBudgetNoChecksFlags(t *testing.T) {
	bounds := fmt.Sprintf("@mk.std.rt.PanicAt(i32 noundef %d,", types.PanicBounds)
	over := fmt.Sprintf("@mk.std.rt.PanicAt(i32 noundef %d,", types.PanicOverflow)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, flags := range []struct {
			name             string
			noOver, noBounds bool
		}{{"checked", false, false}, {"nooverflow", true, false}, {"nobounds", false, true}, {"none", true, true}} {
			t.Run(target.Name+"/"+flags.name, func(t *testing.T) {
				e := newTestEnv()
				bodies := boundsBodies(e)
				bodies = append(bodies, binFn(e, "SubU8", mir.Sub, types.U8, types.U8, mir.Copy(place(2))),
					binFn(e, "MulI32", mir.Mul, types.I32, types.I32, mir.Copy(place(2))))
				u := unitOf(e, bodies)
				var b strings.Builder
				cfg := Config{Target: target, Profile: types.Release, TestNoOverflowChecks: flags.noOver, TestNoBoundsChecks: flags.noBounds}
				if _, err := EmitPack(u, 0, e, cfg, &b); err != nil {
					t.Fatal(err)
				}
				ll := b.String()
				hasOv := strings.Contains(ll, "with.overflow") || strings.Contains(ll, over)
				if hasOv == flags.noOver {
					t.Errorf("overflow checks present %v with TestNoOverflowChecks %v", hasOv, flags.noOver)
				}
				if flags.noOver {
					sum := fnText(ll, "mk.main.SumAll")
					if !strings.Contains(sum, "add nsw i64") || !strings.Contains(fnText(ll, "mk.main.SubU8"), "sub i8") ||
						strings.Contains(fnText(ll, "mk.main.SubU8"), "sub nsw") || !strings.Contains(fnText(ll, "mk.main.MulI32"), "mul nsw i32") {
						t.Errorf("unchecked arithmetic is not C's:\n%s", ll)
					}
				}
				if got := strings.Contains(ll, bounds); got == flags.noBounds {
					t.Errorf("bounds traps present %v with TestNoBoundsChecks %v", got, flags.noBounds)
				}
				if flags.noBounds && !strings.Contains(fnText(ll, "mk.main.At"), "unreachable") {
					t.Errorf("At has no unreachable:\n%s", fnText(ll, "mk.main.At"))
				}
				clangC(t, target, t.TempDir(), "pack", ll, "-O2")
			})
		}
	}
}
