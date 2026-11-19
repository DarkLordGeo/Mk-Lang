// Why: MIR text alone does not show a lowering is right; running it does.
// What: TestEvalHalves and TestEvalHalvesPanics, tables of programs with the
// result or panic each must produce.
// How: lowered MIR is run by internal/eval, the reference semantics, through
// the test-only edge lower -> eval (archtest TestEdges).

package lower

import (
	"encoding/binary"
	"testing"

	"mk/internal/eval"
	"mk/internal/source"
	"mk/internal/types"
)

// evalProg serves eval the lowered bodies of one test module and check's
// layouts.
type evalProg struct {
	l      *lowered
	byItem map[types.ItemID]string
}

func (p *evalProg) Body(fn types.ItemID, args []types.GenericArg) eval.Body {
	k, ok := p.byItem[fn]
	if !ok {
		return eval.Body{State: eval.BodyMissing}
	}
	return eval.Body{MIR: p.l.bodies[k], State: eval.BodyReady}
}
func (p *evalProg) Layout(t types.TypeID) (*types.Layout, source.Hash) {
	l := p.l.w.c.Layout(t)
	return &l, source.Hash{}
}
func (p *evalProg) LayoutName(args []types.GenericArg) string  { return "" }
func (p *evalProg) Const(item types.ItemID) eval.Const         { return eval.Const{} }
func (p *evalProg) ConstHash(c types.ConstID) source.Hash      { return source.Hash{} }
func (p *evalProg) TargetHash(f types.TargetField) source.Hash { return source.Hash{} }

// evalH lowers src and evaluates its H(): int with no fuel worry. It reads
// the result as 8 little-endian bytes, so a case that returns a value must
// return int (a narrower result is fine only for a case that panics).
func evalH(t *testing.T, src string) (int64, *types.EvalError) {
	t.Helper()
	l := lowerSrc(t, src)
	p := &evalProg{l: l, byItem: map[types.ItemID]string{}}
	var h types.ItemID
	for _, id := range l.b.m.Units() {
		k := l.b.key(id)
		p.byItem[id] = k
		if k == "H" {
			h = id
		}
	}
	ev := eval.New(eval.Config{Types: l.w.tab, Items: l.w.items, Consts: l.w.consts, Interner: l.w.in, Program: p, Known: *l.w.cfg.Known})
	r := ev.Eval(&types.EvalRequest{What: types.EvalCall, Item: h, Fuel: 1 << 20, Stage: types.StageBodies, Module: l.b.id})
	if r.Err != nil {
		return 0, r.Err
	}
	v := l.w.consts.Get(r.Value)
	return int64(binary.LittleEndian.Uint64(v.Bytes)), nil
}

func TestEvalHalves(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      int64
	}{
		{"R13", `Next(n: mut int): int {
	*n += 1
	return *n
}
Pair(a: int, b: int): int {
	return a * 10 + b
}
H(): int {
	var n := 1
	return Pair(n, Next(mut n))
}
`, 12},
		{"R13binary", `Next(n: mut int): int {
	*n += 1
	return *n
}
H(): int {
	var n := 1
	return n + Next(mut n)
}
`, 3},
		{"REF099", `H(): int {
	var a := [1, 2, 3]
	for x in a {
		a[0] += x
	}
	return a[0]
}
`, 7},
		{"OrOrder", `Next(log: mut int): ?int {
	*log = *log * 10 + 1
	return none
}
Fallback(log: mut int): int {
	*log = *log * 10 + 2
	return 5
}
H(): int {
	var log := 0
	v := Next(mut log) else { Fallback(mut log) }
	return log * 10 + v
}
`, 125},
		{"Defers", `H(): int {
	var x := 0
	for _ in 0..1 {
		defer { x = x * 10 + 1 }
		defer { x = x * 10 + 2 }
	}
	return x
}
`, 21},
		{"Counter", `H(): int {
	xs := [5, 6, 7]
	var s := 0
	for i in 0..xs.Len() {
		s += xs[i]
	}
	return s
}
`, 18},
		{"TryOnerr", `error Bad
Step(log: mut int, fail_: bool): Bad!int {
	*log = *log * 10 + 1
	if fail_ {
		fail Bad
	}
	return 3
}
Run(log: mut int): Bad!int {
	defer { *log = *log * 10 + 7 }
	onerr { *log = *log * 10 + 8 }
	a := try Step(log, false)
	b := try Step(log, true)
	return a + b
}
H(): int {
	var log := 0
	v := Run(mut log) else {
		return log * 10 + 9
	}
	return v
}
`, 11879},
		{"Match", `enum Shape {
	Circle(r: int)
	Rect(w: int, h: int)
	Dot
}
Area(s: ?Shape): int {
	match s {
		some(.Circle(r: 1)) { return 100 }
		some(.Circle(r)) { return r * r }
		some(.Rect(w, h)) { return w * h }
		some(.Dot) | none { return 0 }
	}
}
H(): int {
	return Area(some(Shape.Circle(r: 1))) + Area(some(Shape.Circle(r: 3))) * 1000 + Area(some(Shape.Rect(w: 2, h: 5))) * 100000 + Area(none)
}
`, 1009100},
		{"ElseOpt", `Find(xs: [3]int, x: int): ?int {
	for i, y in xs {
		if y == x {
			return some(i)
		}
	}
	return none
}
H(): int {
	xs := [4, 5, 6]
	a := Find(xs, 6) else {
		return -1
	}
	b := Find(xs, 9) else {
		return a * 10 + 9
	}
	return b
}
`, 29},
		{"Fields", `struct P {
	x: int
	y: int
}
H(): int {
	var p := P(x: 1, y: 2)
	p.y = p.x + p.y * 10
	P(x: a, y: b) := p
	return a * 100 + b
}
`, 121},
		{"CompoundPlaceOnce", `I(log: mut int): int {
	*log = *log * 10 + 1
	return 0
}
F(log: mut int): int {
	*log = *log * 10 + 2
	return 5
}
H(): int {
	var log := 0
	var xs := [1, 2]
	xs[I(mut log)] += F(mut log)
	return log * 100 + xs[0]
}
`, 2106},
		{"AssignRightFirst", `I(log: mut int): int {
	*log = *log * 10 + 1
	return 1
}
F(log: mut int): int {
	*log = *log * 10 + 2
	return 9
}
H(): int {
	var log := 0
	var xs := [1, 2]
	xs[I(mut log)] = F(mut log)
	return log * 100 + xs[1]
}
`, 2109},
		{"BreakContinueDefers", `H(): int {
	var x := 0
	for i in 0..5 {
		defer { x = x * 10 + 1 }
		if i == 1 {
			continue
		}
		if i == 3 {
			break
		}
		x = x * 10 + 2
	}
	return x
}
`, 211211},
		{"LabeledBreak", `H(): int {
	var n := 0
	outer: for i in 0..4 {
		for j in 0..4 {
			if j == 2 {
				continue outer
			}
			if i == 3 {
				break outer
			}
			n += 1
		}
	}
	return n
}
`, 6},
		{"WhileHeader", `Next(c: mut int): ?int {
	if *c >= 3 {
		return none
	}
	*c += 1
	return some(*c)
}
H(): int {
	var c := 0
	var s := 0
	for x := Next(mut c) {
		s = s * 10 + x
	}
	return s
}
`, 123},
		{"ShortCircuit", `T(log: mut int, v: bool): bool {
	*log = *log * 10 + 1
	return v
}
H(): int {
	var log := 0
	if T(mut log, false) && T(mut log, true) {
		log += 100
	}
	if T(mut log, true) || T(mut log, true) {
		log += 1000
	}
	return log
}
`, 1011},
		// The evaluation-order slot for field and deref paths, slice bounds,
		// and compound assignment through an index.
		{"R13FieldPath", r13Prelude + `H(): int {
	var c := C(n: 1)
	return c.n + Bump(mut c)
}
`, 3},
		{"R13FieldPathCall", r13Prelude + `H(): int {
	var c := C(n: 1)
	return Pair(c.n, Bump(mut c))
}
`, 12},
		{"R13DerefPath", r13Prelude + `C.Sum(mut self): int {
	return self.n + self.Bump()
}
H(): int {
	var c := C(n: 1)
	return c.Sum()
}
`, 3},
		{"R13SliceLo", r13Prelude + `H(): int {
	xs := [5, 6, 7]
	var i := 0
	s := xs[i..Next(mut i)]
	return s.Len() * 10 + s[0]
}
`, 15},
		{"R13SameLocalMut", r13Prelude + `Set(n: int, p: mut int): int {
	*p = 9
	return n
}
H(): int {
	var n := 4
	return Set(n, mut n) * 10 + n
}
`, 49},
		{"R13CompoundIndex", r13Prelude + `H(): int {
	var xs := [10, 20]
	var i := 0
	xs[Next(mut i)] += i
	return xs[1]
}
`, 20},
		{"R13CalleeFirst", `struct Hold {
	f: fn(int): int
}
Inc(x: int): int {
	return x + 1
}
Dbl(x: int): int {
	return x * 2
}
Reset(h: mut Hold): int {
	h.f = Dbl
	return 5
}
H(): int {
	var h := Hold(f: Inc)
	return h.f(Reset(mut h))
}
`, 6},
		{"R13OrReceiver", `Clear(o: mut ?int): int {
	*o = none
	return 7
}
H(): int {
	var o: ?int = some(1)
	return o else { Clear(mut o) }
}
`, 1},
		// A range's lo is read before hi runs (1..2, one
		// iteration adding 1; the old order counted 2..2).
		{"R13RangeLo", `Bump(n: mut int): int {
	*n = *n + 1
	return *n
}
H(): int {
	var n := 1
	var s := 0
	for i in n..Bump(mut n) {
		s = s + 10 + i
	}
	return s
}
`, 11},
		// Compound assignment: the right side runs first, then the place
		// (Bump makes i 1), and the check and the store read the same i.
		{"CompoundIndexAfterRight", `Bump(n: mut int): int {
	*n = *n + 1
	return *n
}
H(): int {
	var xs := [10, 20, 30]
	var i := 0
	xs[i] += Bump(mut i)
	return xs[0] * 100 + xs[1]
}
`, 1021},
	} {
		got, err := evalH(t, c.src)
		if err != nil {
			t.Errorf("%s: eval: %+v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: H() = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestEvalHalvesPanics(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      types.PanicCode
		msg       string
	}{
		{"NarrowNegIndex", `H(): int {
	xs := [1, 2, 3]
	var i: i8 = 0
	i -= 1
	return xs[i]
}
`, types.PanicBounds, ""},
		{"Must", `error Denied
Load(id: int): Denied!int {
	if id < 0 {
		fail Denied
	}
	return id
}
H(): int {
	return Load(-1).Must()
}
`, types.PanicMust, "Denied"},
		{"Requires", `SafeDiv(a: int, b: int): int requires b != 0 {
	return a / b
}
H(): int {
	return SafeDiv(1, 0)
}
`, types.PanicContract, ""},
		{"Ensures", `Pos(a: int): int ensures result > 0 {
	return a
}
H(): int {
	return Pos(0)
}
`, types.PanicContract, ""},
		{"Invariant", `struct Span {
	lo: int
	hi: int
	invariant lo <= hi
}
H(): int {
	s := Span(lo: 2, hi: 1)
	return s.lo
}
`, types.PanicContract, ""},
		{"Overflow", `H(): i8 {
	var x: i8 = 127
	x += 1
	return x
}
`, types.PanicOverflow, ""},
		{"OptMust", `None(): ?int {
	return none
}
H(): int {
	return None().Must()
}
`, types.PanicMust, ""},
	} {
		_, err := evalH(t, c.src)
		if err == nil || err.Panic != c.want || (c.msg != "" && err.Message != c.msg) {
			t.Errorf("%s: %+v, want panic %v %q", c.name, err, c.want, c.msg)
		}
	}
}

// r13Prelude is the shared part of the evaluation-order cases.
const r13Prelude = `struct C {
	n: int
}
C.Bump(mut self): int {
	self.n += 1
	return self.n
}
Bump(c: mut C): int {
	c.n += 1
	return c.n
}
Next(n: mut int): int {
	*n += 1
	return *n
}
Pair(a: int, b: int): int {
	return a * 10 + b
}
`
