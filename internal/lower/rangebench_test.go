// Why: the range analysis runs inside Lower on every body, so its compile
// cost must stay small and linear.
// What: BenchmarkPC_CheckedBody and BenchmarkPC_CheckedBodyOff, and
// TestRule8Linear, an allocation bound for many accumulators.
// How: times Lower of one body of a shape most code has (checked + - *, a
// branch, a counted loop over a slice with an accumulator and two checked
// casts) with the pass on and off. Nothing here is a measurement until it
// is run on a named machine and recorded with the command.

package lower

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/types"
)

// pcCheckedSrc is n functions of the shape of the generated module that
// mk check is timed on.
func pcCheckedSrc(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "F%d(a: int, b: int, xs: []u8): int {\n\tvar v := a * %d + b\n\tif v > %d {\n\t\tv = v - %d\n\t}\n"+
			"\tfor j in 0..xs.Len() {\n\t\tv += int(xs[j] & 15) + j\n\t}\n\treturn v + Cast[int](Cast[u8](b & 255))\n}\n", i, i%7+1, i, i)
	}
	return sb.String()
}

func benchChecked(b *testing.B, off bool) {
	cfg, c, body := benchUnit(b, pcCheckedSrc(20), "F10")
	cfg.NoRanges = off
	stmts := 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stmts = stmtCount(Lower(cfg, FromCheck(cfg, c, body)))
	}
	b.ReportMetric(float64(stmts), "stmts/op")
}

// BenchmarkPC_CheckedBody is Lower of that body with the pass.
func BenchmarkPC_CheckedBody(b *testing.B) { benchChecked(b, false) }

// BenchmarkPC_CheckedBodyOff is the same with NoRanges.
func BenchmarkPC_CheckedBodyOff(b *testing.B) { benchChecked(b, true) }

// pcAccSrc is one function with k accumulators summed in one counted loop,
// the shape that once made rule 8 quadratic: no add is proved (a is any i64),
// so rule 8 examines every one of the k sites.
func pcAccSrc(k int) string {
	var sb strings.Builder
	sb.WriteString("F(a: i64, n: int): i64 {\n")
	for i := 0; i < k; i++ {
		fmt.Fprintf(&sb, "\tvar s%d: i64 = 0\n", i)
	}
	sb.WriteString("\tfor _ in 0..n {\n")
	for i := 0; i < k; i++ {
		fmt.Fprintf(&sb, "\t\ts%d += a\n", i)
	}
	sb.WriteString("\t}\n\treturn s0\n}\n")
	return sb.String()
}

// lowerBytes is the bytes one Lower of body allocates, averaged over runs
// after one warm-up run; nothing else runs beside a test that is not
// parallel, so the count is the call's.
func lowerBytes(cfg *Config, c *check.Checker, body *check.Body, runs int) (uint64, *mir.Body) {
	mb := Lower(cfg, FromCheck(cfg, c, body))
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	for i := 0; i < runs; i++ {
		mb = Lower(cfg, FromCheck(cfg, c, body))
	}
	runtime.ReadMemStats(&m1)
	return (m1.TotalAlloc - m0.TotalAlloc) / uint64(runs), mb
}

// TestRule8Linear: rule 8's cost grows linearly with a loop's
// accumulators. Before, each unproved add rescanned the loop and copied two
// whole states, so at k = 4000 one Lower allocated about 1.06 GB against 68
// MB at k = 1000 (15.6x for 4x the body), with the cost cap satisfied.
// Bytes, not time, so the test holds on a loaded machine: with the pass on,
// k = 4000 allocates at most 8x what k = 1000 does (4x is linear), and at
// most 4x what it allocates with NoRanges.
func TestRule8Linear(t *testing.T) {
	if testing.Short() {
		t.Skip("checks two functions of 2,000 and 8,000 statements")
	}
	measure := func(k int, off bool) (uint64, int) {
		cfg, c, body := benchUnit(t, pcAccSrc(k), "F")
		cfg.NoRanges = off
		n, mb := lowerBytes(cfg, c, body, 2)
		adds := 0
		for _, blk := range mb.Blocks {
			for _, s := range blk.Stmts {
				if s.Kind == mir.StAssign && s.Rvalue.Kind == mir.RvBinary && s.Rvalue.Bin == mir.Add && s.Rvalue.Type == types.I64 {
					adds++
				}
			}
		}
		return n, adds
	}
	on1, adds1 := measure(1000, false)
	on4, adds4 := measure(4000, false)
	off4, _ := measure(4000, true)
	t.Logf("bytes per Lower: k=1000 on %d; k=4000 on %d, off %d; growth %.2fx, on/off %.2fx", on1, on4, off4, float64(on4)/float64(on1), float64(on4)/float64(off4))
	if adds1 != 1000 || adds4 != 4000 {
		t.Fatalf("checked i64 adds left: %d and %d, want 1000 and 4000 (no accumulator is provable)", adds1, adds4)
	}
	if on4 > 8*on1 {
		t.Errorf("k=4000 allocates %d bytes, over 8x k=1000's %d: rule 8 is not linear in the accumulators", on4, on1)
	}
	if on4 > 4*off4 {
		t.Errorf("k=4000 allocates %d bytes with the pass, over 4x the %d without it", on4, off4)
	}
}
