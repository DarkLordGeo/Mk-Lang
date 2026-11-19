// Why: the printer and validator must hold at every depth, not only at the top
// level of a type or aggregate.
// What: TestPrinterConstByValue, TestInstanceBodyIsClosed and tests of
// invariants M3, M4, M5, M9 and M11.
// How: builds nested types, aggregates and loops into the fixture body and
// expects the named invariant.

package mir

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// local adds a temporary of type t.
func local(b *Body, t types.TypeID) LocalID {
	return b.AddLocal(Local{Type: t, Kind: LocalTemp, Loop: NoLoop})
}

// assign appends dest = rv to bb1 of the Show body.
func assign(b *Body, dest Place, rv Rvalue) {
	b.Blocks[1].Stmts = append(b.Blocks[1].Stmts, Statement{Kind: StAssign, Place: dest, Rvalue: rv})
}

func use(o Operand) Rvalue { return Rvalue{Kind: RvUse, A: o} }

func wantOK(t *testing.T, f *fixture, b *Body, what string) {
	t.Helper()
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("%s rejected: %v", what, err)
	}
}

// TestPrinterConstByValue: const operands, repeat lengths and const
// generic arguments print by value, independent of intern order.
func TestPrinterConstByValue(t *testing.T) {
	print := func(prior int, n uint64) string {
		f := newFixture()
		for i := 0; i < prior; i++ {
			f.consts.Intern(types.ConstValue{Type: types.U32, Align: 4, Bytes: []byte{byte(i), 1, 2, 3}})
		}
		seven := f.consts.Intern(types.ConstValue{Type: types.U8, Align: 1, Bytes: []byte{7}})
		nb := make([]byte, 8)
		types.StoreUint(nb, 8, n)
		ln := types.ConstArg{Kind: types.CAValue, Value: f.consts.Intern(types.ConstValue{Type: types.Int, Align: 8, Bytes: nb})}
		b := f.showBody()
		x := local(b, types.U8)
		arr := local(b, f.tab.Array(ln, types.U8))
		assign(b, LocalPlace(x), use(Const(types.U8, seven)))
		assign(b, LocalPlace(arr), Rvalue{Kind: RvRepeat, A: Copy(LocalPlace(x)), Len: ln})
		fnv := local(b, f.tab.Fn(types.FnType{Result: types.Void}))
		assign(b, LocalPlace(fnv), Rvalue{Kind: RvFnRef, Fn: FnRef{Item: f.load, Args: b.AddGenericArgs(types.ConstGenericArg(ln))}})
		return (&Printer{Types: f.tab, Items: f.items}).Print(b)
	}
	a, c := print(0, 4), print(3, 4)
	if a != c {
		t.Fatalf("intern order changed the text:\n%s\n%s", a, c)
	}
	for _, want := range []string{"= const u8 7", "[copy _4; 4]", "fn main.Load[4]", "let _5: [4]u8"} {
		if !strings.Contains(a, want) {
			t.Fatalf("missing %q in\n%s", want, a)
		}
	}
	if print(0, 8) == a {
		t.Fatal("[x; 4] and [x; 8] print the same")
	}
}

// TestM11NestedInferOrPoison: invariant M11 refuses an Infer or Poison type
// nested anywhere in a local's type.
func TestM11NestedInferOrPoison(t *testing.T) {
	expectViolation(t, "M11", func(f *fixture, b *Body) { local(b, f.tab.Ref(types.Poison)) })
	expectViolation(t, "M11", func(f *fixture, b *Body) { local(b, f.tab.Slice(f.tab.Infer(0))) })
	expectViolation(t, "M11", func(f *fixture, b *Body) {
		local(b, f.tab.Fn(types.FnType{Params: []types.FnParam{{Type: f.tab.Optional(types.Poison), Into: types.NoParam}}, Result: types.Void}))
	})
	expectViolation(t, "M11", func(f *fixture, b *Body) {
		local(b, f.tab.Record([]types.RecordField{{Name: f.in.Intern("a"), Type: f.tab.Mut(f.tab.Infer(2))}}))
	})
}

// TestInstanceBodyIsClosed: GenericCount 0 admits no generic part.
func TestInstanceBodyIsClosed(t *testing.T) {
	expectViolation(t, "M2", func(f *fixture, b *Body) { local(b, f.tab.Param(0)) })
	expectViolation(t, "M2", func(f *fixture, b *Body) { local(b, f.tab.Ref(f.tab.SelfType())) })
	expectViolation(t, "M2", func(f *fixture, b *Body) {
		b.Blocks[3].Term = Terminator{Kind: TermTypeTest, Param: 0, Type: types.I32, Yes: 4, No: 1}
	})
	expectViolation(t, "M2", func(f *fixture, b *Body) {
		assign(b, LocalPlace(local(b, types.Bool)), Rvalue{Kind: RvTypeIs, Param: 0, Type: types.I32})
	})
	expectViolation(t, "M2", func(f *fixture, b *Body) {
		rec := local(b, f.tab.Record([]types.RecordField{{Name: f.in.Intern("a"), Type: types.I32}}))
		b.Loops = append(b.Loops, LoopInfo{Header: 1, Exit: 2, Holder: NoLocal})
		src := b.Place(rec, Projection{Kind: ProjField, FieldLoop: true, A: 0, Type: types.I32})
		assign(b, LocalPlace(2), use(Copy(src)))
	})
	f := newFixture()
	b := f.showBody()
	b.GenericCount = 1
	local(b, f.tab.Param(0))
	wantOK(t, f, b, "a generic local in a generic body")
}

// TestM3DowncastAndPayload: invariant M3 for enum downcasts and payloads.
func TestM3DowncastAndPayload(t *testing.T) {
	enumOf := func(f *fixture) types.TypeID {
		m := f.items.Get(f.load).Module
		id := f.items.AddItem(&types.Item{Module: m, Name: f.in.Intern("Shape"), Kind: types.KindEnum, Key: types.ItemKey("main", "Shape"),
			Decl: &types.EnumDecl{Variants: []types.VariantDecl{{Fields: []types.FieldDecl{{Type: types.I32}}}, {}}}})
		return f.tab.Named(id, nil)
	}
	f := newFixture()
	b := f.showBody()
	opt := local(b, f.tab.Optional(types.I32))
	ot := f.tab.Optional(types.I32)
	some := b.Place(opt, Projection{Kind: ProjDowncast, Sel: SelSome, Type: ot}, Projection{Kind: ProjField, A: 0, Type: types.I32})
	assign(b, LocalPlace(2), use(Copy(some)))
	en := enumOf(f)
	e := local(b, en)
	v0 := b.Place(e, Projection{Kind: ProjDowncast, Sel: SelEnum, A: 0, Type: en}, Projection{Kind: ProjField, A: 0, Type: types.I32})
	assign(b, LocalPlace(2), use(Copy(v0)))
	errs := f.sigs[f.load].Errors
	eu := local(b, f.tab.ErrorUnion(errs, types.I32))
	es := f.tab.ErrorSetType(errs)
	errSide := b.Place(eu, Projection{Kind: ProjDowncast, Sel: SelErr, Type: es})
	assign(b, LocalPlace(3), use(Copy(errSide)))
	wantOK(t, f, b, "some, variant and err downcasts")

	read := func(base func(f *fixture, b *Body) LocalID, steps ...Projection) func(f *fixture, b *Body) {
		return func(f *fixture, b *Body) {
			assign(b, LocalPlace(2), use(Copy(b.Place(base(f, b), steps...))))
		}
	}
	optLocal := func(f *fixture, b *Body) LocalID { return local(b, f.tab.Optional(types.I32)) }
	enumLocal := func(f *fixture, b *Body) LocalID { return local(b, enumOf(f)) }
	// (a) a variant downcast of an optional, yielding str.
	expectViolation(t, "M3", read(optLocal, Projection{Kind: ProjDowncast, Sel: SelEnum, A: 7, Type: types.Str}))
	// (b) a payload field with no downcast.
	expectViolation(t, "M3", read(optLocal, Projection{Kind: ProjField, A: 0, Type: types.Str}))
	// (c) a non-loop field of a type parameter.
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		b.GenericCount = 1
		read(func(f *fixture, b *Body) LocalID { return local(b, f.tab.Param(0)) }, Projection{Kind: ProjField, A: 99, Type: types.Str})(f, b)
	})
	// A some downcast whose payload has the wrong type.
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		ot := f.tab.Optional(types.I32)
		read(optLocal, Projection{Kind: ProjDowncast, Sel: SelSome, Type: ot}, Projection{Kind: ProjField, A: 0, Type: types.Str})(f, b)
	})
	// SelSome on an enum, a variant past the end, a field past the variant.
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		en := enumOf(f)
		read(func(*fixture, *Body) LocalID { return local(b, en) }, Projection{Kind: ProjDowncast, Sel: SelSome, Type: en})(f, b)
	})
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		en := enumOf(f)
		read(func(*fixture, *Body) LocalID { return local(b, en) }, Projection{Kind: ProjDowncast, Sel: SelEnum, A: 2, Type: en})(f, b)
	})
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		en := enumOf(f)
		read(func(*fixture, *Body) LocalID { return local(b, en) },
			Projection{Kind: ProjDowncast, Sel: SelEnum, A: 1, Type: en}, Projection{Kind: ProjField, A: 0, Type: types.I32})(f, b)
	})
	// A field of an enum without a downcast.
	expectViolation(t, "M3", read(enumLocal, Projection{Kind: ProjField, A: 0, Type: types.I32}))
	// SelErr must yield the error-set value, not the union.
	expectViolation(t, "M3", func(f *fixture, b *Body) {
		eu := f.tab.ErrorUnion(f.sigs[f.load].Errors, types.I32)
		l := local(b, eu)
		assign(b, LocalPlace(3), use(Copy(b.Place(l, Projection{Kind: ProjDowncast, Sel: SelErr, Type: eu}))))
	})
}

// TestM5CallValue: calls through a fn value are checked against the fn
// type, and a variadic callee still needs its fixed arguments.
func TestM5CallValue(t *testing.T) {
	viaValue := func(f *fixture, b *Body) {
		ft := f.tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.I64, Into: types.NoParam}}, Result: types.I32, Errors: f.sigs[f.load].Errors})
		fv := local(b, ft)
		b.Blocks[0].Term.Callee = Callee{Kind: CallValue, Value: Copy(LocalPlace(fv))}
	}
	f := newFixture()
	b := f.showBody()
	viaValue(f, b)
	wantOK(t, f, b, "a call through a fallible fn value with its edge")
	expectViolation(t, "M5", func(f *fixture, b *Body) { viaValue(f, b); b.Blocks[0].Term.HasErr = false })
	expectViolation(t, "M5", func(f *fixture, b *Body) {
		viaValue(f, b)
		b.Blocks[0].Term.Dest = LocalPlace(local(b, types.Bool))
	})
	expectViolation(t, "M5", func(f *fixture, b *Body) {
		viaValue(f, b)
		b.Blocks[0].Term.Args = b.AddArgs(Arg{Operand: Copy(LocalPlace(1))}, Arg{Operand: Copy(LocalPlace(1))})
	})
	expectViolation(t, "M5", func(f *fixture, b *Body) {
		s := f.sigs[f.load]
		s.Variadic = true
		s.Params = append(s.Params, types.Param{Name: f.in.Intern("y"), Type: types.I64})
	})
}

// TestM4RecordAggregate: invariant M4 for record aggregates.
func TestM4RecordAggregate(t *testing.T) {
	rec := func(f *fixture) types.TypeID {
		return f.tab.Record([]types.RecordField{{Name: f.in.Intern("a"), Type: types.I32}})
	}
	f := newFixture()
	b := f.showBody()
	r := local(b, rec(f))
	assign(b, LocalPlace(r), Rvalue{Kind: RvAggregate, Agg: AggRecord, Type: rec(f), Ops: b.AddOperands(Imm(types.I32, 1))})
	wantOK(t, f, b, "a record aggregate")
	expectViolation(t, "M4", func(f *fixture, b *Body) {
		assign(b, LocalPlace(local(b, types.Bool)), Rvalue{Kind: RvAggregate, Agg: AggRecord, Type: rec(f), Ops: b.AddOperands(Imm(types.I32, 1))})
	})
	expectViolation(t, "M4", func(f *fixture, b *Body) {
		assign(b, LocalPlace(local(b, rec(f))), Rvalue{Kind: RvAggregate, Agg: AggRecord, Type: rec(f),
			Ops: b.AddOperands(Imm(types.I32, 1), Imm(types.I32, 2))})
	})
	expectViolation(t, "M4", func(f *fixture, b *Body) {
		s := local(b, types.Str)
		assign(b, LocalPlace(local(b, rec(f))), Rvalue{Kind: RvAggregate, Agg: AggRecord, Type: rec(f), Ops: b.AddOperands(Copy(LocalPlace(s)))})
	})
	expectViolation(t, "M4", func(f *fixture, b *Body) {
		assign(b, LocalPlace(local(b, types.Bool)), Rvalue{Kind: RvAggregate, Agg: AggSome, Ops: b.AddOperands(Imm(types.I32, 1))})
	})
	expectViolation(t, "M4", func(f *fixture, b *Body) {
		assign(b, LocalPlace(local(b, f.tab.Fn(types.FnType{Result: types.Bool}))), Rvalue{Kind: RvFnRef, Fn: FnRef{Item: f.load}})
	})
}

// TestM9ElementInsideLoop: an element loan lies inside its loop's blocks
// (reachable from the header without passing the exit).
func TestM9ElementInsideLoop(t *testing.T) {
	elem := func(f *fixture, b *Body, block BlockID) {
		r := local(b, f.tab.Ref(types.I64))
		b.Blocks[block].Stmts = append(b.Blocks[block].Stmts, Statement{Kind: StAssign, Place: LocalPlace(r),
			Rvalue: Rvalue{Kind: RvRef, Origin: OriginLoopElement, Loop: 0, Place: LocalPlace(1)}})
	}
	f := newFixture()
	b := f.showBody()
	b.Loops = append(b.Loops, LoopInfo{Header: 1, Exit: 2, Holder: NoLocal})
	elem(f, b, 1)
	wantOK(t, f, b, "an element loan in the loop header")
	expectViolation(t, "M9", func(f *fixture, b *Body) {
		b.Loops = append(b.Loops, LoopInfo{Header: 1, Exit: 2, Holder: NoLocal})
		elem(f, b, 0)
	})
}
