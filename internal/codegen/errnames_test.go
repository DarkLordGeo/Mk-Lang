// Why: the support pack's ErrorName table should name only the errors a
// program can make, or hello world carries every std error's name.
// What: tests of errnames.go: a row for each error the program can make,
// none for an error it never makes, and a row for an error that lives only
// in another error's payload.
// How: small units make chosen errors, and the emitted table's rows are read.

package codegen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// errorMaker is a unit of one body that makes an error of each item, so the
// support pack names each (the shape AggError has in a lowered `fail`).
func errorMaker(e *testEnv, items ...types.ItemID) *Unit {
	b := mir.NewBody(types.NoItem, source.Span{}, types.Void)
	var blk mir.Block
	for _, it := range items {
		l := b.AddLocal(mir.Local{Type: e.tt.ErrorSetType(e.tt.ErrorSet([]types.ErrorMember{{Item: it}})), Kind: mir.LocalTemp, Loop: mir.NoLoop})
		blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: b.Place(l),
			Rvalue: mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: it}})
	}
	b.Blocks = append(b.Blocks, blk)
	return &Unit{Funcs: []Func{{Symbol: "maker", Body: b}}}
}

// TestErrorNameOnlyLiveErrors: an error some body makes, or that a
// constant's type holds, has its row; one the program never makes has none.
func TestErrorNameOnlyLiveErrors(t *testing.T) {
	e := newTestEnv()
	made := e.errItem("Made")
	e.errItem("Never")
	held := e.stdErrItem("std.fs", "Held")
	e.stdErrItem("std.net", "Unreached")
	e.stdFn("std.rt", "", "ErrorName", 0, []types.TypeID{types.U32}, types.Str)
	u := errorMaker(e, made)
	// A constant operand of an error-set type: the evaluator's value may
	// hold any member of its set.
	b := u.Funcs[0].Body
	c := mir.Const(e.tt.ErrorSetType(e.tt.ErrorSet([]types.ErrorMember{{Item: held}})), types.NoConst)
	b.Operands = append(b.Operands, c)
	var sb strings.Builder
	if err := EmitSupport(u, e, Config{Target: types.X86_64Windows, RootModule: "main"}, &sb); err != nil {
		t.Fatal(err)
	}
	ll := sb.String()
	for _, want := range []string{`c"main.Made"`, `c"fs.Held"`, `c"unknown error"`} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %s in the support pack", want)
		}
	}
	for _, bad := range []string{"Never", "Unreached"} {
		if strings.Contains(ll, bad) {
			t.Errorf("the support pack names %s, which the program never makes", bad)
		}
	}
}

// TestErrorNameOfPayloadError: an error that lives only in another
// error's payload inside a constant keeps its row, so a panic with it
// prints its name. `Pick(&Boom, i)` with `const Boom: [2]Outer` and Outer's
// field `inner: Inner` reads Boom by an RvConstRef (or RvGlobalRef), and a
// const operand of an error-set type may hold a payload error too.
func TestErrorNameOfPayloadError(t *testing.T) {
	e := newTestEnv()
	inner := e.errItem("Inner")
	deep := e.errItem("Deep")
	e.errItem("Never")
	set := func(it types.ItemID) types.TypeID {
		return e.tt.ErrorSetType(e.tt.ErrorSet([]types.ErrorMember{{Item: it}}))
	}
	outer := e.errItem("Outer", types.Int, set(inner))
	wrap := e.errItem("Wrap", set(deep))
	e.stdFn("std.rt", "", "ErrorName", 0, []types.TypeID{types.U32}, types.Str)
	u := errorMaker(e)
	b := u.Funcs[0].Body
	boom := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 2)}, set(outer))
	l := b.AddLocal(mir.Local{Type: boom, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	b.Blocks[0].Stmts = append(b.Blocks[0].Stmts, mir.Statement{Kind: mir.StAssign, Place: b.Place(l),
		Rvalue: mir.Rvalue{Kind: mir.RvConstRef, Item: outer, Type: boom}})
	b.Operands = append(b.Operands, mir.Const(set(wrap), types.NoConst))
	var sb strings.Builder
	if err := EmitSupport(u, e, Config{Target: types.X86_64Windows, RootModule: "main"}, &sb); err != nil {
		t.Fatal(err)
	}
	ll := sb.String()
	for _, want := range []string{`c"main.Outer"`, `c"main.Inner"`, `c"main.Wrap"`, `c"main.Deep"`} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %s in the support pack", want)
		}
	}
	if strings.Contains(ll, "Never") {
		t.Errorf("the support pack names Never, which the program never makes")
	}
}
