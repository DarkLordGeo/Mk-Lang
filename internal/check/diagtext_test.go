// Why: a diagnostic's message, facts and fix must name only what is true
// of the code at hand: a conversion the pair takes, a fact with a value.
// What: TestE0303_NamesOnlyValidConversions, TestDestructurePathNotStruct,
// TestE0908_ConstSizeOfSuggestsTheType, TestE0705_ArrayLengthNoParamFact.
// How: each case checks one body and reads the diagnostic's message, fix
// and facts.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// TestE0303_NamesOnlyValidConversions: E0303 at `T(x)` names Cast and
// Trunc for two integers, and only Cast for a float narrowing, a float to
// an integer, an integer to a float and an integer to an enum, where Trunc
// is E0302. The fix's reason says what Cast does: from an integer to a
// float it rounds, and never panics.
func TestE0303_NamesOnlyValidConversions(t *testing.T) {
	t.Parallel()
	const enum = "enum Color: u8 {\n\tRed\n\tGreen\n}\n"
	for _, c := range []struct{ name, src, want, use, why string }{
		{"int to u8", "F(x: int): u8 {\n\treturn u8(x)\n}\n", "E0303@2:9", "use `Cast` or `Trunc`", "Cast panics on a value that does not fit; Trunc keeps the low bits"},
		{"f64 to f32", "F(x: f64): f32 {\n\treturn f32(x)\n}\n", "E0303@2:9", "use `Cast`", "Cast panics on a value that does not fit"},
		{"f64 to int", "F(x: f64): int {\n\treturn int(x)\n}\n", "E0303@2:9", "use `Cast`", "Cast panics on a value that does not fit"},
		{"int to f64", "F(x: int): f64 {\n\treturn f64(x)\n}\n", "E0303@2:9", "use `Cast`", "Cast rounds to the nearest value"},
		{"u8 to enum", enum + "F(b: u8): Color {\n\treturn Color(b)\n}\n", "E0303@6:9", "use `Cast`", "Cast panics on a value that does not fit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, c.want, c.src)
			for _, d := range b.bodyDiags(t) {
				if d.Code != diag.E0303 {
					continue
				}
				if !strings.HasSuffix(d.Message, c.use) {
					t.Errorf("message %q does not end in %q", d.Message, c.use)
				}
				trunc := strings.Contains(c.use, "Trunc")
				if strings.Contains(d.Message, "Trunc") != trunc {
					t.Errorf("message %q: Trunc named %v, want %v", d.Message, !trunc, trunc)
				}
				if len(d.Fixes) != 1 || d.Fixes[0].Why != c.why {
					t.Errorf("fixes %+v, want one with reason %q", d.Fixes, c.why)
				}
			}
		})
	}
}

// TestDestructurePathNotStruct: a variant or `some` pattern on the left of
// `:=` is E0306 (take it apart with match); a type parameter or a
// primitive there is E0301, since the name is no struct.
func TestDestructurePathNotStruct(t *testing.T) {
	t.Parallel()
	const p = "struct P {\n\ta: int\n}\n"
	runBodyCases(t, []bodyCase{
		{"variant", "enum One {\n\tOnly(x: int)\n}\nF(o: One): int {\n\tOne.Only(x) := o\n\treturn x\n}\n", "E0306@5:2"},
		{"some", "F(o: ?int): int {\n\tsome(v) := o\n\treturn v\n}\n", "E0306@2:2"},
		{"type parameter", p + "F[T](p: P, q: T): int {\n\tT(a) := p\n\treturn a\n}\n", "E0301@5:2"},
		{"primitive", "F(x: int): int {\n\tint(a) := x\n\treturn a\n}\n", "E0301@2:2"},
	})
}

// TestE0908_ConstSizeOfSuggestsTheType: E0908 for a const holding a bare
// `SizeOf[In]()` of a same-module struct, named in a type, suggests
// writing the SizeOf in the type; a const computing more from it does not
// get that suggestion, which would be E0705 in the type.
func TestE0908_ConstSizeOfSuggestsTheType(t *testing.T) {
	t.Parallel()
	const in = "struct In {\n\ta: int\n}\n"
	for _, c := range []struct {
		name, init string
		suggest    bool
	}{
		{"bare", "SizeOf[In]()", true},
		{"computed", "SizeOf[In]() + 1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, "E0908@6:9", in+"const S = "+c.init+"\nstruct Buf {\n\tdata: [S]u8\n}\n")
			for _, d := range b.diags() {
				if d.Code != diag.E0908 {
					continue
				}
				got := false
				for _, a := range d.Alternatives {
					if strings.Contains(a, "write `SizeOf[In]()` in the type itself") {
						got = true
					}
				}
				if got != c.suggest {
					t.Errorf("suggestion to write SizeOf in the type: %v, want %v\n%s", got, c.suggest, dump(w, []*diag.Diagnostic{d}))
				}
			}
		})
	}
}

// TestE0705_ArrayLengthNoParamFact: E0705 at a run-time array length has
// no `param` fact, since a length binds no const parameter; at a const
// parameter's argument it names the parameter.
func TestE0705_ArrayLengthNoParamFact(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src, want, param string }{
		{"array length", "F(n: int): int {\n\ta: [n]int = [0; 4]\n\treturn a.Len()\n}\n", "E0705@2:6", ""},
		{"const parameter", "Lane(const i: int): int {\n\treturn i\n}\nF(n: int): int {\n\treturn Lane(n)\n}\n", "E0705@5:14", "i"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, c.want, c.src)
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0705 {
					continue
				}
				got, has := "", false
				for _, f := range d.Facts {
					if f.Key == "param" {
						got, has = dump(w, []*diag.Diagnostic{d}), true
					}
				}
				if c.param == "" && has {
					t.Errorf("E0705 at an array length has a param fact:\n%s", got)
				}
				if c.param != "" && (!has || !strings.Contains(got, c.param)) {
					t.Errorf("E0705 at a const argument lacks param %q:\n%s", c.param, dump(w, []*diag.Diagnostic{d}))
				}
			}
		})
	}
}
