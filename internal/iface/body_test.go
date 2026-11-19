// Why: cached MIR bodies must decode to exactly what was encoded, and their
// hashes must move only when the code does.
// What: round trips of every statement, rvalue, terminator, projection,
// operand and callee kind; mk.body.v1's treatment of positions, names and span
// IDs; instance names and content hashes; and the pack cut predicate.
// How: builds bodies by hand, also in a session with noise interned first,
// and compares encodings, hashes and partitions.

package iface

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

const bodyText = "fn Main() {\n  x := a[i]\n  for f in xs { g(f) }\n  return x\n}\n"

// everyBody builds a body using every kind the MIR has. It is not a valid
// program; the encodings do not care.
func everyBody(s *session, f *source.File) *mir.Body {
	t := s.tab
	sym := s.in.Intern
	main := s.id("geo.Main")
	at := func(sub string) source.Span {
		i := strings.Index(bodyText, sub)
		return source.NewSpan(f.ID, uint32(i), uint32(i+len(sub)))
	}
	b := mir.NewBody(main, at("fn Main() {"), types.I32)
	b.GenericCount = 2
	sp1, sp2, sp3 := b.AddSpan(at("a[i]")), b.AddSpan(at("g(f)")), b.AddSpan(at("return x"))
	p := b.AddParam(t.Ref(t.Named(s.id("geo.Point"), nil)), sym("p"), sp1)
	x := b.AddLocal(mir.Local{Type: types.I32, Kind: mir.LocalUser, Name: sym("x"), Var: true, Loop: mir.NoLoop, Span: sp1})
	tmp := b.AddLocal(mir.Local{Type: t.ErrorSetType(t.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrA")}})), Kind: mir.LocalTemp, Loop: mir.NoLoop, Span: sp2})
	sc := b.AddLocal(mir.Local{Type: types.Void, Kind: mir.LocalScope, Name: sym("s"), Loop: mir.NoLoop, Span: sp2})
	hold := b.AddLocal(mir.Local{Type: t.Slice(types.I32), Kind: mir.LocalLoopHolder, Loop: 0, Span: sp2})
	b.Loops = append(b.Loops, mir.LoopInfo{Header: 1, Exit: 2, Holder: hold, Label: sym("outer"), Span: sp2,
		FieldsOf: t.Named(s.id("geo.Point"), nil), FieldsParam: 1})
	b.Defers = append(b.Defers, mir.DeferInfo{Span: sp3}, mir.DeferInfo{Onerr: true, Span: sp3})
	pl := b.Place(p, mir.Projection{Kind: mir.ProjDeref, Type: t.Named(s.id("geo.Point"), nil)},
		mir.Projection{Kind: mir.ProjField, A: 1, Type: types.F64})
	pi := b.Place(hold, mir.Projection{Kind: mir.ProjIndex, A: uint32(x), Type: types.I32})
	pc := b.Place(tmp, mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelCase, A: uint32(s.id("geo.ErrA")), Type: tmp2(s)},
		mir.Projection{Kind: mir.ProjField, FieldLoop: true, A: 0, Type: types.U8})
	pe := b.Place(p, mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelEnum, A: 1, Type: types.I32})
	ga := b.AddGenericArgs(types.TypeArg(types.I32), types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: s.uintConst(3)}))
	ops := b.AddOperands(mir.Copy(pl), mir.Move(pi), mir.Const(types.Uint, s.uintConst(5)), mir.Imm(types.I32, 42))
	args := b.AddArgs(mir.Arg{Operand: mir.Copy(pl)}, mir.Arg{Operand: mir.Move(pc), Mode: mir.ArgMove},
		mir.Arg{Operand: mir.Copy(pe), Mode: mir.ArgReborrow})
	cases := b.AddCases(mir.SwitchCase{Value: 0, Target: 1}, mir.SwitchCase{Value: 7, Target: 2})
	lit := s.id("geo.Main#fn0")
	rvs := []mir.Rvalue{
		{Kind: mir.RvUse, A: mir.Imm(types.I32, 1)},
		{Kind: mir.RvRef, Borrow: mir.Exclusive, Origin: mir.OriginLoopElement, Loop: 0, Place: pi},
		{Kind: mir.RvSlice, Place: pi, HasLo: true, A: mir.Imm(types.Int, 0), HasHi: true, B: mir.Copy(mir.LocalPlace(x)), Origin: mir.OriginLoopHolder},
		{Kind: mir.RvUnary, Un: mir.BitNot, A: mir.Copy(mir.LocalPlace(x))},
		{Kind: mir.RvBinary, Bin: mir.Shr, A: mir.Copy(mir.LocalPlace(x)), B: mir.Imm(types.I32, 2)},
		{Kind: mir.RvCast, Cast: mir.CastTrunc, A: mir.Copy(mir.LocalPlace(x)), Type: types.U8},
		{Kind: mir.RvAggregate, Agg: mir.AggEnum, Item: s.id("geo.Shape"), Variant: 1, Ops: ops, Type: t.Named(s.id("geo.Shape"), nil)},
		{Kind: mir.RvRepeat, A: mir.Imm(types.U8, 0), Len: types.ConstArg{Kind: types.CAParam, Param: 1}},
		{Kind: mir.RvLen, Place: pi},
		{Kind: mir.RvDiscriminant, Place: pe},
		{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: s.id("geo.Add"), Args: ga}},
		{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: lit}},
		{Kind: mir.RvGlobalRef, Item: s.id("geo.Point")},
		{Kind: mir.RvConstRef, Item: s.id("geo.Wrap")},
		{Kind: mir.RvSizeOf, Type: t.Param(0)},
		{Kind: mir.RvAlignOf, Type: types.F64},
		{Kind: mir.RvFieldCount, Type: t.Named(s.id("geo.Point"), nil)},
		{Kind: mir.RvTypeIs, Param: 0, Type: types.Bool},
		{Kind: mir.RvFieldName, Field: mir.FieldRef{Loop: true, Index: 0}},
		{Kind: mir.RvConstArg, Len: types.ConstArg{Kind: types.CASizeOf, Type: types.I64}, Type: types.Uint},
	}
	bb := func(origin mir.BlockOrigin, d mir.DeferID) *mir.Block {
		return &b.Blocks[b.AddBlock(origin, d)]
	}
	b0 := bb(mir.OriginBody, mir.NoDefer)
	for _, rv := range rvs {
		b0.Stmts = append(b0.Stmts, mir.Statement{Kind: mir.StAssign, Place: mir.LocalPlace(x), Rvalue: rv, Span: sp1})
	}
	b0.Stmts = append(b0.Stmts, mir.Statement{Kind: mir.StScopeEnd, Local: x, Span: sp2},
		mir.Statement{Kind: mir.StJoin, Local: sc, Cancel: true, Span: sp2}, mir.Statement{Kind: mir.StNop, Span: sp3})
	b0.Term = mir.Terminator{Kind: mir.TermSwitch, Span: sp1, On: mir.Copy(mir.LocalPlace(x)), Cases: cases, Default: mir.NoBlock}
	terms := []mir.Terminator{
		{Kind: mir.TermGoto, Target: 2, ElseExit: true, Span: sp2},
		{Kind: mir.TermCall, Span: sp2, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: s.id("geo.Add"), Args: ga}},
			Args: args, Dest: mir.LocalPlace(x), Target: 3, HasErr: true, Err: mir.ErrorEdge{Kind: mir.EdgeElse, Block: 4, Error: tmp}, Spawn: mir.NoLocal},
		{Kind: mir.TermCall, Span: sp2, Callee: mir.Callee{Kind: mir.CallInterface, Iface: s.id("geo.Wrap"), Method: 2, SelfType: t.Param(0)},
			Dest: mir.LocalPlace(x), Target: 4, Spawn: sc},
		{Kind: mir.TermCall, Span: sp2, Callee: mir.Callee{Kind: mir.CallValue, Value: mir.Copy(pl)}, Dest: mir.LocalPlace(x), Target: 5, Spawn: mir.NoLocal},
		{Kind: mir.TermCall, Span: sp2, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinQuote}, Dest: mir.LocalPlace(x), Target: 6, Spawn: mir.NoLocal},
		{Kind: mir.TermTypeTest, Span: sp3, Param: 1, Type: types.Str, Yes: 7, No: 8},
		{Kind: mir.TermOnerrGuard, Span: sp3, Value: tmp, Yes: 8, No: 9},
		{Kind: mir.TermReturn, Span: sp3, Exit: mir.ExitFail, NewError: true},
		{Kind: mir.TermPanic, Span: sp3, Panic: types.PanicMust, HasMsg: true, Msg: mir.Move(mir.LocalPlace(tmp))},
		{Kind: mir.TermUnreachable, Span: sp3},
	}
	for i, tm := range terms {
		origin, d := mir.OriginBody, mir.NoDefer
		switch i {
		case 3:
			origin, d = mir.OriginDefer, 0
		case 4:
			origin, d = mir.OriginOnerr, 1
		}
		blk := bb(origin, d)
		blk.Term = tm
	}
	return b
}

// tmp2 is the error type a SelCase downcast yields.
func tmp2(s *session) types.TypeID {
	return s.tab.ErrorSetType(s.tab.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrA")}}))
}

func bodySession(noise bool) (*session, *source.File) {
	s := withDecls(newGeo(noise))
	if noise {
		s.sm.Add("noise.mk", []byte("x"))
	}
	f := s.addFile("geo/main.mk", bodyText, "geo.Main")
	return s, f
}

func bodyTrip(s *session, env *Env, b *mir.Body) ([]byte, *mir.Body, error) {
	p := newPools(s.h, s.env, types.NoModule)
	p.prefer = []AnchorKey{"geo.Main"}
	var e Encoder
	w := walker{e: &e, h: s.h, p: p}
	w.writeBody(b)
	enc := p.finish(recValues, e.Out())
	var got *mir.Body
	err := decodeRecord(env, enc, recValues, nil, func(r *reader) error { got = r.readBody(r.d); return r.d.Err })
	return enc, got, err
}

func TestRoundTripBodies(t *testing.T) {
	a, fa := bodySession(false)
	body := everyBody(a, fa)
	_, got, err := bodyTrip(a, a.env, body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, body) {
		t.Errorf("same-session body differs:\n got %+v\nwant %+v", got, body)
	}
	// A small body built through the contract's builders round-trips too.
	small := retBody(a, a.id("geo.Main"), types.Void)
	if _, g2, err := bodyTrip(a, a.env, small); err != nil || !reflect.DeepEqual(g2, small) {
		t.Errorf("small body: err %v, got %+v", err, g2)
	}
	b, fb := bodySession(true)
	enc, got, err := bodyTrip(a, b.env, body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spans[1].File != fb.ID || b.sm.Text(got.Spans[1]) != "a[i]" {
		t.Errorf("span decoded onto %q", b.sm.Text(got.Spans[1]))
	}
	if a.h.Body(body) != b.h.Body(got) {
		t.Error("body hash differs across sessions")
	}
	if re, _, err := bodyTrip(b, b.env, got); err != nil || !bytes.Equal(re, enc) {
		t.Errorf("re-encoding the body changed the bytes (err %v)", err)
	}
	// The same body built in the noisy session encodes identically.
	if re, _, _ := bodyTrip(b, b.env, everyBody(b, fb)); !bytes.Equal(re, enc) {
		t.Error("body encoding depends on intern order")
	}
}

func cloneBody(b *mir.Body) *mir.Body {
	c := *b
	c.Locals = slices.Clone(b.Locals)
	c.Blocks = slices.Clone(b.Blocks)
	for i := range c.Blocks {
		c.Blocks[i].Stmts = slices.Clone(c.Blocks[i].Stmts)
	}
	c.Spans = slices.Clone(b.Spans)
	c.Loops = slices.Clone(b.Loops)
	c.Defers = slices.Clone(b.Defers)
	c.Operands = slices.Clone(b.Operands)
	return &c
}

func TestBodyHashIgnoresSpansAndNames(t *testing.T) {
	s, f := bodySession(false)
	body := everyBody(s, f)
	h := s.h.Body(body)
	c := cloneBody(body)
	for i := range c.Spans {
		c.Spans[i] = source.NewSpan(f.ID, c.Spans[i].Start+1, c.Spans[i].End+3)
	}
	for i := range c.Locals {
		c.Locals[i].Name = s.in.Intern("renamed")
	}
	c.Loops[0].Label = s.in.Intern("inner")
	c.Item = s.id("geo.Add")
	if s.h.Body(c) != h {
		t.Error("positions, names, labels or the body's item changed mk.body.v1")
	}
	c.Operands[3].Bits = 43
	if s.h.Body(c) == h {
		t.Error("an operand change left mk.body.v1 equal")
	}
}

func TestSpanIDsInBodyHash(t *testing.T) {
	s, f := bodySession(false)
	body := everyBody(s, f)
	h := s.h.Body(body)
	edits := map[string]func(c *mir.Body){
		"statement span":  func(c *mir.Body) { c.Blocks[0].Stmts[0].Span = 2 },
		"terminator span": func(c *mir.Body) { c.Blocks[0].Term.Span = 3 },
		"local span":      func(c *mir.Body) { c.Locals[1].Span = 3 },
		"loop span":       func(c *mir.Body) { c.Loops[0].Span = 3 },
		"defer span":      func(c *mir.Body) { c.Defers[0].Span = 1 },
		"spans length":    func(c *mir.Body) { c.Spans = append(c.Spans, c.Spans[1]) },
	}
	for name, edit := range edits {
		c := cloneBody(body)
		edit(c)
		if s.h.Body(c) == h {
			t.Errorf("%s: mk.body.v1 unchanged (I-25)", name)
		}
	}
}

// layoutWorld answers ReadLayout from a map and nothing else.
type layoutWorld map[string]source.Hash

func (w layoutWorld) Part(r types.Read) (source.Hash, bool) {
	if r.Kind != types.ReadLayout {
		return source.Hash{}, false
	}
	h, ok := w[r.Key.String()+r.Name]
	return h, ok
}

func TestInstanceNameStable(t *testing.T) {
	s, f := bodySession(false)
	s.items.Get(s.id("geo.Wrap")).Decl = &types.ConstDecl{Type: types.Uint, Value: s.uintConst(1), HasValue: true}
	s.items.Get(s.id("geo.Point")).Decl = &types.GlobalDecl{Type: types.Uint}
	body := everyBody(s, f)
	body.GenericCount = 0
	w := layoutWorld{"geo.Point": source.HashString("t", "p"), "geo.Shape": source.HashString("t", "s"),
		"geo.ErrA": source.HashString("t", "ea"), "geo.ErrB": source.HashString("t", "eb")}
	args := []types.GenericArg{types.TypeArg(types.I32)}
	name := s.h.InstanceName(types.KeyFromString("geo.Add"), args)
	content := s.h.InstanceContentHash(w, body)
	edited := cloneBody(body)
	edited.Operands[3].Bits = 9
	if s.h.InstanceName(types.KeyFromString("geo.Add"), args) != name {
		t.Error("InstanceName changed")
	}
	if s.h.InstanceContentHash(w, edited) == content {
		t.Error("a body edit left InstanceContentHash equal")
	}
	// A layout change of a mentioned type changes the content hash only.
	w["geo.Point"] = source.HashString("t", "p2")
	if s.h.InstanceContentHash(w, body) == content {
		t.Error("a layout change left InstanceContentHash equal")
	}
	// A const's value is baked in.
	w["geo.Point"] = source.HashString("t", "p")
	s.items.Get(s.id("geo.Wrap")).Decl.(*types.ConstDecl).Value = s.uintConst(2)
	if s.h.InstanceContentHash(w, body) == content {
		t.Error("a const value change left InstanceContentHash equal")
	}
	if s.h.InstanceName(types.KeyFromString("geo.Add"), []types.GenericArg{types.TypeArg(types.I64)}) == name {
		t.Error("different arguments give one InstanceName")
	}
	delete(w, "geo.Shape")
	if !catchICE(func() { s.h.InstanceContentHash(w, body) }) {
		t.Error("a missing layout did not raise an ICE")
	}
}

// literalArg is a const generic argument holding fn literal lit.
func literalArg(s *session, lit types.ItemID) types.GenericArg {
	fnT := s.tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.I32}, {Type: types.I32}}, Result: types.Bool})
	c := s.consts.Intern(types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: lit}}}})
	return types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: c})
}

func addLiteral(s *session, key string, panicCode types.PanicCode) types.ItemID {
	lit := s.item(s.mod, types.KeyFromString(key), "", types.KindLiteral, false)
	s.items.Get(lit).Decl = &types.FuncDecl{Sig: types.Signature{Item: lit,
		Params: []types.Param{{Name: s.in.Intern("a"), Type: types.I32}, {Name: s.in.Intern("b"), Type: types.I32}}, Result: types.Bool}}
	b := retBody(s, lit, types.Bool)
	b.Blocks[0].Term.Panic = panicCode
	s.bodies[lit] = b
	return lit
}

func TestFnLiteralContent(t *testing.T) {
	s := withDecls(newGeo(false))
	l1, l2 := addLiteral(s, "geo.Add#fn0", 0), addLiteral(s, "geo.Div#fn4", 0)
	a1, a2 := literalArg(s, l1), literalArg(s, l2)
	if a1 == a2 {
		t.Fatal("fixture: the two literal consts interned as one")
	}
	if s.h.GenericArgs([]types.GenericArg{a1}) != s.h.GenericArgs([]types.GenericArg{a2}) {
		t.Error("identical fn literal arguments at different sites hash differently")
	}
}

func TestFnLiteralSharesInstance(t *testing.T) {
	s := withDecls(newGeo(false))
	l1, l2 := addLiteral(s, "geo.Add#fn0", 0), addLiteral(s, "geo.Div#fn4", 0)
	l3 := addLiteral(s, "geo.Div#fn5", types.PanicAssert) // differs in one "operator"
	sort := types.KeyFromString("std.sort.Sort")
	n1 := s.h.InstanceName(sort, []types.GenericArg{types.TypeArg(types.I32), literalArg(s, l1)})
	n2 := s.h.InstanceName(sort, []types.GenericArg{types.TypeArg(types.I32), literalArg(s, l2)})
	n3 := s.h.InstanceName(sort, []types.GenericArg{types.TypeArg(types.I32), literalArg(s, l3)})
	if n1 != n2 {
		t.Error("identical comparators at two sites give two instances")
	}
	if n1 == n3 {
		t.Error("different comparators give one instance")
	}
	if s.h.LiteralName(l1) != s.h.LiteralName(l2) || s.h.LiteralName(l1) == s.h.LiteralName(l3) {
		t.Error("LiteralName is not by content")
	}
}

// partition is the pack rule over (name, ops) instances sorted by name:
// cut after an instance whose name PackCutAfter accepts; an instance over
// PackSoloOps is a pack of its own.
type inst struct {
	name source.Hash
	ops  int
}

func partition(xs []inst) [][]inst {
	var packs [][]inst
	var cur []inst
	flush := func() {
		if len(cur) > 0 {
			packs = append(packs, cur)
			cur = nil
		}
	}
	for _, x := range xs {
		if x.ops > PackSoloOps {
			flush()
			packs = append(packs, []inst{x})
			continue
		}
		cur = append(cur, x)
		if PackCutAfter(x.name) {
			flush()
		}
	}
	flush()
	return packs
}

func packKeys(ps [][]inst) []string {
	var out []string
	for _, p := range ps {
		var b strings.Builder
		for _, x := range p {
			b.WriteString(x.name.String()[:8])
			b.WriteByte(':')
			b.WriteString(strings.Repeat("#", x.ops/1000))
			b.WriteByte(' ')
		}
		out = append(out, b.String())
	}
	return out
}

func diffCount(a, b []string) int {
	in := map[string]int{}
	for _, x := range a {
		in[x]++
	}
	n := 0
	for _, x := range b {
		if in[x] > 0 {
			in[x]--
		} else {
			n++
		}
	}
	return n
}

func TestPackCutsLocal(t *testing.T) {
	var xs []inst
	total := 0
	for i := 0; i < 200; i++ {
		ops := 100 + (i*7919)%900
		xs = append(xs, inst{name: source.HashString("test.inst", strings.Repeat("x", i)), ops: ops})
		total += ops
	}
	slices.SortFunc(xs, func(a, b inst) int { return a.name.Compare(b.name) })
	base := partition(xs)
	if len(base) < 3 {
		t.Fatalf("200 instances made %d packs; the rule should cut several", len(base))
	}
	bk := packKeys(base)
	// Grow one instance by 5,000 operations: one pack's key changes.
	g := slices.Clone(xs)
	g[57].ops += 5000
	if n := diffCount(bk, packKeys(partition(g))); n != 1 {
		t.Errorf("growing one instance changed %d packs, want 1", n)
	}
	// Add one instance: only the pack it lands in changes.
	add := append(slices.Clone(xs), inst{name: source.HashString("test.inst", "new"), ops: 300})
	slices.SortFunc(add, func(a, b inst) int { return a.name.Compare(b.name) })
	if n := diffCount(bk, packKeys(partition(add))); n > 2 {
		t.Errorf("adding one instance changed %d packs, want at most 2 (its pack, or a cut it brings)", n)
	}
	// Carry one instance over PackSoloOps: its old pack splits, nothing
	// else changes.
	solo := slices.Clone(xs)
	solo[100].ops = PackSoloOps + 1
	after := packKeys(partition(solo))
	if n := diffCount(bk, after); n > 3 {
		t.Errorf("a solo instance changed %d packs, want at most 3 (its pack split in up to three)", n)
	}
	if diffCount(after, bk) != 1 {
		t.Errorf("a solo instance removed %d old packs, want 1", diffCount(after, bk))
	}
	_ = total
}

// TestStoreFormMasks: the store form writes an operand or an rvalue as a
// mask and its non-zero fields (perf-back). A body whose operand and
// argument lists are all zero (one mask byte each) decodes, so the list
// counts' minimum sizes fit the smaller form, and an rvalue with every
// field set round-trips as one with none.
func TestStoreFormMasks(t *testing.T) {
	a, fa := bodySession(false)
	body := retBody(a, a.id("geo.Main"), types.Void)
	body.Operands = make([]mir.Operand, 300)
	body.Args = make([]mir.Arg, 300)
	if _, got, err := bodyTrip(a, a.env, body); err != nil || !reflect.DeepEqual(got, body) {
		t.Fatalf("zero operands and args: err %v", err)
	}
	full := everyBody(a, fa)
	full = cloneBody(full)
	blk := &full.Blocks[0]
	st := blk.Stmts[0]
	op := mir.Operand{Kind: mir.OpConst, Place: mir.Place{Local: 1, Proj: mir.Range{Start: 0, Len: 1}}, Type: types.I64, Const: st.Rvalue.A.Const, Bits: 1 << 40}
	st.Rvalue = mir.Rvalue{Kind: mir.RvAggregate, Borrow: 1, Origin: 1, Loop: 1, Place: op.Place, A: op, B: op, HasLo: true, HasHi: true,
		Un: 1, Bin: 1, Cast: 1, Type: types.I64, Agg: 1, Item: a.id("geo.Main"), Variant: 3, Ops: mir.Range{Start: 0, Len: 1},
		Len: types.ConstArg{Kind: types.CAParam, Param: 2}, Param: 5, Field: mir.FieldRef{Loop: true, Index: 7}, Fn: mir.FnRef{Item: a.id("geo.Main")}}
	blk.Stmts = append([]mir.Statement{st, {Kind: st.Kind, Place: st.Place}}, blk.Stmts[1:]...)
	_, got, err := bodyTrip(a, a.env, full)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Blocks[0].Stmts[:2], full.Blocks[0].Stmts[:2]) {
		t.Errorf("rvalue masks:\n got %+v\nwant %+v", got.Blocks[0].Stmts[:2], full.Blocks[0].Stmts[:2])
	}
}
