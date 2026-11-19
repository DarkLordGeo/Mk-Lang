// Why: some lowering shapes are reached by no other test, corpus body or
// fuzz seed; each is pinned here so a change to it is seen.
// What: TestIf_FoldedCondition, TestElse_StatementFallsThrough,
// TestSpawn_ThroughRefScope, TestMatch_ScrutineeForms,
// TestCleanupExpansionLinear and other single-shape tests.
// How: each lowers a small program with Validate on and checks its MIR text.

package lower

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// Shapes no other test, corpus body or fuzz seed reached (found with go test
// -coverprofile), each pinned with Validate on.

// TestIf_FoldedCondition is the folded if: a condition check folded to a
// bool lowers only the taken branch, with no switch.
func TestIf_FoldedCondition(t *testing.T) {
	l := lowerSrc(t, `const Debug: bool = false
const On: bool = true
G() {}
H() {}
A() {
	if Debug {
		G()
	} else {
		H()
	}
	if On {
		G()
	}
	if true {
		H()
	}
}
`)
	a := l.print("A")
	wantIn(t, a, "call m.H()", "call m.G()", "call m.H()")
	if strings.Contains(a, "switch") || strings.Count(a, "call m.G()") != 1 {
		t.Fatalf("a folded if lowers both branches or a switch:\n%s", a)
	}
}

// TestElse_StatementFallsThrough is the E!void ignore form of else: the
// handler's end and the success successor reach one join; a named handler
// binding ends inside the handler.
func TestElse_StatementFallsThrough(t *testing.T) {
	l := lowerSrc(t, `error Denied
F(): Denied!void {
	fail Denied
}
G() {}
Use(e: Denied) {}
B() {
	F() else {
		G()
	}
	G()
}
C(): Denied!void {
	F() else e {
		Use(e)
	}
	return
}
`)
	wantIn(t, l.print("B"), "call m.F() -> bb2 err else _1 -> bb1", "bb1:", "call m.G() -> bb3", "bb2:", "goto bb4", "bb3:", "goto bb4", "bb4:", "call m.G()")
	wantIn(t, l.print("C"), "err else _1 -> bb1", "call m.Use(copy _1) -> bb3", "bb3:", "scope_end _1", "goto bb4", "bb4:", "_0 = ok()", "return normal")
}

// TestSpawn_ThroughRefScope is s.go on a &Scope (a spawn goes into a scope
// local or a &Scope): a parameter is the spawn local itself; a field of one
// is copied into a &Scope temporary first.
func TestSpawn_ThroughRefScope(t *testing.T) {
	l := lowerSrc(t, `error E
Work(): E!void {
}
Spawn(s: &Scope[E]) {
	s.go Work()
}
struct Holder {
	s: &Scope[E]
}
Field(h: &Holder) {
	h.s.go Work()
}
`)
	wantIn(t, l.print("Spawn"), "call m.Work() -> bb1 spawn _1")
	wantIn(t, l.print("Field"), "let _2: &Scope[E]", "_2 = copy (*_1).0", "call m.Work() -> bb1 spawn _2")
}

// TestMatch_ScrutineeForms is matchStmt's scrutinee table: a &T parameter
// binds shared references through its Deref (z19), a mut T one mutable
// ones, a call result is matched in its temporary, &Make() borrows it.
func TestMatch_ScrutineeForms(t *testing.T) {
	l := lowerSrc(t, `enum Shape {
	Circle(r: int)
	Square(side: int)
}
Make(): Shape {
	return Shape.Circle(r: 1)
}
Set(r: mut int) {}
ByRef(s: &Shape): int {
	match s {
		.Circle(r) { return *r }
		.Square(side) { return *side }
	}
}
ByMut(s: mut Shape) {
	match s {
		.Circle(r) { Set(r) }
		.Square(side) { Set(side) }
	}
}
ByCall(): int {
	match Make() {
		.Circle(r) { return r }
		.Square(side) { return side }
	}
}
ByTempRef(): int {
	match &Make() {
		.Circle(r) { return *r }
		.Square(side) { return *side }
	}
}
`)
	wantIn(t, l.print("ByRef"), "Discriminant((*_1))", "_3 = &((*_1) as variant0).0", "_4 = &((*_1) as variant1).0")
	wantIn(t, l.print("ByMut"), "Discriminant((*_1))", "_3 = mut ((*_1) as variant0).0", "call m.Set(reborrow copy _3)")
	wantIn(t, l.print("ByCall"), "_1 = call m.Make()", "Discriminant(_1)", "_3 = copy (_1 as variant0).0")
	wantIn(t, l.print("ByTempRef"), "_1 = call m.Make()", "Discriminant(_1)", "_3 = &(_1 as variant0).0")
}

// TestDestructure_RenamedAndWildcard is destructuring with renamed
// fields and a wildcard: one whole temporary, takes of the named fields
// only.
func TestDestructure_RenamedAndWildcard(t *testing.T) {
	l := lowerSrc(t, `struct P {
	x: int
	y: int
	z: int
}
D(p: P): int {
	P(x: a, y: _, z: c) := p
	return a + c
}
`)
	d := l.print("D")
	wantIn(t, d, "_2 = copy _1", "_3 = copy _2.0", "_4 = copy _2.2", "scope_end _2", "Add(copy _3, copy _4)")
	if strings.Contains(d, "_2.1") {
		t.Fatalf("the wildcard field is read:\n%s", d)
	}
}

// TestConstFnArg_GenericBody pins today's shape of a const fn argument
// whose generic arguments mention the caller's parameters: Keep[T] inside
// Outer[T] is not a closed constant, so it is an RvFnRef temporary copied
// into the call, not an OpConst with RelocFn as a closed fn argument is;
// Keep[i32] in a closed body is one.
func TestConstFnArg_GenericBody(t *testing.T) {
	l := lowerSrc(t, `Keep[T](x: &T): bool {
	return true
}
Count[T](xs: []T, const keep: fn(&T): bool): int {
	return 0
}
Outer[T](xs: []T): int {
	return Count(xs, Keep[T])
}
Closed(xs: []i32): int {
	return Count(xs, Keep[i32])
}
`)
	wantIn(t, l.print("Outer"), "_3 = fn m.Keep[$0]", "call m.Count[$0](copy _2, copy _3)")
	wantIn(t, l.print("Closed"), "call m.Count[i32](copy _2, const fn(&i32): bool", "@0 fn m.Keep[i32]")
}

// TestOpenSizeOfAndEvalArena: an open SizeOf[T]() and AlignOf[T]() are
// RvSizeOf and RvAlignOf of $0 into int temporaries (before the fix in rvType
// the temporary took the measured type $0, which failed in mir, and was moved
// as a non-plain value), a closed one is check's folded constant, and
// EvalArena() in a const initializer is a CallBuiltin into an &Arena
// temporary. Every body validates (lowerSrc has Validate on).
func TestOpenSizeOfAndEvalArena(t *testing.T) {
	l := lowerSrc(t, `Size[T](): int {
	return SizeOf[T]() + AlignOf[T]()
}
Build(a: &Arena): int {
	return 1
}
const K: int = Build(EvalArena())
Closed(): int {
	return SizeOf[i64]()
}
`)
	wantIn(t, l.print("Size"), "let _1: int // temp", "let _2: int // temp", "_1 = SizeOf[$0]", "_2 = AlignOf[$0]", "Add(copy _1, copy _2)")
	wantIn(t, l.print("K"), "let _1: &Arena // temp", "_1 = call builtin EvalArena()", "call m.Build(copy _1)")
	c := l.print("Closed")
	wantIn(t, c, "_0 = int 8")
	if strings.Contains(c, "SizeOf") {
		t.Fatalf("a closed SizeOf is check's constant:\n%s", c)
	}
}

// TestCleanupExpansionLinear: cleanup copies cost O(exits x
// cleanup size), never copies of copies. F has three nested defers (body,
// loop, block) and k returns inside the innermost block; each return
// copies the three defers once, so the statement count is a + b*k exactly:
// the increment per added exit is the same from 1 to 2, 2 to 4 and 4 to 8.
// The nesting depth is fixed (three cleanups); a blow-up in depth alone
// (copies of copies per nested scope) is not what this measures.
func TestCleanupExpansionLinear(t *testing.T) {
	count := func(k int) int {
		var sb strings.Builder
		sb.WriteString("G() {}\nF(n: int) {\n\tdefer G()\n\tfor _ in 0..n {\n\t\tdefer G()\n\t\tif n > 100 {\n\t\t\tdefer G()\n")
		for j := 0; j < k; j++ {
			sb.WriteString("\t\t\tif n == " + itoa(j) + " {\n\t\t\t\treturn\n\t\t\t}\n")
		}
		sb.WriteString("\t\t}\n\t}\n}\n")
		return stmtCount(lowerSrc(t, sb.String()).body("F"))
	}
	s1, s2, s4, s8 := count(1), count(2), count(4), count(8)
	d := s2 - s1
	if d <= 0 || s4-s2 != 2*d || s8-s4 != 4*d {
		t.Fatalf("statements for 1, 2, 4, 8 exits: %d, %d, %d, %d; not linear (per exit %d)", s1, s2, s4, s8, d)
	}
}

// TestImplementerConstArg is implArgs over a const generic parameter
// (matchConst): impl[const N: int] Slice[int] for Buf[N] at Buf[4] calls
// Items with the argument 4.
func TestImplementerConstArg(t *testing.T) {
	l := lowerSrc(t, `struct Buf[const N: int] {
	p: *int
}
impl[const N: int] Slice[int] for Buf[N] {
	Items(&self): []int from self {
		Panic("x")
	}
	ItemsMut(mut self): mut []int from self {
		Panic("x")
	}
}
Sum(b: &Buf[4]): int {
	var s := 0
	for x in b {
		s += x
	}
	return s
}
`)
	wantIn(t, l.print("Sum"), "_3 = &holder(loop0) (*_1)", "Slice for m.Buf):Items[4](copy _3)", "_8 = copy _4[_6]")
}

// TestCheckGap_ScopeValueNotACall_Closed: `scope a := io.Scope { }` names a
// method without calling it. FuzzLower found it accepted silently
// (testdata/fuzz/FuzzLower/70b14f3db36c9913); check reports E0609 at the
// value, so the body never reaches lower.
func TestCheckGap_ScopeValueNotACall_Closed(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, `W() {
}
A(io: Io) {
	scope a := io.Scope {
		a.go W()
	}
}
`)
	if ds := b.diags(); len(ds) > 0 {
		t.Fatalf("stage-5 diagnostics:\n%s", dump(w, ds))
	}
	found := false
	for _, id := range b.m.Units() {
		for _, d := range w.c.Check(b.m, id).Diags {
			if d.Code == diag.E0609 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("check no longer reports E0609 for a scope over a method value")
	}
}
