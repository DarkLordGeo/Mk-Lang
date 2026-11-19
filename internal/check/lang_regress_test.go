// Why: `become` and range patterns had gaps found after they landed; each
// must stay fixed.
// What: TestLangResultInvariant (a become where lower checks the
// result's struct invariants), _B5_MethodTypeArguments (a method call with
// type arguments), _N1_GenericFlows (a generic callee's vacuous flows),
// _N2_Texts (E0313's and E0314's texts), _N3_Witnesses (missing intervals).
// How: checks each test program and compares codes and texts.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// fixCase is a body case whose diagnostics' dump must also hold has.
type fixCase struct {
	name, src, want string
	has             []string
}

func runFixCases(t *testing.T, cases []fixCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := w.add("m", opt{}, tc.src)
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != tc.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
			text := dump(w, ds)
			for _, h := range tc.has {
				if !strings.Contains(text, h) {
					t.Errorf("diagnostics lack %q:\n%s", h, text)
				}
			}
		})
	}
}

const fixSpan = "struct Span {\n\tlo: i32\n\thi: i32\n\tinvariant lo <= hi\n}\n"

// TestLangResultInvariant: p/inv2 (a fn-table callee returning
// Span), p/inv3 (Nope!Span), p/inv1 (a generic callee), and a method
// returning Self: lower checks Span's invariant on the success value
// before returning, which a tail call would skip. A struct without
// invariants, or one returned by reference, is accepted.
func TestLangResultInvariant(t *testing.T) {
	runFixCases(t, []fixCase{
		{"fn table", fixSpan + "const Tab: [1]fn(Span): Span = [fn(s: Span): Span { return s }]\nBad(s: Span): Span {\n\tvar t := s\n\tt.lo = 10\n\tt.hi = 0\n\tbecome Tab[0](t)\n}\n",
			"E0312@11:9", []string{"reason=frame", "invariants"}},
		{"fallible", fixSpan + "error Nope\nconst Tab: [1]fn(Span): Nope!Span = [fn(s: Span): Nope!Span { return s }]\nBad(s: Span): Nope!Span {\n\tbecome Tab[0](s)\n}\n",
			"E0312@9:9", []string{"reason=frame"}},
		{"generic", fixSpan + "Id[T: Plain](x: T): T {\n\treturn x\n}\nBad(s: Span): Span {\n\tbecome Id[Span](s)\n}\n",
			"E0312@10:9", []string{"reason=frame"}},
		{"method", fixSpan + "Span.Again(self, n: int): Span {\n\tif n == 0 {\n\t\treturn self\n\t}\n\tbecome self.Again(n - 1)\n}\n",
			"E0312@10:9", []string{"reason=frame"}},
		{"no invariant", "struct P {\n\tlo: i32\n\thi: i32\n}\nId(p: P): P {\n\treturn p\n}\nF(p: P): P {\n\tbecome Id(p)\n}\n", "", nil},
		{"by reference", fixSpan + "Id(p: &Span): &Span {\n\treturn p\n}\nF(p: &Span): &Span {\n\tbecome Id(p)\n}\n", "", nil},
	})
}

const fixBig = "struct Big {\n\ta: int\n\tb: int\n\tc: int\n}\nstruct Vm {\n\tn: int\n}\n"

// TestLangMethodTypeArguments: p/n_genmethod3 (a new Big as the
// last argument), p/n_genmethod5 (a var Big passed on) and p/n_genmethod
// (an own parameter at its position, wrongly refused before) with
// self.G[U](..): the receiver is Params[0] behind the type arguments too.
func TestLangMethodTypeArguments(t *testing.T) {
	runFixCases(t, []fixCase{
		{"new Big", fixBig + "Vm.G[U](&self, k: int, j: int, a: Big): int {\n\tif k == 0 {\n\t\treturn a.a + j\n\t}\n\tbecome self.G[U](k - 1, j, Big(a: a.a + 1, b: 0, c: 0))\n}\n",
			"E0313@13:29", []string{"param=a"}},
		{"var Big", fixBig + "Vm.G[U](&self, k: int, var b: Big): int {\n\tif k == 0 {\n\t\treturn b.a\n\t}\n\tb.a += 1\n\tbecome self.G[U](k - 1, b)\n}\n",
			"E0313@14:26", []string{"param=b"}},
		{"own parameter", "struct Vm[T] {\n\tk: T\n\tn: int\n}\nVm[T].Gen[U: Plain](&self, u: U, n: int): int {\n\tif n == 0 {\n\t\treturn self.n\n\t}\n\tbecome self.Gen[U](u, n - 1)\n}\n", "", nil},
		{"own Big", fixBig + "Vm.G[U](&self, k: int, a: Big): int {\n\tif k == 0 {\n\t\treturn a.a\n\t}\n\tbecome self.G[U](k - 1, a)\n}\n", "", nil},
		// The argument count is asserted only for a call that checked
		// clean: a wrong count is the call's E0304, not an E0000.
		{"too few", "F(n: int, m: int): int {\n\tbecome F(n)\n}\n", "E0304@2:9", nil},
		{"too many", "G(n: int): int {\n\tbecome G(n, 1)\n}\n", "E0304@2:9", nil},
		{"method too many", fixBig + "Vm.H[U](&self, n: int): int {\n\tbecome self.H[U](n, 2)\n}\n", "E0304@10:9", nil},
	})
}

// TestLangGenericFlows: p/e312a, G[i32]'s automatic `from x` is
// vacuous once T is i32, so the callee is the caller's fn(i32): i32; a
// real flow difference is E0312 naming the flows.
func TestLangGenericFlows(t *testing.T) {
	runFixCases(t, []fixCase{
		{"explicit", "G[T](x: T): T {\n\treturn x\n}\nC(s: i32): i32 {\n\tbecome G[i32](s)\n}\n", "", nil},
		{"inferred", "G[T](x: T): T {\n\treturn x\n}\nD(s: i32): i32 {\n\tbecome G(s)\n}\n", "", nil},
		{"reference keeps its flow", "G[T](x: T): T {\n\treturn x\n}\nD(s: &int): &int {\n\tbecome G(s)\n}\n", "", nil},
		{"flows named", "G(a: &int, b: &int): &int from b {\n\treturn b\n}\nF(a: &int, b: &int): &int from a {\n\tbecome G(a, b)\n}\n",
			"E0312@5:9", []string{"flows are `from #2`", "`from #1`"}},
	})
}

// TestLangTexts: p/b_store, a niche optional is register-sized but
// not one of the kinds the register-sized rule admits, so E0313 says so and
// suggests no reference; a bodied `export extern` callee's E0314 does not
// claim a C ABI every Mk function shares.
func TestLangTexts(t *testing.T) {
	runFixCases(t, []fixCase{
		{"niche optional", "F(p: ?&int, n: int): int {\n\tbecome F(none, n - 1)\n}\n",
			"E0313@2:11", []string{"not a kind every target passes in registers"}},
		{"memory", "struct Big {\n\ta: int\n\tb: int\n\tc: int\n}\nF(b: Big, n: int): int {\n\tbecome F(Big(a: 1, b: 2, c: 3), n - 1)\n}\n",
			"E0313@7:11", []string{"passes in memory"}},
		{"export extern", "export extern Cb(n: int): int {\n\treturn n\n}\nF(n: int): int {\n\tbecome Cb(n)\n}\n",
			"E0314@5:9", []string{"export extern", "reason=extern"}},
	})
}

// TestLangWitnesses: a missing interval inside a struct field or a
// variant's payload is printed; a char range ending in the surrogates
// prints the next scalar value as its end.
func TestLangWitnesses(t *testing.T) {
	runFixCases(t, []fixCase{
		{"struct field", "struct P {\n\ta: u8\n\tb: bool\n}\nF(r: P): int {\n\treturn match r {\n\t\tP(a: 0..128, b: true) { 1 }\n\t\tP(b: false) { 2 }\n\t}\n}\n",
			"E0306@6:9", []string{"P(a: 128.., b: true)"}},
		{"variant payload", "enum Tok {\n\tNum(v: u8)\n\tEnd\n}\nF(t: Tok): int {\n\treturn match t {\n\t\t.Num(v: 0..10) { 1 }\n\t\t.End { 2 }\n\t}\n}\n",
			"E0306@6:9", []string{"Num(v: 10..)"}},
		{"char surrogates", "F(c: char): int {\n\treturn match c {\n\t\t..'\\u{100}' { 1 }\n\t\t'\\u{e000}'.. { 2 }\n\t}\n}\n",
			"E0306@2:9", []string{`'\u{100}'..'\u{e000}'`}},
	})
}
