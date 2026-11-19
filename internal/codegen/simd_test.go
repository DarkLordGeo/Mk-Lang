// Why: every std.simd intrinsic is lowered by codegen, and each must compile
// for every lane type and keep evaluator, dev and release in agreement.
// What: TestEveryIntrinsicCompiles (every intrinsic of
// std/simd/vec.mk for every lane type at two lane counts, clang -O0 and -O2,
// both x86_64 targets), TestVectorIRMemoryForm, TestNoFastMathFlags,
// TestEveryDeclaredIntrinsicLowered, TestTargetFeaturesAttribute,
// TestVolatileIR and TestMaskLogicOnLaneWords.
// How: simdKit builds Vec types and intrinsic calls in hand-built bodies;
// simdModule emits one function per lane type.

package codegen

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

type simdLane struct {
	name   string
	id     types.TypeID
	w      int
	signed bool
	float  bool
}

var simdLanes = []simdLane{
	{"i8", types.I8, 1, true, false}, {"i16", types.I16, 2, true, false}, {"i32", types.I32, 4, true, false},
	{"i64", types.I64, 8, true, false}, {"u8", types.U8, 1, false, false}, {"u16", types.U16, 2, false, false},
	{"u32", types.U32, 4, false, false}, {"u64", types.U64, 8, false, false}, {"int", types.Int, 8, true, false},
	{"uint", types.Uint, 8, false, false}, {"f32", types.F32, 4, false, true}, {"f64", types.F64, 8, false, true},
}

// wider is the integer or float type twice as wide, of the same signedness.
var wider = map[types.TypeID]types.TypeID{types.I8: types.I16, types.I16: types.I32, types.I32: types.I64,
	types.U8: types.U16, types.U16: types.U32, types.U32: types.U64, types.F32: types.F64}

var narrower = map[types.TypeID]types.TypeID{types.I16: types.I8, types.I32: types.I16, types.I64: types.I32,
	types.U16: types.U8, types.U32: types.U16, types.U64: types.U32, types.Int: types.I32, types.Uint: types.U32}

// simdKit builds Vec types and intrinsic items in a test env.
type simdKit struct {
	e     *testEnv
	vecs  map[string]types.TypeID
	items map[string]types.ItemID
}

func newSimdKit(e *testEnv) *simdKit {
	return &simdKit{e: e, vecs: map[string]types.TypeID{}, items: map[string]types.ItemID{}}
}

func (k *simdKit) arr(n int, elem types.TypeID) types.TypeID {
	return k.e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: k.e.intConst(types.Int, uint64(n))}, elem)
}

func (k *simdKit) vec(lane types.TypeID, n int) types.TypeID {
	key := fmt.Sprint(lane, "x", n)
	if t, ok := k.vecs[key]; ok {
		return t
	}
	t := k.e.structIn("std.simd", fmt.Sprintf("V%dx%d", lane, n), k.arr(n, lane))
	k.vecs[key] = t
	return t
}

func (k *simdKit) fn(key string) types.ItemID {
	if id, ok := k.items[key]; ok {
		return id
	}
	owner, name := "", strings.TrimPrefix(key, "std.simd.")
	if o, n, ok := strings.Cut(name, ":"); ok {
		owner, name = o, n
	}
	id := k.e.stdFn("std.simd", owner, name, 0, nil, types.Void)
	k.items[key] = id
	return id
}

func cN(e *testEnv, n int) types.GenericArg {
	return types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, uint64(n))})
}

// simdBody is one function calling every intrinsic that applies to lane at
// lane count n; it returns the body and the keys it calls.
func simdBody(k *simdKit, l simdLane, n int) (*mir.Body, []string) {
	e := k.e
	v := k.vec(l.id, n)
	i32v := k.vec(types.I32, n)
	sl, msl := e.tt.Slice(l.id), e.tt.MutSlice(l.id)
	params := []types.TypeID{v, v, v, sl, msl, types.Int, l.id, types.U64, i32v}
	f := e.body(e.fn(fmt.Sprintf("Simd_%s_%d", l.name, n), params, types.Void, 0))
	a, b, c, xs, ys, i, x, bits, idx := mir.Copy(place(1)), mir.Copy(place(2)), mir.Copy(place(3)), mir.Copy(place(4)),
		mir.Copy(place(5)), mir.Copy(place(6)), mir.Copy(place(7)), mir.Copy(place(8)), mir.Copy(place(9))
	ga := []types.GenericArg{types.TypeArg(l.id), cN(e, n)}
	var keys []string
	call := func(key string, extra []types.GenericArg, dest types.TypeID, ops ...mir.Operand) mir.LocalID {
		keys = append(keys, key)
		return f.call(k.fn(key), append(append([]types.GenericArg(nil), ga...), extra...), dest, ops...)
	}
	m := call("std.simd.Vec:Eq", nil, v, a, b) // a Mask: laid out as the Vec
	mo := mir.Copy(place(m))
	call("std.simd.Vec:Splat", nil, v, x)
	call("std.simd.Vec:Zero", nil, v)
	call("std.simd.Vec:Iota", nil, v)
	arrv := call("std.simd.Vec:ToArray", nil, k.arr(n, l.id), a)
	call("std.simd.Vec:FromArray", nil, v, mir.Copy(place(arrv)))
	call("std.simd.Vec:FromSlice", nil, v, xs, i)
	call("std.simd.Vec:FromSliceOr", nil, v, xs, i, c)
	call("std.simd.Vec:CopyTo", nil, types.Void, a, ys, i)
	call("std.simd.Vec:CopyToMasked", nil, types.Void, a, ys, i, mo)
	call("std.simd.Vec:Gather", []types.GenericArg{types.TypeArg(types.I32)}, v, xs, idx)
	call("std.simd.Vec:Lane", nil, l.id, a, mir.Imm(types.Int, 1))
	call("std.simd.Vec:With", nil, v, a, mir.Imm(types.Int, 1), x)
	shuf := make([]byte, 8*n)
	for j := 0; j < n; j++ {
		types.StoreUint(shuf[8*j:], 8, uint64((j*3)%(2*n)))
	}
	sc := e.consts.Intern(types.ConstValue{Type: k.arr(n, types.Int), Align: 8, Bytes: shuf})
	call("std.simd.Vec:Shuffle", nil, v, a, b, mir.Const(k.arr(n, types.Int), sc))
	call("std.simd.Vec:Reverse", nil, v, a)
	call("std.simd.Vec:Broadcast", nil, v, a, mir.Imm(types.Int, 1))
	call("std.simd.Vec:Rotate", nil, v, a, mir.Imm(types.Int, 1))
	call("std.simd.Vec:Slide", nil, v, a, b, mir.Imm(types.Int, 1))
	for _, p := range []string{"InterleaveLo", "InterleaveHi", "DeinterleaveEven", "DeinterleaveOdd", "Min", "Max"} {
		call("std.simd.Vec:"+p, nil, v, a, b)
	}
	for _, p := range []string{"ReduceMin", "ReduceMax"} {
		call("std.simd.Vec:"+p, nil, l.id, a)
	}
	for _, p := range []string{"Eq", "Ne", "Lt", "Le", "Gt", "Ge"} {
		call("std.simd.Vec:"+p, nil, v, a, b)
	}
	call("std.simd.Mask:FromBits", nil, v, bits)
	call("std.simd.Mask:Bits", nil, types.U64, mo)
	call("std.simd.Mask:Any", nil, types.Bool, mo)
	call("std.simd.Mask:All", nil, types.Bool, mo)
	call("std.simd.Mask:CountSet", nil, types.Int, mo)
	call("std.simd.Mask:FirstSet", nil, e.tt.Optional(types.Int), mo)
	call("std.simd.Mask:Select", nil, v, mo, a, b)
	call("std.simd.Mask:Cast", []types.GenericArg{types.TypeArg(types.U8)}, k.vec(types.U8, n), mo)
	for _, p := range []string{"And", "Or", "Xor"} {
		call("std.simd.Mask:"+p, nil, v, mo, mo)
	}
	call("std.simd.Mask:Not", nil, v, mo)
	if n*l.w <= 64 {
		call("std.simd.Vec:Reinterpret", []types.GenericArg{types.TypeArg(types.U8), cN(e, n*l.w)}, k.vec(types.U8, n*l.w), a)
	}
	if l.float {
		for _, p := range []string{"Add", "Sub", "Mul", "Div", "Fma"} {
			ops := []mir.Operand{a, b}
			if p == "Fma" {
				ops = append(ops, c)
			}
			call("std.simd.Vec:"+p, nil, v, ops...)
		}
		for _, p := range []string{"Neg", "Abs", "Sqrt", "Floor", "Ceil", "RoundEven", "RoundToZero"} {
			call("std.simd.Vec:"+p, nil, v, a)
		}
		call("std.simd.Vec:ReduceSum", nil, l.id, a)
		call("std.simd.Vec:Cast", []types.GenericArg{types.TypeArg(types.I32)}, i32v, a)
		call("std.simd.Vec:CastSat", []types.GenericArg{types.TypeArg(types.I16)}, k.vec(types.I16, n), a)
		call("std.simd.Vec:CastSat", []types.GenericArg{types.TypeArg(types.U8)}, k.vec(types.U8, n), a)
		other := types.F64
		if l.id == types.F64 {
			other = types.F32
		}
		call("std.simd.Vec:Cast", []types.GenericArg{types.TypeArg(other)}, k.vec(other, n), a)
	} else {
		for _, p := range []string{"AddWrap", "SubWrap", "MulWrap", "And", "Or", "Xor", "AddSat", "SubSat", "DivTrunc", "ShlEach", "ShrEach"} {
			call("std.simd.Vec:"+p, nil, v, a, b)
		}
		call("std.simd.Vec:Not", nil, v, a)
		for _, p := range []string{"ReduceSumWrap", "ReduceAnd", "ReduceOr", "ReduceXor"} {
			call("std.simd.Vec:"+p, nil, l.id, a)
		}
		call("std.simd.Vec:Shl", []types.GenericArg{types.TypeArg(types.Int)}, v, a, i)
		call("std.simd.Vec:Shr", []types.GenericArg{types.TypeArg(types.U8)}, v, a, mir.Imm(types.U8, 3))
		call("std.simd.Vec:Cast", []types.GenericArg{types.TypeArg(types.F64)}, k.vec(types.F64, n), a)
		call("std.simd.Vec:Cast", []types.GenericArg{types.TypeArg(types.U8)}, k.vec(types.U8, n), a)
		call("std.simd.Vec:Cast", []types.GenericArg{types.TypeArg(types.I16)}, k.vec(types.I16, n), a)
		call("std.simd.Vec:Trunc", []types.GenericArg{types.TypeArg(types.U8)}, k.vec(types.U8, n), a)
		call("std.simd.Vec:CastSat", []types.GenericArg{types.TypeArg(types.U8)}, k.vec(types.U8, n), a)
		call("std.simd.Vec:CastSat", []types.GenericArg{types.TypeArg(types.I8)}, k.vec(types.I8, n), a)
		if l.signed {
			call("std.simd.Vec:AbsWrap", nil, v, a)
		}
		if l.w <= 4 {
			call("std.simd.Vec:ReduceSumWide", nil, types.I64, a)
			call("std.simd.Vec:MulHigh", nil, v, a, b)
		}
		if u, ok := narrower[l.id]; ok && 2*n <= 64 {
			call("std.simd.Vec:NarrowSat", []types.GenericArg{types.TypeArg(u), cN(e, 2*n)}, k.vec(u, 2*n), a, b)
		}
	}
	if u, ok := wider[l.id]; ok {
		wga := []types.GenericArg{types.TypeArg(u), cN(e, n/2)}
		call("std.simd.Vec:WidenLo", wga, k.vec(u, n/2), a)
		call("std.simd.Vec:WidenHi", wga, k.vec(u, n/2), a)
		if !l.float {
			call("std.simd.Vec:MulWidenLo", wga, k.vec(u, n/2), a, b)
			call("std.simd.Vec:MulWidenHi", wga, k.vec(u, n/2), a, b)
		}
	}
	if n == 4 {
		// simd.Lanes[T]() of an instance's T.
		keys = append(keys, "std.simd.Lanes")
		f.call(k.fn("std.simd.Lanes"), []types.GenericArg{types.TypeArg(l.id)}, types.Int)
	}
	if l.id == types.U8 && n >= 16 {
		keys = append(keys, "std.simd.Lookup16")
		tbl := f.call(k.fn("std.simd.Vec:Splat"), []types.GenericArg{types.TypeArg(types.U8), cN(e, 16)}, k.vec(types.U8, 16), x)
		f.call(k.fn("std.simd.Lookup16"), []types.GenericArg{cN(e, n)}, v, a, mir.Copy(place(tbl)))
	}
	f.ret(f.at(30, 1))
	return f.b, keys
}

// simdModule is the IR of one function per lane type at lane counts 4 and
// 16 (and 64 for u8, to reach Lookup16's three-chunk form), and the keys
// they call.
func simdModule(t *testing.T, target types.Target) (string, map[string]bool) {
	e := newTestEnv()
	k := newSimdKit(e)
	var bodies []*mir.Body
	called := map[string]bool{}
	for _, l := range simdLanes {
		counts := []int{4, 16}
		if l.id == types.U8 {
			counts = append(counts, 64)
		}
		for _, n := range counts {
			b, keys := simdBody(k, l, n)
			bodies = append(bodies, b)
			for _, key := range keys {
				called[key] = true
			}
		}
	}
	u := unitOf(e, bodies)
	var sb strings.Builder
	for _, f := range u.Funcs {
		if err := f.Body.Validate(&mir.Env{Types: e.tt}); err != nil {
			t.Fatalf("%s: %v", f.Symbol, err)
		}
	}
	if _, err := EmitPack(u, 0, e, Config{Target: target, Profile: types.Dev, CPU: "x86-64-v2"}, &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String(), called
}

func TestEveryIntrinsicCompiles(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			ll, _ := simdModule(t, target)
			for _, want := range []string{"shufflevector", "@llvm.vector.reduce.add", "@llvm.masked.load", "@llvm.masked.gather",
				"@llvm.x86.ssse3.pshuf.b.128", "@llvm.fptosi.sat", "@llvm.minimum", "icmp eq <16 x i8>"} {
				if !strings.Contains(ll, want) {
					t.Errorf("no %q in the emitted IR", want)
				}
			}
			dir := t.TempDir()
			clangC(t, target, dir, "o0", ll, "-O0")
			clangC(t, target, dir, "o2", ll, "-O2")
		})
	}
}

// TestVectorIRMemoryForm: every load and store of a vector type says
// align of its lane (never the vector's natural alignment), and no alloca
// or global has a vector type.
func TestVectorIRMemoryForm(t *testing.T) {
	ll, _ := simdModule(t, types.X86_64Windows)
	mem := regexp.MustCompile(`(load|store) <(\d+) x (i\d+|float|double)>.*, align (\d+)`)
	seen := 0
	for _, line := range strings.Split(ll, "\n") {
		if strings.Contains(line, "alloca <") || strings.HasPrefix(line, "@") && strings.Contains(line, "<") && strings.Contains(line, " x ") && !strings.Contains(line, "[") {
			t.Errorf("a vector in memory: %s", strings.TrimSpace(line))
		}
		mm := mem.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		seen++
		want := map[string]string{"i8": "1", "i16": "2", "i32": "4", "i64": "8", "float": "4", "double": "8", "i1": "1"}[mm[3]]
		if mm[4] != want {
			t.Errorf("%s of <%s x %s> with align %s, want %s: %s", mm[1], mm[2], mm[3], mm[4], want, strings.TrimSpace(line))
		}
	}
	if seen < 100 {
		t.Fatalf("only %d vector loads and stores found", seen)
	}
}

// TestNoFastMathFlags: no instruction of the vector lowering carries a
// fast-math flag, so evaluator, dev and release agree.
func TestNoFastMathFlags(t *testing.T) {
	ll, _ := simdModule(t, types.X86_64Windows)
	flag := regexp.MustCompile(`= (fadd|fsub|fmul|fdiv|fneg|fcmp|call)( [a-z]+)*? (fast|nnan|ninf|nsz|arcp|contract|afn|reassoc)\b`)
	for _, line := range strings.Split(ll, "\n") {
		if flag.MatchString(line) {
			t.Errorf("fast-math flag: %s", strings.TrimSpace(line))
		}
	}
}

// TestEveryDeclaredIntrinsicLowered: each bodiless declaration of
// std/simd/vec.mk is called by simdModule's bodies (so emitted and compiled
// above), and none of those calls is a key vec.mk does not declare.
func TestEveryDeclaredIntrinsicLowered(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "std", "simd", "vec.mk"))
	if err != nil {
		t.Skipf("std/simd/vec.mk: %v", err)
	}
	defer f.Close()
	decl := regexp.MustCompile(`^(?:export )?([A-Z][A-Za-z0-9]*)(?:\[[^\]]*\])?(?:\.([A-Z][A-Za-z0-9]*))?(?:\[[^\]]*\])?\(`)
	declared := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "{") || strings.HasPrefix(line, "//") {
			continue
		}
		mm := decl.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		key := "std.simd." + mm[1]
		if mm[2] != "" {
			key += ":" + mm[2]
		}
		declared[key] = true
	}
	_, called := simdModule(t, types.X86_64Windows)
	for key := range declared {
		switch key {
		case "std.simd.Cpu", "std.simd.CpuFeatures:Of", "std.simd.CpuFeatures:Has":
			continue // their own tests: a const str argument and a support read
		}
		if !called[key] {
			t.Errorf("%s is declared in vec.mk and not lowered by the test", key)
		}
	}
	for key := range called {
		if !declared[key] {
			t.Errorf("%s is lowered but vec.mk does not declare it", key)
		}
	}
	if len(declared) < 90 {
		t.Fatalf("read only %d declarations", len(declared))
	}
}

// TestTargetFeaturesAttribute: a function whose signature carries a
// cpu set gets "target-features" with the LLVM spellings of its closure,
// sorted; a function without one gets none.
func TestTargetFeaturesAttribute(t *testing.T) {
	e := newTestEnv()
	fast := e.fn("Fast", []types.TypeID{types.Int}, types.Int, 0)
	set, _ := types.CPUClosure(types.ArchX64, []string{"avx2", "bmi2"})
	e.items.Get(fast).Decl.(*types.FuncDecl).Sig.CPU = set
	f := e.body(fast)
	f.assign(place(0), use(mir.Copy(place(1))), f.at(1, 1))
	f.ret(f.at(1, 1))
	plain := e.fn("Plain", []types.TypeID{types.Int}, types.Int, 0)
	g := e.body(plain)
	g.assign(place(0), use(mir.Copy(place(1))), g.at(2, 1))
	g.ret(g.at(2, 1))
	ll := emit(t, e, unitOf(e, []*mir.Body{f.b, g.b}), types.X86_64Linux, types.Release)
	want := `"target-features"="+avx,+avx2,+bmi2,+sse,+sse2,+sse3,+sse4.1,+sse4.2,+ssse3"`
	if !strings.Contains(ll, want) {
		t.Fatalf("no %s in:\n%s", want, ll)
	}
	if strings.Count(ll, `"target-features"`) != 1 {
		t.Fatalf("target-features on a function without a cpu clause:\n%s", ll)
	}
}

// TestVolatileIR: std.mmio lowers to one load or store volatile of
// exactly T's type with align AlignOf[T] and no load facts, fence seq_cst,
// and inttoptr for Reg.New; the IR compiles.
func TestVolatileIR(t *testing.T) {
	e := newTestEnv()
	p0 := e.tt.Param(0)
	pu32 := e.tt.RawPtr(types.U32)
	load := e.stdFn("std.mmio", "", "Load", 1, []types.TypeID{e.tt.RawPtr(p0)}, p0)
	store := e.stdFn("std.mmio", "", "Store", 1, []types.TypeID{e.tt.RawPtr(p0), p0}, types.Void)
	fence := e.stdFn("std.mmio", "", "Fence", 0, nil, types.Void)
	reg := e.structIn("std.mmio", "Reg", pu32)
	newReg := e.stdFn("std.mmio", "Reg", "New", 1, []types.TypeID{types.Uint}, reg)
	read := e.stdFn("std.mmio", "Reg", "Read", 1, []types.TypeID{e.tt.Ref(reg)}, p0)
	write := e.stdFn("std.mmio", "Reg", "Write", 1, []types.TypeID{e.tt.Ref(reg), p0}, types.Void)
	ga := []types.GenericArg{types.TypeArg(types.U32)}
	f := e.body(e.fn("Dev", []types.TypeID{pu32}, types.U32, 0))
	v := f.call(load, ga, types.U32, mir.Copy(place(1)))
	f.call(store, ga, types.Void, mir.Copy(place(1)), mir.Copy(place(v)))
	f.call(fence, nil, types.Void)
	r := f.call(newReg, ga, reg, mir.Imm(types.Uint, 0xFEE00030))
	rr := f.local(e.tt.Ref(reg))
	f.assign(place(rr), mir.Rvalue{Kind: mir.RvRef, Place: place(r)}, f.at(5, 1))
	x := f.call(read, ga, types.U32, mir.Copy(place(rr)))
	f.call(write, ga, types.Void, mir.Copy(place(rr)), mir.Copy(place(x)))
	f.assign(place(0), use(mir.Copy(place(x))), f.at(6, 1))
	f.ret(f.at(6, 1))
	ll := emit(t, e, unitOf(e, []*mir.Body{f.b}), types.X86_64Linux, types.Release)
	for _, want := range []string{"load volatile i32", "store volatile i32", "fence seq_cst", "inttoptr i64 "} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %q in:\n%s", want, fnText(ll, "mk.main.Dev"))
		}
	}
	for _, line := range strings.Split(ll, "\n") {
		if strings.Contains(line, "volatile") && (strings.Contains(line, "!range") || strings.Contains(line, "!nonnull") || strings.Contains(line, "!noundef")) {
			t.Errorf("a fact on a volatile access: %s", strings.TrimSpace(line))
		}
		if strings.Contains(line, "load volatile i32") && !strings.Contains(line, ", align 4") {
			t.Errorf("volatile load not aligned as u32: %s", strings.TrimSpace(line))
		}
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "o2", ll, "-O2")
}

// TestMaskLogicOnLaneWords: Mask.And, Or, Xor and Not compute on the
// mask's memory form <N x iW> (each lane 0 or all ones), never on
// <N x i1>. Through <N x i1>, instcombine strips the sext and compare
// around a loop-carried Mask and leaves a phi of <N x i1>, which x86-64
// keeps in a packed register shape and converts at every use (mandelvec's
// active mask: vextractf128, vpackssdw, vpslld, vtestps, then vpmovsxdq
// for Select, against one vptest on the words).
func TestMaskLogicOnLaneWords(t *testing.T) {
	e := newTestEnv()
	k := newSimdKit(e)
	var bodies []*mir.Body
	for _, c := range []struct {
		l simdLane
		n int
	}{{simdLanes[11], 4}, {simdLanes[10], 8}, {simdLanes[4], 16}, {simdLanes[1], 2}} {
		v := k.vec(c.l.id, c.n)
		f := e.body(e.fn(fmt.Sprintf("MaskLogic_%s_%d", c.l.name, c.n), []types.TypeID{v, v}, types.Void, 0))
		ga := []types.GenericArg{types.TypeArg(c.l.id), cN(e, c.n)}
		m := f.call(k.fn("std.simd.Vec:Lt"), ga, v, mir.Copy(place(1)), mir.Copy(place(2)))
		mo := mir.Copy(place(m))
		for _, p := range []string{"And", "Or", "Xor"} {
			f.call(k.fn("std.simd.Mask:"+p), ga, v, mo, mo)
		}
		f.call(k.fn("std.simd.Mask:Not"), ga, v, mo)
		f.ret(f.at(30, 1))
		bodies = append(bodies, f.b)
	}
	u := unitOf(e, bodies)
	var sb strings.Builder
	if _, err := EmitPack(u, 0, e, Config{Target: types.X86_64Windows, Profile: types.Release, CPU: "x86-64-v3"}, &sb); err != nil {
		t.Fatal(err)
	}
	ll := sb.String()
	onI1 := regexp.MustCompile(`= (and|or|xor) <\d+ x i1>`)
	for _, line := range strings.Split(ll, "\n") {
		if onI1.MatchString(line) {
			t.Errorf("mask logic on <N x i1>: %s", strings.TrimSpace(line))
		}
	}
	for _, want := range []string{"and <4 x i64>", "or <4 x i64>", "xor <4 x i64>", "and <8 x i32>", "xor <16 x i8>", "or <2 x i16>"} {
		if !strings.Contains(ll, "= "+want) {
			t.Errorf("no %q: mask logic not on the lane words", want)
		}
	}
	dir := t.TempDir()
	clangC(t, types.X86_64Windows, dir, "o2", ll, "-O2")
}
