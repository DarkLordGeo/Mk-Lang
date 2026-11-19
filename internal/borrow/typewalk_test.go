// Why: type walks decide what a reference reaches, and every loan rule rests
// on their answers.
// What: tests of type walks, abstract nodes and their modes, the reads a walk
// records, the inline layout of Scope[E] and Subst.
// How: walkerOf builds a walker over the kit's types; each test checks one
// walk's result.

package borrow

import (
	"testing"

	"mk/internal/types"
)

func walkerOf(k *kit, rs *types.ReadSet) *walker {
	return newWalker(k.env(nil), rs)
}

// p: mut &mut i32 gives p.outer exclusive, p.inner shared, p#2's
// types {mut i32, i32}, not writable.
func TestRefStepsAndNodeModes(t *testing.T) {
	k := newKit(t)
	w := walkerOf(k, nil)
	mi := k.tab.Mut(types.I32)
	pt := k.tab.Mut(k.tab.Ref(mi))
	n1, n2, _ := w.abstractNodes(pt)
	if !n1.excl || n2.excl {
		t.Fatalf("modes: outer excl %v (want true), inner excl %v (want false)", n1.excl, n2.excl)
	}
	if len(n1.types) != 1 || n1.types[0] != k.tab.Ref(mi) {
		t.Fatalf("p#1 types %v", n1.types)
	}
	if len(n2.types) != 2 || n2.types[0] != mi || n2.types[1] != types.I32 {
		t.Fatalf("p#2 types %v, want [mut i32, i32]", n2.types)
	}
	if _, writable, _, _ := w.nodeFlags(&n2); writable {
		t.Fatalf("p#2 of mut &mut i32 must not be writable by type")
	}
	// A view, a KParam or Self adds (exclusive, ANY).
	if s := w.refSteps(k.tab.Param(0)).steps; len(s) != 1 || !s[0].any || !s[0].excl {
		t.Fatalf("refSteps(T) = %+v", s)
	}
	text := k.named("Text")
	if s := w.refSteps(text).steps; len(s) < 2 || !s[0].any {
		t.Fatalf("refSteps(Text) = %+v: a view adds ANY and still recurses its fields", s)
	}
	// str is (shared, u8).
	if s := w.refSteps(types.Str).steps; len(s) != 1 || s[0].excl || s[0].referent != types.U8 {
		t.Fatalf("refSteps(str) = %+v", s)
	}
}

// Recursive types terminate: Node { value: i32, link: ?mut Node },
// Tie { a: Cell[?&Tie] }.
func TestRecursiveTypesTerminate(t *testing.T) {
	k := newKit(t)
	node := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Node"), Kind: types.KindStruct,
		Key: types.ItemKey("main", "Node")})
	nodeT := k.tab.Named(node, nil)
	k.items.Get(node).Decl = &types.StructDecl{Fields: []types.FieldDecl{{Name: k.in.Intern("value"), Type: types.I32},
		{Name: k.in.Intern("link"), Type: k.tab.Optional(k.tab.Mut(nodeT))}}}
	tie := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Tie"), Kind: types.KindStruct,
		Key: types.ItemKey("main", "Tie")})
	tieT := k.tab.Named(tie, nil)
	k.items.Get(tie).Decl = &types.StructDecl{Fields: []types.FieldDecl{{Name: k.in.Intern("a"),
		Type: k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(k.tab.Optional(k.tab.Ref(tieT)))})}}}
	w := walkerOf(k, nil)
	for _, ty := range []types.TypeID{nodeT, tieT} {
		w.holdsView(ty)
		w.holdsMutexNonPlain(ty)
		w.refSteps(ty)
		w.abstractNodes(k.tab.Mut(ty))
	}
	if !w.writableByType(tieT) {
		t.Fatalf("Tie holds a Cell inline: writable by type")
	}
	n1, n2, _ := w.abstractNodes(k.tab.Mut(nodeT))
	if len(n1.types) != 1 || !n2.excl {
		t.Fatalf("mut Node: p#1 %v, p#2 excl %v (want true: link is ?mut Node)", n1.types, n2.excl)
	}
}

// holdsMutexNonPlain sees Mutex[Res] inline, not Mutex[i32], and not
// behind a reference.
func TestHoldsMutexNonPlain(t *testing.T) {
	k := newKit(t)
	w := walkerOf(k, nil)
	mres := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(k.Res)})
	mi := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(types.I32)})
	if !w.holdsMutexNonPlain(mres) || w.holdsMutexNonPlain(mi) {
		t.Fatalf("Mutex[Res] %v (want true), Mutex[i32] %v (want false)", w.holdsMutexNonPlain(mres), w.holdsMutexNonPlain(mi))
	}
	if !w.holdsMutexNonPlain(k.tab.Array(k.lenArg(2), mres)) {
		t.Fatalf("[2]Mutex[Res] holds one inline")
	}
	if w.holdsMutexNonPlain(k.tab.Ref(mres)) {
		t.Fatalf("&Mutex[Res] holds none inline")
	}
}

// Scope[E] is {hdr: Scope, err: E!void}, never its declaration.
func TestScopeOfErrorsInline(t *testing.T) {
	k := newKit(t)
	es := k.errs("Closed")
	se := k.tab.Named(k.known.Scope, []types.GenericArg{types.TypeArg(k.tab.ErrorSetType(es))})
	w := walkerOf(k, nil)
	fs := w.inlineFields(se)
	if len(fs) != 2 || fs[0] != k.Scope || fs[1] != k.tab.ErrorUnion(es, types.Void) {
		t.Fatalf("inline(Scope[E]) = %v", fs)
	}
}

// A walk records ReadFields "all" for every declaration it reads and
// ReadShape for every named type whose facts it consults, nested ones
// included.
func TestReadsRecordWalkedDecls(t *testing.T) {
	k := newKit(t)
	mi := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(types.I32)})
	k.structItem(k.main, "W", 0, false, false, field("m", mi))
	var rs types.ReadSet
	w := walkerOf(k, &rs)
	w.holdsMutexNonPlain(k.named("W"))
	want := map[types.Read]bool{
		{Kind: types.ReadFields, Key: types.ItemKey("main", "W"), Name: "all"}: false,
		{Kind: types.ReadShape, Key: types.ItemKey("std.core", "Mutex")}:       false,
	}
	for _, r := range rs.Sorted() {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for r, seen := range want {
		if !seen {
			t.Errorf("missing read %v %s %q; got %v", r.Kind, r.Key, r.Name, rs.Sorted())
		}
	}
}

// subst replaces generic parameters through every constructor.
func TestSubst(t *testing.T) {
	k := newKit(t)
	w := walkerOf(k, nil)
	p0 := k.tab.Param(0)
	src := k.tab.Optional(k.tab.Mut(k.named("List", p0)))
	got := w.subst(src, []types.GenericArg{types.TypeArg(types.Str)})
	want := k.tab.Optional(k.tab.Mut(k.named("List", types.Str)))
	if got != want {
		t.Fatalf("subst: got %s, want %s", k.tab.TypeString(got, nil), k.tab.TypeString(want, nil))
	}
}
