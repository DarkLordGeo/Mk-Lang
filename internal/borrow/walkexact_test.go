// Why: walkExtra memoizes answers inside a walk (walkExact); a stored answer
// must be the one every walk gives, and the memo must keep the walk linear on
// long reference chains.
// What: fixed walkExtra answers on cyclic struct graphs in every root order,
// and the count of walkFrom calls over the roots &i32 .. &^n i32.
// How: builds the graphs with the kit, walks them on one walker per order and
// checks each answer; reads walker.walkCalls for the count.

package borrow

import (
	"fmt"
	"testing"

	"mk/internal/types"
)

// cyclicStructs declares the structs of fields (in order) with no fields, then
// sets the fields, so they may refer to one another.
func cyclicStructs(k *kit, fields map[string]func() []fieldSpec, order ...string) {
	ids := map[string]types.ItemID{}
	for _, n := range order {
		ids[n] = k.structItem(k.main, n, 0, false, false)
	}
	for _, n := range order {
		d := k.items.Get(ids[n]).Decl.(*types.StructDecl)
		for _, f := range fields[n]() {
			d.Fields = append(d.Fields, types.FieldDecl{Name: k.in.Intern(f.name), Type: f.t})
		}
	}
}

func permutations(n int) [][]int {
	if n == 1 {
		return [][]int{{0}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			out = append(out, append(append(append([]int{}, p[:i]...), n-1), p[i:]...))
		}
	}
	return out
}

// Y { t: &T, m: mut i32 }, T { y: &Y }, Z { t: &T }: every one reaches the
// mut i32 through the cycle Y -> T -> Y, in every root order. Walking Y
// first cuts the walk of T at Y; that cut answer for T is no answer of T's
// own, and Z (walked after) still reaches Y. W { u: &U }, U { w: &W } is a
// cycle that reaches nothing.
func TestWalkExtraCyclesEveryOrder(t *testing.T) {
	names := []string{"Y", "T", "Z", "W", "U"}
	want := map[string]bool{"Y": true, "T": true, "Z": true, "W": false, "U": false}
	for _, p := range permutations(len(names)) {
		k := newKit(t)
		ref := func(n string) types.TypeID { return k.tab.Ref(k.named(n)) }
		cyclicStructs(k, map[string]func() []fieldSpec{
			"Y": func() []fieldSpec { return []fieldSpec{field("t", ref("T")), field("m", k.tab.Mut(types.I32))} },
			"T": func() []fieldSpec { return []fieldSpec{field("y", ref("Y"))} },
			"Z": func() []fieldSpec { return []fieldSpec{field("t", ref("T"))} },
			"W": func() []fieldSpec { return []fieldSpec{field("u", ref("U"))} },
			"U": func() []fieldSpec { return []fieldSpec{field("w", ref("W"))} },
		}, names...)
		w := walkerOf(k, nil)
		for _, i := range p {
			n := names[i]
			for _, root := range []types.TypeID{k.named(n), ref(n)} {
				if got := w.walkExtra(root); got != want[n] {
					t.Errorf("order %v: walkExtra(%s) = %v, want %v", p, k.tab.TypeString(root, nil), got, want[n])
				}
			}
		}
	}
}

// The roots &i32, &&i32, .. &^n i32, outermost first and innermost first,
// cost O(n) walkFrom calls in all, not one walk of the whole chain per root.
func TestWalkExtraRefChainLinear(t *testing.T) {
	const n = 2000
	for _, outerFirst := range []bool{false, true} {
		k := newKit(t)
		roots := make([]types.TypeID, n)
		ty := types.I32
		for i := range roots {
			ty = k.tab.Ref(ty)
			roots[i] = ty
		}
		w := walkerOf(k, nil)
		for i := range roots {
			r := roots[i]
			if outerFirst {
				r = roots[n-1-i]
			}
			if w.walkExtra(r) {
				t.Fatalf("walkExtra(&^%d i32) = true", i+1)
			}
		}
		if w.walkCalls > 4*n {
			t.Errorf("%s: %d walkFrom calls for %d roots, want at most %d",
				fmt.Sprint(map[bool]string{false: "innermost first", true: "outermost first"}[outerFirst]), w.walkCalls, n, 4*n)
		}
	}
}
