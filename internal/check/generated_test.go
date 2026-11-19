// Why: three rules about unusual call forms: Arena.Slice[T] needs a
// zero-valid T, str.FromC in namespace form needs unsafe, and a
// const-parameter function is no fn value; and generated impls must cite
// their gen lines.
// What: TestE1101_StrFromCNamespaceForm, TestArenaSliceZeroValid,
// TestE0705_ConstParamFnValue, TestE0704_Generated*, TestGenDeclKeptFromStage4.
// How: checks small programs, some with gen-emitted text, and compares codes
// and the diagnostics' facts.

package check

import (
	"fmt"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// generatedCore is the test prelude with the std declarations these rules key on.
func generatedCore() string {
	return fakePrelude() +
		"export Arena.Slice[T](&self, n: int): OutOfMemory!mut []T\n" +
		"export str.FromC(p: *u8): str\n"
}

func runM4Cases(t *testing.T, cases []bodyCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBody(t, newWorldCore(t, true, generatedCore()), tc.want, tc.src)
		})
	}
}

// TestE1101_StrFromCNamespaceForm: `str.FromC(p)` is a call
// of a type's function in namespace form, and needs `unsafe` as a call by
// name does.
func TestE1101_StrFromCNamespaceForm(t *testing.T) {
	runM4Cases(t, []bodyCase{
		{"outside unsafe", "F(p: *u8): str {\n\treturn str.FromC(p)\n}\n", "E1101@2:9"},
		{"in unsafe", "F(p: *u8): str {\n\tunsafe \"p is a C string\" {\n\t\treturn str.FromC(p)\n\t}\n}\n", ""},
	})
}

const zvDecls = "struct Pt {\n\tx: int\n\ty: i32\n}\n\nlinear struct L {\n\tn: int\n}\n\nstruct R {\n\tr: &int\n}\n\n" +
	"enum Two {\n\tA\n\tB\n}\n\nenum Color: u8 {\n\tRed = 1\n\tGreen = 2\n}\n\nenum Sh {\n\tC(r: &int)\n\tE\n}\n\n" +
	"struct Pos {\n\tn: int\n\tinvariant n > 0\n}\n\n"

// zvCase is a body calling a.Slice[ty](1); the call is on line 35.
func zvCase(name, ty, want string) bodyCase {
	return bodyCase{name, zvDecls + "F(a: &Arena): OutOfMemory!void {\n\t_ = try a.Slice[" + ty + "](1)\n}\n", want}
}

// TestArenaSliceZeroValid: Arena.Slice zero-fills, so its T must be
// a type whose all-zero bytes are a value, else E0701 at the call.
func TestArenaSliceZeroValid(t *testing.T) {
	const at = "E0701@35:12"
	runM4Cases(t, []bodyCase{
		zvCase("u8", "u8", ""),
		zvCase("bool", "bool", ""),
		zvCase("char", "char", ""),
		zvCase("f64", "f64", ""),
		zvCase("raw pointer", "*int", ""),
		zvCase("struct of integers", "Pt", ""),
		zvCase("optional reference", "?&int", ""),
		zvCase("optional str", "?str", ""),
		zvCase("array", "[4]u8", ""),
		zvCase("record", "(a: int, b: bool)", ""),
		zvCase("enum with a zero discriminant", "Two", ""),
		zvCase("error union of int", "OutOfMemory!int", ""),
		zvCase("reference", "&int", at),
		zvCase("str", "str", at),
		zvCase("slice", "[]u8", at),
		zvCase("fn", "fn(int): int", at),
		zvCase("linear struct", "L", at),
		zvCase("struct with a reference", "R", at),
		zvCase("array of references", "[2]&int", at),
		zvCase("enum without a zero discriminant", "Color", at),
		zvCase("enum whose zero variant holds a reference", "Sh", at),
		zvCase("struct with an invariant", "Pos", at),
		zvCase("error union of str", "OutOfMemory!str", at),
		{"type parameter", zvDecls + "F[T](a: &Arena): OutOfMemory!void {\n\t_ = try a.Slice[T](1)\n}\n", at},
	})
}

// TestE0705_ConstParamFnValue: a function with a `const` value parameter
// used as a fn value is E0705 at the use; a direct call, and a function
// whose const is a generic argument, are not.
func TestE0705_ConstParamFnValue(t *testing.T) {
	const twice = "Twice(const k: int): int {\n\treturn k * 2\n}\n\nGet[const N: int](): int {\n\treturn N\n}\n\nstruct P {\n\tx: int\n}\n\nP.Scale(&self, const k: int): int {\n\treturn self.x * k\n}\n\n"
	runM4Cases(t, []bodyCase{
		{"by name", twice + "F(): int {\n\tf := Twice\n\treturn f(3)\n}\n", "E0705@18:7"},
		{"as an argument", twice + "Apply(f: fn(int): int): int {\n\treturn f(1)\n}\n\nF(): int {\n\treturn Apply(Twice)\n}\n", "E0705@22:15"},
		{"namespace form", twice + "F(p: &P): int {\n\tg := P.Scale\n\treturn g(p, 2)\n}\n", "E0705@18:7"},
		{"direct call", twice + "F(): int {\n\treturn Twice(3)\n}\n", ""},
		{"const generic is a value", twice + "F(): int {\n\tf := Get[3]\n\treturn f()\n}\n", ""},
	})
}

// TestE0704_GeneratedDuplicateCitesGenLine: two `gen` lines emitting one
// impl are E0704 citing both lines. The diagnostic sits in the generated
// text, which gen.Wrap re-anchors at the second line; its `first` fact is
// the first impl's gen line, and `first_output` the first impl's generated
// text. A handwritten first impl keeps `first` at its declaration.
func TestE0704_GeneratedDuplicateCitesGenLine(t *testing.T) {
	const src = "interface I {\n}\n\nstruct S {\n}\n\nimpl I for S {\n}\n\nimpl I for S {\n}\n"
	for _, generated := range []bool{false, true} {
		w := newWorld(t, true)
		b := w.add("m", opt{stopBefore: true}, src)
		var first types.ItemID
		for _, ii := range b.info.Items {
			if w.items.Get(ii.Item).Kind == types.KindImpl {
				first = ii.Item
			}
		}
		if first == types.NoItem {
			t.Fatal("no impl item")
		}
		it := w.items.Get(first)
		gen := source.Span{File: it.Span.File, Start: 0, End: 9}
		if generated {
			it.Origin = &types.GenOrigin{Output: it.Span, Emitter: it.Span, Gen: gen}
		}
		b.m = w.c.Signatures(&ModuleInput{Module: b.id, Files: b.files, Info: b.info})
		var got []string
		for _, d := range b.diags() {
			if d.Code == diag.E0704 {
				got = append(got, fact(d, "reason")+" first="+fact(d, "first")+" output="+fact(d, "first_output"))
			}
		}
		want := fmt.Sprintf("duplicate first=@%d output=", it.Span.Start)
		if generated {
			want = fmt.Sprintf("duplicate first=@%d output=@%d", gen.Start, it.Span.Start)
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("generated=%v: E0704s %q, want [%q]\n%s", generated, got, want, dump(w, b.diags()))
		}
	}
}

// TestE0704_GeneratedOrphanAtGenLine is check's half of the orphan
// case (tests/conformance/codes/E0704_gen_orphan.mk, which check's corpus
// stand-in excepts because it runs no generator): an impl in generated text
// for an interface and a type the module declares neither of is E0704
// `reason: orphan`, at the impl in the generated text (gen.Wrap re-anchors
// it at the gen line), whether or not the impl came from a generator.
func TestE0704_GeneratedOrphanAtGenLine(t *testing.T) {
	const lib = "export interface I {\n}\n\nexport struct S {\n}\n"
	for _, generated := range []bool{false, true} {
		w := newWorld(t, true)
		w.add("lib", opt{}, lib)
		b := w.add("m", opt{stopBefore: true}, "import lib\n\nimpl lib.I for lib.S {\n}\n")
		var impl types.ItemID
		for _, ii := range b.info.Items {
			if w.items.Get(ii.Item).Kind == types.KindImpl {
				impl = ii.Item
			}
		}
		if impl == types.NoItem {
			t.Fatal("no impl item")
		}
		it := w.items.Get(impl)
		if generated {
			it.Origin = &types.GenOrigin{Output: it.Span, Emitter: it.Span, Gen: source.Span{File: it.Span.File, Start: 0, End: 10}}
		}
		b.m = w.c.Signatures(&ModuleInput{Module: b.id, Files: b.files, Info: b.info})
		var got []string
		for _, d := range b.diags() {
			if d.Code == diag.E0704 {
				got = append(got, fmt.Sprintf("%s@%d first=%s", fact(d, "reason"), d.At.Start, fact(d, "first")))
			}
		}
		want := fmt.Sprintf("orphan@%d first=", it.Span.Start)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("generated=%v: E0704s %q, want [%q]\n%s", generated, got, want, dump(w, b.diags()))
		}
	}
}

// TestGenDeclKeptFromStage4: the GenDecl gen writes at stage 4 (its Args
// included) survives stage 5, and check reports none of the gen line's
// codes then; a line gen did not bind still gets check's own declaration.
func TestGenDeclKeptFromStage4(t *testing.T) {
	const lib = "export struct Gen {\n\tp: *u8\n}\n\nexport Eq(g: mut Gen, t: int) {\n}\n"
	for _, staged := range []bool{false, true} {
		w := newWorld(t, true)
		l := w.add("derive", opt{}, lib)
		b := w.add("m", opt{stopBefore: true}, "import derive\n\nstruct User {\n}\n\ngen derive.Eq(User) fuel -1\n")
		var line types.ItemID
		for _, ii := range b.info.Items {
			if w.items.Get(ii.Item).Kind == types.KindGen {
				line = ii.Item
			}
		}
		if line == types.NoItem {
			t.Fatal("no gen item")
		}
		want := &types.GenDecl{Generator: l.item("Eq"), Args: []types.GenArg{{}}, Fuel: 7}
		if staged {
			w.items.Get(line).Decl = want
		}
		b.m = w.c.Signatures(&ModuleInput{Module: b.id, Files: b.files, Info: b.info})
		got, _ := w.items.Get(line).Decl.(*types.GenDecl)
		var e0705 int
		for _, d := range b.m.Diags {
			if d.Code == diag.E0705 {
				e0705++
			}
		}
		switch {
		case staged && (got != want || e0705 != 0):
			t.Fatalf("staged: Decl %+v (kept %v), %d E0705; want gen's, none", got, got == want, e0705)
		case !staged && (got == nil || got == want || e0705 != 1):
			t.Fatalf("unstaged: Decl %+v, %d E0705; want check's own and one E0705 for fuel -1", got, e0705)
		}
	}
}
