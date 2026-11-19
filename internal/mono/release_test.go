// Why: std.raw.Release[T, A] and std.raw.HasFree[T] depend on whether T has
// an impl of Free, which is known only per instance.
// What: tests of release.go: Release resolved to the impl's Free or
// to Forget, HasFree folded, non-generic callers rewritten, generic impls, and
// the ICEs for a linear type with no Free.
// How: r1World is a fake std.raw and a fake std.core that declares its own
// interface Free; the tests collect and read the rewritten calls.

package mono

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// r1World is the mono test world with std.raw's Release, HasFree and
// Forget, std.core's Alloc and (optionally) interface Free, a struct S whose
// impl of Free is SFree, and linear types by request.
type r1World struct {
	*world
	raw, core           types.ModuleID
	release, hasFree    types.ItemID
	forget, free, sFree types.ItemID
	alloc, s, refAlloc  types.TypeID
	linear              map[types.TypeID]bool
	yes, no             types.ItemID
	useRelease, useHas  types.ItemID
	guarded             types.ItemID
	p0                  types.TypeID
}

func (w *r1World) Layout(t types.TypeID) *types.Layout {
	l := w.world.Layout(t)
	if w.linear[t] {
		c := *l
		c.Shape.Kind = types.Linear
		return &c
	}
	return l
}

// stdFn declares a bodiless intrinsic of a std module.
func (w *r1World) stdFn(mod types.ModuleID, path, name string, generics int, params []types.TypeID, result types.TypeID) types.ItemID {
	id := w.items.AddItem(&types.Item{Module: mod, Key: types.ItemKey(path, name), Name: w.in.Intern(name), Kind: types.KindFunc})
	sig := types.Signature{Item: id, Name: w.in.Intern(name), Result: result, Span: w.span(), OwnGenerics: uint32(generics)}
	for i := 0; i < generics; i++ {
		sig.Generics = append(sig.Generics, types.GenericParam{Name: w.in.Intern(fmt.Sprintf("T%d", i))})
	}
	for i, p := range params {
		sig.Params = append(sig.Params, types.Param{Name: w.in.Intern(fmt.Sprintf("p%d", i)), Type: p})
	}
	w.items.Get(id).Decl = &types.FuncDecl{Sig: sig, Bodiless: true, Intrinsic: true}
	return id
}

// newR1World builds the world; withFree declares std.core's interface Free
// and S's impl of it.
func newR1World(t *testing.T, withFree bool) *r1World {
	t.Helper()
	w := &r1World{world: newWorld(), linear: map[types.TypeID]bool{}}
	tab := w.tab
	w.p0 = tab.Param(0)
	p1 := tab.Param(1)
	w.raw = w.items.AddModule(&types.Module{Path: "std.raw", Std: true})
	w.core = w.items.AddModule(&types.Module{Path: "std.core", Std: true})
	allocItem := w.items.AddItem(&types.Item{Module: w.core, Key: types.ItemKey("std.core", "Alloc"), Name: w.in.Intern("Alloc"),
		Kind: types.KindStruct, Decl: &types.StructDecl{}})
	w.alloc = tab.Named(allocItem, nil)
	w.refAlloc = tab.Ref(w.alloc)
	w.release = w.stdFn(w.raw, "std.raw", "Release", 2, []types.TypeID{w.p0, tab.Ref(p1)}, types.Void)
	w.hasFree = w.stdFn(w.raw, "std.raw", "HasFree", 1, nil, types.Bool)
	w.forget = w.stdFn(w.raw, "std.raw", "Forget", 1, []types.TypeID{w.p0}, types.Void)
	sItem := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "S"), Name: w.in.Intern("S"),
		Kind: types.KindStruct, Decl: &types.StructDecl{}})
	w.s = tab.Named(sItem, nil)
	w.sFree = w.fn("SFree", 0, []types.TypeID{w.s, w.refAlloc}, types.Void)
	w.body(w.sFree).ret()
	if withFree {
		w.free = w.items.AddItem(&types.Item{Module: w.core, Key: types.ItemKey("std.core", "Free"), Name: w.in.Intern("Free"),
			Kind: types.KindInterface, Decl: &types.InterfaceDecl{Methods: []types.Signature{{Name: w.in.Intern("Free")}}}})
		w.impls[implKey{w.free, 0, w.s}] = w.sFree
	}
	w.yes, w.no = w.leaf("Yes"), w.leaf("No")

	// UseRelease[T](x: T, a: &Alloc) { raw.Release[T, Alloc](x, a) }
	w.useRelease = w.fn("UseRelease", 1, []types.TypeID{w.p0, w.refAlloc}, types.Void)
	ub := w.body(w.useRelease)
	ub.call(w.release, []types.GenericArg{ty(w.p0), ty(w.alloc)}, ub.temp(types.Void), mir.Arg{Operand: mir.Move(ub.b.Place(1)), Mode: mir.ArgMove},
		arg(copyOf(ub.b, 2)))
	ub.ret()

	// UseHas[T]() { if raw.HasFree[T]() { Yes() } else { No() } }
	w.useHas = w.fn("UseHas", 1, nil, types.Void)
	hb := w.body(w.useHas)
	d := hb.temp(types.Bool)
	hb.call(w.hasFree, []types.GenericArg{ty(w.p0)}, d)
	w.branchCalls(hb, d, w.yes, w.no)

	// Guarded[T](x: T, a: &Alloc) { if raw.HasFree[T]() { raw.Release(x, a) } else { raw.Forget(x) } }
	w.guarded = w.fn("Guarded", 1, []types.TypeID{w.p0, w.refAlloc}, types.Void)
	gb := w.body(w.guarded)
	gd := gb.temp(types.Bool)
	gb.call(w.hasFree, []types.GenericArg{ty(w.p0)}, gd)
	sw := gb.cur
	yesB := gb.b.AddBlock(mir.OriginBody, mir.NoDefer)
	noB := gb.b.AddBlock(mir.OriginBody, mir.NoDefer)
	gb.cur = yesB
	gb.call(w.release, []types.GenericArg{ty(w.p0), ty(w.alloc)}, gb.temp(types.Void), mir.Arg{Operand: mir.Move(gb.b.Place(1)), Mode: mir.ArgMove},
		arg(copyOf(gb.b, 2)))
	join := gb.cur
	gb.cur = noB
	gb.call(w.forget, []types.GenericArg{ty(w.p0)}, gb.temp(types.Void), mir.Arg{Operand: mir.Move(gb.b.Place(1)), Mode: mir.ArgMove})
	w.gotoTerm(gb.b, gb.cur, join)
	gb.b.Blocks[sw].Term = mir.Terminator{Kind: mir.TermSwitch, Span: gb.b.AddSpan(w.span()), On: copyOf(gb.b, gd),
		Cases: gb.b.AddCases(mir.SwitchCase{Value: 1, Target: yesB}), Default: noB}
	gb.cur = join
	gb.ret()
	return w
}

// branchCalls ends f's current block with a switch on d: yes() or no(),
// joined, then return.
func (w *r1World) branchCalls(f *fb, d mir.LocalID, yes, no types.ItemID) {
	sw := f.cur
	yb := f.b.AddBlock(mir.OriginBody, mir.NoDefer)
	nb := f.b.AddBlock(mir.OriginBody, mir.NoDefer)
	f.b.Blocks[sw].Term = mir.Terminator{Kind: mir.TermSwitch, Span: f.b.AddSpan(w.span()), On: copyOf(f.b, d),
		Cases: f.b.AddCases(mir.SwitchCase{Value: 1, Target: yb}), Default: nb}
	f.cur = yb
	f.call(yes, nil, f.temp(types.Void))
	join := f.cur
	f.cur = nb
	f.call(no, nil, f.temp(types.Void))
	w.gotoTerm(f.b, f.cur, join)
	f.cur = join
	f.ret()
}

// main declares a non-generic root Main(x: t, a: &Alloc) calling item[t]
// (with (x, a) when withArgs).
func (w *r1World) main(name string, item types.ItemID, t types.TypeID, withArgs bool) types.ItemID {
	m := w.fn(name, 0, []types.TypeID{t, w.refAlloc}, types.Void)
	mb := w.body(m)
	var args []mir.Arg
	if withArgs {
		args = []mir.Arg{{Operand: mir.Move(mb.b.Place(1)), Mode: mir.ArgMove}, arg(copyOf(mb.b, 2))}
	}
	mb.call(item, []types.GenericArg{ty(t)}, mb.temp(types.Void), args...)
	mb.ret()
	return m
}

// collectR1 runs Collect with validation over the r1World.
func (w *r1World) collectR1(t *testing.T, roots ...types.ItemID) *Instances {
	t.Helper()
	var rs []Root
	for _, r := range roots {
		rs = append(rs, mainRoot(r))
	}
	var in *Instances
	if ie := source.Guard("mono test", source.Span{}, func() { in, _ = Collect(w, rs, Options{Validate: true}) }); ie != nil {
		t.Fatalf("Collect: %s\n%s", ie, ie.Stack)
	}
	return in
}

// r1Inst finds the one instance whose name starts with prefix.
func r1Inst(t *testing.T, in *Instances, prefix string) *Instance {
	t.Helper()
	for i := range in.List {
		if strings.HasPrefix(in.List[i].Name, prefix) {
			return &in.List[i]
		}
	}
	t.Fatalf("no instance %q in\n%s", prefix, summary(in))
	return nil
}

func hasInst(in *Instances, prefix string) bool {
	for i := range in.List {
		if strings.HasPrefix(in.List[i].Name, prefix) {
			return true
		}
	}
	return false
}

// r1Calls lists the direct callees of b's call terminators.
func r1Calls(b *mir.Body) []types.ItemID {
	var out []types.ItemID
	for i := range b.Blocks {
		if t := &b.Blocks[i].Term; t.Kind == mir.TermCall && t.Callee.Kind == mir.CallDirect {
			out = append(out, t.Callee.Fn.Item)
		}
	}
	return out
}

// releaseCall is the one call of b, which must be Release's rewrite.
func releaseCall(t *testing.T, b *mir.Body) *mir.Terminator {
	t.Helper()
	var got *mir.Terminator
	for i := range b.Blocks {
		if tm := &b.Blocks[i].Term; tm.Kind == mir.TermCall {
			if got != nil {
				t.Fatal("more than one call")
			}
			got = tm
		}
	}
	if got == nil {
		t.Fatal("no call")
	}
	return got
}

// TestReleaseResolvesToImplFree: Release[S, Alloc](x, a) with S's impl
// of std.core.Free becomes a direct call of that impl's Free with both
// arguments, and the impl's method is collected.
func TestReleaseResolvesToImplFree(t *testing.T) {
	w := newR1World(t, true)
	in := w.collectR1(t, w.main("Main", w.useRelease, w.s, true))
	u := r1Inst(t, in, "m.UseRelease[")
	c := releaseCall(t, u.Body)
	if c.Callee.Kind != mir.CallDirect || c.Callee.Fn.Item != w.sFree || c.Args.Len != 2 {
		t.Fatalf("Release[S] became %v of %s with %d arguments", c.Callee.Kind, w.items.Get(c.Callee.Fn.Item).Key, c.Args.Len)
	}
	w.inst(t, in, "m.SFree")
	if hasInst(in, "std.raw.Release") || hasInst(in, "std.raw.Forget") {
		t.Fatalf("Release or Forget collected:\n%s", summary(in))
	}
	if r1Calls(w.bodies[w.useRelease])[0] != w.release {
		t.Fatal("the generic body was modified")
	}
}

// TestReleaseOfPlainTypeForgets: Release[int, Alloc](x, a), int having
// no impl of Free, becomes Forget[int](x): one argument, a left unused.
func TestReleaseOfPlainTypeForgets(t *testing.T) {
	w := newR1World(t, true)
	in := w.collectR1(t, w.main("Main", w.useRelease, types.Int, true))
	u := r1Inst(t, in, "m.UseRelease[")
	c := releaseCall(t, u.Body)
	ga := u.Body.GenericArgs[c.Callee.Fn.Args.Start:c.Callee.Fn.Args.End()]
	if c.Callee.Fn.Item != w.forget || len(ga) != 1 || ga[0].Type != types.Int || c.Args.Len != 1 {
		t.Fatalf("Release[int] became %s%v with %d arguments", w.items.Get(c.Callee.Fn.Item).Key, ga, c.Args.Len)
	}
	if a := u.Body.Args[c.Args.Start]; a.Operand.Place.Local != 1 {
		t.Fatalf("Forget's argument is local %d, want x (1)", a.Operand.Place.Local)
	}
	r1Inst(t, in, "std.raw.Forget[")
}

// TestReleaseWithoutFreeInterfaceForgets: with no interface Free in
// std.core (today's live tree), every Release is a Forget and HasFree is
// false, even for a type that would have an impl.
func TestReleaseWithoutFreeInterfaceForgets(t *testing.T) {
	w := newR1World(t, false)
	in := w.collectR1(t, w.main("Main", w.useRelease, w.s, true), w.main("Main2", w.useHas, w.s, false))
	c := releaseCall(t, r1Inst(t, in, "m.UseRelease[").Body)
	if c.Callee.Fn.Item != w.forget || c.Args.Len != 1 {
		t.Fatalf("Release[S] with no interface Free became %s", w.items.Get(c.Callee.Fn.Item).Key)
	}
	if hasInst(in, "m.Yes") || !hasInst(in, "m.No") {
		t.Fatalf("HasFree[S] with no interface Free is not false:\n%s", summary(in))
	}
}

// TestHasFreeFolds: HasFree[T]() is the constant answer per instance,
// and its branch is folded: UseHas[S] reaches only Yes, UseHas[int] only
// No, and no instance body calls HasFree.
func TestHasFreeFolds(t *testing.T) {
	w := newR1World(t, true)
	in := w.collectR1(t, w.main("MainS", w.useHas, w.s, false))
	if !hasInst(in, "m.Yes") || hasInst(in, "m.No") {
		t.Fatalf("HasFree[S] is not true:\n%s", summary(in))
	}
	in = w.collectR1(t, w.main("MainInt", w.useHas, types.Int, false))
	if hasInst(in, "m.Yes") || !hasInst(in, "m.No") {
		t.Fatalf("HasFree[int] is not false:\n%s", summary(in))
	}
	for _, i := range in.List {
		if i.Body == nil {
			continue
		}
		for _, c := range r1Calls(i.Body) {
			if c == w.hasFree {
				t.Fatalf("%s still calls HasFree", i.Name)
			}
		}
	}
	if hasInst(in, "std.raw.HasFree") {
		t.Fatal("HasFree collected")
	}
	// Instantiate (eval's path) folds it too.
	b := Instantiate(w, w.useHas, []types.GenericArg{ty(w.s)}, Options{Validate: true})
	if cs := r1Calls(b); len(cs) != 1 || cs[0] != w.yes {
		t.Fatalf("Instantiate UseHas[S] calls %v", cs)
	}
}

// TestLinearWithoutFreeICE: a linear T with no impl of Free reaching a
// live Release is an ICE (check's release bounds should rule it out); the
// same Release under `if raw.HasFree[T]()` is pruned first, so the guarded
// form builds.
func TestLinearWithoutFreeICE(t *testing.T) {
	w := newR1World(t, true)
	lin := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "L"), Name: w.in.Intern("L"),
		Kind: types.KindStruct, Decl: &types.StructDecl{}})
	l := w.tab.Named(lin, nil)
	w.linear[l] = true
	m := w.main("Main", w.useRelease, l, true)
	ie := source.Guard("mono test", source.Span{}, func() { Collect(w, []Root{mainRoot(m)}, Options{Validate: true}) })
	if ie == nil || !strings.Contains(ie.What, "std.raw.Release") || !strings.Contains(ie.What, "linear") {
		t.Fatalf("got %v, want an ICE naming std.raw.Release and a linear type", ie)
	}
	in := w.collectR1(t, w.main("MainGuarded", w.guarded, l, true))
	g := r1Inst(t, in, "m.Guarded[")
	if cs := r1Calls(g.Body); len(cs) != 1 || cs[0] != w.forget {
		t.Fatalf("Guarded[L] calls %v, want Forget only", cs)
	}
	// A linear T with an impl releases through it.
	w.linear[w.s] = true
	in = w.collectR1(t, w.main("MainS", w.useRelease, w.s, true))
	if c := releaseCall(t, r1Inst(t, in, "m.UseRelease[").Body); c.Callee.Fn.Item != w.sFree {
		t.Fatalf("Release of a linear S with Free became %s", w.items.Get(c.Callee.Fn.Item).Key)
	}
}

// TestNonGenericCallerRewritten: a non-generic body calling
// Release[S, Alloc] and HasFree[S] directly is rewritten too (needsRewrite),
// so codegen never meets either key.
func TestNonGenericCallerRewritten(t *testing.T) {
	w := newR1World(t, true)
	m := w.fn("Direct", 0, []types.TypeID{w.s, w.refAlloc}, types.Void)
	mb := w.body(m)
	mb.call(w.release, []types.GenericArg{ty(w.s), ty(w.alloc)}, mb.temp(types.Void),
		mir.Arg{Operand: mir.Move(mb.b.Place(1)), Mode: mir.ArgMove}, arg(copyOf(mb.b, 2)))
	d := mb.temp(types.Bool)
	mb.call(w.hasFree, []types.GenericArg{ty(w.s)}, d)
	w.branchCalls(mb, d, w.yes, w.no)
	in := w.collectR1(t, m)
	cs := r1Calls(r1Inst(t, in, "m.Direct").Body)
	if len(cs) != 2 || cs[0] != w.sFree || cs[1] != w.yes {
		t.Fatalf("Direct calls %v, want SFree then Yes", cs)
	}
	if b := Instantiate(w, m, nil, Options{Validate: true}); b == w.bodies[m] {
		t.Fatal("Instantiate returned the lowered body unrewritten")
	}
}

// TestImplDependencyRecorded: the instance's resolved Release depends on
// whether T has an impl of std.core.Free, recorded as ifaceCall's lookup is:
// the resolved callee is in Instance.Content (iface.Hasher.
// InstanceContentHash), so adding `impl Free for S` changes the content of
// UseRelease[S] (and so its pack's key) under the same instance key, and
// removing it restores the old content.
func TestImplDependencyRecorded(t *testing.T) {
	w := newR1World(t, true)
	delete(w.impls, implKey{w.free, 0, w.s})
	m := w.main("Main", w.useRelease, w.s, true)
	before := *r1Inst(t, w.collectR1(t, m), "m.UseRelease[")
	if c := releaseCall(t, before.Body); c.Callee.Fn.Item != w.forget {
		t.Fatalf("no impl: Release[S] became %s", w.items.Get(c.Callee.Fn.Item).Key)
	}
	w.impls[implKey{w.free, 0, w.s}] = w.sFree
	with := *r1Inst(t, w.collectR1(t, m), "m.UseRelease[")
	if c := releaseCall(t, with.Body); c.Callee.Fn.Item != w.sFree {
		t.Fatalf("impl added: Release[S] became %s", w.items.Get(c.Callee.Fn.Item).Key)
	}
	if with.Key != before.Key || with.Content == before.Content {
		t.Fatalf("impl added: key %v->%v, content %x->%x; want the same key and new content", before.Key.Text, with.Key.Text, before.Content, with.Content)
	}
	delete(w.impls, implKey{w.free, 0, w.s})
	after := *r1Inst(t, w.collectR1(t, m), "m.UseRelease[")
	if after.Content != before.Content {
		t.Fatalf("impl removed: content %x, want %x", after.Content, before.Content)
	}
	// HasFree's fold depends on it the same way.
	h := w.main("MainH", w.useHas, w.s, false)
	no := r1Inst(t, w.collectR1(t, h), "m.UseHas[").Content
	w.impls[implKey{w.free, 0, w.s}] = w.sFree
	if yes := r1Inst(t, w.collectR1(t, h), "m.UseHas[").Content; yes == no {
		t.Fatal("HasFree[S]: the content did not change when the impl was added")
	}
	// std.core's NeedsRelease shape: the folded constant is the instance's
	// result, and the two instances differ only by that immediate.
	needs := w.fn("Needs", 1, nil, types.Bool)
	nb := w.body(needs)
	nb.call(w.hasFree, []types.GenericArg{ty(w.p0)}, 0)
	nb.ret()
	mn := w.main("MainN", needs, w.s, false)
	with = *r1Inst(t, w.collectR1(t, mn), "m.Needs[")
	delete(w.impls, implKey{w.free, 0, w.s})
	without := *r1Inst(t, w.collectR1(t, mn), "m.Needs[")
	if with.Key != without.Key || with.Content == without.Content {
		t.Fatalf("Needs[S]: key %v->%v, content %x->%x; want the same key and new content", with.Key.Text, without.Key.Text, with.Content, without.Content)
	}
}

// gWorld is r1World with a generic container G[X] whose impl of Free is the
// generic method GFree[X] (the shape of std.core's `impl[T] Free for
// HeapList[T]`): ImplMethod binds the impl's generic to G's argument, as the
// driver's binds it through check's ImplArgs, and remembers what it bound.
type gWorld struct {
	*r1World
	gItem, gFree types.ItemID
	bound        map[types.TypeID][]types.GenericArg
}

func (w *gWorld) ImplMethod(i types.ItemID, m uint32, self types.TypeID) (types.ItemID, []types.GenericArg, bool) {
	if st := w.tab.Get(self); i == w.free && i != types.NoItem && m == 0 && st.Kind == types.KNamed && st.Item == w.gItem {
		args := []types.GenericArg{st.Args[0]}
		w.bound[self] = args
		return w.gFree, args, true
	}
	return w.r1World.ImplMethod(i, m, self)
}

// newGWorld adds G[X] and GFree[X](self: G[X], a: &Alloc) { e: X;
// UseRelease[X](e, a) }: freeing a G releases its element.
func newGWorld(t *testing.T) *gWorld {
	t.Helper()
	w := &gWorld{r1World: newR1World(t, true), bound: map[types.TypeID][]types.GenericArg{}}
	w.gItem = w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "G"), Name: w.in.Intern("G"),
		Kind: types.KindStruct, Decl: &types.StructDecl{}})
	w.gFree = w.fn("GFree", 1, []types.TypeID{w.g(w.p0), w.refAlloc}, types.Void)
	gb := w.body(w.gFree)
	e := gb.temp(w.p0)
	gb.call(w.useRelease, []types.GenericArg{ty(w.p0)}, gb.temp(types.Void), mir.Arg{Operand: mir.Move(gb.b.Place(e)), Mode: mir.ArgMove},
		arg(copyOf(gb.b, 2)))
	gb.ret()
	return w
}

func (w *gWorld) g(x types.TypeID) types.TypeID {
	return w.tab.Named(w.gItem, []types.GenericArg{ty(x)})
}

// collectG runs Collect with validation over the gWorld.
func (w *gWorld) collectG(t *testing.T, roots ...types.ItemID) *Instances {
	t.Helper()
	var rs []Root
	for _, r := range roots {
		rs = append(rs, mainRoot(r))
	}
	var in *Instances
	if ie := source.Guard("mono test", source.Span{}, func() { in, _ = Collect(w, rs, Options{Validate: true}) }); ie != nil {
		t.Fatalf("Collect: %s\n%s", ie, ie.Stack)
	}
	return in
}

// TestGenericImplRelease: Release through a generic impl's Free.
// UseRelease[G[G[S]]] resolves through GFree[G[S]], UseRelease[G[S]] and
// GFree[S] to S's SFree, and UseRelease[G[int]] through GFree[int] to
// Forget[int]; each rewritten call's generic arguments are the ones the impl
// lookup bound, and HasFree[G[int]] folds true (G's impl does not depend on
// its element).
func TestGenericImplRelease(t *testing.T) {
	w := newGWorld(t)
	ggs, gi := w.g(w.g(w.s)), w.g(types.Int)
	in := w.collectG(t, w.main("Main", w.useRelease, ggs, true), w.main("Main2", w.useRelease, gi, true),
		w.main("Main3", w.useHas, gi, false))
	gotG := map[types.TypeID]bool{}
	nUse := 0
	for _, i := range in.List {
		if i.Item == w.release || i.Item == w.hasFree {
			t.Fatalf("%s collected:\n%s", i.Name, summary(in))
		}
		if i.Body != nil {
			for _, c := range r1Calls(i.Body) {
				if c == w.release || c == w.hasFree {
					t.Fatalf("%s still calls %s", i.Name, w.items.Get(c).Key)
				}
			}
		}
		switch i.Item {
		case w.gFree:
			gotG[i.Args[0].Type] = true
		case w.useRelease:
			nUse++
			self := i.Args[0].Type
			c := releaseCall(t, i.Body)
			ga := i.Body.GenericArgs[c.Callee.Fn.Args.Start:c.Callee.Fn.Args.End()]
			switch {
			case self == w.s:
				if c.Callee.Fn.Item != w.sFree || len(ga) != 0 || c.Args.Len != 2 {
					t.Errorf("%s: Release became %s%v/%d, want SFree", i.Name, w.items.Get(c.Callee.Fn.Item).Key, ga, c.Args.Len)
				}
			case self == types.Int:
				if c.Callee.Fn.Item != w.forget || len(ga) != 1 || ga[0].Type != types.Int || c.Args.Len != 1 {
					t.Errorf("%s: Release became %s%v/%d, want Forget[int]", i.Name, w.items.Get(c.Callee.Fn.Item).Key, ga, c.Args.Len)
				}
			default:
				want, ok := w.bound[self]
				if !ok || c.Callee.Fn.Item != w.gFree || c.Args.Len != 2 || len(ga) != len(want) || ga[0] != want[0] {
					t.Errorf("%s: Release became %s%v/%d, want GFree%v", i.Name, w.items.Get(c.Callee.Fn.Item).Key, ga, c.Args.Len, want)
				}
			}
		}
	}
	for _, x := range []types.TypeID{w.g(w.s), w.s, types.Int} {
		if !gotG[x] {
			t.Errorf("GFree[%s] not collected:\n%s", w.tab.TypeString(x, nil), summary(in))
		}
	}
	if nUse != 5 { // G[G[S]], G[S], S, G[int], int
		t.Errorf("%d UseRelease instances, want 5:\n%s", nUse, summary(in))
	}
	w.inst(t, in, "m.SFree")
	if !hasInst(in, "m.Yes") || hasInst(in, "m.No") {
		t.Errorf("HasFree[G[int]] is not true:\n%s", summary(in))
	}
}

// TestICEContainerNamesCheckGap: a container with an impl of Free over a
// linear element with none passes check's release rules and reaches a live
// Release of the element. That stays an ICE (mono has no instance-time
// E0701, and the cure is check's), but the message does not say check rules
// it out: it names the instance, the type, E0701 and that reaching it is a
// check bug or a direct Release.
func TestICEContainerNamesCheckGap(t *testing.T) {
	w := newGWorld(t)
	lin := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "L"), Name: w.in.Intern("L"),
		Kind: types.KindStruct, Decl: &types.StructDecl{}})
	l := w.tab.Named(lin, nil)
	w.linear[l] = true
	m := w.main("Main", w.useRelease, w.g(l), true)
	ie := source.Guard("mono test", source.Span{}, func() { Collect(w, []Root{mainRoot(m)}, Options{Validate: true}) })
	if ie == nil {
		t.Fatal("no ICE for Release of a linear element with no impl of Free")
	}
	for _, want := range []string{"E0701", "check bug", "direct std.raw.Release", "m.UseRelease[L]", "linear"} {
		if !strings.Contains(ie.What, want) {
			t.Errorf("ICE %q lacks %q", ie.What, want)
		}
	}
	if strings.Contains(ie.What, "rule this out") {
		t.Errorf("ICE %q still says check rules this out", ie.What)
	}
}
