// Why: a kernel that fixes one lane count from another (`M == N / 2`)
// should have its SIMD clauses judged over the implied set, not restated.
// What: TestImpliedSet over the real std.simd.
// How: checks a widening kernel and its mutants and compares the E0706
// lines.

package driver

import (
	"strconv"
	"strings"
	"testing"
)

// TestImpliedSet: a widening kernel over the real std.simd
// lists N and fixes M by `M == N / 2`, so the lane clause of Vec[u16, M] and
// the clauses that relate the two counts (WidenLo, NarrowSat) are judged
// over the three pairs and nothing is restated. The mutants: an N whose half
// is no lane count (N == 2 gives M == 1) and the multiplicative form, which
// implies no set, are still E0706.
func TestImpliedSet(t *testing.T) {
	t.Parallel()
	const kernel = `
    var i := 0
    for i + N <= src.Len() {
        v := simd.Vec[u8, N].FromSlice(src, i)
        lo := v.WidenLo[u16, M]() +% simd.Vec[u16, M].Splat(1)
        hi := v.WidenHi[u16, M]()
        lo.NarrowSat[u8, N](hi).CopyTo(dst, i)
        i += N
    }
}
`
	r := checkText(t, `import std.simd

Good[const N: int, const M: int](src: []u8, dst: mut []u8)
    requires N == 16 || N == 32 || N == 64
    requires M == N / 2
{`+kernel+`
TwoLevel[const N: int, const M: int](src: []u8, dst: mut []u8)
    requires N == 2 || N == 16
    requires M == N / 2
{`+kernel+`
Product[const N: int, const M: int](src: []u8, dst: mut []u8)
    requires N == 16 || N == 32 || N == 64
    requires M * 2 == N
{`+kernel+`
Use(src: []u8, dst: mut []u8) {
    Good[16, 8](src, dst)
    Good[64, 32](src, dst)
}
`)
	lines := map[int]bool{}
	for _, e := range r.Report.Diags {
		if e.Diag.Code.String() != "E0706" {
			t.Fatalf("unexpected %s:\n%s", e.Diag.Code, diagLines(r))
		}
		lines[int(r.Sources.Position(e.Diag.At.StartPos()).Line)] = true
	}
	// Good's body is lines 7 to 15, TwoLevel's 20 to 28, Product's 33 to 41.
	var in []string
	for l := range lines {
		switch {
		case l >= 7 && l <= 15:
			in = append(in, "Good@"+strconv.Itoa(l))
		case l >= 20 && l <= 28:
			in = append(in, "TwoLevel")
		case l >= 33 && l <= 41:
			in = append(in, "Product")
		default:
			in = append(in, "other@"+strconv.Itoa(l))
		}
	}
	got := strings.Join(in, " ")
	if strings.Contains(got, "Good") || strings.Contains(got, "other") || !strings.Contains(got, "TwoLevel") || !strings.Contains(got, "Product") {
		t.Errorf("want E0706 in TwoLevel and Product only, got %v:\n%s", got, diagLines(r))
	}
}
