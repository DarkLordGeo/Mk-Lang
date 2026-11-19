// Why: a capturing fn literal bound to a const parameter is called directly
// in the instance, so its environment has to travel as a hidden parameter.
// What: tests of the hidden parameter, forwarding to further const
// parameters, two capturing arguments, shared instances, the Slice adapter,
// forwarding through a slot, and non-generic forwarders as roots.
// How: hand-built MIR in the shape lower emits; two sites with equal literal
// and environment type must share one instance. capture_source_test.go runs the same
// cases on MIR lowered from source.

package mono

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// capWorld is a world with the environment record of one capture of type
// elem named origin.
type capWorld struct {
	*world
	fnT    types.TypeID // fn(&i32): bool
	rec    types.TypeID // (origin: &elem)
	envRef types.TypeID // &(origin: &elem)
}

func newCapWorld(elem types.TypeID) *capWorld {
	w := newWorld()
	cw := &capWorld{world: w}
	cw.fnT = w.tab.Fn(types.FnType{Params: []types.FnParam{{Type: w.tab.Ref(types.I32)}}, Result: types.Bool})
	cw.rec = w.tab.Record([]types.RecordField{{Name: w.in.Intern("origin"), Type: w.tab.Ref(elem)}})
	cw.envRef = w.tab.Ref(cw.rec)
	return cw
}

// envFor makes the environment type of one capture of type elem.
func (cw *capWorld) envFor(elem types.TypeID) (rec, ref types.TypeID) {
	rec = cw.tab.Record([]types.RecordField{{Name: cw.in.Intern("origin"), Type: cw.tab.Ref(elem)}})
	return rec, cw.tab.Ref(rec)
}

// capLiteral declares literal n of owner: fn(a: &i32): bool with a hidden
// environment parameter of type envRef whose body reads its one capture.
func (cw *capWorld) capLiteral(owner types.ItemID, n int, rec, envRef types.TypeID) types.ItemID {
	lit := cw.literal(owner, n, []types.TypeID{cw.tab.Ref(types.I32)}, types.Bool)
	lb := cw.body(lit)
	env := lb.b.AddParam(envRef, cw.in.Intern("env"), 0)
	elem := cw.tab.Get(cw.tab.Get(rec).Fields[0].Type).Elem
	v := lb.temp(elem)
	p := lb.b.Place(env, mir.Projection{Kind: mir.ProjDeref, Type: rec},
		mir.Projection{Kind: mir.ProjField, A: 0, Type: cw.tab.Ref(elem)},
		mir.Projection{Kind: mir.ProjDeref, Type: elem})
	lb.assign(v, mir.Rvalue{Kind: mir.RvUse, A: mir.Copy(p)})
	lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
	lb.ret()
	return lit
}

// fnConst is the fn constant naming item.
func (cw *capWorld) fnConst(item types.ItemID) mir.Operand {
	c := cw.consts.Intern(types.ConstValue{Type: cw.fnT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: item}}}})
	return mir.Const(cw.fnT, c)
}

// buildEnv emits lower's pass 3 for capturing local or parameter src:
// t := &src; e := (origin: copy t); r := &e; it returns copy r.
func (cw *capWorld) buildEnv(f *fb, src mir.LocalID, rec, envRef types.TypeID) mir.Arg {
	ft := cw.tab.Get(rec).Fields[0].Type
	t := f.temp(ft)
	f.assign(t, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: f.b.Place(src), Type: ft})
	e := f.temp(rec)
	f.assign(e, mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggRecord, Type: rec, Ops: f.b.AddOperands(copyOf(f.b, t))})
	r := f.temp(envRef)
	f.assign(r, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: f.b.Place(e), Type: envRef})
	return arg(copyOf(f.b, r))
}

// sortChain declares Inner(x: &i32, const less): bool, which calls less(x),
// and Outer(x: &i32, const less): bool, which forwards less to Inner.
func (cw *capWorld) sortChain() (outer, inner types.ItemID) {
	inner = cw.fnWith("Inner", 0, []types.TypeID{cw.tab.Ref(types.I32), cw.fnT}, []bool{false, true}, types.Bool)
	ib := cw.body(inner)
	ib.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(ib.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(ib.b, 1)))
	ib.ret()
	outer = cw.fnWith("Outer", 0, []types.TypeID{cw.tab.Ref(types.I32), cw.fnT}, []bool{false, true}, types.Bool)
	ob := cw.body(outer)
	ob.call(inner, nil, mir.ReturnLocal, arg(copyOf(ob.b, 1)), arg(copyOf(ob.b, 2)))
	ob.ret()
	return outer, inner
}

// instOf is the one instance of item, failing unless there is exactly one.
func instOf(t *testing.T, in *Instances, w *world, item types.ItemID) *Instance {
	t.Helper()
	var got []*Instance
	for i := range in.List {
		if in.List[i].Item == item {
			got = append(got, &in.List[i])
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d instances of %s, want 1:\n%s", len(got), w.items.Get(item).Key, summary(in))
	}
	return got[0]
}

// callArgs is the arguments of the first call terminator of b whose callee
// is item (direct).
func callArgs(t *testing.T, b *mir.Body, item types.ItemID) []mir.Arg {
	t.Helper()
	for _, blk := range b.Blocks {
		tm := blk.Term
		if tm.Kind == mir.TermCall && tm.Callee.Kind == mir.CallDirect && tm.Callee.Fn.Item == item {
			return b.Args[tm.Args.Start:tm.Args.End()]
		}
	}
	t.Fatalf("no direct call of item %d", item)
	return nil
}

func isLocalCopy(a mir.Arg, l mir.LocalID) bool {
	return a.Mode == mir.ArgCopy && a.Operand.Kind == mir.OpCopy && a.Operand.Place.Local == l && a.Operand.Place.Proj.Len == 0
}

// TestInstanceHiddenParam: Outer and Inner at a capturing less each take
// one hidden parameter (ParamCount 3, local 3 of the environment type);
// Inner's less(x) is a direct call of the literal with the environment;
// Outer passes it to Inner after the literal; every instance validates.
func TestInstanceHiddenParam(t *testing.T) {
	cw := newCapWorld(types.I32)
	outer, inner := cw.sortChain()
	main := cw.fn("Main", 0, []types.TypeID{cw.tab.Ref(types.I32), types.I32}, types.Bool)
	lit := cw.capLiteral(main, 0, cw.rec, cw.envRef)
	mb := cw.body(main)
	env := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
	mb.call(outer, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(cw.fnConst(lit)), env)
	mb.ret()
	in, ds := cw.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, cw.world, in)
	for _, item := range []types.ItemID{outer, inner} {
		b := instOf(t, in, cw.world, item).Body
		if b.ParamCount != 3 || b.Locals[3].Kind != mir.LocalParam || b.Locals[3].Type != cw.envRef {
			t.Fatalf("%s: ParamCount %d, local 3 %+v; want the hidden &(origin: &i32)", cw.items.Get(item).Key, b.ParamCount, b.Locals[3])
		}
	}
	ib := instOf(t, in, cw.world, inner).Body
	la := callArgs(t, ib, lit)
	if len(la) != 2 || !isLocalCopy(la[1], 3) {
		t.Fatalf("Inner's call of the literal passes %d arguments, want (x, copy _3): %+v", len(la), la)
	}
	ob := instOf(t, in, cw.world, outer).Body
	oa := callArgs(t, ob, inner)
	if len(oa) != 3 || oa[1].Operand.Kind != mir.OpConst || !isLocalCopy(oa[2], 3) {
		t.Fatalf("Outer's call of Inner passes %+v, want (x, const L, copy _3)", oa)
	}
	// The literal's own instance keeps its hidden parameter.
	if lb := instOf(t, in, cw.world, lit).Body; lb.ParamCount != 2 {
		t.Fatalf("the literal's instance has ParamCount %d, want 2", lb.ParamCount)
	}
}

// TestForwardingThreadsEnv: the environment passes through a chain of
// forwards (Main to A to B to Inner), each instance taking and passing it;
// a call that passes a capturing literal with no environment argument is an
// ICE naming the call.
func TestForwardingThreadsEnv(t *testing.T) {
	cw := newCapWorld(types.I32)
	_, inner := cw.sortChain()
	prev := inner
	var chain []types.ItemID
	for _, name := range []string{"B", "A"} {
		f := cw.fnWith(name, 0, []types.TypeID{cw.tab.Ref(types.I32), cw.fnT}, []bool{false, true}, types.Bool)
		fb := cw.body(f)
		fb.call(prev, nil, mir.ReturnLocal, arg(copyOf(fb.b, 1)), arg(copyOf(fb.b, 2)))
		fb.ret()
		chain = append(chain, f)
		prev = f
	}
	main := cw.fn("Main", 0, []types.TypeID{cw.tab.Ref(types.I32), types.I32}, types.Bool)
	lit := cw.capLiteral(main, 0, cw.rec, cw.envRef)
	mb := cw.body(main)
	env := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
	mb.call(prev, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(cw.fnConst(lit)), env)
	mb.ret()
	in, ds := cw.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	callees := map[types.ItemID]types.ItemID{chain[1]: chain[0], chain[0]: inner}
	for f, callee := range callees {
		b := instOf(t, in, cw.world, f).Body
		if a := callArgs(t, b, callee); len(a) != 3 || !isLocalCopy(a[2], 3) {
			t.Errorf("%s passes %+v to %s, want the hidden parameter last", cw.items.Get(f).Key, a, cw.items.Get(callee).Key)
		}
	}
	if a := callArgs(t, instOf(t, in, cw.world, inner).Body, lit); len(a) != 2 || !isLocalCopy(a[1], 3) {
		t.Errorf("Inner calls the literal with %+v", a)
	}

	// A literal passed with no environment argument: the count is mono's to
	// check, an ICE naming the call.
	cw2 := newCapWorld(types.I32)
	outer2, _ := cw2.sortChain()
	main2 := cw2.fn("Main", 0, []types.TypeID{cw2.tab.Ref(types.I32), types.I32}, types.Bool)
	lit2 := cw2.capLiteral(main2, 0, cw2.rec, cw2.envRef)
	wrap := cw2.fnWith("Wrap", 0, []types.TypeID{cw2.tab.Ref(types.I32)}, nil, types.Bool)
	wb := cw2.body(wrap)
	wb.call(outer2, nil, mir.ReturnLocal, arg(copyOf(wb.b, 1)), arg(cw2.fnConst(lit2)))
	wb.ret()
	mb2 := cw2.body(main2)
	mb2.call(wrap, nil, mir.ReturnLocal, arg(copyOf(mb2.b, 1)))
	mb2.ret()
	// Wrap is not generic, so mono keeps its body as is; instantiate it
	// at a const argument through a generic copy to reach threadEnvs.
	gw := cw2.fnWith("GWrap", 1, []types.TypeID{cw2.tab.Ref(types.I32)}, nil, types.Bool)
	gb := cw2.body(gw)
	gb.call(outer2, nil, mir.ReturnLocal, arg(copyOf(gb.b, 1)), arg(cw2.fnConst(lit2)))
	gb.ret()
	mustICE(t, "with no environment argument", func() {
		Instantiate(cw2, gw, []types.GenericArg{ty(types.I32)}, Options{})
	})
}

// TestTwoCapturingArgs: two const parameters bound to two capturing
// literals give two hidden parameters in parameter order; Mix forwards one
// and writes the other at its call, and the trailing arguments follow the
// callee's const-parameter order.
func TestTwoCapturingArgs(t *testing.T) {
	cw := newCapWorld(types.I32)
	_, inner := cw.sortChain()
	ri := cw.tab.Ref(types.I32)
	two := cw.fnWith("Two", 0, []types.TypeID{ri, cw.fnT, cw.fnT}, []bool{false, true, true}, types.Bool)
	tb := cw.body(two)
	tb.call(inner, nil, mir.ReturnLocal, arg(copyOf(tb.b, 1)), arg(copyOf(tb.b, 3)))
	tb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(tb.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(tb.b, 1)))
	tb.ret()
	mix := cw.fnWith("Mix", 0, []types.TypeID{ri, types.I32, cw.fnT}, []bool{false, false, true}, types.Bool)
	main := cw.fn("Main", 0, []types.TypeID{ri, types.I32}, types.Bool)
	recHi, refHi := cw.envFor(types.I64)
	l1 := cw.capLiteral(main, 0, cw.rec, cw.envRef)
	l3 := cw.capLiteral(mix, 0, recHi, refHi)
	xb := cw.body(mix)
	big := xb.temp(types.I64)
	xb.assign(big, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.I64, 9)})
	e3 := cw.buildEnv(xb, big, recHi, refHi)
	xb.call(two, nil, mir.ReturnLocal, arg(copyOf(xb.b, 1)), arg(copyOf(xb.b, 3)), arg(cw.fnConst(l3)), e3)
	xb.ret()
	mb := cw.body(main)
	e1 := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
	mb.call(mix, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(copyOf(mb.b, 2)), arg(cw.fnConst(l1)), e1)
	mb.ret()
	in, ds := cw.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	tw := instOf(t, in, cw.world, two).Body
	if tw.ParamCount != 5 || tw.Locals[4].Type != cw.envRef || tw.Locals[5].Type != refHi {
		t.Fatalf("Two's instance: ParamCount %d, hidden %s and %s", tw.ParamCount,
			cw.tab.TypeString(tw.Locals[4].Type, nil), cw.tab.TypeString(tw.Locals[5].Type, nil))
	}
	if a := callArgs(t, tw, l1); len(a) != 2 || !isLocalCopy(a[1], 4) {
		t.Errorf("Two's a(x) passes %+v, want the first hidden parameter", a)
	}
	if a := callArgs(t, tw, inner); len(a) != 3 || !isLocalCopy(a[2], 5) {
		t.Errorf("Two's Inner(x, b) passes %+v, want the second hidden parameter", a)
	}
	mx := instOf(t, in, cw.world, mix).Body
	a := callArgs(t, mx, two)
	if len(a) != 5 || !isLocalCopy(a[3], 4) || mx.OperandType(a[4].Operand) != refHi {
		t.Fatalf("Mix's call of Two passes %d arguments %+v, want (x, const a, const l3, copy hidden, copy e3)", len(a), a)
	}
}

// TestSharedInstance: two literals with equal MIR and environment type at
// two sites share one instance of Outer; a literal whose capture has another
// type gets its own.
func TestSharedInstance(t *testing.T) {
	cw := newCapWorld(types.I32)
	outer, _ := cw.sortChain()
	main := cw.fn("Main", 0, []types.TypeID{cw.tab.Ref(types.I32), types.I32, types.I64}, types.Bool)
	l1 := cw.capLiteral(main, 0, cw.rec, cw.envRef)
	l2 := cw.capLiteral(main, 1, cw.rec, cw.envRef)
	rec64, ref64 := cw.envFor(types.I64)
	l3 := cw.capLiteral(main, 2, rec64, ref64)
	mb := cw.body(main)
	for _, l := range []types.ItemID{l1, l2} {
		env := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
		mb.call(outer, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(cw.fnConst(l)), env)
	}
	env := cw.buildEnv(mb, 3, rec64, ref64)
	mb.call(outer, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(cw.fnConst(l3)), env)
	mb.ret()
	in, ds := cw.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	var outers []*Instance
	for i := range in.List {
		if in.List[i].Item == outer {
			outers = append(outers, &in.List[i])
		}
	}
	if len(outers) != 2 {
		t.Fatalf("%d instances of Outer, want 2 (one shared by the equal literals, one for the i64 capture):\n%s", len(outers), summary(in))
	}
	types2 := map[types.TypeID]bool{}
	for _, o := range outers {
		types2[o.Body.Locals[3].Type] = true
	}
	if !types2[cw.envRef] || !types2[ref64] {
		t.Fatalf("Outer instances' hidden types: %v", types2)
	}
}

// TestInsertParams: insertParams moves every local above the parameters
// by k and rewrites every reference (places, index projections, scope ends,
// destinations, error locals, loop holders), so the body still validates and
// prints equal to the original with its locals renumbered.
func TestInsertParams(t *testing.T) {
	cw := newCapWorld(types.I32)
	_, inner := cw.sortChain()
	ri := cw.tab.Ref(types.I32)
	arr := cw.tab.Array(types.ConstArg{Kind: types.CAValue, Value: intConst(cw.consts, 4)}, types.I32)
	f := cw.fn("F", 0, []types.TypeID{ri, types.Int}, types.Bool)
	fb := cw.body(f)
	a := fb.temp(arr)
	fb.assign(a, mir.Rvalue{Kind: mir.RvRepeat, A: mir.Imm(types.I32, 7), Len: types.ConstArg{Kind: types.CAValue, Value: intConst(cw.consts, 4)}, Type: arr})
	i := fb.temp(types.Int)
	fb.assign(i, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Int, 1)})
	el := fb.temp(types.I32)
	fb.assign(el, mir.Rvalue{Kind: mir.RvUse, A: mir.Copy(fb.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: types.I32}))})
	r := fb.temp(ri)
	fb.assign(r, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: fb.b.Place(el), Type: ri})
	fb.call(inner, nil, mir.ReturnLocal, arg(copyOf(fb.b, r)), arg(cw.fnConst(inner)))
	blk := &fb.b.Blocks[fb.cur]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StScopeEnd, Local: r, Span: 0})
	fb.ret()
	orig := cloneBody(fb.b)
	p := &mir.Printer{Types: cw.tab, Items: cw.items}
	before := p.Print(orig)
	b := cloneBody(orig)
	ids := insertParams(b, []types.TypeID{cw.envRef, cw.envRef})
	if len(ids) != 2 || ids[0] != 3 || ids[1] != 4 || b.ParamCount != 4 {
		t.Fatalf("insertParams gave %v, ParamCount %d; want [3 4], 4", ids, b.ParamCount)
	}
	sig := cw.items.Get(f).Decl.(*types.FuncDecl).Sig
	if err := b.Validate(&mir.Env{Types: cw.tab, Sig: &sig}); err != nil {
		t.Fatalf("after insertParams: %v", err)
	}
	after := p.Print(b)
	// The original with every local above 2 renumbered by two, and the two
	// hidden parameters' declarations dropped, prints the same.
	re := regexp.MustCompile(`_(\d+)`)
	want := re.ReplaceAllStringFunc(before, func(m string) string {
		n, _ := strconv.Atoi(m[1:])
		if n > 2 {
			n += 2
		}
		return fmt.Sprintf("_%d", n)
	})
	var kept []string
	for _, line := range strings.Split(after, "\n") {
		if strings.Contains(line, "let _3:") || strings.Contains(line, "let _4:") {
			continue
		}
		kept = append(kept, line)
	}
	gotText := strings.Join(kept, "\n")
	gotText = strings.Replace(gotText, "params=4", "params=2", 1)
	if gotText != want {
		t.Fatalf("after insertParams:\n%s\nwant:\n%s", gotText, want)
	}
}

// TestSliceAdapterKeepsEnv: a Slice[T] namespace function with a const
// fn parameter, called on an implementer (List[int]) with a capturing
// literal, keeps the trailing environment through slice.go's receiver
// rewrite: the caller passes the Items() slice, the literal and its
// environment, and the namespace function's instance takes the environment
// and calls the literal directly with it.
func TestSliceAdapterKeepsEnv(t *testing.T) {
	sw := newSliceWorld()
	tab := sw.tab
	cw := &capWorld{world: sw.world}
	cw.fnT = tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(types.I32)}}, Result: types.Bool})
	cw.rec, cw.envRef = cw.envFor(types.I32)
	sl := tab.Named(sw.iface, []types.GenericArg{ty(tab.Param(0))})
	// Slice[T].Each(&self, const keep: fn(&i32): bool): bool { v: i32 = 0; return keep(&v) }
	each := sw.fnWith("Slice:Each", 1, []types.TypeID{tab.Ref(sl), cw.fnT}, []bool{false, true}, types.Bool)
	it := sw.items.Get(each)
	it.Owner, it.Name = sw.iface, sw.in.Intern("Each")
	eb := sw.body(each)
	v := eb.temp(types.I32)
	eb.assign(v, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.I32, 0)})
	rv := eb.temp(tab.Ref(types.I32))
	eb.assign(rv, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: eb.b.Place(v), Type: tab.Ref(types.I32)})
	eb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(eb.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(eb.b, rv)))
	eb.ret()
	// Main(l: &List[int], k: i32): bool { return Slice[int].Each(l, fn(a) { .. k .. }) }
	main := sw.fn("Main", 0, []types.TypeID{tab.Ref(sw.listInt), types.I32}, types.Bool)
	lit := cw.capLiteral(main, 0, cw.rec, cw.envRef)
	mb := sw.body(main)
	env := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
	mb.call(each, []types.GenericArg{ty(types.Int)}, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(cw.fnConst(lit)), env)
	mb.ret()

	in, ds := sw.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	ma := callArgs(t, instOf(t, in, sw.world, main).Body, each)
	mi := instOf(t, in, sw.world, main).Body
	if len(ma) != 3 {
		t.Fatalf("Main's call of Each passes %d arguments, want 3 (the slice, the literal, its environment)", len(ma))
	}
	if k := tab.Kind(mi.OperandType(ma[0].Operand)); k != types.KSlice {
		t.Errorf("Each's receiver is a %v, want the Items() slice", k)
	}
	if ma[1].Operand.Kind != mir.OpConst || !mir.IsEnvType(tab, mi.OperandType(ma[2].Operand)) {
		t.Errorf("Each's arguments 2 and 3 are %+v, want the literal and its environment", ma[1:])
	}
	eib := instOf(t, in, sw.world, each).Body
	if eib.ParamCount != 3 || eib.Locals[3].Type != cw.envRef {
		t.Fatalf("Each[int] has ParamCount %d, local 3 %s; want the hidden environment", eib.ParamCount, tab.TypeString(eib.Locals[3].Type, nil))
	}
	if la := callArgs(t, eib, lit); len(la) != 2 || !isLocalCopy(la[1], 3) {
		t.Fatalf("Each[int] calls the literal with %+v, want (&v, copy _3)", la)
	}
}

// fnRefTemp is lower's const slot of a literal written at a call: a temporary
// assigned the literal's fn value (constArgs folds it into a constant).
func (cw *capWorld) fnRefTemp(f *fb, lit types.ItemID) mir.Operand {
	t := f.temp(cw.fnT)
	f.assign(t, mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: lit, Args: f.b.AddGenericArgs()}, Type: cw.fnT})
	return copyOf(f.b, t)
}

// slotOf is lower's slot of an operand a later argument could change:
// a temporary assigned a copy of local l.
func slotOf(f *fb, l mir.LocalID, t types.TypeID) mir.Operand {
	s := f.temp(t)
	f.assign(s, mir.Rvalue{Kind: mir.RvUse, A: copyOf(f.b, l)})
	return copyOf(f.b, s)
}

// TestForwardThroughSlot: lower copies a forwarded const fn
// parameter into a temporary whenever a later argument is not inert (a fn
// literal, capturing or not, or a call), as `_5 = copy _3` before
// `call Apply2(copy _4, copy _5, copy _6, copy _9)` (lower's MIR of
// `Apply2(xs, f, fn(..) { .. m .. })`, read from internal/lower). The
// instance still passes its hidden parameter where the literal constant now
// stands, and a literal written at the call keeps its own environment. A plan
// that saw only whole copies of the parameter made the walk ICE "passes a
// capturing literal to const parameter N with no environment argument".
func TestForwardThroughSlot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		laterLit  bool // a literal follows the forwarded parameter
		capturing bool // and it captures
		call      bool // a call follows it instead
		envs      int  // environment arguments of the call in Fwd's instance
	}{
		{"capturing literal after", true, true, false, 2},
		{"plain literal after", true, false, false, 1},
		{"call after", false, false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cw := newCapWorld(types.I32)
			i32r := cw.tab.Ref(types.I32)
			// Two(x, const a, const b): bool { a(x); return b(x) }
			two := cw.fnWith("Two", 0, []types.TypeID{i32r, cw.fnT, cw.fnT}, []bool{false, true, true}, types.Bool)
			tb := cw.body(two)
			tb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(tb.b, 2)}, tb.temp(types.Bool), mir.NoLocal, arg(copyOf(tb.b, 1)))
			tb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(tb.b, 3)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(tb.b, 1)))
			tb.ret()
			// Ap3(x, const a, n: i32): bool { return a(x) }
			ap3 := cw.fnWith("Ap3", 0, []types.TypeID{i32r, cw.fnT, types.I32}, []bool{false, true, false}, types.Bool)
			ab := cw.body(ap3)
			ab.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(ab.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(ab.b, 1)))
			ab.ret()
			compute := cw.fn("Compute", 0, nil, types.I32)
			cb := cw.body(compute)
			cb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.I32, 7)})
			cb.ret()
			// Fwd(x, m: i32, const f): bool, in lower's shape.
			fwd := cw.fnWith("Fwd", 0, []types.TypeID{i32r, types.I32, cw.fnT}, []bool{false, false, true}, types.Bool)
			var later types.ItemID
			if tc.laterLit && tc.capturing {
				later = cw.capLiteral(fwd, 0, cw.rec, cw.envRef)
			} else if tc.laterLit {
				later = cw.literal(fwd, 0, []types.TypeID{i32r}, types.Bool)
				lb := cw.body(later)
				lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
				lb.ret()
			}
			fb := cw.body(fwd)
			x := slotOf(fb, 1, i32r)
			f := slotOf(fb, 3, cw.fnT)
			switch {
			case tc.laterLit:
				g := cw.fnRefTemp(fb, later)
				args := []mir.Arg{arg(x), arg(f), arg(g)}
				if tc.capturing {
					args = append(args, cw.buildEnv(fb, 2, cw.rec, cw.envRef))
				}
				fb.call(two, nil, mir.ReturnLocal, args...)
			case tc.call:
				n := fb.temp(types.I32)
				fb.call(compute, nil, n)
				fb.call(ap3, nil, mir.ReturnLocal, arg(x), arg(f), arg(copyOf(fb.b, n)))
			}
			fb.ret()
			main := cw.fn("Main", 0, []types.TypeID{i32r, types.I32}, types.Bool)
			lit := cw.capLiteral(main, 0, cw.rec, cw.envRef)
			mb := cw.body(main)
			env := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
			mb.call(fwd, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(mir.Imm(types.I32, 3)), arg(cw.fnConst(lit)), env)
			mb.ret()

			in, ds := cw.collect(t, Options{}, mainRoot(main))
			if len(ds) != 0 {
				t.Fatalf("diagnostics: %v", ds)
			}
			fi := instOf(t, in, cw.world, fwd).Body
			if fi.ParamCount != 4 || !mir.IsEnvType(cw.tab, fi.Locals[4].Type) {
				t.Fatalf("Fwd's instance has ParamCount %d, want 4 (one hidden environment)", fi.ParamCount)
			}
			callee, nparams := two, 3
			if tc.call {
				callee = ap3
			}
			ca := callArgs(t, fi, callee)
			if len(ca) != nparams+tc.envs {
				t.Fatalf("Fwd's call of %s passes %d arguments, want %d", cw.items.Get(callee).Key, len(ca), nparams+tc.envs)
			}
			if !isLocalCopy(ca[nparams], 4) {
				t.Errorf("the forwarded parameter's environment is %+v, want copy _4 (Fwd's hidden parameter)", ca[nparams])
			}
			ci := instOf(t, in, cw.world, callee).Body
			if h := int(ci.ParamCount) - nparams; h != tc.envs {
				t.Errorf("%s's instance has %d hidden parameters, want %d", cw.items.Get(callee).Key, h, tc.envs)
			}
		})
	}
}

// TestNonGenericForwarderRoot: the driver's instance-limit walk takes
// every non-generic function as a root (internal/driver/walk.go), a function
// with a const parameter included. As a root such a function passes a
// parameter no constant binds, and mono raised "const parameter argument is
// not a constant". Collect skips it; its instance at the literal its caller
// passes threads the environment. A const int parameter forwarded the same way
// is the same case.
func TestNonGenericForwarderRoot(t *testing.T) {
	cw := newCapWorld(types.I32)
	outer, inner := cw.sortChain() // Outer(x, const less) forwards less to Inner
	main := cw.fn("Main", 0, []types.TypeID{cw.tab.Ref(types.I32), types.I32}, types.Bool)
	lit := cw.capLiteral(main, 0, cw.rec, cw.envRef)
	g := cw.fnWith("G", 0, []types.TypeID{types.Int}, []bool{true}, types.Int)
	gb := cw.body(g)
	gb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: copyOf(gb.b, 1)})
	gb.ret()
	f := cw.fnWith("F", 0, []types.TypeID{types.Int}, []bool{true}, types.Int)
	fbb := cw.body(f)
	fbb.call(g, nil, mir.ReturnLocal, arg(copyOf(fbb.b, 1)))
	fbb.ret()
	mb := cw.body(main)
	mb.call(f, nil, mb.temp(types.Int), arg(mir.Imm(types.Int, 5)))
	env := cw.buildEnv(mb, 2, cw.rec, cw.envRef)
	mb.call(outer, nil, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(cw.fnConst(lit)), env)
	mb.ret()
	var roots []Root
	for _, id := range []types.ItemID{f, g, inner, main, outer} {
		roots = append(roots, Root{Kind: RootExport, Item: id})
	}
	in, ds := cw.collect(t, Options{}, roots...)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	for _, item := range []types.ItemID{outer, inner} {
		b := instOf(t, in, cw.world, item).Body
		if b.ParamCount != 3 {
			t.Errorf("%s: ParamCount %d, want 3 (the instance at Main's literal, none as a root)", cw.items.Get(item).Key, b.ParamCount)
		}
	}
	for _, item := range []types.ItemID{f, g} {
		instOf(t, in, cw.world, item) // exactly one, at 5
	}
}
