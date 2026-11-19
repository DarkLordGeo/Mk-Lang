// Why: the range analysis is costly, so the driver runs it once per body.
// What: TestPM_ProvedBoundsMatchFold.
// How: LowerProved keeps the bounds checks Lower's own run of the range
// analysis proved, and ProvedBounds.Fold folds them once borrow has accepted
// the body; on every tests/bench kernel the bodies must equal those of
// Lower and FoldProvedBounds.

package lower

import (
	"reflect"
	"testing"
)

// TestPM_ProvedBoundsMatchFold: on every tests/bench kernel, LowerProved
// and Fold give exactly the bodies Lower and FoldProvedBounds give, and
// with NoRanges nothing is proved.
func TestPM_ProvedBoundsMatchFold(t *testing.T) {
	folded := 0
	for _, f := range pcKernels(t) {
		src, name := readKernel(t, f), kernelName(f)
		want := pcLower(t, src, pcOpt{bin: true, fold: true})
		got := pcLower(t, src, pcOpt{bin: true, proved: true})
		for _, k := range want.keys {
			if !reflect.DeepEqual(got.bodies[k], want.bodies[k]) {
				t.Errorf("%s: %s differs between LowerProved+Fold and Lower+FoldProvedBounds", name, k)
			}
			c := got.counts(k)
			folded += c.bounds - c.boundsLeft
		}
		off := pcLower(t, src, pcOpt{bin: true, proved: true, tweak: func(c *Config) { c.NoRanges = true }})
		for _, k := range off.keys {
			if c := off.counts(k); c.bounds != c.boundsLeft {
				t.Errorf("%s: %s folded %d checks with NoRanges", name, k, c.bounds-c.boundsLeft)
			}
		}
	}
	if folded == 0 {
		t.Fatal("no kernel had a folded bounds check: the comparison is vacuous")
	}
}
