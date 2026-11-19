// Why: a try scope reads its error from Scope[E]'s err field, so the validator
// must know Scope[E]'s two fields.
// What: TestScopeOfErrorsFields over scopeWorld.
// How: invariant M3 reads Scope[E] as {hdr: Scope, err: E!void}, the layout
// check gives it; without Env.Known, or when std declares Scope with generics,
// the declaration decides.

package mir

import (
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// scopeWorld builds a body with one Scope[D] local and a temp per step under
// test: `_t = copy _1.<field>` with the step's type, where Scope is declared
// with one field (as std declares it, with no generics) and D and D2 are
// errors. It returns the table, Known, Scope[D], {D}, Scope and {D2}.
func scopeWorld(t *testing.T, generics int) (*types.Table, *types.KnownItems, types.TypeID, types.ErrorSetID, types.ItemID, types.ErrorSetID) {
	t.Helper()
	in := source.NewInterner()
	items := types.NewItems()
	tab := types.NewTable(in, items, types.NewConstTable())
	m := items.AddModule(&types.Module{Path: "std.core"})
	decl := &types.StructDecl{View: true, Fields: []types.FieldDecl{{Name: in.Intern("words"), Type: types.U64}}}
	for i := 0; i < generics; i++ {
		decl.Generics = append(decl.Generics, types.GenericParam{Name: in.Intern("E")})
	}
	scope := items.AddItem(&types.Item{Module: m, Name: in.Intern("Scope"), Kind: types.KindStruct, Exported: true,
		Key: types.ItemKey("std.core", "Scope"), Decl: decl})
	d := items.AddItem(&types.Item{Module: m, Name: in.Intern("D"), Kind: types.KindError, Key: types.ItemKey("std.core", "D"),
		Decl: &types.ErrorDecl{}})
	d2 := items.AddItem(&types.Item{Module: m, Name: in.Intern("D2"), Kind: types.KindError, Key: types.ItemKey("std.core", "D2"),
		Decl: &types.ErrorDecl{}})
	es := tab.ErrorSet([]types.ErrorMember{{Item: d}})
	st := tab.Named(scope, []types.GenericArg{types.TypeArg(tab.ErrorSetType(es))})
	return tab, &types.KnownItems{Scope: scope}, st, es, scope, tab.ErrorSet([]types.ErrorMember{{Item: d2}})
}

// readField validates `_2 = copy _1.<a>` with the step typed want, _1 a
// scope local of type st and _2 a temp of type want.
func readField(tab *types.Table, known *types.KnownItems, st types.TypeID, a uint32, want types.TypeID) error {
	b := NewBody(types.NoItem, source.Span{File: 1, Start: 0, End: 10}, types.Void)
	sp := b.AddSpan(source.Span{File: 1, Start: 2, End: 8})
	s := b.AddLocal(Local{Type: st, Kind: LocalScope, Loop: NoLoop, Span: sp})
	x := b.AddLocal(Local{Type: want, Kind: LocalTemp, Loop: NoLoop, Span: sp})
	b0 := b.AddBlock(OriginBody, NoDefer)
	b.Blocks[b0].Stmts = []Statement{
		{Kind: StAssign, Place: LocalPlace(x), Span: sp,
			Rvalue: Rvalue{Kind: RvUse, A: Copy(b.Place(s, Projection{Kind: ProjField, A: a, Type: want}))}},
		{Kind: StScopeEnd, Local: s, Span: sp},
	}
	b.Blocks[b0].Term = Terminator{Kind: TermReturn, Exit: ExitNormal, Span: sp}
	return b.Validate(&Env{Types: tab, Known: known})
}

// TestScopeOfErrorsFields: field 1 of Scope[E] is E!void and field 0 the
// plain Scope; any other index or type is M3. Without Env.Known, or when
// std declares Scope with generics of its own, M3 reads the declaration.
func TestScopeOfErrorsFields(t *testing.T) {
	tab, known, st, es, scope, other := scopeWorld(t, 0)
	errT := tab.ErrorUnion(es, types.Void)
	plain := tab.Named(scope, nil)
	for _, c := range []struct {
		name string
		a    uint32
		want types.TypeID
		msg  string // "" accepts
	}{
		{"err field, E!void", 1, errT, ""},
		{"hdr field, plain Scope", 0, plain, ""},
		{"err field of another type", 1, tab.ErrorUnion(es, types.I32), "M3:"},
		{"err field of another set", 1, tab.ErrorUnion(other, types.Void), "M3:"},
		{"hdr field typed Scope[E]", 0, st, "M3:"},
		{"field 2", 2, types.U64, "which has 2"},
	} {
		err := readField(tab, known, st, c.a, c.want)
		switch {
		case c.msg == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.msg != "" && (err == nil || !strings.Contains(err.Error(), c.msg)):
			t.Errorf("%s: want %q, got %v", c.name, c.msg, err)
		}
	}
	// Without Known the declared fields decide (one field).
	if err := readField(tab, nil, st, 1, errT); err == nil || !strings.Contains(err.Error(), "field 1 of") {
		t.Errorf("no Known: want the declared-fields M3, got %v", err)
	}
	// Plain Scope keeps its declared fields.
	if err := readField(tab, known, plain, 0, types.U64); err != nil {
		t.Errorf("plain Scope's declared field refused: %v", err)
	}
	if err := readField(tab, known, plain, 1, errT); err == nil || !strings.Contains(err.Error(), "M3:") {
		t.Errorf("plain Scope has no err field: %v", err)
	}
	// A Scope that std declares with a generic of its own is an ordinary
	// generic struct: its declared fields decide.
	tab, known, st, es, _, _ = scopeWorld(t, 1)
	if err := readField(tab, known, st, 1, tab.ErrorUnion(es, types.Void)); err == nil || !strings.Contains(err.Error(), "field 1 of") {
		t.Errorf("generic Scope declaration: want the declared-fields M3, got %v", err)
	}
}
