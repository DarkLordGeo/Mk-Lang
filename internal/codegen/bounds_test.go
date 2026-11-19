// Why: index and slice bounds checks are lower's explicit MIR, so codegen
// must emit exactly the PanicBounds traps the MIR holds and add none.
// What: TestBoundsCheckEmittedOnce over three hand-built functions.
// How: codegen's tests cannot import lower, so the bodies copy the shapes in
// internal/lower/testdata/corpus.golden (`Trunc[uint]` of index and length,
// `Lt`/`Le`, a switch to one `panic bounds` block); the IR's panic calls are
// counted per function. That lower elides the for-loop check is lower's test;
// this one shows codegen does not undo it.

package codegen

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func truncUint(o mir.Operand) mir.Rvalue {
	return mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastTrunc, A: o, Type: types.Uint}
}

// boundsBodies builds the three test functions:
//
//	fn SumAll(xs: []i64): i64 { s := 0; for i in 0..xs.Len() { s += xs[i] }; return s }  // check elided
//	fn At(xs: []i64, i: int): i64 { return xs[i] }                                      // one explicit check
//	fn Mid(xs: []i64, a: int, b: int): []i64 { return xs[a..b] }                        // a <= b <= len, one panic block
func boundsBodies(e *testEnv) []*mir.Body {
	sl := e.tt.Slice(types.I64)

	f := e.body(e.fn("SumAll", []types.TypeID{sl}, types.I64, 0))
	s, i, n, c, x := f.local(types.I64), f.local(types.Int), f.local(types.Int), f.local(types.Bool), f.local(types.I64)
	f.assign(place(s), use(mir.Imm(types.I64, 0)), f.at(2, 3))
	f.assign(place(i), use(mir.Imm(types.Int, 0)), f.at(3, 7))
	f.assign(place(n), mir.Rvalue{Kind: mir.RvLen, Place: place(1)}, f.at(3, 15))
	head, loop, done := f.newBlock(), f.newBlock(), f.newBlock()
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: head, Span: f.at(3, 3)})
	f.cur = head
	f.assign(place(c), bin(mir.Lt, mir.Copy(place(i)), mir.Copy(place(n))), f.at(3, 7))
	f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)),
		Cases: f.b.AddCases(mir.SwitchCase{Value: 0, Target: done}), Default: loop, Span: f.at(3, 3)})
	f.cur = loop
	f.assign(place(x), use(mir.Copy(f.b.Place(1, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: types.I64}))), f.at(4, 10))
	f.assign(place(s), bin(mir.Add, mir.Copy(place(s)), mir.Copy(place(x))), f.at(4, 5))
	f.assign(place(i), bin(mir.AddWrap, mir.Copy(place(i)), mir.Imm(types.Int, 1)), f.at(3, 7))
	f.term(mir.Terminator{Kind: mir.TermGoto, Target: head, Span: f.at(3, 3)})
	f.cur = done
	f.assign(place(0), use(mir.Copy(place(s))), f.at(6, 3))
	f.ret(f.at(6, 3))

	g := e.body(e.fn("At", []types.TypeID{sl, types.Int}, types.I64, 0))
	iu, ln, lu, ok := g.local(types.Uint), g.local(types.Int), g.local(types.Uint), g.local(types.Bool)
	g.assign(place(iu), truncUint(mir.Copy(place(2))), g.at(9, 10))
	g.assign(place(ln), mir.Rvalue{Kind: mir.RvLen, Place: place(1)}, g.at(9, 10))
	g.assign(place(lu), truncUint(mir.Copy(place(ln))), g.at(9, 10))
	g.assign(place(ok), bin(mir.Lt, mir.Copy(place(iu)), mir.Copy(place(lu))), g.at(9, 10))
	bad, good := g.newBlock(), g.newBlock()
	g.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(ok)),
		Cases: g.b.AddCases(mir.SwitchCase{Value: 0, Target: bad}), Default: good, Span: g.at(9, 10)})
	g.cur = bad
	g.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicBounds, Span: g.at(9, 10)})
	g.cur = good
	g.assign(place(0), use(mir.Copy(g.b.Place(1, mir.Projection{Kind: mir.ProjIndex, A: 2, Type: types.I64}))), g.at(9, 10))
	g.ret(g.at(9, 3))

	h := e.body(e.fn("Mid", []types.TypeID{sl, types.Int, types.Int}, sl, 0))
	au, hl, hu, bu, c1, c2 := h.local(types.Uint), h.local(types.Int), h.local(types.Uint), h.local(types.Uint), h.local(types.Bool), h.local(types.Bool)
	h.assign(place(au), truncUint(mir.Copy(place(2))), h.at(12, 10))
	h.assign(place(hl), mir.Rvalue{Kind: mir.RvLen, Place: place(1)}, h.at(12, 10))
	h.assign(place(hu), truncUint(mir.Copy(place(hl))), h.at(12, 10))
	h.assign(place(bu), truncUint(mir.Copy(place(3))), h.at(12, 10))
	h.assign(place(c1), bin(mir.Le, mir.Copy(place(au)), mir.Copy(place(bu))), h.at(12, 10))
	pb, second, body := h.newBlock(), h.newBlock(), h.newBlock()
	h.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c1)),
		Cases: h.b.AddCases(mir.SwitchCase{Value: 0, Target: pb}), Default: second, Span: h.at(12, 10)})
	h.cur = pb
	h.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicBounds, Span: h.at(12, 10)})
	h.cur = second
	h.assign(place(c2), bin(mir.Le, mir.Copy(place(bu)), mir.Copy(place(hu))), h.at(12, 10))
	h.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c2)),
		Cases: h.b.AddCases(mir.SwitchCase{Value: 0, Target: pb}), Default: body, Span: h.at(12, 10)})
	h.cur = body
	h.assign(place(0), mir.Rvalue{Kind: mir.RvSlice, Place: place(1), HasLo: true, A: mir.Copy(place(2)),
		HasHi: true, B: mir.Copy(place(3))}, h.at(12, 10))
	h.ret(h.at(12, 3))
	return []*mir.Body{f.b, g.b, h.b}
}

// mirBoundsPanics counts the body's `panic bounds` terminators.
func mirBoundsPanics(b *mir.Body) int {
	n := 0
	for _, blk := range b.Blocks {
		if blk.Term.Kind == mir.TermPanic && blk.Term.Panic == types.PanicBounds {
			n++
		}
	}
	return n
}

// TestBoundsCheckEmittedOnce: per function, the emitted IR calls
// the panic hook with PanicBounds exactly as often as the MIR has a
// `panic bounds` block (0, 1, 1), on every target and profile, and the
// loop body of SumAll compares nothing but the loop condition. Every
// module compiles with clang -O2.
func TestBoundsCheckEmittedOnce(t *testing.T) {
	want := map[string]int{"SumAll": 0, "At": 1, "Mid": 1}
	hook := fmt.Sprintf("@mk.std.rt.PanicAt(i32 noundef %d,", types.PanicBounds)
	for _, target := range types.Targets() {
		for _, profile := range []types.Profile{types.Dev, types.Release} {
			t.Run(fmt.Sprintf("%s/%v", target.Name, profile), func(t *testing.T) {
				e := newTestEnv()
				bodies := boundsBodies(e)
				ll := emit(t, e, unitOf(e, bodies), target, profile)
				for _, b := range bodies {
					name := e.in.Str(e.items.Get(b.Item).Name)
					if got := mirBoundsPanics(b); got != want[name] {
						t.Fatalf("%s: the MIR has %d panic bounds blocks, the test wants %d", name, got, want[name])
					}
					text := fnText(ll, "mk.main."+name)
					if text == "" {
						t.Fatalf("no definition of %s in\n%s", name, ll)
					}
					if got := strings.Count(text, hook); got != want[name] {
						t.Errorf("%s: %d PanicBounds hook calls, want %d (the MIR's)\n%s", name, got, want[name], text)
					}
					// The one icmp is the loop condition: s += x's overflow
					// check is llvm.sadd.with.overflow, no icmp, so a change
					// in check.go's overflow shape also trips this line.
					if name == "SumAll" && strings.Count(text, "icmp") != 1 {
						t.Errorf("SumAll: want exactly one icmp (the loop condition), got %d\n%s", strings.Count(text, "icmp"), text)
					}
				}
				clangC(t, target, t.TempDir(), "bounds", ll, "-O2")
			})
		}
	}
}
