// Why: fields loops and `if T is X` are lowered before monomorphization,
// and mono depends on the exact block shapes lower builds.
// What: TestFieldsLoop_UnrolledConcrete, TestFieldsLoop_GenericLatch,
// TestTypeTest_ReturnNarrowsBack, TestTypeTest_NarrowBackOnlyBySubstitution.
// How: lowers small generic programs and checks the MIR text and block
// order.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

func TestFieldsLoop_UnrolledConcrete(t *testing.T) {
	l := lowerSrc(t, `struct P {
	a: int
	b: int
	c: int
}
Sum(p: P): int {
	var s := 0
	for name, value in fields(&p) {
		if name.Len() == 1 {
			s += *value
		}
		if s > 100 {
			break
		}
	}
	return s
}
Skip(p: &P): int {
	var s := 0
	for _, value in fields(&p) {
		if *value < 0 {
			continue
		}
		s += *value
	}
	return s
}
Rec(r: (y: int, x: i32)): int {
	var n := 0
	for name, value in fields(&r) {
		n += name.Len() + Cast[int](*value) - Cast[int](*value)
	}
	return n
}
Empty() {
}
`)
	b := l.body("Sum")
	if len(b.Loops) != 3 {
		t.Fatalf("one LoopInfo per copy, got %d", len(b.Loops))
	}
	for _, li := range b.Loops {
		if li.FieldsOf != 0 || li.Exit != b.Loops[0].Exit {
			t.Fatalf("copies share one exit and are not generic: %+v", b.Loops)
		}
	}
	f := l.print("Sum")
	wantIn(t, f, "= &_1.0", "= &_1.1", "= &_1.2")
	wantIn(t, f, `"a"`, `"b"`, `"c"`)
	if strings.Contains(f, "loop0") || strings.Contains(f, "FieldName") {
		t.Fatalf("a concrete loop has no FieldRef{Loop}:\n%s", f)
	}
	// Through a reference: the base is the struct, *p, never a deref of a
	// non-reference.
	wantIn(t, l.print("Skip"), "= &(*_", ".0", ".1", ".2")
	// A record's fields in sorted order: x then y.
	r := l.print("Rec")
	wantIn(t, r, `"x"`, `"y"`)
	for _, bb := range l.body("Rec").Blocks {
		if bb.Term.Kind == mir.TermSwitch && strings.Contains(r, "panic") {
			t.Fatalf("no checks in the record loop:\n%s", r)
		}
	}
}

// TestTypeTest_ReturnNarrowsBack: inside `if T is &i32`, `return &self.f`
// is typed &i32 by check while _0 keeps T, with no conversion recorded;
// lower retypes the value by CastNarrow before _0 (found by
// TestEveryUnitLowers on tests/conformance/model/H23.mk). The same for an
// E!T result, where the narrowed value is AggOk's operand.
func TestTypeTest_ReturnNarrowsBack(t *testing.T) {
	l := lowerSrc(t, `struct Wrap[T: Plain] {
	v: T
	f: i32
}
Wrap[T].Get(&self): T {
	if T is &i32 {
		return &self.f
	}
	return self.v
}
error Bad
Pick[T: Plain](v: T, n: i32): Bad!T {
	if T is i32 {
		return n
	}
	return v
}
`)
	get := l.print("Wrap:Get")
	wantIn(t, get, "typetest $0 is &i32", "Narrow[$0](")
	pick := l.print("Pick")
	wantIn(t, pick, "typetest $0 is i32", "Narrow[$0](copy _2)", "ok(")
	for _, f := range []string{get, pick} {
		if n := strings.Count(f, "Narrow[$0]("); n != 1 {
			t.Errorf("want one CastNarrow back to $0, got %d:\n%s", n, f)
		}
	}
}

// TestTypeTest_NarrowBackOnlyBySubstitution: narrowBack casts only a value
// whose type is the result type with each enclosing branch's T replaced by
// its X; any other mismatch is returned unchanged for Validate's M4.
func TestTypeTest_NarrowBackOnlyBySubstitution(t *testing.T) {
	l := lowerSrc(t, `Id[T: Plain](v: T): T {
	return v
}
`)
	w, cfg := l.w, l.w.lowerCfg()
	for _, id := range l.b.m.Units() {
		if l.b.key(id) != "Id" {
			continue
		}
		in := FromCheck(cfg, w.c, w.c.Check(l.b.m, id))
		bl := newBuilder(cfg, in)
		bl.cur = bl.newBlock()
		bl.openScope(scopeFn)
		bl.pushFrame(source.Span{})
		want := bl.sig.Result // T
		if bl.narrowBack(mir.Imm(types.I32, 1), want, source.Span{}).Type != types.I32 {
			t.Fatal("cast outside an if T is X branch")
		}
		bl.openScope(scopeTypeTestYes)
		bl.scopes[len(bl.scopes)-1].ttParam, bl.scopes[len(bl.scopes)-1].ttType = 0, types.I32
		if got := bl.narrowBack(mir.Imm(types.Bool, 1), want, source.Span{}); got.Kind != mir.OpImm || got.Type != types.Bool {
			t.Fatalf("a bool under T is i32 was retyped: %+v", got)
		}
		if got := bl.narrowBack(mir.Imm(types.I32, 1), want, source.Span{}); bl.b.OperandType(got) != want {
			t.Fatalf("an i32 under T is i32 was not retyped to T: %+v", got)
		}
		return
	}
	t.Fatal("no unit Id")
}

// TestFieldsLoop_GenericLatch pins the block order mono relies on: in a
// generic fields loop the latch is Header + 2 (fieldsGeneric
// allocates header, body, latch in that order), it ends in goto Exit, and
// the fall-through and continue reach it, while break goes to Exit directly
// (through its cleanup copies). mono cannot tell the latch from a break
// block by shape, so a change of allocation order must change mono too.
func TestFieldsLoop_GenericLatch(t *testing.T) {
	l := lowerSrc(t, `Log() {
}
Dump[A: Fields[Format]](w: mut Writer, args: A): WriteError!void {
	for name, value in fields(&args) {
		defer Log()
		if name.Len() == 1 {
			continue
		}
		if name.Len() == 2 {
			break
		}
		try value.Format(w)
	}
}
`)
	b := l.body("Dump")
	f := l.print("Dump")
	if len(b.Loops) != 1 || b.Loops[0].FieldsOf == 0 {
		t.Fatalf("loop info %+v", b.Loops)
	}
	lp := b.Loops[0]
	latch := lp.Header + 2
	if int(latch) >= int(lp.Exit) {
		t.Fatalf("latch bb%d not inside [bb%d, bb%d):\n%s", latch, lp.Header, lp.Exit, f)
	}
	if tm := b.Blocks[latch].Term; tm.Kind != mir.TermGoto || tm.Target != lp.Exit {
		t.Fatalf("bb%d (Header+2) is not goto Exit bb%d:\n%s", latch, lp.Exit, f)
	}
	if n := b.PredecessorCounts()[latch]; n < 2 {
		t.Fatalf("latch bb%d has %d predecessors, want the fall-through and continue:\n%s", latch, n, f)
	}
	// Another block of the loop going to Exit is a break, never the latch.
	breaks := 0
	for i := lp.Header; i < lp.Exit; i++ {
		if tm := b.Blocks[i].Term; i != latch && tm.Kind == mir.TermGoto && tm.Target == lp.Exit {
			breaks++
		}
	}
	if breaks != 1 {
		t.Fatalf("want one break block to Exit besides the latch, got %d:\n%s", breaks, f)
	}
}
