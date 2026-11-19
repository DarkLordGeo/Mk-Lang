// Why: std.simd code runs at compile time too, so the evaluator's vector
// intrinsics must give exactly what native code gives, NaNs and edges included.
// What: every lane-wise intrinsic against a plain Go reference on random lanes
// of every lane type; ReduceSum's tree order, Min and Max on NaN and signed
// zeros, Fma's single rounding on f32, conversions, panics, Cpu()'s baseline,
// std.mmio refusals and Lanes. The memory intrinsics (FromSlice, CopyTo,
// Gather, masked forms) are covered by codegen's differential test.
// How: the references use Go's own typed arithmetic, not arith.go, so a fault
// shared by both cannot hide.

package eval

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// lt is a lane type for the references.
type lt struct {
	name   string
	id     types.TypeID
	w      int // bytes
	signed bool
	float  bool
}

var laneTypes = []lt{
	{"i8", types.I8, 1, true, false}, {"i16", types.I16, 2, true, false}, {"i32", types.I32, 4, true, false},
	{"i64", types.I64, 8, true, false}, {"u8", types.U8, 1, false, false}, {"u16", types.U16, 2, false, false},
	{"u32", types.U32, 4, false, false}, {"u64", types.U64, 8, false, false}, {"int", types.Int, 8, true, false},
	{"uint", types.Uint, 8, false, false}, {"f32", types.F32, 4, false, true}, {"f64", types.F64, 8, false, true},
}

func (l lt) mask() uint64 {
	if l.w == 8 {
		return ^uint64(0)
	}
	return 1<<(8*uint(l.w)) - 1
}

func (l lt) sx(x uint64) int64 { s := 64 - 8*uint(l.w); return int64(x<<s) >> s }

func (l lt) fl(x uint64) float64 {
	if l.w == 4 {
		return float64(math.Float32frombits(uint32(x)))
	}
	return math.Float64frombits(x)
}

func (l lt) fb(f float64) uint64 {
	if l.w == 4 {
		return uint64(math.Float32bits(float32(f)))
	}
	return math.Float64bits(f)
}

func (l lt) isNaN(x uint64) bool { return l.float && math.IsNaN(l.fl(x)) }

// less is the lane order (signed, unsigned or float).
func (l lt) less(x, y uint64) bool {
	switch {
	case l.float:
		return l.fl(x) < l.fl(y)
	case l.signed:
		return l.sx(x) < l.sx(y)
	}
	return x < y
}

// rnd is a random lane, with the edge values often.
func (l lt) rnd(r *rand.Rand) uint64 {
	if l.float {
		specials := []float64{0, math.Copysign(0, -1), 1, -1, math.Inf(1), math.Inf(-1), math.NaN(), 0.5, -2.5, 3.5, 1e30, -1e-30}
		if r.IntN(4) == 0 {
			return l.fb(specials[r.IntN(len(specials))])
		}
		return l.fb((r.Float64() - 0.5) * math.Pow(2, float64(r.IntN(40)-20)))
	}
	edges := []uint64{0, 1, l.mask(), l.mask() >> 1, (l.mask() >> 1) + 1, 2}
	if r.IntN(4) == 0 {
		return edges[r.IntN(len(edges))] & l.mask()
	}
	return r.Uint64() & l.mask()
}

// vworld holds the Vec and Mask types of each lane type and count.
type vworld struct {
	*world
	vecs  map[string]types.TypeID
	fns   int
	items map[string]types.ItemID
}

func newVWorld(t *testing.T) *vworld {
	return &vworld{world: newWorld(t), vecs: map[string]types.TypeID{}, items: map[string]types.ItemID{}}
}

// vecT is a struct laid out as Vec[T, N] (one [N]T field).
func (v *vworld) vecT(l lt, n int) types.TypeID {
	k := fmt.Sprint(l.name, "x", n)
	if t, ok := v.vecs[k]; ok {
		return t
	}
	_, t := v.structType("std.simd", "V"+k, v.array(n, l.id))
	v.vecs[k] = t
	return t
}

// maskT is Mask[T, N]: the item std.simd.Mask at [T, N], laid out as
// [N]T (its layout given, since the test world substitutes no arrays).
func (v *vworld) maskT(l lt, n int) types.TypeID {
	k := fmt.Sprint("mask", l.name, "x", n)
	if t, ok := v.vecs[k]; ok {
		return t
	}
	mi, ok := v.items["#mask"]
	if !ok {
		mi = v.item("std.simd", "Mask", types.KindStruct, &types.StructDecl{})
		v.items["#mask"] = mi
	}
	t := v.tab.Named(mi, []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)})
	v.layouts[t] = &types.Layout{Shape: types.Shape{Size: uint64(n * l.w), Align: uint32(l.w)}}
	v.vecs[k] = t
	return t
}

func (v *vworld) intrinsic(key string) types.ItemID {
	if f, ok := v.items[key]; ok {
		return f
	}
	f := v.fn("std.simd", strings.TrimPrefix(key, "std.simd."), types.Void)
	fd := v.world.items.Get(f).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	v.items[key] = f
	return f
}

func constN(w *world, n int) types.GenericArg {
	return types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, int64(n))})
}

// vecConst is a Vec (or Mask) const of type vt with the given lanes.
func (v *vworld) vecConst(vt types.TypeID, l lt, lanes []uint64) types.ConstID {
	b := make([]byte, len(lanes)*l.w)
	for j, x := range lanes {
		types.StoreUint(b[j*l.w:], l.w, x)
	}
	return v.consts.Intern(types.ConstValue{Type: vt, Align: uint32(l.w), Bytes: b})
}

func (v *vworld) scalarConst(l lt, x uint64) types.ConstID {
	b := make([]byte, l.w)
	types.StoreUint(b, l.w, x)
	return v.consts.Intern(types.ConstValue{Type: l.id, Align: uint32(l.w), Bytes: b})
}

// run calls key[ga] with the const arguments (params of the given types)
// followed by the immediates, and returns the result.
func (v *vworld) run(key string, ga []types.GenericArg, result types.TypeID, params []types.TypeID, args []types.ConstID, imms ...mir.Operand) types.EvalResult {
	v.fns++
	f := v.fn("main", fmt.Sprintf("F%d", v.fns), result, params...)
	x := v.body(f, result, params...)
	var as []mir.Arg
	for i := range params {
		as = append(as, mir.Arg{Operand: cp(mir.LocalID(i + 1))})
	}
	for _, o := range imms {
		as = append(as, mir.Arg{Operand: o})
	}
	next := x.block()
	x.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: v.intrinsic(key), Args: x.b.AddGenericArgs(ga...)}},
		Args: x.b.AddArgs(as...), Dest: lp(0), Target: next, Spawn: mir.NoLocal})
	x.ret(next)
	x.done()
	return v.call(v.evaluator(0), f, 0, args...)
}

// lanesOf decodes a result const as lanes of l.
func (v *vworld) lanesOf(t *testing.T, r types.EvalResult, l lt) []uint64 {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("evaluation failed: %+v", r.Err)
	}
	b := v.consts.Get(r.Value).Bytes
	out := make([]uint64, len(b)/l.w)
	for j := range out {
		out[j] = types.LoadUint(b[j*l.w:], l.w)
	}
	return out
}

func sameLanes(l lt, got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for j := range got {
		if got[j] != want[j] && !(l.isNaN(got[j]) && l.isNaN(want[j])) {
			return false
		}
	}
	return true
}

// binRef is a lane-wise binary reference; ok false means the key does not
// apply to the lane type.
type binRef func(l lt, x, y uint64) (r uint64, ok bool)

func intOnly(f func(l lt, x, y uint64) uint64) binRef {
	return func(l lt, x, y uint64) (uint64, bool) {
		if l.float {
			return 0, false
		}
		return f(l, x, y) & l.mask(), true
	}
}

func floatOnly(f func(x, y float64, f32 bool) float64) binRef {
	return func(l lt, x, y uint64) (uint64, bool) {
		if !l.float {
			return 0, false
		}
		return l.fb(f(l.fl(x), l.fl(y), l.w == 4)), true
	}
}

// f32r rounds to the lane's precision explicitly (no fusing).
func f32r(x float64, f32 bool) float64 {
	if f32 {
		return float64(float32(x))
	}
	return x
}

func refMinMax(l lt, x, y uint64, max bool) uint64 {
	if l.float {
		a, b := l.fl(x), l.fl(y)
		switch {
		case math.IsNaN(a) || math.IsNaN(b):
			return l.fb(math.NaN())
		case a == 0 && b == 0:
			if max == math.Signbit(a) { // max picks +0, min picks -0
				return y
			}
			return x
		case (a < b) != max:
			return x
		}
		return y
	}
	if l.less(x, y) != max {
		return x
	}
	return y
}

func refSat(l lt, x, y uint64, sub bool) uint64 {
	if l.signed {
		lo, hi := -int64(1)<<(8*l.w-1), int64(1)<<(8*l.w-1)-1
		a, b := l.sx(x), l.sx(y)
		var r int64
		var over bool
		if sub {
			r = a - b
			over = (b < 0 && r < a) || (b > 0 && r > a)
		} else {
			r = a + b
			over = (b > 0 && r < a) || (b < 0 && r > a)
		}
		switch {
		case over && a < 0, r < lo:
			r = lo
		case over, r > hi:
			r = hi
		}
		return uint64(r) & l.mask()
	}
	if sub {
		if y > x {
			return 0
		}
		return x - y
	}
	r := x + y
	if r < x || r > l.mask() {
		return l.mask()
	}
	return r
}

var binRefs = map[string]binRef{
	"Add":     floatOnly(func(x, y float64, f bool) float64 { return f32r(x+y, f) }),
	"Sub":     floatOnly(func(x, y float64, f bool) float64 { return f32r(x-y, f) }),
	"Mul":     floatOnly(func(x, y float64, f bool) float64 { return f32r(x*y, f) }),
	"Div":     floatOnly(func(x, y float64, f bool) float64 { return f32r(x/y, f) }),
	"AddWrap": intOnly(func(_ lt, x, y uint64) uint64 { return x + y }),
	"SubWrap": intOnly(func(_ lt, x, y uint64) uint64 { return x - y }),
	"MulWrap": intOnly(func(_ lt, x, y uint64) uint64 { return x * y }),
	"And":     intOnly(func(_ lt, x, y uint64) uint64 { return x & y }),
	"Or":      intOnly(func(_ lt, x, y uint64) uint64 { return x | y }),
	"Xor":     intOnly(func(_ lt, x, y uint64) uint64 { return x ^ y }),
	"Min":     func(l lt, x, y uint64) (uint64, bool) { return refMinMax(l, x, y, false), true },
	"Max":     func(l lt, x, y uint64) (uint64, bool) { return refMinMax(l, x, y, true), true },
	"AddSat":  intOnly(func(l lt, x, y uint64) uint64 { return refSat(l, x, y, false) }),
	"SubSat":  intOnly(func(l lt, x, y uint64) uint64 { return refSat(l, x, y, true) }),
	"MulHigh": func(l lt, x, y uint64) (uint64, bool) {
		if l.float || l.w > 4 {
			return 0, false
		}
		if l.signed {
			return uint64((l.sx(x)*l.sx(y))>>(8*l.w)) & l.mask(), true
		}
		return (x * y >> (8 * l.w)) & l.mask(), true
	},
}

type unRef func(l lt, x uint64) (uint64, bool)

func floatUn(f func(x float64) float64) unRef {
	return func(l lt, x uint64) (uint64, bool) {
		if !l.float {
			return 0, false
		}
		return l.fb(f(l.fl(x))), true
	}
}

var unRefs = map[string]unRef{
	"Neg":         floatUn(func(x float64) float64 { return -x }),
	"Abs":         floatUn(math.Abs),
	"Sqrt":        floatUn(math.Sqrt),
	"Floor":       floatUn(math.Floor),
	"Ceil":        floatUn(math.Ceil),
	"RoundEven":   floatUn(math.RoundToEven),
	"RoundToZero": floatUn(math.Trunc),
	"Not": func(l lt, x uint64) (uint64, bool) {
		return ^x & l.mask(), !l.float
	},
	"AbsWrap": func(l lt, x uint64) (uint64, bool) {
		if !l.signed {
			return 0, false
		}
		if v := l.sx(x); v < 0 {
			return uint64(-v) & l.mask(), true
		}
		return x, true
	},
}

var counts = []int{2, 4, 16, 64}

func TestLaneWise(t *testing.T) {
	v := newVWorld(t)
	r := rand.New(rand.NewPCG(12, 34))
	for _, l := range laneTypes {
		for _, n := range counts {
			vt := v.vecT(l, n)
			ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
			gen := func() []uint64 {
				xs := make([]uint64, n)
				for j := range xs {
					xs[j] = l.rnd(r)
				}
				return xs
			}
			for _, name := range sortedKeys(binRefs) {
				ref := binRefs[name]
				if _, ok := ref(l, 0, 0); !ok {
					continue
				}
				a, b := gen(), gen()
				want := make([]uint64, n)
				for j := range want {
					want[j], _ = ref(l, a[j], b[j])
				}
				got := v.lanesOf(t, v.run("std.simd.Vec:"+name, ga, vt, []types.TypeID{vt, vt}, []types.ConstID{v.vecConst(vt, l, a), v.vecConst(vt, l, b)}), l)
				if !sameLanes(l, got, want) {
					t.Errorf("%s Vec[%s, %d]: a %x b %x got %x want %x", name, l.name, n, a, b, got, want)
				}
			}
			for _, name := range sortedKeys(unRefs) {
				ref := unRefs[name]
				if _, ok := ref(l, 0); !ok {
					continue
				}
				a := gen()
				want := make([]uint64, n)
				for j := range want {
					want[j], _ = ref(l, a[j])
				}
				got := v.lanesOf(t, v.run("std.simd.Vec:"+name, ga, vt, []types.TypeID{vt}, []types.ConstID{v.vecConst(vt, l, a)}), l)
				if !sameLanes(l, got, want) {
					t.Errorf("%s Vec[%s, %d]: a %x got %x want %x", name, l.name, n, a, got, want)
				}
			}
			laneWiseCompare(t, v, l, n, gen)
			laneWisePermute(t, v, l, n, gen)
			laneWiseReduce(t, v, l, n, gen)
			laneWiseMask(t, v, l, n, r)
			laneWiseConstruct(t, v, l, n)
			laneWiseShift(t, v, l, n, gen, r)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	for i := range ks {
		for j := i + 1; j < len(ks); j++ {
			if ks[j] < ks[i] {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	return ks
}

func laneWiseCompare(t *testing.T, v *vworld, l lt, n int, gen func() []uint64) {
	vt := v.vecT(l, n)
	ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
	cmps := map[string]func(x, y uint64) bool{
		"Eq": func(x, y uint64) bool {
			if l.float {
				return l.fl(x) == l.fl(y)
			}
			return x == y
		},
		"Ne": func(x, y uint64) bool {
			if l.float {
				return l.fl(x) != l.fl(y) // true on NaN (fcmp une)
			}
			return x != y
		},
		"Lt": func(x, y uint64) bool { return l.less(x, y) },
		"Le": func(x, y uint64) bool {
			if l.float {
				return l.fl(x) <= l.fl(y)
			}
			return !l.less(y, x)
		},
		"Gt": func(x, y uint64) bool { return l.less(y, x) },
		"Ge": func(x, y uint64) bool {
			if l.float {
				return l.fl(x) >= l.fl(y)
			}
			return !l.less(x, y)
		},
	}
	for _, name := range sortedKeys(cmps) {
		a, b := gen(), gen()
		b[0] = a[0] // one equal lane
		want := make([]uint64, n)
		for j := range want {
			if cmps[name](a[j], b[j]) {
				want[j] = l.mask()
			}
		}
		got := v.lanesOf(t, v.run("std.simd.Vec:"+name, ga, v.maskT(l, n), []types.TypeID{vt, vt}, []types.ConstID{v.vecConst(vt, l, a), v.vecConst(vt, l, b)}), l)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s Vec[%s, %d]: a %x b %x got %x want %x", name, l.name, n, a, b, got, want)
		}
	}
}

func laneWisePermute(t *testing.T, v *vworld, l lt, n int, gen func() []uint64) {
	vt := v.vecT(l, n)
	ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
	a, b := gen(), gen()
	cat := append(append([]uint64(nil), a...), b...)
	two := map[string]func(j int) int{
		"InterleaveLo": func(j int) int {
			if j%2 == 0 {
				return j / 2
			}
			return n + j/2
		},
		"InterleaveHi": func(j int) int {
			if j%2 == 0 {
				return n/2 + j/2
			}
			return n + n/2 + j/2
		},
		"DeinterleaveEven": func(j int) int { return 2 * j },
		"DeinterleaveOdd":  func(j int) int { return 2*j + 1 },
	}
	check := func(name string, got []uint64, idx func(j int) int) {
		t.Helper()
		want := make([]uint64, n)
		for j := range want {
			want[j] = cat[idx(j)]
		}
		if !sameLanes(l, got, want) {
			t.Errorf("%s Vec[%s, %d]: got %x want %x", name, l.name, n, got, want)
		}
	}
	args2 := []types.ConstID{v.vecConst(vt, l, a), v.vecConst(vt, l, b)}
	for _, name := range sortedKeys(two) {
		check(name, v.lanesOf(t, v.run("std.simd.Vec:"+name, ga, vt, []types.TypeID{vt, vt}, args2), l), two[name])
	}
	args1 := []types.ConstID{v.vecConst(vt, l, a)}
	check("Reverse", v.lanesOf(t, v.run("std.simd.Vec:Reverse", ga, vt, []types.TypeID{vt}, args1), l), func(j int) int { return n - 1 - j })
	k := n/2 + 1
	if k >= n {
		k = 1
	}
	check("Broadcast", v.lanesOf(t, v.run("std.simd.Vec:Broadcast", ga, vt, []types.TypeID{vt}, args1, imm(types.Int, int64(k))), l), func(int) int { return k })
	check("Rotate", v.lanesOf(t, v.run("std.simd.Vec:Rotate", ga, vt, []types.TypeID{vt}, args1, imm(types.Int, int64(k))), l), func(j int) int { return (j + k) % n })
	// Slide(self = a, prev = b, k): the last k lanes of prev, then the
	// first N - k of self.
	check("Slide", v.lanesOf(t, v.run("std.simd.Vec:Slide", ga, vt, []types.TypeID{vt, vt}, args2, imm(types.Int, int64(k))), l), func(j int) int {
		if j < k {
			return n + (n - k + j)
		}
		return j - k
	})
	got := v.lanesOf(t, v.run("std.simd.Vec:Lane", ga, l.id, []types.TypeID{vt}, args1, imm(types.Int, int64(k))), l)
	if !sameLanes(l, got, a[k:k+1]) {
		t.Errorf("Lane(%d) Vec[%s, %d] = %x, want %x", k, l.name, n, got[0], a[k])
	}
	xop := mir.Operand{Kind: mir.OpConst, Type: l.id, Const: v.scalarConst(l, b[0])}
	with := v.lanesOf(t, v.run("std.simd.Vec:With", ga, vt, []types.TypeID{vt}, args1, imm(types.Int, int64(k)), xop), l)
	wantW := append([]uint64(nil), a...)
	wantW[k] = b[0]
	if !sameLanes(l, with, wantW) {
		t.Errorf("With Vec[%s, %d]: got %x want %x", l.name, n, with, wantW)
	}
}

func laneWiseReduce(t *testing.T, v *vworld, l lt, n int, gen func() []uint64) {
	vt := v.vecT(l, n)
	ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
	a := gen()
	args := []types.ConstID{v.vecConst(vt, l, a)}
	one := func(key string, res lt, want uint64) {
		t.Helper()
		got := v.lanesOf(t, v.run("std.simd.Vec:"+key, ga, res.id, []types.TypeID{vt}, args), res)
		if !sameLanes(res, got, []uint64{want}) {
			t.Errorf("%s Vec[%s, %d] of %x = %x, want %x", key, l.name, n, a, got[0], want)
		}
	}
	mn, mx := a[0], a[0]
	for _, x := range a[1:] {
		mn, mx = refMinMax(l, mn, x, false), refMinMax(l, mx, x, true)
	}
	one("ReduceMin", l, mn)
	one("ReduceMax", l, mx)
	if l.float {
		vals := make([]float64, n)
		for j, x := range a {
			vals[j] = l.fl(x)
		}
		for h := n / 2; h >= 1; h /= 2 {
			for j := 0; j < h; j++ {
				vals[j] = f32r(vals[j]+vals[j+h], l.w == 4)
			}
		}
		one("ReduceSum", l, l.fb(vals[0]))
		return
	}
	var sum, and, or, xor uint64 = 0, l.mask(), 0, 0
	var wide int64
	for _, x := range a {
		sum, and, or, xor = sum+x, and&x, or|x, xor^x
		if l.signed {
			wide += l.sx(x)
		} else {
			wide += int64(x)
		}
	}
	one("ReduceSumWrap", l, sum&l.mask())
	one("ReduceAnd", l, and)
	one("ReduceOr", l, or)
	one("ReduceXor", l, xor)
	if l.w <= 4 {
		one("ReduceSumWide", laneTypes[3], uint64(wide)) // i64
	}
}

func laneWiseMask(t *testing.T, v *vworld, l lt, n int, r *rand.Rand) {
	vt, mt := v.vecT(l, n), v.maskT(l, n)
	ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
	bitsIn := r.Uint64()
	lanes := make([]uint64, n)
	var bits uint64
	count := 0
	first := -1
	for j := range lanes {
		if bitsIn>>uint(j)&1 == 1 {
			lanes[j] = l.mask()
			bits |= 1 << uint(j)
			count++
			if first < 0 {
				first = j
			}
		}
	}
	u64, i64, boolT := laneTypes[7], laneTypes[3], lt{"bool", types.Bool, 1, false, false}
	got := v.lanesOf(t, v.run("std.simd.Mask:FromBits", ga, mt, []types.TypeID{types.U64}, []types.ConstID{v.scalarConst(u64, bitsIn)}), l)
	if fmt.Sprint(got) != fmt.Sprint(lanes) {
		t.Errorf("FromBits Mask[%s, %d](%x) = %x, want %x", l.name, n, bitsIn, got, lanes)
	}
	m := []types.ConstID{v.vecConst(mt, l, lanes)}
	scalar := func(key string, res lt, want uint64) {
		t.Helper()
		got := v.lanesOf(t, v.run("std.simd.Mask:"+key, ga, res.id, []types.TypeID{mt}, m), res)
		if got[0] != want {
			t.Errorf("%s Mask[%s, %d] bits %x = %x, want %x", key, l.name, n, bits, got[0], want)
		}
	}
	scalar("Bits", u64, bits)
	scalar("CountSet", i64, uint64(count))
	scalar("Any", boolT, b2u(count > 0))
	scalar("All", boolT, b2u(count == n))
	opt := v.tab.Optional(types.Int)
	res := v.run("std.simd.Mask:FirstSet", ga, opt, []types.TypeID{mt}, m)
	if res.Err != nil {
		t.Fatalf("FirstSet: %+v", res.Err)
	}
	ob := v.consts.Get(res.Value).Bytes
	if (ob[0] == 1) != (first >= 0) || first >= 0 && types.LoadUint(ob[8:], 8) != uint64(first) {
		t.Errorf("FirstSet Mask[%s, %d] bits %x = %x, want lane %d", l.name, n, bits, ob, first)
	}
	a, b := make([]uint64, n), make([]uint64, n)
	for j := range a {
		a[j], b[j] = l.rnd(r), l.rnd(r)
	}
	sel := v.lanesOf(t, v.run("std.simd.Mask:Select", ga, vt, []types.TypeID{mt, vt, vt}, []types.ConstID{m[0], v.vecConst(vt, l, a), v.vecConst(vt, l, b)}), l)
	for j := range sel {
		want := b[j]
		if lanes[j] != 0 {
			want = a[j]
		}
		if !sameLanes(l, sel[j:j+1], []uint64{want}) {
			t.Errorf("Select Mask[%s, %d] lane %d = %x, want %x", l.name, n, j, sel[j], want)
		}
	}
	not := v.lanesOf(t, v.run("std.simd.Mask:Not", ga, mt, []types.TypeID{mt}, m), l)
	for j := range not {
		if not[j] != lanes[j]^l.mask() {
			t.Errorf("Not Mask[%s, %d] lane %d = %x", l.name, n, j, not[j])
		}
	}
	// A lane neither all zeros nor all ones is refused.
	bad := append([]uint64(nil), lanes...)
	bad[0] = 1
	wantRefusal(t, v.run("std.simd.Mask:Any", ga, types.Bool, []types.TypeID{mt}, []types.ConstID{v.vecConst(mt, l, bad)}), types.RefuseRawAccess)
}

func laneWiseConstruct(t *testing.T, v *vworld, l lt, n int) {
	vt := v.vecT(l, n)
	ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
	x := l.mask() >> 2
	if l.float {
		x = l.fb(-1.25)
	}
	splat := v.lanesOf(t, v.run("std.simd.Vec:Splat", ga, vt, []types.TypeID{l.id}, []types.ConstID{v.scalarConst(l, x)}), l)
	iota := v.lanesOf(t, v.run("std.simd.Vec:Iota", ga, vt, nil, nil), l)
	zero := v.lanesOf(t, v.run("std.simd.Vec:Zero", ga, vt, nil, nil), l)
	for j := 0; j < n; j++ {
		wantI := uint64(j) & l.mask()
		if l.float {
			wantI = l.fb(float64(j))
		}
		if splat[j] != x || iota[j] != wantI || zero[j] != 0 {
			t.Errorf("Vec[%s, %d] lane %d: Splat %x Iota %x Zero %x", l.name, n, j, splat[j], iota[j], zero[j])
		}
	}
}

func laneWiseShift(t *testing.T, v *vworld, l lt, n int, gen func() []uint64, r *rand.Rand) {
	if l.float {
		return
	}
	vt := v.vecT(l, n)
	ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, n)}
	a, k := gen(), make([]uint64, n)
	for j := range k {
		k[j] = uint64(r.IntN(8 * l.w))
	}
	shl := v.lanesOf(t, v.run("std.simd.Vec:ShlEach", ga, vt, []types.TypeID{vt, vt}, []types.ConstID{v.vecConst(vt, l, a), v.vecConst(vt, l, k)}), l)
	shr := v.lanesOf(t, v.run("std.simd.Vec:ShrEach", ga, vt, []types.TypeID{vt, vt}, []types.ConstID{v.vecConst(vt, l, a), v.vecConst(vt, l, k)}), l)
	amt := int64(r.IntN(8 * l.w))
	gaK := append(append([]types.GenericArg(nil), ga...), types.TypeArg(types.Int))
	shs := v.lanesOf(t, v.run("std.simd.Vec:Shl", gaK, vt, []types.TypeID{vt}, []types.ConstID{v.vecConst(vt, l, a)}, imm(types.Int, amt)), l)
	sra := v.lanesOf(t, v.run("std.simd.Vec:Shr", gaK, vt, []types.TypeID{vt}, []types.ConstID{v.vecConst(vt, l, a)}, imm(types.Int, amt)), l)
	sr := func(x, s uint64) uint64 {
		if l.signed {
			return uint64(l.sx(x)>>s) & l.mask()
		}
		return x >> s
	}
	for j := range a {
		if shl[j] != (a[j]<<k[j])&l.mask() || shr[j] != sr(a[j], k[j]) || shs[j] != (a[j]<<uint64(amt))&l.mask() || sra[j] != sr(a[j], uint64(amt)) {
			t.Errorf("shifts Vec[%s, %d] lane %d of %x by %d/%d: %x %x %x %x", l.name, n, j, a[j], k[j], amt, shl[j], shr[j], shs[j], sra[j])
		}
	}
}

// TestConversions checks Cast, Trunc, CastSat, Reinterpret, the
// widening forms and NarrowSat on chosen lanes.
func TestConversions(t *testing.T) {
	v := newVWorld(t)
	i8, i16, u8, u16, i32, f32, f64 := laneTypes[0], laneTypes[1], laneTypes[4], laneTypes[5], laneTypes[2], laneTypes[10], laneTypes[11]
	conv := func(key string, from lt, n int, to lt, m int, a []uint64) []uint64 {
		t.Helper()
		ga := []types.GenericArg{types.TypeArg(from.id), constN(v.world, n), types.TypeArg(to.id)}
		if m != n {
			ga = append(ga, constN(v.world, m))
		}
		return v.lanesOf(t, v.run("std.simd.Vec:"+key, ga, v.vecT(to, m), []types.TypeID{v.vecT(from, n)}, []types.ConstID{v.vecConst(v.vecT(from, n), from, a)}), to)
	}
	neg := func(l lt, x int64) uint64 { return uint64(x) & l.mask() }
	if got := conv("WidenLo", i8, 4, i16, 2, []uint64{neg(i8, -1), 5, 7, 9}); fmt.Sprint(got) != fmt.Sprint([]uint64{0xffff, 5}) {
		t.Errorf("WidenLo i8 = %x", got)
	}
	if got := conv("WidenHi", u8, 4, u16, 2, []uint64{1, 2, 0xff, 0x80}); fmt.Sprint(got) != fmt.Sprint([]uint64{0xff, 0x80}) {
		t.Errorf("WidenHi u8 = %x", got)
	}
	if got := conv("WidenLo", f32, 4, f64, 2, []uint64{f32.fb(1.5), f32.fb(-0.25), 0, 0}); got[0] != f64.fb(1.5) || got[1] != f64.fb(-0.25) {
		t.Errorf("WidenLo f32 = %x", got)
	}
	if got := conv("Cast", i32, 4, f32, 4, []uint64{neg(i32, -3), 0, 16777217, 7}); got[0] != f32.fb(-3) || got[2] != f32.fb(16777216) {
		t.Errorf("Cast i32 to f32 = %x", got)
	}
	if got := conv("Trunc", i32, 4, u8, 4, []uint64{0x1234, neg(i32, -1), 0x100, 0x7f}); fmt.Sprint(got) != fmt.Sprint([]uint64{0x34, 0xff, 0, 0x7f}) {
		t.Errorf("Trunc = %x", got)
	}
	if got := conv("CastSat", i32, 4, i8, 4, []uint64{1000, neg(i32, -1000), 5, neg(i32, -5)}); fmt.Sprint(got) != fmt.Sprint([]uint64{0x7f, 0x80, 5, 0xfb}) {
		t.Errorf("CastSat i32 to i8 = %x", got)
	}
	if got := conv("CastSat", f32, 4, u8, 4, []uint64{f32.fb(300), f32.fb(-4), f32.fb(math.NaN()), f32.fb(7.9)}); fmt.Sprint(got) != fmt.Sprint([]uint64{0xff, 0, 0, 7}) {
		t.Errorf("CastSat f32 to u8 = %x", got)
	}
	if got := conv("Reinterpret", u16, 2, u8, 4, []uint64{0x0201, 0x0403}); fmt.Sprint(got) != fmt.Sprint([]uint64{1, 2, 3, 4}) {
		t.Errorf("Reinterpret = %x", got)
	}
	// MulWidenLo and NarrowSat take a second Vec.
	ga := []types.GenericArg{types.TypeArg(i8.id), constN(v.world, 4), types.TypeArg(i16.id), constN(v.world, 2)}
	vi8 := v.vecT(i8, 4)
	mw := v.lanesOf(t, v.run("std.simd.Vec:MulWidenLo", ga, v.vecT(i16, 2), []types.TypeID{vi8, vi8},
		[]types.ConstID{v.vecConst(vi8, i8, []uint64{neg(i8, -128), 100, 0, 0}), v.vecConst(vi8, i8, []uint64{neg(i8, -128), neg(i8, -100), 0, 0})}), i16)
	if fmt.Sprint(mw) != fmt.Sprint([]uint64{16384, neg(i16, -10000)}) {
		t.Errorf("MulWidenLo = %x", mw)
	}
	vi16 := v.vecT(i16, 2)
	gaN := []types.GenericArg{types.TypeArg(i16.id), constN(v.world, 2), types.TypeArg(u8.id), constN(v.world, 4)}
	ns := v.lanesOf(t, v.run("std.simd.Vec:NarrowSat", gaN, v.vecT(u8, 4), []types.TypeID{vi16, vi16},
		[]types.ConstID{v.vecConst(vi16, i16, []uint64{300, neg(i16, -1)}), v.vecConst(vi16, i16, []uint64{5, 255})}), u8)
	if fmt.Sprint(ns) != fmt.Sprint([]uint64{255, 0, 5, 255}) {
		t.Errorf("NarrowSat = %x", ns)
	}
	// A Cast lane that does not fit panics, before any lane is written.
	r := v.run("std.simd.Vec:Cast", []types.GenericArg{types.TypeArg(i32.id), constN(v.world, 4), types.TypeArg(u8.id)}, v.vecT(u8, 4),
		[]types.TypeID{v.vecT(i32, 4)}, []types.ConstID{v.vecConst(v.vecT(i32, 4), i32, []uint64{1, 2, 3, 256})})
	if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicCast {
		t.Errorf("Cast 256 to u8: %+v", r.Err)
	}
}

// TestUnjudgedSizeClausePanics: a generic over the lane types calls
// Reinterpret, a widening form, NarrowSat or Trunc with a size clause, or a
// lane-class rule, that check does not judge for a type parameter, so an
// instance can break it. A size or lane-count clause over a bare lane type
// parameter is restated by the caller; this backstop stays for three cases: a
// compound argument, the lane-class rules, and a restatement check demands and
// does not judge (one that also names SizeOf of a closed type). The evaluator
// panics with PanicContract, as codegen does, instead of writing past the
// result, reading past the source (a larger source was a Go index panic in
// Reinterpret) or converting across lane classes.
func TestUnjudgedSizeClausePanics(t *testing.T) {
	v := newVWorld(t)
	i8, u8, u16, u32, u64, f32, f64 := laneTypes[0], laneTypes[4], laneTypes[5], laneTypes[6], laneTypes[7], laneTypes[10], laneTypes[11]
	for _, c := range []struct {
		key      string
		from     lt
		n        int
		to       lt
		m        int
		operands int
		ok       bool
	}{
		{"Reinterpret", u8, 4, u64, 4, 1, false},    // 4 bytes to 32
		{"Reinterpret", u64, 4, u8, 4, 1, false},    // 32 bytes to 4
		{"WidenLo", u8, 8, u16, 2, 1, false},        // 4 lanes made, 2 held
		{"WidenHi", i8, 4, u64, 4, 1, false},        // 2 lanes made, 4 held
		{"NarrowSat", u16, 4, u8, 4, 2, false},      // 8 lanes made, 4 held
		{"MulWidenLo", u8, 8, u16, 2, 2, false},     // 4 lanes made, 2 held
		{"WidenLo", f64, 4, f32, 2, 1, false},       // float to a narrower float
		{"WidenLo", f32, 4, u64, 2, 1, false},       // float to integer
		{"WidenLo", u8, 4, f32, 2, 1, false},        // integer to float
		{"WidenLo", i8, 4, u16, 2, 1, false},        // signedness changed
		{"Trunc", u32, 4, f32, 4, 1, false},         // integer to float
		{"Trunc", u8, 4, u16, 4, 1, false},          // wider
		{"Reinterpret", u8, 2, u16, 1, 1, false},    // M < 2, the bytes agree
		{"Reinterpret", u16, 64, u8, 128, 1, false}, // M > 64, the bytes agree
		{"WidenLo", u8, 2, u16, 1, 1, false},        // M < 2, the bytes agree
		{"NarrowSat", u32, 4, u8, 8, 2, false},      // SizeOf[T]() is 4 * SizeOf[U]()
		{"NarrowSat", u16, 64, u8, 128, 2, false},   // M > 64
		{"Reinterpret", u16, 2, u8, 4, 1, true},     // the same 4 bytes
		{"WidenLo", u8, 8, u16, 4, 1, true},         // a valid widening
		{"WidenHi", f32, 4, f64, 2, 1, true},        // f32 to f64
		{"NarrowSat", u16, 4, u8, 8, 2, true},       // a valid narrowing
		{"Trunc", u32, 4, u8, 4, 1, true},           // a valid truncation
	} {
		vt := v.vecT(c.from, c.n)
		params, args := []types.TypeID{vt}, []types.ConstID{v.vecConst(vt, c.from, make([]uint64, c.n))}
		if c.operands == 2 {
			params, args = append(params, vt), append(args, args[0])
		}
		ga := []types.GenericArg{types.TypeArg(c.from.id), constN(v.world, c.n), types.TypeArg(c.to.id)}
		if c.key != "Trunc" {
			ga = append(ga, constN(v.world, c.m))
		}
		r := v.run("std.simd.Vec:"+c.key, ga, v.vecT(c.to, c.m), params, args)
		switch {
		case c.ok && r.Err != nil:
			t.Errorf("%s Vec[%s, %d] to Vec[%s, %d]: %+v, want a value", c.key, c.from.name, c.n, c.to.name, c.m, r.Err)
		case !c.ok && (r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicContract):
			t.Errorf("%s Vec[%s, %d] to Vec[%s, %d]: %+v, want a contract panic", c.key, c.from.name, c.n, c.to.name, c.m, r.Err)
		}
	}
}

func TestReduceSumTreeOrder(t *testing.T) {
	v := newVWorld(t)
	f32 := laneTypes[10]
	vt := v.vecT(f32, 4)
	ga := []types.GenericArg{types.TypeArg(f32.id), constN(v.world, 4)}
	// Left to right: 1e8 + 1 = 1e8, - 1e8 = 0, + 1 = 1. The tree: (1e8 + -1e8)
	// + (1 + 1) = 2.
	a := []uint64{f32.fb(1e8), f32.fb(1), f32.fb(-1e8), f32.fb(1)}
	got := v.lanesOf(t, v.run("std.simd.Vec:ReduceSum", ga, f32.id, []types.TypeID{vt}, []types.ConstID{v.vecConst(vt, f32, a)}), f32)
	if f32.fl(got[0]) != 2 {
		t.Fatalf("ReduceSum = %v, want 2 (the pairwise tree)", f32.fl(got[0]))
	}
}

func TestMinMaxNaNAndZero(t *testing.T) {
	v := newVWorld(t)
	for _, l := range laneTypes[10:] {
		vt := v.vecT(l, 4)
		ga := []types.GenericArg{types.TypeArg(l.id), constN(v.world, 4)}
		negZ, posZ := l.fb(math.Copysign(0, -1)), l.fb(0)
		a := []uint64{negZ, posZ, l.fb(math.NaN()), l.fb(1)}
		b := []uint64{posZ, negZ, l.fb(1), l.fb(math.NaN())}
		args := []types.ConstID{v.vecConst(vt, l, a), v.vecConst(vt, l, b)}
		mn := v.lanesOf(t, v.run("std.simd.Vec:Min", ga, vt, []types.TypeID{vt, vt}, args), l)
		mx := v.lanesOf(t, v.run("std.simd.Vec:Max", ga, vt, []types.TypeID{vt, vt}, args), l)
		if mn[0] != negZ || mn[1] != negZ || !l.isNaN(mn[2]) || !l.isNaN(mn[3]) {
			t.Errorf("Min %s = %x", l.name, mn)
		}
		if mx[0] != posZ || mx[1] != posZ || !l.isNaN(mx[2]) || !l.isNaN(mx[3]) {
			t.Errorf("Max %s = %x", l.name, mx)
		}
		rm := v.lanesOf(t, v.run("std.simd.Vec:ReduceMin", ga, l.id, []types.TypeID{vt}, args[:1]), l)
		if !l.isNaN(rm[0]) {
			t.Errorf("ReduceMin %s with a NaN lane = %x", l.name, rm)
		}
	}
}

func TestFmaF32OneRounding(t *testing.T) {
	// a*b = 1 + 2^-11 + 2^-24 exactly, a tie between two f32 values; c =
	// 2^-70 puts the exact sum above the tie, so one rounding gives
	// 1 + 2^-11 + 2^-23, while rounding to f64 first loses c and the tie
	// then rounds to even, 1 + 2^-11.
	a := float32(1 + math.Ldexp(1, -12))
	c := float32(math.Ldexp(1, -70))
	want := float32(1 + math.Ldexp(1, -11) + math.Ldexp(1, -23))
	if got := fma32(a, a, c); got != want {
		t.Fatalf("fma32 = %v, want %v", got, want)
	}
	if twice := float32(math.FMA(float64(a), float64(a), float64(c))); twice == want {
		t.Fatalf("the case does not separate one rounding from two")
	}
	v := newVWorld(t)
	f32 := laneTypes[10]
	vt := v.vecT(f32, 2)
	ga := []types.GenericArg{types.TypeArg(f32.id), constN(v.world, 2)}
	fa, fc := uint64(math.Float32bits(a)), uint64(math.Float32bits(c))
	got := v.lanesOf(t, v.run("std.simd.Vec:Fma", ga, vt, []types.TypeID{vt, vt, vt},
		[]types.ConstID{v.vecConst(vt, f32, []uint64{fa, f32.fb(2)}), v.vecConst(vt, f32, []uint64{fa, f32.fb(3)}), v.vecConst(vt, f32, []uint64{fc, f32.fb(1)})}), f32)
	if got[0] != uint64(math.Float32bits(want)) || f32.fl(got[1]) != 7 {
		t.Fatalf("Fma lanes = %x", got)
	}
	// Exact zero and special values.
	if r := fma32(1, -1, 1); math.Signbit(float64(r)) || r != 0 {
		t.Errorf("fma32(1, -1, 1) = %v, want +0", r)
	}
	if r := fma32(float32(math.Inf(1)), 0, 1); !math.IsNaN(float64(r)) {
		t.Errorf("fma32(inf, 0, 1) = %v, want NaN", r)
	}
}

func TestPanicsBeforeWrite(t *testing.T) {
	v := newVWorld(t)
	i8 := laneTypes[0]
	vt := v.vecT(i8, 4)
	ga := []types.GenericArg{types.TypeArg(i8.id), constN(v.world, 4)}
	cases := []struct {
		key  string
		a, b []uint64
		want types.PanicCode
	}{
		{"DivTrunc", []uint64{1, 2, 3, 4}, []uint64{1, 1, 1, 0}, types.PanicDivideByZero},
		{"DivTrunc", []uint64{1, 2, 3, 0x80}, []uint64{1, 1, 1, 0xff}, types.PanicDivideByZero}, // MIN / -1, scalar /'s code
		{"ShlEach", []uint64{1, 2, 3, 4}, []uint64{0, 1, 7, 8}, types.PanicShiftAmount},
		{"ShrEach", []uint64{1, 2, 3, 4}, []uint64{0xff, 1, 2, 3}, types.PanicShiftAmount}, // -1 read as unsigned
	}
	for _, c := range cases {
		r := v.run("std.simd.Vec:"+c.key, ga, vt, []types.TypeID{vt, vt}, []types.ConstID{v.vecConst(vt, i8, c.a), v.vecConst(vt, i8, c.b)})
		if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != c.want {
			t.Errorf("%s %x %x: %+v, want panic %v", c.key, c.a, c.b, r.Err, c.want)
		}
	}
	// A scalar shift amount out of range panics once (the scalar rule).
	gaK := append(append([]types.GenericArg(nil), ga...), types.TypeArg(types.Int))
	for _, k := range []int64{-1, 8} {
		r := v.run("std.simd.Vec:Shl", gaK, vt, []types.TypeID{vt}, []types.ConstID{v.vecConst(vt, i8, []uint64{1, 2, 3, 4})}, imm(types.Int, k))
		if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicShiftAmount {
			t.Errorf("Shl by %d: %+v", k, r.Err)
		}
	}
}

func TestCpuIsBaseline(t *testing.T) {
	v := newVWorld(t)
	_, cf := v.structType("std.simd", "CpuFeatures", v.array(2, types.U64))
	r := v.run("std.simd.Cpu", nil, cf, nil, nil)
	got := v.lanesOf(t, r, laneTypes[7])
	want := cpuBits(types.ArchX64, types.CPUBaseline(types.X86_64Windows))
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Cpu() = %x, want the baseline %x", got, want)
	}
	// sse (bit 0) to sse4.2 (bit 5), popcnt, cx16, sahf: x86-64-v2's
	// features; the level name has no bit of its own.
	if want[0] != 0x1ff || want[1] != 0 {
		t.Fatalf("baseline bits = %x, want 1ff", want)
	}
	reads := 0
	for _, rd := range r.Reads.Sorted() {
		if rd.Kind == types.ReadTarget {
			reads++
		}
	}
	if reads == 0 {
		t.Errorf("Cpu() recorded no target read")
	}
	if got := cpuNames("avx2, fma,bmi2 "); fmt.Sprint(got) != "[avx2 fma bmi2]" {
		t.Errorf("cpuNames = %q", got)
	}
	v3, _ := types.CPUClosure(types.ArchX64, []string{"x86-64-v3"})
	if b := cpuBits(types.ArchX64, v3); b[0]&want[0] != want[0] || b[0]&(1<<10) == 0 {
		t.Errorf("x86-64-v3 bits %x lack the baseline or avx2", b)
	}
}

// TestMmioRefused: every std.mmio intrinsic is refused with
// RefuseVolatile (E0901, refused: volatile).
func TestMmioRefused(t *testing.T) {
	for _, name := range []string{"Load", "Store", "Fence", "Reg:New", "Reg:Read", "Reg:Write"} {
		v := newVWorld(t)
		f := v.fn("std.mmio", name, types.Void)
		fd := v.world.items.Get(f).Decl.(*types.FuncDecl)
		fd.Bodiless, fd.Intrinsic = true, true
		g := v.fn("main", "G", types.Void)
		x := v.body(g, types.Void)
		next := x.block()
		x.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: f}},
			Dest: lp(0), Target: next, Spawn: mir.NoLocal})
		x.ret(next)
		x.done()
		wantRefusal(t, v.call(v.evaluator(0), g, 0), types.RefuseVolatile)
	}
}

// TestLanesIsVectorBitsOverSize: simd.Lanes[T]() of the T an instance
// fixed is Target.vector_bits / (8 * SizeOf[T]()), 16 for u8 down to 2 for
// f64, recorded as a read of vector_bits.
func TestLanesIsVectorBitsOverSize(t *testing.T) {
	v := newVWorld(t)
	for _, tc := range []struct {
		lane types.TypeID
		want uint64
	}{{types.U8, 16}, {types.I16, 8}, {types.F32, 4}, {types.U32, 4}, {types.F64, 2}, {types.Int, 2}} {
		r := v.run("std.simd.Lanes", []types.GenericArg{types.TypeArg(tc.lane)}, types.Int, nil, nil)
		if r.Err != nil {
			t.Fatalf("Lanes[%s]: %+v", v.tab.TypeString(tc.lane, nil), r.Err)
		}
		if got := types.LoadUint(v.consts.Get(r.Value).Bytes, 8); got != tc.want {
			t.Errorf("Lanes[%s] = %d, want %d", v.tab.TypeString(tc.lane, nil), got, tc.want)
		}
		read := false
		for _, rd := range r.Reads.Sorted() {
			if rd.Kind == types.ReadTarget && rd.Name == "vector_bits" {
				read = true
			}
		}
		if !read {
			t.Errorf("Lanes[%s] recorded no read of vector_bits", v.tab.TypeString(tc.lane, nil))
		}
	}
}
