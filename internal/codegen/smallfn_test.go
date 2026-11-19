// Why: a release build inlines small loop-free functions wherever they are
// called, across packs too, and only where the rule allows.
// What: tests of the small-function rule (smallfn.go): a small loop-free
// function's definition carries alwaysinline in its own pack and in
// whatever pack calls it; each exclusion, cycle and never keeps the mark
// off; and the cross-pack sole-call cases.
// How: smallLeaf and twiceCaller build callees and callers; smallMarked
// reads the mark from the definition line.

package codegen

import (
	"bytes"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// defLine is the define line of symbol in ll ("" when ll defines none). It
// reads the define lines, not fnText's first mention, which is a call when
// a caller is emitted before the callee.
func defLine(ll, symbol string) string {
	for _, line := range strings.Split(ll, "\n") {
		if strings.HasPrefix(line, "define ") && (strings.Contains(line, "@"+symbol+"(") || strings.Contains(line, "@\""+symbol+"\"(")) {
			return line
		}
	}
	return ""
}

// smallMarked reports whether ll's definition of mk.main.<name> carries
// alwaysinline; it fails the test when ll does not define it.
func smallMarked(t *testing.T, ll, name string) bool {
	t.Helper()
	line := defLine(ll, "mk.main."+name)
	if !strings.HasPrefix(line, "define ") {
		t.Fatalf("no definition of %s:\n%s", name, ll)
	}
	return strings.Contains(line, " alwaysinline")
}

// emitPackN is pack p of u as emit gives pack 0.
func emitPackN(t *testing.T, e *testEnv, u *Unit, p int, target types.Target, profile types.Profile) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := EmitPack(u, p, e, Config{Target: target, Profile: profile}, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// smallLeaf is Name(n: int): int with k statements (bodyOps k + 1).
func smallLeaf(e *testEnv, name string, k int) (types.ItemID, *fnb) {
	return soleLeaf(e, name, k, false)
}

// twiceCaller is Name(n: int): int calling each callee twice, so no call
// is the only one of its callee (the sole-call rule stays out).
func twiceCaller(e *testEnv, name string, callees ...types.ItemID) *fnb {
	f := e.body(e.fn(name, []types.TypeID{types.Int}, types.Int, 0))
	for _, c := range callees {
		for range 2 {
			v := f.call(c, nil, types.Int, mir.Copy(place(1)))
			f.assign(place(0), use(mir.Copy(place(v))), f.at(6, 3))
		}
	}
	f.ret(f.at(7, 3))
	return f
}

// TestSmallFunctionMarked: a leaf of smallOps operations called
// twice is marked on its definition, and both calls repeat the mark; one
// operation more, a loop, or a dev build leaves it unmarked; and the IR
// compiles.
func TestSmallFunctionMarked(t *testing.T) {
	for _, c := range []struct {
		name    string
		k       int // statements: bodyOps is k + 1
		loop    bool
		profile types.Profile
		want    bool
	}{
		{"at-bound", smallOps - 1, false, types.Release, true},
		{"over-bound", smallOps, false, types.Release, false},
		{"loop", 1, true, types.Release, false},
		{"dev", 1, false, types.Dev, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv()
			leaf, l := soleLeaf(e, "Leaf", c.k, c.loop)
			f := twiceCaller(e, "Caller", leaf)
			ll := emit(t, e, unitOf(e, []*mir.Body{l.b, f.b}), types.X86_64Linux, c.profile)
			if got := smallMarked(t, ll, "Leaf"); got != c.want {
				t.Errorf("Leaf of %d ops (loop %v, %v): marked %v, want %v", c.k+1, c.loop, c.profile, got, c.want)
			}
			calls := soleCallLines(ll, "mk.main.Caller", "mk.main.Leaf")
			if len(calls) != 2 {
				t.Fatalf("Caller: %d calls of Leaf, want 2", len(calls))
			}
			for _, cl := range calls {
				if strings.Contains(cl, "alwaysinline") != c.want {
					t.Errorf("a call of Leaf: alwaysinline %v, want %v: %s", !c.want, c.want, cl)
				}
			}
			if c.want {
				clangC(t, types.X86_64Linux, t.TempDir(), "small", ll, "-O2")
			}
		})
	}
}

// TestSmallEffectCountsInlinedCallees: the effect adds each small
// callee once per call, and a sole callee whatever its size. Two calls of
// a 16-operation leaf make a 32-operation caller plus its own: not small,
// while the leaf is. A thin wrapper whose one call is the only call of a
// large function (which the sole-call rule inlines into it) is not small.
// A wrapper of a large function called twice is: its call stays a call.
func TestSmallEffectCountsInlinedCallees(t *testing.T) {
	e := newTestEnv()
	half, h := smallLeaf(e, "Half", smallOps/2-1) // bodyOps smallOps/2
	mid := twiceCaller(e, "Mid", half)            // Mid calls Half twice
	top := e.body(e.fn("Top", []types.TypeID{types.Int}, types.Int, 0))
	v := top.call(mid.item, nil, types.Int, mir.Copy(place(1)))
	top.assign(place(0), use(mir.Copy(place(v))), top.at(8, 3))
	top.ret(top.at(8, 3))
	top2 := twiceCaller(e, "Top2", mid.item)
	big, b := smallLeaf(e, "Big", 200)
	wrap := soleCaller(e, "Wrap", big, false, 0) // the only call of Big
	big2, b2 := smallLeaf(e, "Big2", 200)
	wrap2 := soleCaller(e, "Wrap2", big2, false, 0)
	other := soleCaller(e, "Other", big2, false, 0) // a second call of Big2
	useWrap := twiceCaller(e, "UseWrap", wrap.item, wrap2.item)
	u := unitOf(e, []*mir.Body{h.b, mid.b, top.b, top2.b, b.b, wrap.b, b2.b, wrap2.b, other.b, useWrap.b})
	ll := emit(t, e, u, types.X86_64Linux, types.Release)
	for _, c := range []struct {
		name string
		want bool
	}{{"Half", true}, {"Mid", false}, {"Big", false}, {"Wrap", false}, {"Big2", false}, {"Wrap2", true}} {
		if got := smallMarked(t, ll, c.name); got != c.want {
			t.Errorf("%s: marked %v, want %v", c.name, got, c.want)
		}
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "effect", ll, "-O2")
}

// TestSmallExclusions: each eligibility condition alone keeps a
// two-operation leaf unmarked; the same unit without it marks it.
func TestSmallExclusions(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(e *testEnv, u *Unit)
		want bool
	}{
		{name: "plain", want: true},
		{name: "addr-taken", edit: func(e *testEnv, u *Unit) { u.Funcs[0].AddrTaken = true }, want: true},
		{name: "spawned", edit: func(e *testEnv, u *Unit) { u.Funcs[0].Spawned = true }, want: false},
		{name: "naked", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Decl.(*types.FuncDecl).Naked = true
		}, want: false},
		{name: "cpu", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Decl.(*types.FuncDecl).Sig.CPU = []string{"avx", "avx2"}
		}, want: false},
		{name: "section", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Decl.(*types.FuncDecl).Section = ".text.mk"
		}, want: false},
		{name: "panic-hook", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Key = types.ItemKey("std.rt", hookPanicMsg)
		}, want: false},
		// An asm body copied twice into one caller could define an asm
		// label twice.
		{name: "asm", edit: func(e *testEnv, u *Unit) {
			b := u.Funcs[0].Body
			id := b.AddAsm(mir.Asm{Template: "nop", Options: mir.AsmNomem | mir.AsmNostack, Span: b.Blocks[0].Term.Span})
			b.Blocks[0].Stmts = append(b.Blocks[0].Stmts, mir.Statement{Kind: mir.StAsm, Asm: id, Span: b.Blocks[0].Term.Span})
		}, want: false},
		// A std.libc body carries the C library's no-builtins attribute.
		{name: "libc", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Key = types.ItemKey("std.libc", "Leaf")
		}, want: false},
		// An intrinsic with a body (the support pack's, supportBodies) is
		// called as a function, but its body is codegen's: no mark. The
		// key alone does not exclude it.
		{name: "support-body-key", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Key = types.ItemKey("std.rt", "ErrorName")
		}, want: true},
		{name: "intrinsic", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Key = types.ItemKey("std.rt", "ErrorName")
			e.items.Get(u.Funcs[0].Item).Decl.(*types.FuncDecl).Intrinsic = true
		}, want: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnv()
			leaf, l := smallLeaf(e, "Leaf", 1)
			f := twiceCaller(e, "Caller", leaf)
			u := unitOf(e, []*mir.Body{l.b, f.b})
			if c.edit != nil {
				c.edit(e, u)
			}
			ll := emit(t, e, u, types.X86_64Linux, types.Release)
			if got := smallMarked(t, ll, "Leaf"); got != c.want {
				t.Errorf("marked %v, want %v", got, c.want)
			}
		})
	}
}

// TestSmallCyclesAndNever: a function calling itself, two functions
// calling each other, and a function returning never are not small, while
// a small function beside them is.
func TestSmallCyclesAndNever(t *testing.T) {
	e := newTestEnv()
	rec := e.fn("Rec", []types.TypeID{types.Int}, types.Int, 0)
	r := e.body(rec)
	x := r.call(rec, nil, types.Int, mir.Copy(place(1)))
	r.assign(place(0), use(mir.Copy(place(x))), r.at(2, 3))
	r.ret(r.at(2, 3))
	ping := e.fn("Ping", []types.TypeID{types.Int}, types.Int, 0)
	pong := e.fn("Pong", []types.TypeID{types.Int}, types.Int, 0)
	pi := e.body(ping)
	y := pi.call(pong, nil, types.Int, mir.Copy(place(1)))
	pi.assign(place(0), use(mir.Copy(place(y))), pi.at(3, 3))
	pi.ret(pi.at(3, 3))
	po := e.body(pong)
	z := po.call(ping, nil, types.Int, mir.Copy(place(1)))
	po.assign(place(0), use(mir.Copy(place(z))), po.at(4, 3))
	po.ret(po.at(4, 3))
	nv := e.body(e.fn("Stop", []types.TypeID{types.Int}, types.Never, 0))
	nv.term(mir.Terminator{Kind: mir.TermUnreachable, Span: nv.at(5, 3)})
	leaf, l := smallLeaf(e, "Leaf", 1)
	f := twiceCaller(e, "Caller", leaf, ping, rec)
	ll := emit(t, e, unitOf(e, []*mir.Body{r.b, pi.b, po.b, nv.b, l.b, f.b}), types.X86_64Linux, types.Release)
	for _, c := range []struct {
		name string
		want bool
	}{{"Rec", false}, {"Ping", false}, {"Pong", false}, {"Stop", false}, {"Leaf", true}} {
		if got := smallMarked(t, ll, c.name); got != c.want {
			t.Errorf("%s: marked %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSmallAcrossPacks: the mark is on the callee's definition in its
// own pack, though only another pack calls it; the caller's pack declares
// the callee without the mark (ThinLTO imports the definition), and a
// caller whose effect counts a small callee of another pack is judged with
// that callee's operations.
func TestSmallAcrossPacks(t *testing.T) {
	e := newTestEnv()
	leaf, l := smallLeaf(e, "Leaf", smallOps-2) // bodyOps smallOps - 1
	mid := e.body(e.fn("Mid", []types.TypeID{types.Int}, types.Int, 0))
	v := mid.call(leaf, nil, types.Int, mir.Copy(place(1)))
	mid.assign(place(0), use(mir.Copy(place(v))), mid.at(4, 3))
	mid.ret(mid.at(4, 3))
	f := twiceCaller(e, "Caller", leaf, mid.item)
	u := unitOf(e, []*mir.Body{l.b, mid.b, f.b})
	u.Packs = [][]int{{0}, {1, 2}}
	u.Funcs[1].Pack, u.Funcs[2].Pack = 1, 1
	p0 := emitPackN(t, e, u, 0, types.X86_64Linux, types.Release)
	if !smallMarked(t, p0, "Leaf") {
		t.Errorf("Leaf's definition in its pack is not marked:\n%s", defLine(p0, "mk.main.Leaf"))
	}
	p1 := emitPackN(t, e, u, 1, types.X86_64Linux, types.Release)
	if smallMarked(t, p1, "Mid") {
		t.Errorf("Mid (3 ops plus Leaf's %d, over %d) is marked", smallOps-1, smallOps)
	}
	for _, l := range strings.Split(p1, "\n") {
		if strings.HasPrefix(l, "declare ") && strings.Contains(l, "@mk.main.Leaf(") && strings.Contains(l, "alwaysinline") {
			t.Errorf("the declaration of Leaf in the caller's pack carries the mark: %s", l)
		}
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "p0", p0, "-O2")
	clangC(t, types.X86_64Linux, t.TempDir(), "p1", p1, "-O2")
}

// crossUnit is Leaf (k statements, a loop when loop is set) in pack 0 and
// Caller in pack 1 calling it once (inside a loop when callerLoop is set,
// after extra empty blocks), with edit applied before emission; it returns
// pack 0's IR.
func crossUnit(t *testing.T, k int, loop, callerLoop bool, extra int, edit func(e *testEnv, u *Unit)) string {
	t.Helper()
	e := newTestEnv()
	leaf, l := soleLeaf(e, "Leaf", k, loop)
	f := soleCaller(e, "Caller", leaf, callerLoop, extra)
	u := unitOf(e, []*mir.Body{l.b, f.b})
	u.Packs = [][]int{{0}, {1}}
	u.Funcs[1].Pack = 1
	if edit != nil {
		edit(e, u)
	}
	return emitPackN(t, e, u, 0, types.X86_64Linux, types.Release)
}

// addFar adds Far, in a third pack, calling u's Funcs[1] (Caller) once, so
// Caller is one that another pack calls (soleCalls' outside bit).
func addFar(e *testEnv, u *Unit) {
	f := e.body(e.fn("Far", []types.TypeID{types.Int}, types.Int, 0))
	v := f.call(u.Funcs[1].Item, nil, types.Int, mir.Copy(place(1)))
	f.assign(place(0), use(mir.Copy(place(v))), f.at(9, 3))
	f.ret(f.at(9, 3))
	u.Funcs = append(u.Funcs, Func{Symbol: "mk.main.Far", Key: "main.Far", Item: f.item, Body: f.b, Pack: 2})
	u.Packs = append(u.Packs, []int{len(u.Funcs) - 1})
}

// TestCrossPackSole: a large leaf whose one call is in another pack
// gets the mark on its definition; each of soleCallAttr's conditions, seen
// from the callee, alone keeps it off: a second call, AddrTaken, a cpu
// clause the caller lacks, an effect past soleCallOps, a looping callee
// into a looping caller or one of more than thinCallerBlocks blocks, a
// caller that a third pack calls (unless it has a cpu clause), a section,
// a std.libc callee in a caller outside std.libc, and a dev build. Each
// bound has its twin at the bound, marked.
func TestCrossPackSole(t *testing.T) {
	big := 200 // past smallOps: only the cross-pack rule can mark it
	libc := func(e *testEnv, u *Unit, i int, name string) {
		e.items.Get(u.Funcs[i].Item).Key = types.ItemKey("std.libc", name)
	}
	for _, c := range []struct {
		name             string
		k                int // Leaf's statements (bodyOps k + 1); 0 is big
		loop, callerLoop bool
		extra            int // Caller's extra empty blocks (it has extra + 2)
		edit             func(e *testEnv, u *Unit)
		want             bool
	}{
		{name: "plain", want: true},
		{name: "at-soleCallOps", k: soleCallOps - 1, want: true},
		{name: "over-soleCallOps", k: soleCallOps, want: false},
		{name: "loop-into-thin", loop: true, want: true},
		{name: "loop-into-thin-at-bound", loop: true, extra: thinCallerBlocks - 2, want: true},
		{name: "loop-into-five-blocks", loop: true, extra: thinCallerBlocks - 1, want: false},
		{name: "loop-free-into-five-blocks", extra: thinCallerBlocks - 1, want: true},
		{name: "loop-into-loop", loop: true, callerLoop: true, want: false},
		{name: "loop-free-into-loop", callerLoop: true, want: true},
		{name: "libc-callee", edit: func(e *testEnv, u *Unit) { libc(e, u, 0, "Leaf") }, want: false},
		{name: "libc-into-libc", edit: func(e *testEnv, u *Unit) { libc(e, u, 0, "Leaf"); libc(e, u, 1, "Caller") }, want: true},
		{name: "libc-caller", edit: func(e *testEnv, u *Unit) { libc(e, u, 1, "Caller") }, want: true},
		{name: "addr-taken", edit: func(e *testEnv, u *Unit) { u.Funcs[0].AddrTaken = true }, want: false},
		{name: "spawned", edit: func(e *testEnv, u *Unit) { u.Funcs[0].Spawned = true }, want: false},
		{name: "cpu", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Decl.(*types.FuncDecl).Sig.CPU = []string{"avx", "avx2"}
		}, want: false},
		{name: "section", edit: func(e *testEnv, u *Unit) {
			e.items.Get(u.Funcs[0].Item).Decl.(*types.FuncDecl).Section = ".text.mk"
		}, want: false},
		// A runtime entry codegen calls by itself (codegenCalled): its one
		// MIR call is not its only call.
		{name: "runtime-entry", edit: func(e *testEnv, u *Unit) { e.items.Get(u.Funcs[0].Item).Key = keyJoin }, want: false},
		{name: "second-call", edit: func(e *testEnv, u *Unit) {
			f := e.body(e.fn("Again", []types.TypeID{types.Int}, types.Int, 0))
			v := f.call(u.Funcs[0].Item, nil, types.Int, mir.Copy(place(1)))
			f.assign(place(0), use(mir.Copy(place(v))), f.at(9, 3))
			f.ret(f.at(9, 3))
			u.Funcs = append(u.Funcs, Func{Symbol: "mk.main.Again", Key: "main.Again", Item: f.item, Body: f.b, Pack: 1})
			u.Packs[1] = append(u.Packs[1], len(u.Funcs)-1)
		}, want: false},
		{name: "caller-called-from-a-third-pack", edit: addFar, want: false},
		// A caller with a cpu clause is never inlined into a baseline
		// caller, so another pack's call of it does not keep the mark off.
		{name: "caller-with-cpu-called-from-a-third-pack", edit: func(e *testEnv, u *Unit) {
			addFar(e, u)
			e.items.Get(u.Funcs[1].Item).Decl.(*types.FuncDecl).Sig.CPU = []string{"avx", "avx2"}
		}, want: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			k := c.k
			if k == 0 {
				k = big
			}
			ll := crossUnit(t, k, c.loop, c.callerLoop, c.extra, func(e *testEnv, u *Unit) {
				if c.extra > 0 && len(u.Funcs[1].Body.Blocks) != c.extra+2 {
					t.Fatalf("Caller has %d blocks, want %d", len(u.Funcs[1].Body.Blocks), c.extra+2)
				}
				if c.edit != nil {
					c.edit(e, u)
				}
			})
			if got := smallMarked(t, ll, "Leaf"); got != c.want {
				t.Errorf("marked %v, want %v", got, c.want)
			}
		})
	}
	// Dev never marks.
	e := newTestEnv()
	leaf, l := soleLeaf(e, "Leaf", big, false)
	f := soleCaller(e, "Caller", leaf, false, 0)
	u := unitOf(e, []*mir.Body{l.b, f.b})
	u.Packs = [][]int{{0}, {1}}
	u.Funcs[1].Pack = 1
	if smallMarked(t, emitPackN(t, e, u, 0, types.X86_64Linux, types.Dev), "Leaf") {
		t.Error("a dev build marks Leaf")
	}
}

// TestCrossPackSoleNever: a function returning never whose one call
// is in another pack gets no mark: a panic path stays one cold call.
func TestCrossPackSoleNever(t *testing.T) {
	e := newTestEnv()
	stop := e.fn("Stop", []types.TypeID{types.Int}, types.Never, 0)
	nv := e.body(stop)
	for range 40 {
		nv.assign(place(1), use(mir.Copy(place(1))), nv.at(5, 3))
	}
	nv.term(mir.Terminator{Kind: mir.TermUnreachable, Span: nv.at(5, 3)})
	f := e.body(e.fn("Caller", []types.TypeID{types.Int}, types.Int, 0))
	f.call(stop, nil, types.Never, mir.Copy(place(1)))
	f.term(mir.Terminator{Kind: mir.TermUnreachable, Span: f.at(6, 3)})
	u := unitOf(e, []*mir.Body{nv.b, f.b})
	u.Packs = [][]int{{0}, {1}}
	u.Funcs[1].Pack = 1
	if smallMarked(t, emitPackN(t, e, u, 0, types.X86_64Linux, types.Release), "Stop") {
		t.Error("Stop, returning never, is marked")
	}
}

// TestCrossPackSoleLoopReachesCaller (the wrapper-of-a-loop case of
// solecall_test.go, across packs): Main's loop calls Step once, in Main's pack;
// Step's one call is of Advance, which loops, in another pack. Advance gets
// the cross mark (Step is thin), so Step carries Advance's loop, and Main's
// call of Step must stay unmarked: otherwise Step would go into Main's loop
// and Advance, imported into it, with it.
func TestCrossPackSoleLoopReachesCaller(t *testing.T) {
	e := newTestEnv()
	adv, a := soleLeaf(e, "Advance", 1, true)
	s := soleCaller(e, "Step", adv, false, 0)
	m := soleCaller(e, "Main", s.item, true, 0)
	u := unitOf(e, []*mir.Body{a.b, s.b, m.b})
	u.Packs = [][]int{{1, 2}, {0}}
	u.Funcs[0].Pack = 1
	p0 := emitPackN(t, e, u, 0, types.X86_64Linux, types.Release)
	if soleMarked(t, p0, "Main", "Step") {
		t.Errorf("Main's loop has Step, which carries Advance's loop from another pack, marked alwaysinline")
	}
	if smallMarked(t, p0, "Step") {
		t.Errorf("Step, which carries a loop, is marked on its definition")
	}
	p1 := emitPackN(t, e, u, 1, types.X86_64Linux, types.Release)
	if !smallMarked(t, p1, "Advance") {
		t.Errorf("Advance, called once from the thin Step in another pack, is not marked")
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "p0", p0, "-O2")
	clangC(t, types.X86_64Linux, t.TempDir(), "p1", p1, "-O2")
}

// TestConstantTimeModulesKeptOut: a function of std.crypto or
// std.simd.kernel, and every function it reaches by direct calls (here
// Leaf, Leaf3 through Mid, and LeafK from the kernel's K), gets neither
// mark, so the constant-time tests' readings hold; Leaf2, which only a
// function outside them calls, is still small. The cross-pack shapes are
// TestConstantTimeAcrossPacks.
func TestConstantTimeModulesKeptOut(t *testing.T) {
	e := newTestEnv()
	leaf, l := smallLeaf(e, "Leaf", 1)
	leaf2, l2 := smallLeaf(e, "Leaf2", 1)
	leaf3, l3 := smallLeaf(e, "Leaf3", 1)
	leafK, lk := smallLeaf(e, "LeafK", 1)
	mid := twiceCaller(e, "Mid", leaf3)
	ct := twiceCaller(e, "Ct", leaf, mid.item)
	e.items.Get(ct.item).Key = types.ItemKey("std.crypto", "Ct")
	k := twiceCaller(e, "K", leafK)
	e.items.Get(k.item).Key = types.ItemKey("std.simd.kernel", "K")
	other := twiceCaller(e, "Other", leaf2)
	u := unitOf(e, []*mir.Body{l.b, l2.b, l3.b, lk.b, mid.b, ct.b, k.b, other.b})
	ll := emit(t, e, u, types.X86_64Linux, types.Release)
	for _, c := range []struct {
		name string
		want bool
	}{{"Leaf", false}, {"Leaf3", false}, {"Mid", false}, {"LeafK", false}, {"Leaf2", true}} {
		if got := smallMarked(t, ll, c.name); got != c.want {
			t.Errorf("%s: marked %v, want %v", c.name, got, c.want)
		}
	}
	if line := defLine(ll, "mk.std.crypto.Ct"); !strings.HasPrefix(line, "define ") || strings.Contains(line, " alwaysinline") {
		t.Errorf("std.crypto's Ct: %q, want a definition without the mark", line)
	}
}
