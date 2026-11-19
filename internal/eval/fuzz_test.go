// Why: the evaluator runs arbitrary MIR at compile time, so no input may
// crash or hang it, and its two fuel paths must never disagree.
// What: FuzzEval, its body builder fuzzBody, and TestFuzzEval_ReachesRaw.
// How: random small well-formed MIR from the fuzz bytes; the property is no Go
// panic or ICE, no hang, and the same steps and outcome whether blocks are
// charged once or statement by statement.

package eval

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

type fuzzSrc struct {
	b []byte
	i int
}

func (s *fuzzSrc) n(k int) int {
	if k <= 1 {
		return 0
	}
	if s.i >= len(s.b) {
		return 0
	}
	v := int(s.b[s.i])
	s.i++
	return v % k
}

// fuzzBody builds F(p: int): int over int, i32 and u8 locals: blocks of
// arithmetic, casts and compares, ended by goto, switch, return, a
// recursive call, a panic or an index into a local array, or by a call of a
// std.raw pointer intrinsic (Of over an int local or an array element,
// Offset, Read, Write, Same, IsNull, Null) on two *int locals, so raw access
// in and out of bounds, null and frame memory are reached.
func fuzzBody(w *world, src *fuzzSrc) (types.ItemID, bool) {
	f := w.fn("lib", "F", types.Int, types.Int)
	x := w.body(f, types.Int, types.Int)
	tys := []types.TypeID{types.Int, types.I32, types.U8, types.F64, types.Bool}
	arr := w.array(3, types.Int)
	var locals [5][]mir.LocalID
	locals[0] = append(locals[0], 0, 1)
	for i := 0; i < 2+src.n(5); i++ {
		k := src.n(len(tys))
		locals[k] = append(locals[k], x.local(tys[k]))
	}
	for k := 1; k < len(tys); k++ {
		if len(locals[k]) == 0 {
			locals[k] = append(locals[k], x.local(tys[k]))
		}
	}
	a := x.local(arr)
	cnd := x.local(types.Bool)
	pi, ri := w.tab.RawPtr(types.Int), w.tab.Ref(types.Int)
	ofR, offR := w.raw("Of", pi, ri), w.raw("Offset", pi, pi, types.Int)
	readR, writeR := w.raw("Read", types.Int, pi), w.raw("Write", types.Void, pi, types.Int)
	sameR, isNullR, nullR := w.raw("Same", types.Bool, pi, pi), w.raw("IsNull", types.Bool, pi), w.raw("Null", pi)
	ptrs := [2]mir.LocalID{x.local(pi), x.local(pi)}
	ref, void := x.local(ri), x.local(types.Void)
	nb := 1 + src.n(5)
	for len(x.b.Blocks) < nb {
		x.block()
	}
	operand := func(k int) mir.Operand {
		switch src.n(4) {
		case 0:
			v := int64(src.n(256)) - 64
			switch tys[k] {
			case types.F64:
				return mir.Imm(types.F64, math.Float64bits(float64(v)/4))
			case types.Bool:
				return imm(types.Bool, v&1)
			case types.U8:
				return imm(types.U8, v&0xff)
			case types.I32:
				return imm(types.I32, v&0xffffffff)
			}
			return imm(tys[k], v)
		case 1:
			if tys[k] != types.F64 && tys[k] != types.Bool {
				return mir.Const(tys[k], w.intVal(tys[k], int64(src.n(100))))
			}
		}
		return cp(locals[k][src.n(len(locals[k]))])
	}
	ops := []mir.BinOp{mir.Add, mir.Sub, mir.Mul, mir.Div, mir.Rem, mir.AddWrap, mir.MulWrap, mir.Shl, mir.Shr, mir.BitAnd, mir.BitXor}
	fops := []mir.BinOp{mir.Add, mir.Sub, mir.Mul, mir.Div}
	bops := []mir.BinOp{mir.BitAnd, mir.BitOr, mir.BitXor, mir.Eq, mir.Ne}
	for b := 0; b < nb; b++ {
		blk := mir.BlockID(b)
		for s := 0; s < src.n(5); s++ {
			k := src.n(len(tys))
			dst := lp(locals[k][src.n(len(locals[k]))])
			switch src.n(5) {
			case 0:
				switch tys[k] {
				case types.F64:
					x.assign(blk, dst, bin(fops[src.n(len(fops))], operand(k), operand(k)))
				case types.Bool:
					x.assign(blk, dst, bin(bops[src.n(len(bops))], operand(k), operand(k)))
				default:
					x.assign(blk, dst, bin(ops[src.n(len(ops))], operand(k), operand(k)))
				}
			case 4:
				switch tys[k] {
				case types.Bool:
					x.assign(blk, dst, mir.Rvalue{Kind: mir.RvUnary, Un: mir.Not, A: operand(k)})
				case types.F64, types.Int, types.I32:
					x.assign(blk, dst, mir.Rvalue{Kind: mir.RvUnary, Un: mir.Neg, A: operand(k)})
				default:
					x.assign(blk, dst, use(operand(k)))
				}
			case 1:
				if k > 2 {
					x.assign(blk, dst, use(operand(k)))
					break
				}
				from := src.n(3)
				if src.n(4) == 0 {
					from = 3 // f64 to an integer, checked or truncated
				}
				x.assign(blk, dst, mir.Rvalue{Kind: mir.RvCast, Cast: []mir.CastKind{mir.CastChecked, mir.CastTrunc}[src.n(2)], A: operand(from), Type: tys[k]})
			case 2:
				x.assign(blk, lp(a), mir.Rvalue{Kind: mir.RvRepeat, A: operand(0), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 3)}})
			default:
				idx := locals[0][src.n(len(locals[0]))]
				x.assign(blk, lp(0), use(mir.Copy(x.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: types.Int}))))
			}
		}
		target := func() mir.BlockID { return mir.BlockID(src.n(nb)) }
		switch src.n(6) {
		case 0:
			x.gotoB(blk, target())
		case 1:
			k := src.n(len(tys))
			if k == 4 {
				x.branch(blk, operand(k), target(), target())
				break
			}
			x.assign(blk, lp(cnd), bin(mir.Lt, operand(k), operand(k)))
			x.branch(blk, cp(cnd), target(), target())
		case 2:
			n := x.local(types.Int)
			x.assign(blk, lp(n), bin(mir.SubWrap, cp(1), imm(types.Int, 1)))
			x.call(blk, f, lp(locals[0][src.n(len(locals[0]))]), target(), cp(n))
		case 3:
			x.term(blk, mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicAssert})
		case 4:
			p, ty, next := ptrs[src.n(2)], []types.TypeID{types.Int}, target()
			il := func() mir.LocalID { return locals[0][src.n(len(locals[0]))] }
			switch src.n(7) {
			case 0:
				pl := lp(il())
				if src.n(2) == 0 {
					pl = x.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(il()), Type: types.Int})
				}
				x.assign(blk, lp(ref), mir.Rvalue{Kind: mir.RvRef, Place: pl})
				x.callG(blk, ofR, ty, lp(p), next, cp(ref))
			case 1:
				x.callG(blk, offR, ty, lp(p), next, cp(p), operand(0))
			case 2:
				x.callG(blk, readR, ty, lp(il()), next, cp(p))
			case 3:
				x.callG(blk, writeR, ty, lp(void), next, cp(p), operand(0))
			case 4:
				x.callG(blk, sameR, ty, lp(locals[4][src.n(len(locals[4]))]), next, cp(ptrs[0]), cp(ptrs[1]))
			case 5:
				x.callG(blk, isNullR, ty, lp(locals[4][src.n(len(locals[4]))]), next, cp(p))
			default:
				x.callG(blk, nullR, ty, lp(p), next)
			}
		default:
			x.ret(blk)
		}
	}
	if err := x.b.Validate(&mir.Env{Types: w.tab}); err != nil {
		return 0, false
	}
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	return f, true
}

func FuzzEval(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	f.Add([]byte{0, 0, 0, 0, 0, 2, 0, 0, 2, 4, 4})
	f.Add([]byte{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9, 3, 2, 3, 8, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		w := newWorld(t)
		fn, ok := fuzzBody(w, &fuzzSrc{b: data})
		if !ok {
			return
		}
		var r types.EvalResult
		if ie := source.Guard("FuzzEval", source.Span{}, func() {
			r = w.call(w.evaluator(4096), fn, 20000, w.intVal(types.Int, int64(len(data))))
		}); ie != nil {
			t.Fatalf("Go panic: %s", ie.What)
		}
		if (r.Err == nil) == (r.Value == types.NoConst) {
			t.Fatalf("neither a value nor an error: %+v", r)
		}
		if r.Err != nil && r.Err.Kind == types.EvalFuel && r.Err.Steps > 20000 {
			t.Fatalf("steps %d over the fuel", r.Err.Steps)
		}
		if r.Err != nil && r.Err.Kind == types.EvalFuel {
			return
		}
		// Differential: blocks charged once with ample fuel and statement by
		// statement near the limit give the same steps. With fuel equal to the
		// steps used the outcome is the same; with one less the last charge
		// does not fit. The flat form agrees with the general path (fast.go).
		ref := w.evaluator(4096)
		ref.noFlat = true
		slow := w.call(ref, fn, 20000, w.intVal(types.Int, int64(len(data))))
		if !sameOutcome(slow, r) {
			t.Fatalf("general path %+v %+v, flat %+v %+v", slow, slow.Err, r, r.Err)
		}
		s := r.Steps
		again := w.call(w.evaluator(4096), fn, s, w.intVal(types.Int, int64(len(data))))
		if !sameOutcome(again, r) {
			t.Fatalf("fuel %d: %+v %+v, with ample fuel %+v %+v", s, again, again.Err, r, r.Err)
		}
		if s > 1 {
			less := w.call(w.evaluator(4096), fn, s-1, w.intVal(types.Int, int64(len(data))))
			if less.Err == nil || less.Err.Kind != types.EvalFuel || less.Steps > s-1 {
				t.Fatalf("fuel %d: %+v %+v, want exhaustion", s-1, less, less.Err)
			}
		}
	})
}

// sameOutcome compares two results: steps, value, and the error's kind,
// panic, refusal, message and site.
func sameOutcome(x, y types.EvalResult) bool {
	if x.Steps != y.Steps || x.Value != y.Value || (x.Err == nil) != (y.Err == nil) {
		return false
	}
	if x.Err == nil {
		return true
	}
	a, b := x.Err, y.Err
	return a.Kind == b.Kind && a.Panic == b.Panic && a.Refusal == b.Refusal && a.Message == b.Message && a.At == b.At && a.Steps == b.Steps
}

// TestFuzzEval_ReachesRaw: fuzzBody's std.raw terminators validate and run.
// Over 3,000 fixed pseudo-random inputs, bodies that call a std.raw
// intrinsic are built, none raises a Go panic or an ICE, some give a value,
// and some are refused raw access (a null or out-of-bounds Read or Write),
// so FuzzEval covers the pointer intrinsics and does not silently drop them
// at Validate.
func TestFuzzEval_ReachesRaw(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var withRaw, values, refused int
	for i := 0; i < 3000; i++ {
		data := make([]byte, 8+rng.Intn(40))
		rng.Read(data)
		w := newWorld(t)
		fn, ok := fuzzBody(w, &fuzzSrc{b: data})
		if !ok {
			continue
		}
		raw := false
		b := w.bodies[fn].MIR
		for _, blk := range b.Blocks {
			if blk.Term.Kind == mir.TermCall && strings.HasPrefix(w.items.Get(blk.Term.Callee.Fn.Item).Key.String(), "std.raw.") {
				raw = true
			}
		}
		if !raw {
			continue
		}
		withRaw++
		var r types.EvalResult
		if ie := source.Guard("TestFuzzEval_ReachesRaw", source.Span{}, func() {
			r = w.call(w.evaluator(4096), fn, 20000, w.intVal(types.Int, int64(len(data))))
		}); ie != nil {
			t.Fatalf("input %x: Go panic: %s", data, ie.What)
		}
		switch {
		case r.Err == nil:
			values++
		case r.Err.Kind == types.EvalRefused && r.Err.Refusal == types.RefuseRawAccess:
			refused++
		}
	}
	t.Logf("bodies with a std.raw call: %d, values %d, refused raw access %d", withRaw, values, refused)
	if withRaw < 300 || values == 0 || refused == 0 {
		t.Fatalf("bodies with a std.raw call: %d (want at least 300), values %d, refused raw access %d (want both > 0)", withRaw, values, refused)
	}
}
