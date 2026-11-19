// Why: a signature's flows clause may name sources field by field, and
// check must judge, store and print that form.
// What: TestPerFieldClauseChecks, TestPrivateFieldOfAnotherModule, TestFieldsAndUnion,
// TestE0608_PerField* (E0608 field by field).
// How: checks signatures and reads Flows.Fields, the fn types and the
// diagnostic text.

package check

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

const fldPair = "struct Pair {\n\ta: &i32\n\tb: &i32\n\tn: int\n}\n\n"

func TestPerFieldClauseChecks(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"two fields", fldPair + "F(x: &i32, y: &i32): Pair from (a: x, b: y) {\n\treturn Pair(a: x, b: y, n: 1)\n}\n", ""},
		{"several sources and a field left out", fldPair + "F(x: &i32, y: &i32): Pair from (a: x, y) {\n\treturn Pair(a: x, b: y, n: 1)\n}\n", ""},
		{"under ?T", fldPair + "F(x: &i32, y: &i32): ?Pair from (a: x, b: y) {\n\treturn none\n}\n", ""},
		{"under E!T", fldPair + "error Bad\n\nF(x: &i32, y: &i32): Bad!Pair from (a: x, b: y) {\n\tfail Bad\n}\n", ""},
		{"a record", "F(x: &i32, y: &i32): (p: &i32, q: &i32) from (p: x, q: y) {\n\treturn (p: x, q: y)\n}\n", ""},
		{"a generic struct", "struct Two[T] {\n\ta: T\n\tb: &i32\n}\n\nF[T](x: T, y: &i32): Two[T] from (a: x, b: y) {\n\treturn Two(a: x, b: y)\n}\n", ""},
		{"E0203 unknown field", fldPair + "F(x: &i32, y: &i32): Pair from (a: x, zz: y) {\n\treturn Pair(a: x, b: y, n: 1)\n}\n", "E0203@7:39"},
		{"E0203 on a reference result", "F(x: &i32): &i32 from (a: x) {\n\treturn x\n}\n", "E0203@1:24"},
		{"E0203 on a bare type parameter", "F[T](x: T): T from (a: x) {\n\treturn x\n}\n", "E0203@1:21"},
		{"E0206 a field named twice", fldPair + "F(x: &i32, y: &i32): Pair from (a: x, a: y) {\n\treturn Pair(a: x, b: x, n: 1)\n}\n", "E0206@7:39"},
		{"E0607 a field that holds no loans", fldPair + "F(x: &i32, y: &i32): Pair from (a: x, n: y) {\n\treturn Pair(a: x, b: x, n: 1)\n}\n", "E0607@7:39"},
		{"a view result", "view struct W {\n\ta: []u8\n\tb: []u8\n}\n\nF(x: []u8, y: []u8): W from (a: x, b: y) {\n\treturn W(a: x, b: y)\n}\n", ""},
		{"E0607 a view's raw-pointer field", "view struct V {\n\tp: *i32\n\tb: []u8\n}\n\nF(x: []u8, y: []u8, q: *i32): V from (p: x, b: y) {\n\treturn V(p: q, b: y)\n}\n", "E0607@6:39"},
		{"E0607 a by-value path source", fldPair + "F(x: Pair, y: &i32): Pair from (a: x.a) {\n\treturn Pair(a: y, b: y, n: 1)\n}\n", "E0607@7:36"},
	})
}

func TestPrivateFieldOfAnotherModule(t *testing.T) {
	w := newWorld(t, true)
	w.add("lib", opt{}, "export struct P {\n\texport a: &i32\n\tb: &i32\n}\n\nexport Mk(x: &i32, y: &i32): P from (a: x, b: y) {\n\treturn P(a: x, b: y)\n}\n")
	b := w.add("m", opt{}, "import lib\n\nexport Mk2(x: &i32, y: &i32): lib.P from (a: x, b: y) {\n\treturn lib.Mk(x, y)\n}\n")
	if got := codes(w, b.diags()); got != "E0203@3:49" {
		t.Errorf("a private field of another module's struct: %s, want E0203@3:49\n%s", got, dump(w, b.diags()))
	}
}

func TestFieldsAndUnion(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, fldPair+"F(x: &i32, y: &i32, z: &i32): Pair from (b: z, y, a: x) {\n\treturn Pair(a: x, b: y, n: 1)\n}\n\nG(x: &i32, y: &i32, z: &i32): Pair from (a: x, b: z, y) {\n\treturn Pair(a: x, b: y, n: 1)\n}\n")
	if ds := b.diags(); len(ds) != 0 {
		t.Fatal(codes(w, ds))
	}
	fl := w.items.Get(b.item("F")).Decl.(*types.FuncDecl).Sig.Flows
	if !fl.Declared || len(fl.Fields) != 2 || fl.Fields[0].Field != 0 || fl.Fields[1].Field != 1 {
		t.Fatalf("Fields = %+v, want a then b, sorted by field", fl.Fields)
	}
	if len(fl.Fields[0].Sources) != 1 || fl.Fields[0].Sources[0].Param != 1 || len(fl.Fields[1].Sources) != 2 {
		t.Errorf("field sources = %+v", fl.Fields)
	}
	if len(fl.Sources) != 3 || fl.Sources[0].Param != 1 || fl.Sources[1].Param != 3 || fl.Sources[2].Param != 2 {
		t.Errorf("Sources = %+v, want the union x, z, y in field order", fl.Sources)
	}
	// Clause order is not identity: the same clause written in another
	// order gives the same fn type.
	s1 := w.items.Get(b.item("F")).Decl.(*types.FuncDecl).Sig
	s2 := w.items.Get(b.item("G")).Decl.(*types.FuncDecl).Sig
	if s1.FnType(w.tab) != s2.FnType(w.tab) {
		t.Error("two clause orders gave two fn types")
	}
	// A diagnostic prints the clause as written, by field order.
	sig := w.items.Get(b.item("F")).Decl.(*types.FuncDecl).Sig
	if got, want := w.c.sigString(&sig), "F(x: &i32, y: &i32, z: &i32): Pair from (a: x, b: z, y)"; got != want {
		t.Errorf("sigString = %q, want %q", got, want)
	}
}

func TestE0608_PerField(t *testing.T) {
	const fns = "struct Pair {\n\tr: &i32\n\ts: &i32\n}\n\nApply(g: fn(a: &i32, b: &i32): Pair from (r: a, s: b)) {\n}\n\nApplyW(g: fn(a: &i32, b: &i32): Pair from a, b) {\n}\n\nG(a: &i32, b: &i32): Pair from a, b {\n\treturn Pair(r: b, s: a)\n}\n\nG2(a: &i32, b: &i32): Pair from (r: a, s: b) {\n\treturn Pair(r: a, s: b)\n}\n\nG3(a: &i32, b: &i32): Pair from (r: b, s: a) {\n\treturn Pair(r: b, s: a)\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"W4: a whole value for a per-field fn type", fns + "Run() {\n\tApply(G)\n}\n", "E0608@25:8"},
		{"the same clause", fns + "Run() {\n\tApply(G2)\n}\n", ""},
		{"fields crossed", fns + "Run() {\n\tApply(G3)\n}\n", "E0608@25:8"},
		{"a per-field value for a whole fn type", fns + "Run() {\n\tApplyW(G2)\n\tApplyW(G3)\n}\n", ""},
		{"interface method", "struct Pair {\n\tr: &i32\n\ts: &i32\n}\n\ninterface Mk {\n\tMake(&self, a: &i32, b: &i32): Pair from (r: a, s: b)\n}\n\nstruct T {\n\tn: int\n}\n\nimpl Mk for T {\n\tMake(&self, a: &i32, b: &i32): Pair from a, b {\n\t\treturn Pair(r: a, s: b)\n\t}\n}\n", "E0608@15:2"},
		{"interface method, same clause", "struct Pair {\n\tr: &i32\n\ts: &i32\n}\n\ninterface Mk {\n\tMake(&self, a: &i32, b: &i32): Pair from (r: a, s: b)\n}\n\nstruct T {\n\tn: int\n}\n\nimpl Mk for T {\n\tMake(&self, a: &i32, b: &i32): Pair from (s: b, r: a) {\n\t\treturn Pair(r: a, s: b)\n\t}\n}\n", ""},
	})
}

// TestE0608_PerFieldInstanceAndLiteral: a generic
// instance whose field source vanishes holds nothing in that field, as the
// same signature written out does (instFlows keeps the emptied entry), and
// a fn literal takes a per-field fn type's clause with its sources
// so a literal that builds the declared fields is no
// E0608. Check alone: borrow judges the literal's body field by field
// (tests/soundness/witness/fnlit*.mk).
func TestE0608_PerFieldInstanceAndLiteral(t *testing.T) {
	const s2 = "struct S2[T] {\n\ta: T\n\tb: &i32\n}\n\nApply(g: fn(x: i32, y: &i32): S2[i32] from (a: x, b: y), y: &i32) {\n}\n\n"
	const pair = "struct Pair {\n\tr: &i32\n\ts: &i32\n}\n\nApply(g: fn(a: &i32, b: &i32): Pair from (r: a, s: b)) {\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"R3: a generic instance, a field source vanishing", s2 + "F[T](x: T, y: &i32): S2[T] from (a: x, b: y) {\n\treturn S2(a: x, b: y)\n}\n\nRun(y: &i32) {\n\tApply(F[i32], y)\n}\n", ""},
		{"R3: the same signature written out", s2 + "F2(x: i32, y: &i32): S2[i32] from (a: x, b: y) {\n\treturn S2(a: x, b: y)\n}\n\nRun(y: &i32) {\n\tApply(F2, y)\n}\n", ""},
		{"R3: a source kept by the instance is still compared", "struct S3[T] {\n\ta: T\n\tb: &i32\n}\n\nApply(g: fn(x: &i32, y: &i32): S3[&i32] from (a: y, b: y)) {\n}\n\nF[T](x: T, y: &i32): S3[T] from (a: x, b: y) {\n\treturn S3(a: x, b: y)\n}\n\nRun() {\n\tApply(F[&i32])\n}\n", "E0608@14:8"},
		{"R2: a literal for a per-field fn type", pair + "Run() {\n\tApply(fn(a: &i32, b: &i32): Pair { return Pair(r: a, s: b) })\n}\n", ""},
	})
}

// TestE0608_PerFieldFacts: E0608's expected and found
// facts spell a per-field clause by field, for a fn value (positionally)
// and an impl method (by name); a flat clause is spelled as before.
func TestE0608_PerFieldFacts(t *testing.T) {
	const fns = "struct Pair {\n\tr: &i32\n\ts: &i32\n}\n\nApply(g: fn(a: &i32, b: &i32): Pair from (r: a, s: b)) {\n}\n\nG(a: &i32, b: &i32): Pair from a, b {\n\treturn Pair(r: a, s: b)\n}\n\nG3(a: &i32, b: &i32): Pair from (r: b, s: a) {\n\treturn Pair(r: b, s: a)\n}\n\n"
	const impl = "struct Pair {\n\tr: &i32\n\ts: &i32\n}\n\ninterface Mk {\n\tMake(&self, a: &i32, b: &i32): Pair from (r: a, s: b)\n}\n\nstruct T {\n\tn: int\n}\n\nimpl Mk for T {\n\tMake(&self, a: &i32, b: &i32): Pair from (r: b, s: a) {\n\t\treturn Pair(r: b, s: a)\n\t}\n}\n"
	cases := []struct{ name, src, at, expected, found string }{
		{"a flat value for a per-field fn type", fns + "Run() {\n\tApply(G)\n}\n", "E0608@18:8", "from (r: #1, s: #2)", "from #1, from #2"},
		{"crossed fields", fns + "Run() {\n\tApply(G3)\n}\n", "E0608@18:8", "from (r: #1, s: #2)", "from (r: #2, s: #1)"},
		{"an impl method with crossed fields", impl, "E0608@15:2", "from (r: a, s: b)", "from (r: b, s: a)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true)
			b := checkBody(t, w, tc.at, tc.src)
			d := firstCode(t, append(b.diags(), b.bodyDiags(t)...), diag.E0608)
			if got := fact(d, "expected"); got != tc.expected {
				t.Errorf("expected = %q, want %q", got, tc.expected)
			}
			if got := fact(d, "found"); got != tc.found {
				t.Errorf("found = %q, want %q", got, tc.found)
			}
		})
	}
}
