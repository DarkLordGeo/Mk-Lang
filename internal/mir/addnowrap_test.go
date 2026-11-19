// Why: codegen emits nsw or nuw arithmetic from AddNoWrap, and static slices
// from array aggregates of slice type.
// What: TestAddNoWrap and TestStaticSliceAggregate.
// How: valid forms validate and print by name; float operands, operators past
// the last and wrong operand types are invariant M4.

package mir

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// addNoWrapBody is Show with one more statement in bb1: _4 = AddNoWrap(copy
// _1, 1) over a fresh local of type ty, whose parameter is i64 unless ty is a
// float, when the operands are that float.
func addNoWrapBody(f *fixture, ty types.TypeID) *Body {
	b := f.showBody()
	sp := b.Blocks[1].Term.Span
	a := b.AddLocal(Local{Type: ty, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	c := b.AddLocal(Local{Type: ty, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	src := Operand(Copy(LocalPlace(1)))
	if ty != types.I64 {
		src = Copy(LocalPlace(a))
	}
	st := Statement{Kind: StAssign, Place: LocalPlace(c), Span: sp,
		Rvalue: Rvalue{Kind: RvBinary, Bin: AddNoWrap, A: src, B: Imm(ty, 1), Type: ty}}
	b.Blocks[1].Stmts = append([]Statement{st}, b.Blocks[1].Stmts...)
	return b
}

// TestAddNoWrap: an integer AddNoWrap validates and prints by name; a
// float one is M4; an operator past the last is M4; AddNoWrap is no compare,
// while Eq to Ge still are.
func TestAddNoWrap(t *testing.T) {
	f := newFixture()
	b := addNoWrapBody(f, types.I64)
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("integer AddNoWrap refused: %v", err)
	}
	if got := (&Printer{Types: f.tab, Items: f.items}).Print(b); !strings.Contains(got, "AddNoWrap(copy _1, ") {
		t.Errorf("printer does not name AddNoWrap:\n%s", got)
	}
	f = newFixture()
	b = addNoWrapBody(f, types.F64)
	if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "M4: ") || !strings.Contains(err.Error(), "not an integer") {
		t.Errorf("float AddNoWrap: want M4 not an integer, got %v", err)
	}
	f = newFixture()
	b = addNoWrapBody(f, types.I64)
	b.Blocks[1].Stmts[0].Rvalue.Bin = MulNoWrap + 1
	if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "unknown binary operator") {
		t.Errorf("operator past MulNoWrap: want M4 unknown binary operator, got %v", err)
	}
	if AddNoWrap.IsCompare() {
		t.Error("AddNoWrap.IsCompare() is true")
	}
	for op := Eq; op <= Ge; op++ {
		if !op.IsCompare() {
			t.Errorf("BinOp %d is not a compare", op)
		}
	}
	if Ge != 18 || AddNoWrap != 19 {
		t.Errorf("BinOp numbering moved: Ge %d, AddNoWrap %d (iface encodes it)", Ge, AddNoWrap)
	}
}

// staticSliceBody prepends `_n = [ops...]` of aggregate type ty to Show's
// success block.
func staticSliceBody(f *fixture, ty types.TypeID, ops ...Operand) *Body {
	b := f.showBody()
	sp := b.Blocks[1].Term.Span
	d := b.AddLocal(Local{Type: ty, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	st := Statement{Kind: StAssign, Place: LocalPlace(d), Span: sp,
		Rvalue: Rvalue{Kind: RvAggregate, Agg: AggArray, Type: ty, Ops: b.AddOperands(ops...)}}
	b.Blocks[1].Stmts = append([]Statement{st}, b.Blocks[1].Stmts...)
	return b
}

// TestStaticSliceAggregate: an AggArray whose Type is a []T validates
// with any number of operands of T (a static slice), refuses an
// operand of another type, and a mut []T aggregate stays M4.
func TestStaticSliceAggregate(t *testing.T) {
	f := newFixture()
	b := staticSliceBody(f, f.tab.Slice(types.I64), Imm(types.I64, 1), Imm(types.I64, 2), Imm(types.I64, 3))
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("[]i64 aggregate of three i64 refused: %v", err)
	}
	f = newFixture()
	b = staticSliceBody(f, f.tab.Slice(types.I64))
	if err := b.Validate(f.env()); err != nil {
		t.Fatalf("empty []i64 aggregate refused: %v", err)
	}
	f = newFixture()
	b = staticSliceBody(f, f.tab.Slice(types.I64), Imm(types.I64, 1), Imm(types.I32, 2))
	if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "element 1 is") {
		t.Errorf("[]i64 aggregate with an i32 element: want M4 element 1, got %v", err)
	}
	f = newFixture()
	b = staticSliceBody(f, f.tab.MutSlice(types.I64), Imm(types.I64, 1))
	if err := b.Validate(f.env()); err == nil || !strings.Contains(err.Error(), "array aggregate of") {
		t.Errorf("mut []i64 aggregate: want M4 array aggregate, got %v", err)
	}
}
