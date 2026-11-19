// Why: the interpreter core decides what every compile-time evaluation
// computes, so each statement, rvalue and terminator needs a direct test.
// What: tests of statements, rvalues, terminators, traps, panics, the stack
// limit, onerr decisions, contract arguments and nested const segments.
// How: small hand-built MIR functions run through w.call; results, ICE
// messages and steps are compared with values worked out by hand.

package eval

import (
	"math"
	"runtime/debug"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// iceOf runs fn and returns the ICE message it raised, "" for none.
func iceOf(fn func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*source.ICE)
			if !ok {
				panic(r)
			}
			msg = e.Msg
		}
	}()
	fn()
	return ""
}

// mulAdd declares F(n: int): int { return n * k + d } in module mod.
func (w *world) mulAdd(mod, name string, k, d int64) types.ItemID {
	f := w.fn(mod, name, types.Int, types.Int)
	x := w.body(f, types.Int, types.Int)
	t := x.local(types.Int)
	x.assign(0, lp(t), bin(mir.Mul, cp(1), imm(types.Int, k)))
	x.assign(0, lp(0), bin(mir.Add, cp(t), imm(types.Int, d)))
	x.ret(0)
	x.done()
	return f
}

// constCalling declares const name: int = fn(arg) in module mod.
func (w *world) constCalling(mod, name string, fn types.ItemID, arg int64, fuel uint64) types.ItemID {
	c := w.constItem(mod, name, types.Int, fuel)
	x := w.body(c, types.Int)
	b1 := x.block()
	x.call(0, fn, lp(0), b1, imm(types.Int, arg))
	x.ret(b1)
	x.done()
	return c
}

func constReq(w *world, item types.ItemID, stage types.Stage) *types.EvalRequest {
	return &types.EvalRequest{What: types.EvalConst, Item: item, Stage: stage, Module: w.items.Get(item).Module,
		Site: w.items.Get(item).Span, Target: types.X86_64Windows}
}

func TestConstFromFunction(t *testing.T) {
	w := newWorld(t)
	f := w.mulAdd("lib", "F", 3, 1)
	x := w.constCalling("main", "X", f, 1, 0)
	e := w.evaluator(0)
	r := e.Eval(constReq(w, x, types.StageBodies))
	if r.Err != nil || r.Value == types.NoConst {
		t.Fatalf("X = F(1): err %+v value %d", r.Err, r.Value)
	}
	if got := w.intOf(r.Value); got != 4 {
		t.Errorf("X = %d, want 4", got)
	}
	v := w.consts.Get(r.Value)
	if v.Type != types.Int || len(v.Bytes) != 8 || len(v.Relocs) != 0 {
		t.Errorf("value not canonical int: %+v", v)
	}
	if got, ok := e.Value(x); !ok || got != r.Value {
		t.Errorf("Value(X) = %d, %v", got, ok)
	}
	// Determinism: a second evaluator gives the same interned value.
	if r2 := w.evaluator(0).Eval(constReq(w, x, types.StageBodies)); r2.Value != r.Value || r2.Steps != r.Steps {
		t.Errorf("second run: value %d steps %d, first %d %d", r2.Value, r2.Steps, r.Value, r.Steps)
	}
	var keys []string
	for _, rd := range r.Reads.Sorted() {
		keys = append(keys, rd.Kind.String()+" "+rd.Key.String())
	}
	if want := []string{"body lib.F", "body main.X"}; len(keys) != 2 || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("reads %v, want %v", keys, want)
	}
}

func TestIntegerTraps(t *testing.T) {
	ti := func(w int, s bool) *tinfo {
		return &tinfo{class: cInt, width: w, signed: s, ty: &types.Type{Kind: types.KInt}}
	}
	i8, i32, i64 := ti(1, true), ti(4, true), ti(8, true)
	u8, u32, u64 := ti(1, false), ti(4, false), ti(8, false)
	const minI64, maxI64 = math.MinInt64, math.MaxInt64
	s := func(x int64) uint64 { return uint64(x) }
	cases := []struct {
		name string
		op   mir.BinOp
		ti   *tinfo
		a, b uint64
		want uint64
		pc   types.PanicCode
	}{
		{"i64 5+-3", mir.Add, i64, 5, s(-3), 2, 0},
		{"i64 -1*-1", mir.Mul, i64, s(-1), s(-1), 1, 0},
		{"i64 min+max", mir.Add, i64, s(minI64), s(maxI64), s(-1), 0},
		{"i64 min/2*2", mir.Mul, i64, s(minI64 / 2), 2, s(minI64), 0},
		{"i64 -(min/2)*-2", mir.Mul, i64, s(-(minI64 / 2)), s(-2), s(minI64), 0},
		{"i64 max+1", mir.Add, i64, s(maxI64), 1, 0, types.PanicOverflow},
		{"i64 min-1", mir.Sub, i64, s(minI64), 1, 0, types.PanicOverflow},
		{"i64 min*-1", mir.Mul, i64, s(minI64), s(-1), 0, types.PanicOverflow},
		{"i64 max*2", mir.Mul, i64, s(maxI64), 2, 0, types.PanicOverflow},
		{"i64 (min/2-1)*2", mir.Mul, i64, s(minI64/2 - 1), 2, 0, types.PanicOverflow},
		{"u64 0-1", mir.Sub, u64, 0, 1, 0, types.PanicOverflow},
		{"u64 max*2", mir.Mul, u64, math.MaxUint64, 2, 0, types.PanicOverflow},
		{"u64 max+1", mir.Add, u64, math.MaxUint64, 1, 0, types.PanicOverflow},
		{"u64 max+0", mir.Add, u64, math.MaxUint64, 0, math.MaxUint64, 0},
		{"i8 127+1", mir.Add, i8, 127, 1, 0, types.PanicOverflow},
		{"i8 -128-1", mir.Sub, i8, 0x80, 1, 0, types.PanicOverflow},
		{"i8 -128+127", mir.Add, i8, 0x80, 127, 0xff, 0},
		{"i8 -1*-1", mir.Mul, i8, 0xff, 0xff, 1, 0},
		{"i8 -128*-1", mir.Mul, i8, 0x80, 0xff, 0, types.PanicOverflow},
		{"i32 max*max", mir.Mul, i32, 0x7fffffff, 0x7fffffff, 0, types.PanicOverflow},
		{"u8 255+1", mir.Add, u8, 255, 1, 0, types.PanicOverflow},
		{"u8 3-4", mir.Sub, u8, 3, 4, 0, types.PanicOverflow},
		{"u8 16*16", mir.Mul, u8, 16, 16, 0, types.PanicOverflow},
		{"u32 max*max", mir.Mul, u32, 0xffffffff, 0xffffffff, 0, types.PanicOverflow},
		{"u8 255+%1", mir.AddWrap, u8, 255, 1, 0, 0},
		{"u8 0-%1", mir.SubWrap, u8, 0, 1, 255, 0},
		{"i64 max*%2", mir.MulWrap, i64, s(maxI64), 2, s(-2), 0},
		{"i32 /0", mir.Div, i32, 5, 0, 0, types.PanicDivideByZero},
		{"u64 %0", mir.Rem, u64, 5, 0, 0, types.PanicDivideByZero},
		{"i64 min/-1", mir.Div, i64, s(minI64), s(-1), 0, types.PanicDivideByZero},
		{"i8 -128/-1", mir.Div, i8, 0x80, 0xff, 0, types.PanicDivideByZero},
		{"i8 -128%-1", mir.Rem, i8, 0x80, 0xff, 0, types.PanicDivideByZero},
		{"i32 -7/2", mir.Div, i32, 0xfffffff9, 2, 0xfffffffd, 0},
		{"i32 -7%2", mir.Rem, i32, 0xfffffff9, 2, 0xffffffff, 0},
		{"u8 1<<8", mir.Shl, u8, 1, 8, 0, types.PanicShiftAmount},
		{"u8 1<<7", mir.Shl, u8, 1, 7, 0x80, 0},
		{"i64 1<<64", mir.Shl, i64, 1, 64, 0, types.PanicShiftAmount},
		{"i64 1<<-1", mir.Shl, i64, 1, s(-1), 0, types.PanicShiftAmount},
		{"i8 -128>>7", mir.Shr, i8, 0x80, 7, 0xff, 0},
		{"u8 128>>7", mir.Shr, u8, 0x80, 7, 1, 0},
		{"i8 -1<1", mir.Lt, i8, 0xff, 1, 1, 0},
		{"u8 255<1", mir.Lt, u8, 0xff, 1, 0, 0},
	}
	for _, c := range cases {
		got, pc := binaryOp(c.op, c.ti, c.ti, c.a, c.b)
		if pc != c.pc || (pc == 0 && got != c.want) {
			t.Errorf("%s: %#x %v, want %#x %v", c.name, got, pc, c.want, c.pc)
		}
	}
	for _, c := range []struct {
		name string
		ti   *tinfo
		a    uint64
		want uint64
		pc   types.PanicCode
	}{
		{"-min i64", i64, s(minI64), 0, types.PanicOverflow},
		{"-min i8", i8, 0x80, 0, types.PanicOverflow},
		{"-5 i8", i8, 5, 0xfb, 0},
		{"-1 u8", u8, 1, 0, types.PanicOverflow},
	} {
		got, pc := unary(mir.Neg, c.ti, c.a)
		if pc != c.pc || (pc == 0 && got != c.want) {
			t.Errorf("%s: %#x %v, want %#x %v", c.name, got, pc, c.want, c.pc)
		}
	}
}

func TestCasts(t *testing.T) {
	ti := func(c tclass, w int, s bool) *tinfo { return &tinfo{class: c, width: w, signed: s, ty: &types.Type{}} }
	i8, i64, u8, u64 := ti(cInt, 1, true), ti(cInt, 8, true), ti(cInt, 1, false), ti(cInt, 8, false)
	f64, f32, ch := ti(cFloat, 8, false), ti(cFloat, 4, false), ti(cChar, 4, false)
	for _, c := range []struct {
		name     string
		k        mir.CastKind
		from, to *tinfo
		a, want  uint64
		pc       types.PanicCode
	}{
		{"i8 -1 to i64", mir.CastLossless, i8, i64, 0xff, math.MaxUint64, 0},
		{"u8 255 to i64", mir.CastLossless, u8, i64, 0xff, 255, 0},
		{"i64 300 to u8 checked", mir.CastChecked, i64, u8, 300, 0, types.PanicCast},
		{"i64 -1 to u64 checked", mir.CastChecked, i64, u64, math.MaxUint64, 0, types.PanicCast},
		{"i64 -128 to i8 checked", mir.CastChecked, i64, i8, uint64(1<<64 - 128), 0x80, 0},
		{"i64 -129 to i8 checked", mir.CastChecked, i64, i8, uint64(1<<64 - 129), 0, types.PanicCast},
		{"u64 max to i64 checked", mir.CastChecked, u64, i64, math.MaxUint64, 0, types.PanicCast},
		{"i64 300 trunc u8", mir.CastTrunc, i64, u8, 300, 44, 0},
		{"f64 NaN to i64", mir.CastChecked, f64, i64, types.CanonicalNaN64, 0, types.PanicCast},
		{"f64 -1.5 to i64", mir.CastChecked, f64, i64, math.Float64bits(-1.5), uint64(1<<64 - 1), 0},
		{"f64 2^63 to i64", mir.CastChecked, f64, i64, math.Float64bits(math.Ldexp(1, 63)), 0, types.PanicCast},
		{"f64 -0.9 to u8", mir.CastChecked, f64, u8, math.Float64bits(-0.9), 0, 0},
		{"f64 256 to u8", mir.CastChecked, f64, u8, math.Float64bits(256), 0, types.PanicCast},
		{"i64 3 to f64", mir.CastChecked, i64, f64, 3, math.Float64bits(3), 0},
		{"f64 to f32", mir.CastChecked, f64, f32, math.Float64bits(0.1), uint64(math.Float32bits(0.1)), 0},
		{"u64 0xd800 to char", mir.CastChecked, u64, ch, 0xd800, 0, types.PanicCast},
		{"u64 0x110000 to char", mir.CastChecked, u64, ch, 0x110000, 0, types.PanicCast},
		{"u64 0x41 to char", mir.CastChecked, u64, ch, 0x41, 0x41, 0},
	} {
		got, pc := cast(c.k, c.from, c.to, c.a)
		if pc != c.pc || (pc == 0 && got != c.want) {
			t.Errorf("%s: %#x %v, want %#x %v", c.name, got, pc, c.want, c.pc)
		}
	}
}

// TestF64ToF32Overflow: Cast[f32] of an f64 rounds to nearest even and
// panics only when a finite value rounds to an infinity; NaN and the
// infinities pass through (the same rule codegen follows).
func TestF64ToF32Overflow(t *testing.T) {
	ti := func(w int) *tinfo { return &tinfo{class: cFloat, width: w, ty: &types.Type{}} }
	f64, f32 := ti(8), ti(4)
	max := float64(math.MaxFloat32)
	inf32 := uint64(math.Float32bits(float32(math.Inf(1))))
	for _, c := range []struct {
		name string
		x    float64
		want uint64
		pc   types.PanicCode
	}{
		{"largest f32", max, uint64(math.Float32bits(math.MaxFloat32)), 0},
		{"above the largest, rounds down", max + math.Ldexp(1, 102), uint64(math.Float32bits(math.MaxFloat32)), 0},
		{"halfway, rounds to infinity", max + math.Ldexp(1, 103), 0, types.PanicCast},
		{"1e39", 1e39, 0, types.PanicCast},
		{"-1e39", -1e39, 0, types.PanicCast},
		{"f64 max", math.MaxFloat64, 0, types.PanicCast},
		{"+Inf", math.Inf(1), inf32, 0},
		{"-Inf", math.Inf(-1), inf32 | 1<<31, 0},
		{"NaN", math.NaN(), uint64(types.CanonicalNaN32), 0},
		{"tiny rounds to zero", math.SmallestNonzeroFloat64, 0, 0},
		{"0.1", 0.1, uint64(math.Float32bits(0.1)), 0},
	} {
		got, pc := cast(mir.CastChecked, f64, f32, math.Float64bits(c.x))
		if pc != c.pc || (pc == 0 && got != c.want) {
			t.Errorf("%s: %#x %v, want %#x %v", c.name, got, pc, c.want, c.pc)
		}
	}
}

func TestFloatNoFusion(t *testing.T) {
	a := 1 + math.Ldexp(1, -27)
	b := 1 - math.Ldexp(1, -27)
	p := floatBinary(mir.Mul, 8, math.Float64bits(a), math.Float64bits(b))
	r := f64of(floatBinary(mir.Add, 8, p, math.Float64bits(-1)))
	if fused := math.FMA(a, b, -1); r == fused || r != 0 {
		t.Errorf("a*b+c = %g, fused %g: want the twice-rounded 0", r, fused)
	}
	x := floatBinary(mir.Mul, 4, uint64(math.Float32bits(float32(a))), uint64(math.Float32bits(float32(b))))
	if f32of(x) != float32(float32(a)*float32(b)) {
		t.Errorf("f32 product not rounded to f32")
	}
}

func TestCanonicalNaN(t *testing.T) {
	zero64, zero32 := math.Float64bits(0), uint64(math.Float32bits(0))
	if got := floatBinary(mir.Div, 8, zero64, zero64); got != types.CanonicalNaN64 {
		t.Errorf("0/0 f64 = %#x", got)
	}
	if got := floatBinary(mir.Div, 4, zero32, zero32); got != uint64(types.CanonicalNaN32) {
		t.Errorf("0/0 f32 = %#x", got)
	}
	odd := uint64(0xfff8000000000123) // a negative NaN with payload
	if got := floatBinary(mir.Add, 8, odd, zero64); got != types.CanonicalNaN64 {
		t.Errorf("NaN+0 = %#x", got)
	}
	// Negation is no arithmetic: it flips the sign bit only, a NaN's
	// included, as native fneg does.
	ti := &tinfo{class: cFloat, width: 8, ty: &types.Type{}}
	if got, _ := unary(mir.Neg, ti, types.CanonicalNaN64); got != types.CanonicalNaN64|1<<63 {
		t.Errorf("-NaN = %#x", got)
	}
	if got, _ := unary(mir.Neg, ti, odd); got != odd&^(1<<63) {
		t.Errorf("-(%#x) = %#x, want the payload kept", odd, got)
	}
	ti32 := &tinfo{class: cFloat, width: 4, ty: &types.Type{}}
	for _, x := range []uint64{0x7fc00001, 0xffc00000, 0, 0x80000000, 0x3f800000} {
		if got, _ := unary(mir.Neg, ti32, x); got != x^0x80000000 {
			t.Errorf("f32 -(%#x) = %#x", x, got)
		}
	}
}

// panicking declares a function of module lib whose body runs one statement
// built by mk, after an add-to-return; it returns the function.
func (w *world) panicking(name string, mk func(x *bb)) types.ItemID {
	f := w.fn("lib", name, types.Int)
	x := w.body(f, types.Int)
	mk(x)
	x.done()
	return f
}

func TestE0901_Panics(t *testing.T) {
	w := newWorld(t)
	arr := w.array(4, types.Int)
	str := w.strConst("boom")
	cases := []struct {
		name string
		pc   types.PanicCode
		msg  string
		mk   func(x *bb)
	}{
		{"overflow", types.PanicOverflow, "", func(x *bb) {
			x.assign(0, lp(0), bin(mir.Add, imm(types.Int, math.MaxInt64), imm(types.Int, 1)))
			x.ret(0)
		}},
		{"bounds", types.PanicBounds, "", func(x *bb) {
			a, i := x.local(arr), x.local(types.Int)
			x.assign(0, lp(a), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.Int, 7), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 4)}})
			x.assign(0, lp(i), use(imm(types.Int, 4)))
			x.assign(0, lp(0), use(mir.Copy(x.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: types.Int}))))
			x.ret(0)
		}},
		{"divide by zero", types.PanicDivideByZero, "", func(x *bb) {
			z := x.local(types.Int)
			x.assign(0, lp(z), use(imm(types.Int, 0)))
			x.assign(0, lp(0), bin(mir.Div, imm(types.Int, 1), cp(z)))
			x.ret(0)
		}},
		{"shift amount", types.PanicShiftAmount, "", func(x *bb) {
			x.assign(0, lp(0), bin(mir.Shl, imm(types.Int, 1), imm(types.Int, 64)))
			x.ret(0)
		}},
		{"cast", types.PanicCast, "", func(x *bb) {
			b := x.local(types.U8)
			x.assign(0, lp(b), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastChecked, A: imm(types.Int, 300), Type: types.U8})
			x.ret(0)
		}},
		{"explicit", types.PanicExplicit, "boom", func(x *bb) {
			x.term(0, mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicExplicit, HasMsg: true, Msg: mir.Const(types.Str, str)})
		}},
		{"assert", types.PanicAssert, "", func(x *bb) {
			x.term(0, mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicAssert})
		}},
		{"unreachable", types.PanicUnreachable, "", func(x *bb) {
			x.term(0, mir.Terminator{Kind: mir.TermUnreachable})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := w.panicking("P"+c.name, c.mk)
			r := w.call(w.evaluator(0), f, 0)
			if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != c.pc || r.Err.Message != c.msg {
				t.Fatalf("got %+v, want panic %v %q", r.Err, c.pc, c.msg)
			}
			if len(r.Err.Items) != 1 || r.Err.Items[0] != f || r.Err.Trace[0] != r.Err.At || !r.Err.At.IsValid() {
				t.Errorf("trace %v %v at %v", r.Err.Items, r.Err.Trace, r.Err.At)
			}
		})
	}
}

// strConst interns a str const whose bytes are s.
func (w *world) strConst(s string) types.ConstID {
	arr := w.array(len(s), types.U8)
	data := w.consts.Intern(types.ConstValue{Type: arr, Align: 1, Bytes: []byte(s)})
	b := make([]byte, 16)
	types.StoreUint(b[8:], 8, uint64(len(s)))
	v := types.ConstValue{Type: types.Str, Align: 8, Bytes: b, Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: data}}}
	if err := v.Canonicalize(8); err != nil {
		w.t.Fatal(err)
	}
	return w.consts.Intern(v)
}

func TestE0901_PanicTraceThroughCalls(t *testing.T) {
	w := newWorld(t)
	inner := w.panicking("Inner", func(x *bb) { x.term(0, mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicAssert}) })
	outer := w.fn("lib", "Outer", types.Int)
	x := w.body(outer, types.Int)
	b1 := x.block()
	callAt := x.call(0, inner, lp(0), b1)
	x.ret(b1)
	x.done()
	r := w.call(w.evaluator(0), outer, 0)
	if r.Err == nil || r.Err.Panic != types.PanicAssert {
		t.Fatalf("got %+v", r.Err)
	}
	if len(r.Err.Items) != 2 || r.Err.Items[0] != outer || r.Err.Items[1] != inner || r.Err.Trace[0] != x.b.Span(callAt) {
		t.Errorf("trace items %v spans %v, want [Outer Inner] from the call", r.Err.Items, r.Err.Trace)
	}
}

func TestStackLimit(t *testing.T) {
	old := debug.SetMaxStack(1 << 20) // a Go recursion per Mk call would crash
	defer debug.SetMaxStack(old)
	w := newWorld(t)
	f := w.fn("lib", "Deep", types.Int, types.Int)
	x := w.body(f, types.Int, types.Int)
	n := x.local(types.Int)
	b1 := x.block()
	x.assign(0, lp(n), bin(mir.Add, cp(1), imm(types.Int, 1)))
	x.call(0, f, lp(0), b1, cp(n))
	x.ret(b1)
	x.done()
	r := w.call(w.evaluator(0), f, types.DefaultFuel, w.intVal(types.Int, 0)) // the default fuel keeps the 1 MiB limit (framelimit.go)
	if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicStackLimit {
		t.Fatalf("got %+v, want the stack limit panic", r.Err)
	}
	// Each frame is 3 int locals (24 bytes) rounded to 32, plus 16: 48 bytes.
	if want := DefaultStackLimit / 48; len(r.Err.Items) != 65 {
		t.Errorf("trace of %d entries, want the 64-entry cap plus marker (depth ~%d)", len(r.Err.Items), want)
	}
	if r.Err.Items[32] != types.NoItem || r.Err.Trace[32] != (source.Span{}) {
		t.Errorf("no elision marker at 32")
	}
	small := w.evaluator(1000)
	if r := w.call(small, f, 1<<40, w.intVal(types.Int, 0)); r.Err == nil || r.Err.Panic != types.PanicStackLimit || len(r.Err.Items) != 1000/48 {
		t.Errorf("1000-byte limit: %+v (%d frames)", r.Err, len(r.Err.Items))
	}
}

func TestOnerrFromBorrowFacts(t *testing.T) {
	w := newWorld(t)
	f := w.fn("lib", "G", types.Int)
	x := w.body(f, types.Int)
	x.b.Defers = append(x.b.Defers, mir.DeferInfo{Onerr: true, Span: x.sp()})
	yes := x.b.AddBlock(mir.OriginOnerr, 0)
	no := x.block()
	v := x.local(types.Int)
	x.term(0, mir.Terminator{Kind: mir.TermOnerrGuard, Value: v, Yes: yes, No: no})
	x.assign(yes, lp(0), use(imm(types.Int, 1)))
	x.ret(yes)
	x.assign(no, lp(0), use(imm(types.Int, 2)))
	x.ret(no)
	x.done()
	for _, runs := range []bool{true, false} {
		w.bodies[f].OnerrRuns = []bool{runs, false, false}
		r := w.call(w.evaluator(0), f, 0)
		want := int64(2)
		if runs {
			want = 1
		}
		if r.Err != nil || w.intOf(r.Value) != want {
			t.Errorf("OnerrRuns %v: %+v %d, want %d", runs, r.Err, r.Value, want)
		}
	}
	w.bodies[f].OnerrRuns = nil
	if msg := iceOf(func() { w.call(w.evaluator(0), f, 0) }); msg == "" {
		t.Errorf("TermOnerrGuard with nil OnerrRuns is not an ICE")
	}
}

func TestContractArgsAreRefs(t *testing.T) {
	w := newWorld(t)
	ref := w.tab.Ref(types.Int)
	pred := w.fn("lib", "F#requires0", types.Bool, ref)
	x := w.body(pred, types.Bool, ref)
	x.assign(0, lp(0), bin(mir.Gt, mir.Copy(x.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: types.Int})), imm(types.Int, 0)))
	x.ret(0)
	x.done()
	e := w.evaluator(0)
	req := func(args ...types.ConstID) *types.EvalRequest {
		return &types.EvalRequest{What: types.EvalContract, Item: pred, Args: args, Stage: types.StageSignatures,
			Module: w.module("main"), Site: w.span(), Target: types.X86_64Windows}
	}
	for n, want := range map[int64]int64{3: 1, 0: 0} {
		r := e.Eval(req(w.intVal(types.Int, n)))
		if r.Err != nil || w.intOf(r.Value) != want {
			t.Errorf("n = %d: %+v %d, want %d", n, r.Err, r.Value, want)
		}
	}
	if msg := iceOf(func() { e.Eval(req(w.intVal(types.Int, 1), w.intVal(types.Int, 2))) }); msg == "" {
		t.Errorf("two arguments for a one-parameter predicate is not an ICE")
	}
	if msg := iceOf(func() { e.Eval(req(types.NoConst)) }); msg == "" {
		t.Errorf("a NoConst argument is not an ICE")
	}
}

// TestLongConstChainNoGoStack: a chain of n consts, each one more than the
// last, evaluates from its far end with a Go stack of 1 MiB, about 5 bytes
// per link: a Go call per link would overflow it.
func TestLongConstChainNoGoStack(t *testing.T) {
	n := 200_000
	if testing.Short() {
		n = 20_000
	}
	old := debug.SetMaxStack(1 << 20)
	defer debug.SetMaxStack(old)
	w := newWorld(t)
	cs := make([]types.ItemID, n+1)
	for i := 0; i <= n; i++ {
		cs[i] = w.constItem("main", "A"+itoa(i), types.Int, 0)
	}
	for i := 0; i <= n; i++ {
		x := w.body(cs[i], types.Int)
		if i == 0 {
			x.assign(0, lp(0), use(imm(types.Int, 0)))
		} else {
			t0 := x.local(types.Int)
			x.assign(0, lp(t0), mir.Rvalue{Kind: mir.RvConstRef, Item: cs[i-1]})
			x.assign(0, lp(0), bin(mir.Add, cp(t0), imm(types.Int, 1)))
		}
		x.ret(0)
		if i < 2 || i == n {
			x.done() // validated; the rest are the same shape
		} else {
			w.bodies[cs[i]] = &Body{MIR: x.b, State: BodyReady}
		}
	}
	e := w.evaluator(0)
	r := e.Eval(constReq(w, cs[n], types.StageBodies))
	if r.Err != nil || w.intOf(r.Value) != int64(n) {
		t.Fatalf("A%d = %+v %d", n, r.Err, r.Value)
	}
	// The same slot contents as evaluating in item order.
	e2 := w.evaluator(0)
	for i := 0; i <= n; i += max(1, n/50) {
		a := e2.Eval(constReq(w, cs[i], types.StageBodies))
		b, ok := e.Value(cs[i])
		if !ok || a.Value != b {
			t.Fatalf("A%d: in order %d, last first %d (%v)", i, a.Value, b, ok)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
