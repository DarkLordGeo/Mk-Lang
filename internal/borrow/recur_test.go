// Why: walks that follow references into generic instances must stop on
// polymorphically recursive types, yet never on a merely wide or deep type.
// What: tests of recur, the bound of those walks.
// How: builds wide, deeply nested and recursive generic structs and checks
// storesFirst and walkExtra.

package borrow

import (
	"fmt"
	"testing"

	"mk/internal/types"
)

// TestWalksBoundGrowthNotWidth: a wide type (65 fields ?&B[[i]u8], each
// a different instance of B) and a nest that shrinks as the walk goes into its
// argument (B[B[...66 deep...[i64]]]) give main's answers, false, in
// storesFirst and walkExtra, where a bound on the instances met in a walk made
// them true (E0602 on valid code). A polymorphically recursive G[T] { v: T,
// next: ?&G[?T] } ends: walkExtra with the answer an ANY step gives, true, and
// storesFirst with its closure's, false, since nothing any instance holds
// takes a store (closure_test.go).
func TestWalksBoundGrowthNotWidth(t *testing.T) {
	k := newKit(t)
	k.structItem(k.main, "B", 1, false, false, field("p", k.tab.Ref(k.tab.Param(0))))
	var fs []fieldSpec
	for i := 1; i <= 65; i++ {
		b := k.named("B", k.tab.Array(k.lenArg(uint64(i)), types.U8))
		fs = append(fs, field(fmt.Sprintf("g%d", i), k.tab.Optional(k.tab.Ref(b))))
	}
	k.structItem(k.main, "Wide", 0, false, false, fs...)
	wide := k.named("Wide")
	nest := types.I64
	for range 66 {
		nest = k.named("B", nest)
	}
	k.structItem(k.main, "Deep", 0, false, false, field("d", k.tab.Optional(k.tab.Ref(nest))))
	deep := k.named("Deep")
	g := k.structItem(k.main, "G", 1, false, false)
	gArg := types.TypeArg(k.tab.Optional(k.tab.Param(0)))
	k.items.Get(g).Decl.(*types.StructDecl).Fields = []types.FieldDecl{
		{Name: k.in.Intern("v"), Type: k.tab.Param(0)},
		{Name: k.in.Intern("next"), Type: k.tab.Optional(k.tab.Ref(k.tab.Named(g, []types.GenericArg{gArg})))},
	}
	gInt := k.named("G", types.I32)

	w := walkerOf(k, nil)
	for _, c := range []struct {
		name string
		t    types.TypeID
	}{{"Wide", wide}, {"Deep", deep}} {
		if w.storesFirst(c.t) {
			t.Errorf("storesFirst(%s) = true, want false (no reference in it reaches memory that takes stores)", c.name)
		}
		if w.walkExtra(c.t) {
			t.Errorf("walkExtra(%s) = true, want false (no exclusive reference or writable referent in it)", c.name)
		}
	}
	if w.storesFirst(gInt) || !w.walkExtra(gInt) {
		t.Errorf("G[int]: storesFirst %v, walkExtra %v; want false and true at the bound", w.storesFirst(gInt), w.walkExtra(gInt))
	}
}

// TestRecurCountsGrowthOnThePath: recur stops a nest that grows past
// maxRecurGrowth on one path, never siblings or a nest that shrinks, and
// stops a walk that meets more than maxRecurInstances instances of one
// constructor that grew from an instance above them.
func TestRecurCountsGrowthOnThePath(t *testing.T) {
	k := newKit(t)
	k.structItem(k.main, "B", 1, false, false, field("p", k.tab.Ref(k.tab.Param(0))))
	w := walkerOf(k, nil)

	r := w.newRecur()
	t0 := types.I64
	var leaves []func()
	stopped := -1
	for i := 0; i < 2*maxRecurGrowth; i++ {
		t0 = k.named("B", k.tab.Optional(t0))
		stop, leave := r.enter(t0)
		if stop {
			stopped = i
			break
		}
		leaves = append(leaves, leave)
	}
	if stopped != maxRecurGrowth+1 {
		t.Errorf("a growing nest stopped at depth %d, want %d", stopped, maxRecurGrowth+1)
	}
	for i := len(leaves) - 1; i >= 0; i-- {
		leaves[i]()
	}

	r = w.newRecur()
	for i := 1; i <= 2*maxRecurGrowth; i++ {
		stop, leave := r.enter(k.named("B", k.tab.Array(k.lenArg(uint64(i)), types.U8)))
		if stop {
			t.Fatalf("sibling %d stopped the walk", i)
		}
		leave()
	}

	r = w.newRecur()
	shrink := types.I64
	var nest []types.TypeID
	for range 2 * maxRecurGrowth {
		shrink = k.named("B", shrink)
		nest = append(nest, shrink)
	}
	for i := len(nest) - 1; i >= 0; i-- {
		if stop, _ := r.enter(nest[i]); stop {
			t.Fatalf("a shrinking nest stopped at %d", i)
		}
	}

	// Siblings never grow, so no number of them stops the walk; past
	// maxRecurInstances instances that grew from one above them, it stops.
	r = w.newRecur()
	for i := 1; i <= maxRecurInstances+1; i++ {
		stop, leave := r.enter(k.named("B", k.tab.Array(k.lenArg(uint64(i)), types.U8)))
		if stop {
			t.Fatalf("sibling %d stopped the walk", i)
		}
		leave()
	}
	r = w.newRecur()
	_, top := r.enter(k.named("B", types.I64))
	defer top()
	for i := 1; i <= maxRecurInstances+1; i++ {
		stop, leave := r.enter(k.named("B", k.tab.Array(k.lenArg(uint64(i)), types.I64)))
		if stop != (i > maxRecurInstances) {
			t.Fatalf("grown instance %d: stop %v", i, stop)
		}
		if leave != nil {
			leave()
		}
	}
}
