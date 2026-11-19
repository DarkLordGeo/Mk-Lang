// Why: places, operands, expressions and literals are the base every other
// lowering builds on.
// What: TestPlaces, TestLiterals, TestExprShapes.
// How: lowers small programs and compares the MIR text line by line.

package lower

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

const exprSrc = `const C: int = 7
var G: int = 1
struct P {
	x: int
	y: int
}
H(n: int): int {
	return n
}
F(p: &P, a: [3]int, b: bool): int {
	f := 1.5
	s := "hi"
	t := "hi"
	c := 'x'
	r := (y: 1, x: 2)
	arr := [1, 2, 3]
	rep := [0; 4]
	q := &a
	h := H
	n := -C + G + p.y + r.x + arr[0] + rep[1] + q[2]
	m := b && !b || b
	if m && f > 1.0 && s.Len() == t.Len() && c == 'x' {
		return h(n)
	}
	return 0
}
`

func TestPlaces(t *testing.T) {
	f := lowerSrc(t, exprSrc).print("F")
	// A field through a reference derefs it (check's Adjust); a var global
	// is read through its static reference; an index through a reference
	// derefs before indexing.
	wantIn(t, f, "_15 = &global m.G", "copy (*_15)", "copy (*_1).1", "Len((*_11))", "copy (*_11)[_")
}

func TestLiterals(t *testing.T) {
	l := lowerSrc(t, exprSrc)
	f := l.print("F")
	// Integers, bools and chars are immediates; floats and strings consts.
	wantIn(t, f, "_4 = const f64 1.5", `_5 = const str "hi"`, `_6 = const str "hi"`, "_7 = char 120", "Gt(copy _4, const f64 1)")
	b := l.body("F")
	var strs []types.ConstID
	for _, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvUse && st.Rvalue.A.Kind == mir.OpConst && l.w.tab.Kind(st.Rvalue.A.Type) == types.KStr {
				strs = append(strs, st.Rvalue.A.Const)
			}
		}
	}
	if len(strs) != 2 || strs[0] != strs[1] {
		t.Fatalf("two equal str literals intern to one const: %v", strs)
	}
	v := l.w.consts.Get(strs[0])
	if len(v.Bytes) != 16 || len(v.Relocs) != 1 || v.Relocs[0].Kind != types.RelocData || types.LoadUint(v.Bytes[8:], 8) != 2 {
		t.Fatalf("a str const is {ptr, len} with a RelocData at 0: %+v", v)
	}
	if d := l.w.consts.Get(v.Relocs[0].Data); string(d.Bytes) != "hi" {
		t.Fatalf("the pointee holds the bytes: %q", d.Bytes)
	}
}

func TestExprShapes(t *testing.T) {
	f := lowerSrc(t, exprSrc).print("F")
	wantIn(t, f,
		"(x: int, y: int)(int 2, int 1)", // a record in canonical (sorted) order
		"[3]int(int 1, int 2, int 3)",
		"[int 0; 4]",
		"= &_2",       // a borrow
		"= fn m.H",    // a function value
		"= const m.C", // a const item
		"Neg(copy _13)",
		"call (copy _12)(copy _") // a call through a fn value
	// && and || are control flow: a switch per operand, no And/Or rvalue.
	wantIn(t, f, "switch copy _3 [0: bb", "Not(copy _3)")
}
