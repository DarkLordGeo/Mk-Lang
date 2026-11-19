// Why: a function generic over a const N alone, with a requires clause, whose
// body calls a bodiless std method of a generic struct (the cookbook's SumN
// pattern), must instantiate: lower once passed the predicate's generics as
// type arguments, so N reached mono as KParam(0) and Subst refused it.
// What: TestLaneCountRequiresInstantiates.
// How: collects the pattern from source and requires it to instantiate.

package mono

import (
	"testing"

	"mk/internal/types"
)

func TestLaneCountRequiresInstantiates(t *testing.T) {
	src := `export struct V[T: Plain, const N: int] {
    a: [N]T
}

export V[T, N].Zero(): V[T, N] requires N == 2 || N == 4 || N == 8

SumN[const N: int](x: u32): u32
    requires N == 2 || N == 4 || N == 8 {
    v := V[u32, N].Zero()
    _ = v
    return x
}

Root(x: u32): u32 {
    return SumN[8](x)
}
`
	sw := newSrcWorldAt(t, "std.simd", true, src)
	in, ds := Collect(sw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	sum := sw.item(t, "SumN")
	if got := instancesOf(in, sum); len(got) != 1 || got[0].Name != "std.simd.SumN[8]" {
		t.Fatalf("SumN instances %v, want SumN[8]", got)
	}
}
