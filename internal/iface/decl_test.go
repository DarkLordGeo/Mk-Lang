// Why: declarations, signatures, consts and shapes are what dependent modules
// read from the cache, so their encodings must round-trip and hash by content.
// What: round trips of consts, signatures, every declaration kind, shapes,
// shape terms and layouts, and tests of what the decl hash includes.
// How: each value is round-tripped into the same session (reflect.DeepEqual)
// and into a fresh session with other IDs (equal hashes, equal re-encoded
// bytes).

package iface

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// storeTrip encodes with write in s and decodes with read into env. read
// runs twice (dry, then real) and must overwrite its outputs.
func storeTrip(s *session, env *Env, write func(w *walker), read func(r *reader, d *Decoder)) ([]byte, error) {
	p := newPools(s.h, s.env, types.NoModule)
	var e Encoder
	w := walker{e: &e, h: s.h, p: p}
	write(&w)
	b := p.finish(recValues, e.Out())
	return b, decodeRecord(env, b, recValues, nil, func(r *reader) error { read(r, r.d); return r.d.Err })
}

func retBody(s *session, item types.ItemID, ret types.TypeID) *mir.Body {
	b := mir.NewBody(item, source.Span{}, ret)
	bb := b.AddBlock(mir.OriginBody, mir.NoDefer)
	b.Blocks[bb].Term = mir.Terminator{Kind: mir.TermReturn, Span: 0}
	return b
}

// withDecls fills the Decl of every fixture item and registers the bodies a
// signature or literal hash needs.
func withDecls(s *session) *session {
	t := s.tab
	sym := s.in.Intern
	point := s.id("geo.Point")
	pointT := t.Named(point, nil)
	s.items.Get(point).Decl = &types.StructDecl{Fields: []types.FieldDecl{
		{Name: sym("x"), Type: types.F64, Exported: true},
		{Name: sym("y"), Type: types.F64, Exported: true}}}
	wrap := s.id("geo.Wrap")
	p0 := t.Param(0)
	ptrLeaf := types.Shape{Size: 8, Align: 8, Kind: types.Plain, Carrier: true, Send: true, Share: true, Freeze: true,
		ABI: types.AbiClass{Kind: types.AbiDirect, Count: 1, Parts: [4]types.AbiPart{{Class: types.RegInt, Bytes: 8}}}}
	s.items.Get(wrap).Decl = &types.StructDecl{
		Generics: []types.GenericParam{{Name: sym("T"), Bounds: []types.Bound{{Kind: types.BoundSend}}}},
		Fields:   []types.FieldDecl{{Name: sym("a"), Type: p0, Exported: true}, {Name: sym("b"), Type: t.Ref(p0)}},
		Shape: &types.ShapeTerm{Kind: types.TermStruct, Fields: []types.ShapeTerm{
			{Kind: types.TermParam, Param: 0}, {Kind: types.TermLeaf, Leaf: ptrLeaf}}}}
	s.items.Get(s.id("geo.Shape")).Decl = &types.EnumDecl{Variants: []types.VariantDecl{
		{Name: sym("Circle"), Fields: []types.FieldDecl{{Name: sym("r"), Type: types.F64, Exported: true}}},
		{Name: sym("Square"), Discr: 1}}, Tag: types.U8}
	for _, k := range []string{"geo.ErrA", "geo.ErrB"} {
		id := s.id(k)
		s.items.Get(id).Decl = &types.ErrorDecl{Code: types.ErrorCode(types.KeyFromString(k))}
	}
	add := s.id("geo.Add")
	s.items.Get(add).Decl = &types.FuncDecl{Sig: types.Signature{Item: add, Name: sym("Add"), Exported: true,
		Params: []types.Param{{Name: sym("a"), Type: pointT}, {Name: sym("b"), Type: pointT}}, Result: pointT}}
	main := s.id("geo.Main")
	s.items.Get(main).Decl = &types.FuncDecl{Sig: types.Signature{Item: main, Name: sym("Main"), Result: types.Void}}
	lit := s.id("geo.Main#fn0")
	s.items.Get(lit).Decl = &types.FuncDecl{Sig: types.Signature{Item: lit,
		Params: []types.Param{{Name: sym("a"), Type: types.I32}, {Name: sym("b"), Type: types.I32}}, Result: types.Bool}}
	s.bodies[lit] = retBody(s, lit, types.Bool)
	req := s.id("geo.Div#requires0")
	s.items.Get(req).Decl = &types.FuncDecl{Sig: types.Signature{Item: req,
		Params: []types.Param{{Name: sym("b"), Type: t.Ref(types.I32)}}, Result: types.Bool}}
	s.bodies[req] = retBody(s, req, types.Bool)
	div := s.id("geo.Div")
	s.items.Get(div).Decl = &types.FuncDecl{Sig: divSig(s)}
	return s
}

func divSig(s *session) types.Signature {
	t := s.tab
	sym := s.in.Intern
	div := s.id("geo.Div")
	errA := t.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrA")}})
	return types.Signature{Item: div, Name: sym("Div"), Receiver: types.ByShared, Exported: true,
		Generics: []types.GenericParam{
			{Name: sym("T"), Bounds: []types.Bound{{Kind: types.BoundInterface, Item: s.id("geo.Point"),
				Args: []types.GenericArg{types.TypeArg(types.I32)}}, {Kind: types.BoundShare},
				{Kind: types.BoundFields, Inner: types.BoundPlain}}},
			{Const: true, Name: sym("N"), Type: types.Uint}},
		OwnGenerics: 1,
		Params: []types.Param{{Name: source.SymSelf, Type: t.Ref(t.SelfType())},
			{Name: sym("a"), Type: t.Mut(types.I32), Var: true}, {Name: sym("b"), Type: types.I32, Into: 2},
			{Name: sym("n"), Type: types.Uint, Const: true}},
		Result: t.Ref(types.I32), Errors: errA,
		Flows: types.Flows{Sources: []types.Source{{Param: 2, Behind: true, Path: []types.FieldStep{{Field: 0, Type: types.I32}}}},
			Declared: true, Stores: []types.Store{{From: 3, Into: 2}}, Ends: []types.ParamIndex{2}, Ambiguous: true},
		Contracts: []types.Contract{{Kind: types.Requires, Body: s.id("geo.Div#requires0"), Text: "b != 0"}},
		Variadic:  false, Extern: true}
}

func strConst(s *session, text string) types.ConstID {
	n := len(text)
	pointee := s.consts.Intern(types.ConstValue{Type: s.tab.Array(types.ConstArg{Kind: types.CAValue, Value: s.uintConst(uint64(n))}, types.U8),
		Align: 1, Bytes: []byte(text)})
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b[8:], uint64(n))
	v := types.ConstValue{Type: types.Str, Align: 8, Bytes: b, Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: pointee}}}
	if err := v.Canonicalize(8); err != nil {
		panic(err)
	}
	return s.consts.Intern(v)
}

func fixtureConsts(s *session) []types.ConstID {
	nan := make([]byte, 8)
	binary.LittleEndian.PutUint64(nan, types.CanonicalNaN64)
	f32 := make([]byte, 4)
	binary.LittleEndian.PutUint32(f32, math.Float32bits(1.5))
	fnT := s.tab.Fn(types.FnType{Result: types.Void})
	litT := s.tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.I32}, {Type: types.I32}}, Result: types.Bool})
	fnv := s.consts.Intern(types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: s.id("geo.Add"), Args: []types.GenericArg{types.TypeArg(types.I32)}}}}})
	litv := s.consts.Intern(types.ConstValue{Type: litT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: s.id("geo.Main#fn0")}}}})
	hi := strConst(s, "hi")
	// A slice of two strs: pointee [2]str with two data relocs.
	arr := make([]byte, 32)
	binary.LittleEndian.PutUint64(arr[8:], 2)
	binary.LittleEndian.PutUint64(arr[24:], 2)
	strs := s.consts.Intern(types.ConstValue{Type: s.tab.Array(types.ConstArg{Kind: types.CAValue, Value: s.uintConst(2)}, types.Str),
		Align: 8, Bytes: arr, Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: s.id2(hi)}, {Offset: 16, Kind: types.RelocData, Data: s.id2(hi)}}})
	sl := make([]byte, 16)
	binary.LittleEndian.PutUint64(sl[8:], 2)
	slice := s.consts.Intern(types.ConstValue{Type: s.tab.Slice(types.Str), Align: 8, Bytes: sl,
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: strs}}})
	return []types.ConstID{
		s.uintConst(7),
		s.consts.Intern(types.ConstValue{Type: types.F64, Align: 8, Bytes: nan}),
		s.consts.Intern(types.ConstValue{Type: types.F32, Align: 4, Bytes: f32}),
		s.consts.Intern(types.ConstValue{Type: types.Bool, Align: 1, Bytes: []byte{1}}),
		hi, fnv, litv, slice,
	}
}

// id2 returns the pointee of a str const (the [N]u8 value).
func (s *session) id2(str types.ConstID) types.ConstID { return s.consts.Get(str).Relocs[0].Data }

func TestRoundTripConsts(t *testing.T) {
	a := withDecls(newGeo(false))
	cs := fixtureConsts(a)
	enc := encodeBundle(a, nil, cs, nil)
	// Same session: the same IDs.
	_, got, _, err := decodeBundle(a.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cs) {
		t.Errorf("same-session decode gave %v, want %v", got, cs)
	}
	b := withDecls(newGeo(true))
	_, got, _, err = decodeBundle(b.env, enc)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cs {
		if as, bs := a.tab.ConstString(cs[i]), b.tab.ConstString(got[i]); as != bs {
			t.Errorf("const %d: %q decoded as %q", i, as, bs)
		}
		if a.h.Const(cs[i]) != b.h.Const(got[i]) {
			t.Errorf("const %d (%s): hash differs across sessions", i, a.tab.ConstString(cs[i]))
		}
	}
	if re := encodeBundle(b, nil, got, nil); !bytes.Equal(re, enc) {
		t.Error("re-encoding consts in a fresh session changed the bytes")
	}
}

func TestConstHashIsDeep(t *testing.T) {
	s := withDecls(newGeo(false))
	hi, ho := strConst(s, "hi"), strConst(s, "ho")
	if s.h.Const(hi) == s.h.Const(ho) {
		t.Error("strs with different pointee bytes hash equal")
	}
	if s.h.Const(hi) != s.h.Const(strConst(s, "hi")) {
		t.Error("equal strs hash differently")
	}
	// A literal reloc hashes by content: an equal literal under another key
	// hashes equal.
	f := types.FnRef{Item: s.id("geo.Main#fn0")}
	other := s.item(s.mod, types.KeyFromString("geo.Add#fn3"), "", types.KindLiteral, false)
	s.items.Get(other).Decl = &types.FuncDecl{Sig: types.Signature{Item: other,
		Params: []types.Param{{Name: s.in.Intern("a"), Type: types.I32}, {Name: s.in.Intern("b"), Type: types.I32}}, Result: types.Bool}}
	s.bodies[other] = retBody(s, other, types.Bool)
	if s.h.FnRef(f) != s.h.FnRef(types.FnRef{Item: other}) {
		t.Error("identical fn literals at two sites hash differently")
	}
	if s.h.LiteralName(f.Item) != s.h.LiteralName(other) {
		t.Error("LiteralName depends on the site")
	}
	s.bodies[other].Blocks[0].Term.Panic = types.PanicAssert
	if s.h.FnRef(f) == s.h.FnRef(types.FnRef{Item: other}) {
		t.Error("literals with different bodies hash equal")
	}
	delete(s.bodies, other)
	if !catchICE(func() { s.h.FnRef(types.FnRef{Item: other}) }) {
		t.Error("a literal with no body did not raise an ICE")
	}
}

func TestRoundTripSignatures(t *testing.T) {
	a := withDecls(newGeo(false))
	sig := divSig(a)
	write := func(w *walker) { w.writeSig(&sig, false) }
	var got types.Signature
	read := func(r *reader, d *Decoder) { got = r.readSig(d) }
	if _, err := storeTrip(a, a.env, write, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sig) {
		t.Errorf("same-session signature:\n got %+v\nwant %+v", got, sig)
	}
	b := withDecls(newGeo(true))
	enc, err := storeTrip(a, b.env, write, read)
	if err != nil {
		t.Fatal(err)
	}
	if a.h.Signature(&sig) != b.h.Signature(&got) {
		t.Error("signature hash differs across sessions")
	}
	re, err := storeTrip(b, b.env, func(w *walker) { w.writeSig(&got, false) }, read)
	if err != nil || !bytes.Equal(re, enc) {
		t.Errorf("re-encoding the signature changed the bytes (err %v)", err)
	}
	// The contract is hashed by its body: change it, the hash changes.
	before := a.h.Signature(&sig)
	a.bodies[a.id("geo.Div#requires0")].Blocks[0].Term.Exit = mir.ExitFail
	if a.h.Signature(&sig) == before {
		t.Error("a contract body edit left the signature hash equal")
	}
	delete(a.bodies, a.id("geo.Div#requires0"))
	if !catchICE(func() { a.h.Signature(&sig) }) {
		t.Error("a missing contract body did not raise an ICE")
	}
}

// TestRoundTripReceiverInto: a receiver's `into` is Params[0].Into, kept
// by the interface and counted by the signature hash like any parameter's.
func TestRoundTripReceiverInto(t *testing.T) {
	a := withDecls(newGeo(false))
	plain := divSig(a)
	sig := divSig(a)
	sig.Params[0].Into = 2
	var got types.Signature
	read := func(r *reader, d *Decoder) { got = r.readSig(d) }
	if _, err := storeTrip(a, a.env, func(w *walker) { w.writeSig(&sig, false) }, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sig) || got.Params[0].Into != 2 {
		t.Errorf("receiver into:\n got %+v\nwant %+v", got, sig)
	}
	if a.h.Signature(&sig) == a.h.Signature(&plain) {
		t.Error("the receiver's into left the signature hash equal")
	}
}

// fixtureDeclList is one declaration of every kind.
func fixtureDeclList(s *session) []types.Decl {
	t := s.tab
	sym := s.in.Intern
	var out []types.Decl
	for _, k := range []string{"geo.Point", "geo.Wrap", "geo.Shape", "geo.ErrA", "geo.Add", "geo.Div"} {
		out = append(out, s.items.Get(s.id(k)).Decl)
	}
	sv := s.uintConst(9)
	out = append(out,
		&types.StructDecl{Fields: []types.FieldDecl{{Name: sym("p"), Type: t.RawPtr(types.U8)}}, View: true, Linear: true, Packed: 4,
			Invariants: []types.ItemID{s.id("geo.Div#requires0")}},
		&types.InterfaceDecl{Generics: []types.GenericParam{{Name: sym("T")}}, Methods: []types.Signature{divSig(s)}},
		&types.ImplDecl{Generics: []types.GenericParam{{Name: sym("T")}}, Bound: types.Bound{Kind: types.BoundShare},
			For: t.Named(s.id("geo.Wrap"), []types.GenericArg{types.TypeArg(t.Param(0))}), Methods: []types.ItemID{s.id("geo.Add")}, Unsafe: true},
		&types.AliasDecl{Generics: []types.GenericParam{{Name: sym("T")}}, Type: t.Slice(t.Param(0))},
		&types.ConstDecl{Type: types.Uint, Fuel: 77, Value: sv, HasValue: true},
		&types.ConstDecl{Type: types.Uint, Fuel: types.DefaultFuel},
		&types.GlobalDecl{Type: types.Uint, Value: sv, HasValue: true},
		&types.FuncDecl{Sig: types.Signature{Item: s.id("geo.Main"), Name: sym("Main"), Result: types.Void}, Bodiless: true, Intrinsic: true},
		&types.TestDecl{Name: "adds", TakesSystem: true},
		&types.GenDecl{Generator: s.id("geo.Add"), Args: []types.GenArg{{Kind: types.GenType, Type: t.Named(s.id("geo.Point"), nil)},
			{Kind: types.GenInterface, Item: s.id("geo.Wrap")}, {Kind: types.GenLiteral, Value: sv}}, Fuel: 10},
		nil,
	)
	return out
}

func TestRoundTripDecls(t *testing.T) {
	a := withDecls(newGeo(false))
	ds := fixtureDeclList(a)
	write := func(w *walker) {
		w.e.Uvarint(uint64(len(ds)))
		for _, d := range ds {
			w.writeDecl(d)
		}
	}
	var got []types.Decl
	read := func(r *reader, d *Decoder) {
		got = nil
		n := d.Count(1)
		for i := 0; i < n; i++ {
			got = append(got, r.readDecl(d))
		}
	}
	if _, err := storeTrip(a, a.env, write, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, ds) {
		for i := range ds {
			if !reflect.DeepEqual(got[i], ds[i]) {
				t.Errorf("decl %d (%T):\n got %+v\nwant %+v", i, ds[i], got[i], ds[i])
			}
		}
	}
	b := withDecls(newGeo(true))
	enc, err := storeTrip(a, b.env, write, read)
	if err != nil {
		t.Fatal(err)
	}
	ds2 := got
	re, err := storeTrip(b, b.env, func(w *walker) {
		w.e.Uvarint(uint64(len(ds2)))
		for _, d := range ds2 {
			w.writeDecl(d)
		}
	}, read)
	if err != nil || !bytes.Equal(re, enc) {
		t.Errorf("re-encoding the decls changed the bytes (err %v)", err)
	}
	// mk.decl.v1 and mk.fields.v1 agree across sessions item by item.
	for _, g := range geoItems {
		ia, ib := a.items.Get(a.id(g.key)), b.items.Get(b.id(g.key))
		if ia.Decl == nil {
			continue
		}
		if a.h.Decl(ia) != b.h.Decl(ib) {
			t.Errorf("%s: decl hash differs across sessions", g.key)
		}
		fa, oka := a.h.Fields(ia, false)
		fb, okb := b.h.Fields(ib, false)
		if oka != okb || fa != fb {
			t.Errorf("%s: fields hash differs across sessions", g.key)
		}
	}
}

func TestDeclHashIncludesExported(t *testing.T) {
	s := withDecls(newGeo(false))
	it := s.items.Get(s.id("geo.Shape"))
	before := s.h.Decl(it)
	it.Exported = false
	if s.h.Decl(it) == before {
		t.Error("making an item private left its decl hash equal (E0204 readers)")
	}
}

// TestDeclHashIncludesInvariants: a struct's invariants decide its
// zero-validity, which a foreign zero-valid judgement reads through the
// signature part, so adding one changes mk.decl.v1.
func TestDeclHashIncludesInvariants(t *testing.T) {
	s := withDecls(newGeo(false))
	it := s.items.Get(s.id("geo.Point"))
	d := it.Decl.(*types.StructDecl)
	before := s.h.Decl(it)
	d.Invariants = append(d.Invariants, s.id("geo.Div#requires0"))
	if s.h.Decl(it) == before {
		t.Error("adding an invariant left the struct's decl hash equal (zero-valid readers)")
	}
}

func TestFieldsPrivateBit(t *testing.T) {
	s := withDecls(newGeo(false))
	it := s.items.Get(s.id("geo.Point"))
	d := it.Decl.(*types.StructDecl)
	foreign, _ := s.h.Fields(it, false)
	d.Fields = append(d.Fields, types.FieldDecl{Name: s.in.Intern("c"), Type: types.U8})
	if h, _ := s.h.Fields(it, false); h == foreign {
		t.Error("adding the first private field left the foreign fields hash equal (INC-11)")
	}
	withC, _ := s.h.Fields(it, false)
	d.Fields[2].Name = s.in.Intern("renamed")
	if h, _ := s.h.Fields(it, false); h != withC {
		t.Error("renaming a private field changed the foreign fields hash")
	}
	all, _ := s.h.Fields(it, true)
	d.Fields[2].Name = s.in.Intern("c")
	if h, _ := s.h.Fields(it, true); h == all {
		t.Error("renaming a private field left the same-module fields hash equal")
	}
}

// TestFieldsOfEnum: an enum's fields read (a loan walk through it) is
// answered, in both views, and changes with a variant's payload.
func TestFieldsOfEnum(t *testing.T) {
	s := withDecls(newGeo(false))
	it := s.items.Get(s.id("geo.Shape"))
	all, ok := s.h.Fields(it, true)
	if !ok {
		t.Fatal("no fields hash for an enum")
	}
	if foreign, ok := s.h.Fields(it, false); !ok || foreign != all {
		t.Errorf("an enum's views differ (%v): its variants are all public", ok)
	}
	it.Decl.(*types.EnumDecl).Variants[0].Fields[0].Type = types.F32
	if h, _ := s.h.Fields(it, true); h == all {
		t.Error("changing a variant's payload left the enum's fields hash equal")
	}
}

func fixtureShapes() ([]types.Shape, []types.ShapeTerm, []types.Layout) {
	sh := types.Shape{Size: 24, Align: 8, Kind: types.Unique, Carrier: true, View: true, Send: true, Freeze: true,
		ABI: types.AbiClass{Kind: types.AbiIndirect, Byval: true}}
	sh2 := types.Shape{Size: 16, Align: 8, ABI: types.AbiClass{Kind: types.AbiDirect, Count: 2,
		Parts: [4]types.AbiPart{{Class: types.RegInt, Bytes: 8}, {Class: types.RegFloat, Bytes: 8}}}, Share: true}
	return []types.Shape{sh, sh2, {}}, nil, nil
}

func fixtureTerms(s *session) []types.ShapeTerm {
	shapes, _, _ := fixtureShapes()
	errs := s.tab.ErrorSet([]types.ErrorMember{{Item: s.id("geo.ErrA")}, {IsParam: true, Param: 1}})
	leaf := types.ShapeTerm{Kind: types.TermLeaf, Leaf: shapes[1]}
	param := types.ShapeTerm{Kind: types.TermParam, Param: 2}
	return []types.ShapeTerm{
		leaf, param,
		{Kind: types.TermStruct, Fields: []types.ShapeTerm{leaf, param}, Packed: 2, View: true, Linear: true, Marks: types.MarkInterior | types.MarkNoSend},
		{Kind: types.TermRecord, Fields: []types.ShapeTerm{param, leaf}},
		{Kind: types.TermEnum, Variants: [][]types.ShapeTerm{{leaf}, nil, {param, leaf}}, Discrs: []uint64{0, 5, 6}, Tag: types.U16},
		{Kind: types.TermArray, Len: types.ConstArg{Kind: types.CAParam, Param: 0}, Elem: &param},
		{Kind: types.TermOptional, Elem: &leaf},
		{Kind: types.TermErrorUnion, Elem: &param, Errors: errs},
	}
}

func fixtureLayouts(s *session) []types.Layout {
	shapes, _, _ := fixtureShapes()
	return []types.Layout{
		{Shape: shapes[0], Fields: []types.FieldLayout{{Offset: 0, Type: types.U64}, {Offset: 8, Type: s.tab.Ref(types.U8)}}},
		{Shape: shapes[1], HasTag: true, Tag: types.TagLayout{Offset: 0, Size: 1}, Variants: []types.VariantLayout{
			{Discr: 0}, {Discr: 1, Fields: []types.FieldLayout{{Offset: 8, Type: types.F64}}}}},
		{Shape: shapes[2], HasTag: true, Tag: types.TagLayout{Niche: true, Size: 8}},
	}
}

func TestRoundTripShapes(t *testing.T) {
	a := withDecls(newGeo(false))
	shapes, _, _ := fixtureShapes()
	terms, layouts := fixtureTerms(a), fixtureLayouts(a)
	write := func(w *walker) {
		for i := range shapes {
			w.writeShape(&shapes[i])
		}
		for i := range terms {
			w.writeTerm(&terms[i])
		}
		for i := range layouts {
			w.writeLayout(&layouts[i])
		}
	}
	var gs []types.Shape
	var gt []types.ShapeTerm
	var gl []types.Layout
	read := func(r *reader, d *Decoder) {
		gs, gt, gl = nil, nil, nil
		for range shapes {
			gs = append(gs, r.readShape(d))
		}
		for range terms {
			gt = append(gt, r.readTerm(d, 0))
		}
		for range layouts {
			gl = append(gl, r.readLayout(d))
		}
	}
	if _, err := storeTrip(a, a.env, write, read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gs, shapes) {
		t.Errorf("shapes: got %+v", gs)
	}
	for i := range terms {
		if !reflect.DeepEqual(gt[i], terms[i]) {
			t.Errorf("term %d: got %+v, want %+v", i, gt[i], terms[i])
		}
	}
	for i := range layouts {
		if !reflect.DeepEqual(gl[i], layouts[i]) {
			t.Errorf("layout %d: got %+v, want %+v", i, gl[i], layouts[i])
		}
	}
	b := withDecls(newGeo(true))
	if _, err := storeTrip(a, b.env, write, read); err != nil {
		t.Fatal(err)
	}
	for i := range terms {
		if a.h.ShapeTerm(&terms[i]) != b.h.ShapeTerm(&gt[i]) {
			t.Errorf("term %d: hash differs across sessions", i)
		}
	}
	for i := range layouts {
		if a.h.Layout(&layouts[i]) != b.h.Layout(&gl[i]) {
			t.Errorf("layout %d: hash differs across sessions", i)
		}
	}
	if a.h.Shape(shapes[0]) == a.h.Shape(shapes[1]) {
		t.Error("different shapes hash equal")
	}
	if a.h.Shape(shapes[2]) == a.h.ShapeTerm(&types.ShapeTerm{}) {
		t.Error("a record and a term share a hash")
	}
}

func TestShapeTermGeneric(t *testing.T) {
	s := withDecls(newGeo(false))
	wrap := s.items.Get(s.id("geo.Wrap")).Decl.(*types.StructDecl)
	before := s.h.ShapeTerm(wrap.Shape)
	// A private field rename is not in the term (terms carry no names).
	wrap.Fields[1].Name = s.in.Intern("renamed")
	if s.h.ShapeTerm(wrap.Shape) != before {
		t.Error("a field rename changed the shape term hash")
	}
	// b: &T becomes b: u64: the leaf changes.
	wrap.Shape.Fields[1].Leaf = types.Shape{Size: 8, Align: 8, Send: true, Share: true, Freeze: true,
		ABI: types.AbiClass{Kind: types.AbiDirect, Count: 1, Parts: [4]types.AbiPart{{Class: types.RegInt, Bytes: 8}}}}
	if s.h.ShapeTerm(wrap.Shape) == before {
		t.Error("a field type change left the shape term hash equal (IR-179)")
	}
}
