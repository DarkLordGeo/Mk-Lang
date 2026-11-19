// Why: a float spliced into generated source must read back to the same
// value.
// What: TestGenFloatSpliceRoundTrips.
// How: the spelling is the shortest decimal that the lexer's float rule
// reads back to the same bits, checked at 64 and 32 bits.

package gen

import (
	"math"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/syntax"
)

func TestGenFloatSpliceRoundTrips(t *testing.T) {
	vals := []float64{0, math.Copysign(0, -1), 0.1, 1, 5, -2.5, 1e21, 1e-7, 123456789.125, math.MaxFloat64,
		math.SmallestNonzeroFloat64, 2.2250738585072014e-308, 1.0 / 3, -1e300}
	for _, bits := range []int{64, 32} {
		for _, f := range vals {
			if bits == 32 {
				f = float64(float32(f))
				if math.IsInf(f, 0) {
					continue
				}
			}
			ts, ok := floatToks(f, bits)
			if !ok {
				t.Fatalf("%v (%d bits): no spelling", f, bits)
			}
			lit := ts[len(ts)-1]
			neg := len(ts) == 2 && ts[0].kind == ast.TokMinus
			if lit.kind != ast.TokFloat || len(ts) > 2 || (len(ts) == 2 && !neg) {
				t.Fatalf("%v: tokens %v", f, ts)
			}
			back, ok := syntax.FloatValue(lit.text, bits)
			if !ok {
				t.Fatalf("%v (%d bits): %q does not lex as a float", f, bits, lit.text)
			}
			if neg {
				back = -back
			}
			if bits == 64 && math.Float64bits(back) != math.Float64bits(f) ||
				bits == 32 && math.Float32bits(float32(back)) != math.Float32bits(float32(f)) {
				t.Errorf("%v (%d bits): %q reads back as %v", f, bits, lit.text, back)
			}
			// The lexer reads the text back as one float token.
			toks, _ := syntax.Lex(1, lit.text)
			var kinds []ast.TokenKind
			for _, tk := range toks {
				if tk.Kind != ast.TokEOF && tk.Kind != ast.TokNewline && tk.Kind != ast.TokTerm {
					kinds = append(kinds, tk.Kind)
				}
			}
			if len(kinds) != 1 || kinds[0] != ast.TokFloat || strings.Contains(lit.text, "+") {
				t.Errorf("%q lexes as %v", lit.text, kinds)
			}
		}
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, ok := floatToks(f, 64); ok {
			t.Errorf("%v has a spelling", f)
		}
	}
	if ts, _ := floatToks(0.1, 32); ts[0].text != "0.1" {
		t.Errorf("f32 0.1 spelled %q, want the shortest 0.1", ts[0].text)
	}
}
