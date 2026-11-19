// Why: a fn value may convert to a fn type whose declared flows cover its own,
// and the validator must not refuse what check accepted.
// What: TestFnCoverM4M5 over fnCover (Good in
// tests/conformance/model/X.FnCover.mk).
// How: invariants M4 and M5 accept a fn value whose flows the target fn type
// covers, and still refuse one whose flows it does not.

package mir

import (
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// fnCover builds X.FnCover's Good with &i32 for &Holder:
//
//	ReadOut(x: &i32): &i32 <value flows>
//	Apply(f: fn(&i32): &i32 <target flows>, h: &i32)
//	Good(h: &i32):
//	  bb0: _2 = fn ReadOut; call Apply(copy _2, copy _1) -> _3, bb1
//	  bb1: _4 = copy _2; return normal
//
// The temp _2 has ReadOut's own fn type, or the parameter's when asParam is
// set (so RvFnRef carries the conversion, M4); _4 has the parameter's type
// (StAssign, M4).
func fnCover(value, declared []types.Source, asParam bool) (*Body, *Env, *types.Table) {
	in := source.NewInterner()
	items := types.NewItems()
	tab := types.NewTable(in, items, types.NewConstTable())
	m := items.AddModule(&types.Module{Path: "main"})
	add := func(name string) types.ItemID {
		return items.AddItem(&types.Item{Module: m, Name: in.Intern(name), Kind: types.KindFunc, Key: types.ItemKey("main", name)})
	}
	readOut, apply, good := add("ReadOut"), add("Apply"), add("Good")
	ri := tab.Ref(types.I32)
	target := tab.Fn(types.FnType{Params: []types.FnParam{{Type: ri}}, Result: ri, Sources: declared})
	sigs := map[types.ItemID]*types.Signature{
		readOut: {Item: readOut, Params: []types.Param{{Name: in.Intern("x"), Type: ri}}, Result: ri,
			Flows: types.Flows{Sources: value, Declared: len(value) > 0}},
		apply: {Item: apply, Params: []types.Param{{Name: in.Intern("f"), Type: target}, {Name: in.Intern("h"), Type: ri}}, Result: types.Void},
		good:  {Item: good, Params: []types.Param{{Name: in.Intern("h"), Type: ri}}, Result: types.Void},
	}
	tempType := sigs[readOut].FnType(tab)
	if asParam {
		tempType = target
	}
	b := NewBody(good, source.Span{File: 1, Start: 0, End: 10}, types.Void)
	sp := b.AddSpan(source.Span{File: 1, Start: 2, End: 8})
	h := b.AddParam(ri, in.Intern("h"), sp)
	f := b.AddLocal(Local{Type: tempType, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	d := b.AddLocal(Local{Type: types.Void, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	g := b.AddLocal(Local{Type: target, Kind: LocalUser, Name: in.Intern("g"), Loop: NoLoop, Span: sp})
	b0, b1 := b.AddBlock(OriginBody, NoDefer), b.AddBlock(OriginBody, NoDefer)
	b.Blocks[b0].Stmts = []Statement{{Kind: StAssign, Place: LocalPlace(f), Span: sp,
		Rvalue: Rvalue{Kind: RvFnRef, Fn: FnRef{Item: readOut}}}}
	b.Blocks[b0].Term = Terminator{Kind: TermCall, Span: sp, Callee: Callee{Kind: CallDirect, Fn: FnRef{Item: apply}},
		Args: b.AddArgs(Arg{Operand: Copy(LocalPlace(f)), Mode: ArgCopy}, Arg{Operand: Copy(LocalPlace(h)), Mode: ArgCopy}),
		Dest: LocalPlace(d), Target: b1, Spawn: NoLocal}
	b.Blocks[b1].Stmts = []Statement{{Kind: StAssign, Place: LocalPlace(g), Span: sp,
		Rvalue: Rvalue{Kind: RvUse, A: Copy(LocalPlace(f))}}}
	b.Blocks[b1].Term = Terminator{Kind: TermReturn, Exit: ExitNormal, Span: sp}
	env := &Env{Types: tab, Sig: sigs[good], Callee: func(id types.ItemID) *types.Signature { return sigs[id] }}
	return b, env, tab
}

// TestFnCoverM4M5: X.FnCover's call validates, through the
// argument (M5), the fn reference (M4) and a plain assignment (M4); a flow
// the target does not declare, or B(p) given for H(p), is still refused.
func TestFnCoverM4M5(t *testing.T) {
	h1 := []types.Source{{Param: 1}}
	b1 := []types.Source{{Param: 1, Behind: true}}
	for _, c := range []struct {
		name          string
		value, target []types.Source
		asParam       bool
		inv           string // "" accepts
		msg           string
	}{
		{"no flow for from h (X.FnCover), argument", nil, h1, false, "", ""},
		{"no flow for from h, fn reference", nil, h1, true, "", ""},
		{"B(1) for H(1), argument", b1, h1, false, "", ""},
		{"B(1) for H(1), fn reference", b1, h1, true, "", ""},
		{"same flows", h1, h1, false, "", ""},
		{"H(1) for B(1), argument", h1, b1, false, "M5", "argument 0 is fn(&i32): &i32 [from $1], parameter is fn(&i32): &i32 [B($1)]"},
		{"H(1) for B(1), fn reference", h1, b1, true, "M4", "fn reference of type fn(&i32): &i32 [from $1] into fn(&i32): &i32 [B($1)]"},
		{"from x for none (Id), argument", h1, nil, false, "M5", "argument 0 is fn(&i32): &i32 [from $1], parameter is fn(&i32): &i32"},
	} {
		b, env, _ := fnCover(c.value, c.target, c.asParam)
		err := b.Validate(env)
		switch {
		case c.inv == "" && err != nil:
			t.Errorf("%s: rejected: %v", c.name, err)
		case c.inv != "" && (err == nil || !strings.Contains(err.Error(), c.inv+":") || !strings.Contains(err.Error(), c.msg)):
			t.Errorf("%s: want %s %q, got %v", c.name, c.inv, c.msg, err)
		}
	}
	// The assignment _4 = copy _2 alone: an uncovered flow is M4.
	b, env, tab := fnCover(h1, b1, false)
	b.Blocks[0].Term.Args = b.AddArgs(Arg{Operand: Copy(LocalPlace(4)), Mode: ArgCopy}, Arg{Operand: Copy(LocalPlace(1)), Mode: ArgCopy})
	if err := b.Validate(env); err == nil || !strings.Contains(err.Error(), "M4: bb1[0]: assigns fn(&i32): &i32 [from $1]") {
		t.Errorf("uncovered assignment: %v", err)
	}
	// Only the top level is relaxed: some(fn) into ?fn of other flows is
	// a payload (covered), but ?fn into ?fn of other flows is not.
	b, env, tab = fnCover(nil, h1, false)
	o := b.AddLocal(Local{Type: tab.Optional(b.Locals[4].Type), Kind: LocalTemp, Loop: NoLoop})
	b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: LocalPlace(o),
		Rvalue: Rvalue{Kind: RvAggregate, Agg: AggSome, Ops: b.AddOperands(Copy(LocalPlace(2)))}})
	if err := b.Validate(env); err != nil {
		t.Errorf("some(fn) with covered flows rejected: %v", err)
	}
	p := b.AddLocal(Local{Type: tab.Optional(b.Locals[2].Type), Kind: LocalTemp, Loop: NoLoop})
	b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: LocalPlace(o),
		Rvalue: Rvalue{Kind: RvUse, A: Copy(LocalPlace(p))}})
	if err := b.Validate(env); err == nil || !strings.Contains(err.Error(), "M4:") {
		t.Errorf("?fn into ?fn of other flows: %v", err)
	}
}
