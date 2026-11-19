// Why: Print and Arena.Fmt are declared in Mk, and the compiler builds a
// thin body per call; without it every Print call reached codegen as a
// CallDirect of the bodiless Print ("unknown intrinsic std.core.Print").
// What: TestPrintSynthesized, TestPrintPlainWithoutRunner,
// TestPrintNamedConstFormat, TestFmtStraight_EveryEdge and
// TestFmtStraight_FieldWriter, over format.go.
// How: lowers calls against a fake prelude with the runner items (fmtCore)
// and checks the synthesized body's calls, error edges and spec words.

package lower

import (
	"strings"
	"testing"
)

// fmtCore is the fake prelude plus the format runner items, bodiless.
func fmtCore() string {
	return fakePrelude() + `export struct FmtPiece {
	export lit: str
	export arg: i32
	export spec: u32
}
export struct FmtArg {
	export p: *u8
	export f: fn(*u8, u32, mut Writer): WriteError!void
}
export FmtValue[T: Format](p: *u8, spec: u32, w: mut Writer): WriteError!void
export FmtRun(w: mut Writer, pieces: []FmtPiece, args: []FmtArg): WriteError!void
export FmtArena(a: &Arena, pieces: []FmtPiece, args: []FmtArg): OutOfMemory!str from a
export FmtSpecValue[T: Format](p: *u8, spec: u32, w: mut Writer): WriteError!void
export FmtPadRun(w: mut Writer, pieces: []FmtPiece, args: []FmtArg): WriteError!void
export FmtArenaPad(a: &Arena, pieces: []FmtPiece, args: []FmtArg): OutOfMemory!str from a
`
}

const rawStub = "export Of[T](r: &T): *T\nexport PtrCast[T, U](p: *T): *U\n"

// lowerFmt lowers src over fmtCore and a std.raw stub, Validate on.
func lowerFmt(t *testing.T, src string) *lowered {
	t.Helper()
	w := newWorldCore(t, true, fmtCore())
	w.add("std.raw", opt{std: true, noPrelude: true}, rawStub).wantClean(t)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	return lowerModule(t, w, b)
}

const fmtSrc = `struct S {
	export x: int
	y: int
	export z: int
}
Lit(w: mut Writer): WriteError!void {
	try Print(w, "fizz\n", ())
}
One(w: mut Writer, n: int): WriteError!void {
	try Print(w, "x{i}y{{}}\n", (i: n))
}
Specs(w: mut Writer, b: int, c: f64): WriteError!void {
	try Print(w, "{a} {b:x} {c:.2}", (c: c, b: b, a: 1))
}
Struct(w: mut Writer, s: S): WriteError!void {
	try Print(w, "{z}{x}", s)
}
Arena1(a: &Arena, n: int): OutOfMemory!str from a {
	s := try a.Fmt("{n}!", (n: n))
	return s
}
`

func TestPrintSynthesized(t *testing.T) {
	l := lowerFmt(t, fmtSrc)
	for _, k := range []string{"Lit", "One", "Specs", "Struct", "Arena1"} {
		d := l.print(k)
		if strings.Contains(d, "core.Print") || strings.Contains(d, "Arena:Fmt") || strings.Contains(d, "Arena.Fmt") {
			t.Fatalf("%s: the formatting call is still a call:\n%s", k, d)
		}
	}
	// An unpadded Print is straight-line: no runner, no piece or argument
	// array, no FmtArg (the header).
	for _, k := range []string{"Lit", "One", "Specs", "Struct"} {
		d := l.print(k)
		for _, bad := range []string{"FmtRun", "FmtPiece", "FmtArg", "const fn"} {
			if strings.Contains(d, bad) {
				t.Fatalf("%s: a straight-line Print names %s:\n%s", k, bad, d)
			}
		}
	}
	// No argument: one Writer.Str of the literal, with the call's error
	// edge, and no raw call.
	lit := l.print("Lit")
	wantIn(t, lit, `= call std.core.Writer:Str(reborrow copy _1, const str "fizz\n") -> bb2 err try _2 -> bb1`)
	if strings.Contains(lit, "std.raw") {
		t.Fatalf("Lit: no argument, yet a raw call:\n%s", lit)
	}
	// One hole: &ta.0, raw.Of, raw.PtrCast; then "x", FmtValue[int] on that
	// address with spec 0, "y{}\n" (braces unescaped), every call on the
	// same try edge.
	wantIn(t, l.print("One"), "_5 = copy _4", "= &_5.0", "call std.raw.Of[int](copy _6)", "_8 = call std.raw.PtrCast[int, u8](copy _7)",
		`call std.core.Writer:Str(reborrow copy _1, const str "x") -> bb4 err try _3 -> bb1`,
		"call std.core.FmtValue[int](copy _8, u32 0, reborrow copy _1) -> bb5 err try _3 -> bb1",
		`call std.core.Writer:Str(reborrow copy _1, const str "y{}\n") -> bb6 err try _3 -> bb1`, "_0 = ok()")
	// Specs: the record's canonical order is a, b, c, so the addresses are
	// _9 (a), _12 (b) and _15 (c); the holes' spec words are 0, 1 (hex) and
	// 514 (.2: 2 | 2<<8). A valued spec takes FmtSpecValue. The
	// empty first literal writes nothing, so it has no call.
	sp := l.print("Specs")
	wantIn(t, sp, "call std.core.FmtValue[int](copy _9, u32 0, reborrow copy _1)", `Writer:Str(reborrow copy _1, const str " ")`,
		"call std.core.FmtSpecValue[int](copy _12, u32 1, reborrow copy _1)", `Writer:Str(reborrow copy _1, const str " ")`,
		"call std.core.FmtSpecValue[f64](copy _15, u32 514, reborrow copy _1)")
	if strings.Contains(sp, `const str ""`) {
		t.Fatalf("Specs: the empty literal is written:\n%s", sp)
	}
	// Struct: only exported fields, in declaration order (x = entry 0 at
	// field 0, address _7; z = entry 1 at field 2, address _10); "{z}{x}"
	// calls z's then x's.
	st := l.print("Struct")
	wantIn(t, st, "= &_4.0", "= &_4.2", "call std.core.FmtValue[int](copy _10, u32 0", "call std.core.FmtValue[int](copy _7, u32 0")
	if strings.Contains(st, "&_4.1") {
		t.Fatalf("Struct: the unexported field is formatted:\n%s", st)
	}
	// Arena.Fmt keeps its runner, FmtArena, which builds the Text.
	wantIn(t, l.print("Arena1"), "@0 fn std.core.FmtValue[int]", `FmtPiece(const str "!", i32 4294967295, u32 0)`,
		"call std.core.FmtArena(copy _1, copy _15, copy _11)", "_0 = ok(")
}

// TestPrintPlainWithoutRunner: over the plain fake prelude (no FmtRun,
// no std.raw) Print stays a plain call, as KnownItems' "inert" rule says.
func TestPrintPlainWithoutRunner(t *testing.T) {
	l := lowerSrc(t, "One(w: mut Writer, n: int): WriteError!void {\n\ttry Print(w, \"{i}\", (i: n))\n}\n")
	wantIn(t, l.print("One"), "call std.core.Print[")
}

// TestPrintNamedConstFormat: the format string may be a named str const
// (check's formatText); its bytes reach the pieces the same way.
func TestPrintNamedConstFormat(t *testing.T) {
	l := lowerFmt(t, "const F: str = \"<{i}>\"\nOne(w: mut Writer, n: int): WriteError!void {\n\ttry Print(w, F, (i: n))\n}\n")
	wantIn(t, l.print("One"), `Writer:Str(reborrow copy _1, const str "<")`, "call std.core.FmtValue[int](",
		`Writer:Str(reborrow copy _1, const str ">")`)
}

// TestFmtStraight_EveryEdge: a straight-line Print takes the Print call's
// own error edge on every call, whatever consumes the error (try, else,
// .Must(), an E!T value), and its writer may be a borrowed local (mut w),
// which each call reborrows: the expansion validates, and names no runner.
func TestFmtStraight_EveryEdge(t *testing.T) {
	src := `Else(w: mut Writer, n: int): bool {
	Print(w, "a{n}b", (n)) else {
		return false
	}
	return true
}
Musted(w: mut Writer, n: int) {
	Print(w, "c{n}d", (n)).Must()
}
Value(w: mut Writer, n: int) {
	var r: WriteError!void = Print(w, "<{n}>", (n))
	match r {
		ok(_) {
		}
		err(_) {
		}
	}
}
Local(n: int): WriteError!void {
	var w := Writer()
	try Print(mut w, "x{n}y", (n))
}
`
	l := lowerFmt(t, src)
	for _, c := range []struct{ fn, edge string }{{"Else", "err else"}, {"Musted", "err must"}, {"Value", "err match"}, {"Local", "err try"}} {
		got := l.print(c.fn)
		if strings.Contains(got, "FmtRun") || strings.Contains(got, "FmtPiece") {
			t.Errorf("%s: the runner is called:\n%s", c.fn, got)
		}
		calls := strings.Count(got, "call std.core.Writer:Str(") + strings.Count(got, "call std.core.FmtValue[int](")
		if calls != 3 || strings.Count(got, c.edge) != 3 {
			t.Errorf("%s: want 3 calls each on a %q edge, got %d calls and %d edges:\n%s", c.fn, c.edge, calls, strings.Count(got, c.edge), got)
		}
	}
	// mut w: one borrow of the local, reborrowed by every call.
	loc := l.print("Local")
	if n := strings.Count(loc, "= mut _2"); n != 1 {
		t.Errorf("Local: %d borrows of w, want 1:\n%s", n, loc)
	}
	if n := strings.Count(loc, "(reborrow copy _"); n != 2 || strings.Count(loc, ", reborrow copy _") != 1 {
		t.Errorf("Local: every call should reborrow the one borrow:\n%s", loc)
	}
}

// TestFmtStraight_FieldWriter: a mut Writer field as the writer is read
// into one temporary in pass 1, as for any call, and every
// straight-line call reborrows that temporary; so a writer operand that is
// not a whole local, which keeps the runner, does not arise from source
// today (the fallback is defensive).
func TestFmtStraight_FieldWriter(t *testing.T) {
	src := `view struct Con {
	w: mut Writer
}
Field(c: mut Con, n: int): WriteError!void {
	try Print(c.w, "f{n}", (n))
}
`
	got := lowerFmt(t, src).print("Field")
	wantIn(t, got, "_4 = move (*_1).0", `call std.core.Writer:Str(reborrow copy _4, const str "f")`, "call std.core.FmtValue[int](copy _9, u32 0, reborrow copy _4)")
	if strings.Contains(got, "FmtRun") || strings.Count(got, "(*_1).0") != 1 {
		t.Errorf("the field is read more than once, or the runner is called:\n%s", got)
	}
}
