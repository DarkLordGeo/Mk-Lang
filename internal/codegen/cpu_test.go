// Why: a call from a cpu function must reach the generic instance mono made
// for its features, and that variant must carry them.
// What: TestKeyCPU, TestVariantCallIR ("target-features" of the
// variant) and TestAvx512BringsEvex512.
// How: hand-built units are emitted and the IR text is checked.

package codegen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func TestKeyCPU(t *testing.T) {
	for key, want := range map[string]string{
		"m.G@c544 @cpu(avx,avx2)": "avx,avx2",
		"m.G@c544":                "",
		"m.F":                     "",
		"m.G@c5 @cpu(sse,sse2)":   "sse,sse2",
	} {
		if got := strings.Join(keyCPU(key), ","); got != want {
			t.Errorf("keyCPU(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestVariantCallIR: G[i64] has two instances (mono's baseline one and
// the one inheriting avx and avx2); Fast (cpu "avx2") calls the second and
// Base the first; only the second and Fast carry target-features, and the
// module compiles.
func TestVariantCallIR(t *testing.T) {
	e := newTestEnv()
	g := e.fn("G", []types.TypeID{types.I64}, types.I64, 0)
	ga := []types.GenericArg{types.TypeArg(types.I64)}
	gb := func() *mir.Body {
		f := e.body(g)
		f.assign(place(0), use(mir.Copy(place(1))), f.at(1, 1))
		f.ret(f.at(1, 1))
		return f.b
	}
	fast := e.fn("Fast", []types.TypeID{types.I64}, types.I64, 0)
	set, _ := types.CPUClosure(types.ArchX64, []string{"avx2"})
	e.items.Get(fast).Decl.(*types.FuncDecl).Sig.CPU = set
	fb := e.body(fast)
	r := fb.call(g, ga, types.I64, mir.Copy(place(1)))
	fb.assign(place(0), use(mir.Copy(place(r))), fb.at(2, 1))
	fb.ret(fb.at(2, 1))
	base := e.fn("Base", []types.TypeID{types.I64}, types.I64, 0)
	bb := e.body(base)
	r = bb.call(g, ga, types.I64, mir.Copy(place(1)))
	bb.assign(place(0), use(mir.Copy(place(r))), bb.at(3, 1))
	bb.ret(bb.at(3, 1))
	u := unitOf(e, []*mir.Body{fb.b, bb.b})
	key := e.items.Get(g).Key.String() + "@c544"
	u.Packs[0] = append(u.Packs[0], len(u.Funcs), len(u.Funcs)+1)
	u.Funcs = append(u.Funcs,
		Func{Symbol: "mk.main.G.base", Key: key, Item: g, Args: ga, Body: gb()},
		Func{Symbol: "mk.main.G.avx2", Key: key + " @cpu(avx,avx2)", Item: g, Args: ga, Body: gb()})
	ll := emit(t, e, u, types.X86_64Linux, types.Release)
	if !strings.Contains(fnText(ll, "mk.main.Fast"), "@mk.main.G.avx2(") {
		t.Errorf("Fast does not call the avx2 variant:\n%s", fnText(ll, "mk.main.Fast"))
	}
	if !strings.Contains(fnText(ll, "mk.main.Base"), "@mk.main.G.base(") {
		t.Errorf("Base does not call the baseline instance:\n%s", fnText(ll, "mk.main.Base"))
	}
	attrsOf := func(sym string) string { // the define line: attributes are inline
		for _, ln := range strings.Split(ll, "\n") {
			if strings.HasPrefix(ln, "define ") && strings.Contains(ln, "@"+sym+"(") {
				return ln
			}
		}
		t.Fatalf("no definition of %s", sym)
		return ""
	}
	if a := attrsOf("mk.main.G.avx2"); !strings.Contains(a, "+avx2") {
		t.Errorf("the avx2 variant lacks +avx2: %s", a)
	}
	if a := attrsOf("mk.main.G.base"); strings.Contains(a, "target-features") {
		t.Errorf("the baseline instance has target-features: %s", a)
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "o2", ll, "-O2")
}

// TestAvx512BringsEvex512: a cpu clause naming an AVX-512 feature gets
// +evex512 in its target-features, and a 64-lane byte compare under that
// string compiles from the hosted baseline CPU (x86-64-v2). Without
// +evex512, LLVM 21 fails instruction selection ("Cannot select v32i1
// setcc"), first seen on a std.simd AVX-512 function. An avx2 clause gets
// no +evex512.
func TestAvx512BringsEvex512(t *testing.T) {
	g := &packGen{cfg: &Config{Target: types.X86_64Windows}}
	closure := func(n string) []string {
		set, bad := types.CPUClosure(types.ArchX64, []string{n})
		if bad != "" {
			t.Fatalf("CPUClosure(%s): unknown %q", n, bad)
		}
		return set
	}
	tf := g.targetFeatures(closure("avx512bw"))
	if !strings.Contains(","+tf+",", ",+evex512,") || !strings.Contains(tf, "+avx512bw") {
		t.Fatalf("avx512bw: target-features %q, want +avx512bw and +evex512", tf)
	}
	if a := g.targetFeatures(closure("avx2")); strings.Contains(a, "evex512") {
		t.Errorf("avx2: target-features %q holds evex512", a)
	}
	ll := `define i64 @f(ptr %p, i8 %b) #0 {
  %a = load <64 x i8>, ptr %p, align 1
  %i = insertelement <64 x i8> poison, i8 %b, i64 0
  %s = shufflevector <64 x i8> %i, <64 x i8> poison, <64 x i32> zeroinitializer
  %c = icmp eq <64 x i8> %a, %s
  %x = sext <64 x i1> %c to <64 x i8>
  store <64 x i8> %x, ptr %p
  %y = load <64 x i8>, ptr %p
  %t = trunc <64 x i8> %y to <64 x i1>
  %m = bitcast <64 x i1> %t to i64
  ret i64 %m
}
attributes #0 = { "target-features"="` + tf + `" }
`
	for _, opt := range []string{"-O0 -march=x86-64-v2", "-O2 -march=x86-64-v2"} {
		clangC(t, types.X86_64Windows, t.TempDir(), "evex", ll, opt)
	}
}
