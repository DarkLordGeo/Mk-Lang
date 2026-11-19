// Why: a call's operands are evaluated in a fixed order, and reborrows,
// const parameters and variadic promotions each change what a call passes.
// What: TestR13_* (evaluation order), TestR12_ReborrowLocalOnly,
// TestSliceReceiverNoBorrow, TestVariadicPromotions, TestConstParams,
// TestCallArgIsNoFnConst.
// How: lowers small programs and compares the MIR text of the call sites.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
)

func TestR13_ReceiverFormedLast(t *testing.T) {
	l := lowerSrc(t, `error E
struct L {
	n: int
}
L.Push(mut self, x: int): E!void {
	self.n += x
}
L.Len(&self): int {
	return self.n
}
Grow(l: mut L): E!void {
	try l.Push(l.Len())
}
`)
	// MM Grow: the Len call completes before the receiver's reborrow.
	wantIn(t, l.print("Grow"), "call m.L:Len(", "call m.L:Push(reborrow copy _1, copy _4)")
}

func TestR13_SlotRestoredOnErrorEdge(t *testing.T) {
	l := lowerSrc(t, resSrc+`Encode(n: int): int {
	return n
}
Transmit(c: Res, n: int): E!void {
	onerr c.Abandon()
	try c.Close()
	Assert(n == n)
}
Slot(conn: Res, msg: int): E!void {
	try Transmit(conn, Encode(msg))
}
`)
	// conn is read into its slot at its place in the evaluation order, before
	// Encode runs; the call passes the slot (borrow restores conn on the
	// error edge).
	b := l.body("Slot")
	f := l.print("Slot")
	wantIn(t, f, "_4 = move _1", "call m.Encode(copy _2)", "call m.Transmit(move _4, copy _5)")
	if b.Locals[4].Kind != mir.LocalTemp {
		t.Fatalf("the slot is a temporary:\n%s", f)
	}
}

func TestR12_ReborrowLocalOnly(t *testing.T) {
	l := lowerSrc(t, `UseMut(r: mut int) {
	*r += 1
}
Twice(r: mut int) {
	UseMut(r)
	UseMut(r)
	s := r
	*s += 1
}
`)
	// MM Twice: a mut T local passed to a mut T parameter reborrows; a
	// binding moves (MM Stored).
	f := l.print("Twice")
	if count(f, "call m.UseMut(reborrow copy _1)") != 2 {
		t.Fatalf("two reborrows:\n%s", f)
	}
	wantIn(t, f, "_4 = move _1")
}

func TestSliceReceiverNoBorrow(t *testing.T) {
	l := lowerSrc(t, `UseMutSlice(ys: mut []int) {
	ys[0] = 1
}
F(xs: []int, ys: mut []int): int {
	UseMutSlice(ys)
	UseMutSlice(ys)
	return xs.Len()
}
`)
	f := l.print("F")
	// xs.Len() on a []T reads the slice itself: no borrow, no call.
	wantIn(t, f, "Len(_1)")
	if strings.Contains(f, "&_1") || strings.Contains(f, "call m.Len") {
		t.Fatalf("xs is borrowed for Len:\n%s", f)
	}
	// A mut []T passed on reborrows each time.
	if count(f, "call m.UseMutSlice(reborrow copy _2)") != 2 {
		t.Fatalf("a mut []T argument reborrows:\n%s", f)
	}
}

func TestVariadicPromotions(t *testing.T) {
	l := lowerSrc(t, `extern Printf(f: *u8, ...): i32
Var(p: *u8, a: u8, b: bool, c: f32, d: i8, e: i64) {
	unsafe "Printf is C" {
		_ = Printf(p, a, b, c, d, e)
	}
}
`)
	f := l.print("Var")
	// u8 to u32 (unsigned), bool to i32, f32 to f64, i8 to i32 (signed),
	// each before the call and in argument order; a 64-bit argument and
	// the fixed parameter get no cast.
	wantIn(t, f, "Lossless[u32](copy _2)", "Lossless[i32](copy _3)", "Lossless[f64](copy _4)", "Lossless[i32](copy _5)", "call m.Printf(")
	if count(f, "Lossless") != 4 {
		t.Fatalf("four promotions:\n%s", f)
	}
}

func TestConstParams(t *testing.T) {
	l := lowerSrc(t, `Keep(x: &i32): bool {
	return *x > 0
}
Count(xs: []i32, const keep: fn(&i32): bool): int {
	var n := 0
	for x in &xs {
		if keep(x) {
			n += 1
		}
	}
	return n
}
CallCount(xs: []i32): int {
	return Count(xs, Keep)
}
Pred[const N: int](): int {
	return N - 1
}
`)
	// The const argument is a constant operand with a RelocFn to Keep.
	b := l.body("CallCount")
	var call *mir.Terminator
	for i := range b.Blocks {
		if b.Blocks[i].Term.Kind == mir.TermCall {
			call = &b.Blocks[i].Term
		}
	}
	args := b.Args[call.Args.Start : call.Args.Start+call.Args.Len]
	if len(args) != 2 || args[1].Operand.Kind != mir.OpConst {
		t.Fatalf("the const argument is not a constant operand:\n%s", l.print("CallCount"))
	}
	v := l.w.consts.Get(args[1].Operand.Const)
	if len(v.Relocs) != 1 || v.Relocs[0].Fn.Item != l.b.item("Keep") {
		t.Fatalf("the constant has no RelocFn to Keep: %+v", v)
	}
	// Inside Count, the call through keep is a call through its value.
	wantIn(t, l.print("Count"), "call (copy _2)(copy _8)")
	// A const type parameter used as a value is RvConstArg, then Sub.
	wantIn(t, l.print("Pred"), "_1 = ConstArg[int]($0)", "Sub(copy _1, int 1)")
}

// TestCallArgIsNoFnConst covers lower's side of a defect found in std's
// SHA-1 code: a direct call records its callee's Use on the call node, so
// fnConst read `Lane(Two())`'s argument as the function Two, a constant of fn
// type with zero bytes, and the instance ran with 0 (`Apply(Pick(), 3)` took
// Pick itself). Check rejects the call (E0705); lowered anyway, the argument
// is the call's value, an operand that is no constant, which mono's
// constArgOf refuses.
func TestCallArgIsNoFnConst(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, "Two(): int {\n\treturn 2\n}\n\nLane(const i: int): int {\n\treturn i * 10\n}\n\nF(): int {\n\treturn Lane(Two())\n}\n")
	id := b.item("F")
	body := w.c.Check(b.m, id)
	if len(body.Diags) != 1 || body.Diags[0].Code.String() != "E0705" {
		t.Fatalf("check diagnostics:\n%s", dump(w, body.Diags))
	}
	cfg := w.lowerCfg()
	mb := Lower(cfg, FromCheck(cfg, w.c, body))
	lane := b.item("Lane")
	found := false
	for i := range mb.Blocks {
		tm := &mb.Blocks[i].Term
		if tm.Kind != mir.TermCall || tm.Callee.Fn.Item != lane {
			continue
		}
		found = true
		if op := mb.Args[tm.Args.Start].Operand; op.Kind == mir.OpConst || op.Kind == mir.OpImm {
			p := &mir.Printer{Types: w.tab, Items: w.items}
			t.Fatalf("Lane's argument is a constant operand (kind %d):\n%s", op.Kind, p.Print(mb))
		}
	}
	if !found {
		t.Fatal("no call of Lane")
	}
}
