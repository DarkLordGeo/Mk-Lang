// Why: linkage, visibility and symbol names decide what links and what an
// optimizer may drop or merge.
// What: TestLinkage (dev and release linkage of pack-local and
// cross-pack functions, export extern, var globals, the entry),
// TestSymbols (the driver's symbols, quoted, independent of interning
// order), TestVarGlobalWritable (native), TestUnnamedAddr and
// TestConstants.
// How: linkUnit is emitted in both profiles and the definitions are read.

package codegen

import (
	"bytes"
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// linkUnit is: A (Local) called by B; B, which writes the var globals G1
// (Local) and G2 and reads G1; XExport, an export extern calling B.
//
//	A(x: int): int { return x }
//	B(x: int): int { G1 = x; G2 = x; return A(G1) }
//	export extern XExport(x: int): int { return B(x) }
func linkUnit(e *testEnv) *Unit {
	g1 := e.item("G1", types.KindGlobal, &types.GlobalDecl{Type: types.Int})
	g2 := e.item("G2", types.KindGlobal, &types.GlobalDecl{Type: types.Int, HasValue: true, Value: e.intConst(types.Int, 7)})
	a := e.body(e.fn("A", []types.TypeID{types.Int}, types.Int, 0))
	a.assign(place(0), use(mir.Copy(place(1))), a.at(2, 3))
	a.ret(a.at(2, 3))
	b := e.body(e.fn("B", []types.TypeID{types.Int}, types.Int, 0))
	mi := e.tt.Ref(types.Int) // RvGlobalRef is the static &G
	p1, p2, v := b.local(mi), b.local(mi), b.local(types.Int)
	b.assign(place(p1), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g1}, b.at(4, 3))
	b.assign(b.b.Place(p1, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}), use(mir.Copy(place(1))), b.at(4, 3))
	b.assign(place(p2), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g2}, b.at(5, 3))
	b.assign(b.b.Place(p2, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}), use(mir.Copy(place(1))), b.at(5, 3))
	b.assign(place(v), use(mir.Copy(b.b.Place(p1, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}))), b.at(6, 3))
	b.callTo(direct(a.item), place(0), mir.NoLocal, b.at(6, 10), mir.Copy(place(v)))
	b.ret(b.at(6, 3))
	xi := e.fn("XExport", []types.TypeID{types.Int}, types.Int, 0)
	e.items.Get(xi).Decl.(*types.FuncDecl).Sig.Extern = true
	x := e.body(xi)
	x.callTo(direct(b.item), place(0), mir.NoLocal, x.at(8, 10), mir.Copy(place(1)))
	x.ret(x.at(8, 3))
	u := unitOf(e, []*mir.Body{a.b, b.b, x.b})
	u.Funcs[0].Local = true
	u.Funcs[2].Symbol = "XExport"
	u.Funcs[2].AddrTaken = true
	u.Globals = []Global{{Item: g1, Pack: 0, Local: true}, {Item: g2, Pack: 0}}
	return u
}

func TestLinkage(t *testing.T) {
	type want struct{ a, b, x, g1, g2 string }
	cases := []struct {
		target types.Target
		prof   types.Profile
		w      want
	}{
		{types.X86_64Linux, types.Dev, want{"define internal noundef i64 @mk.main.A(", "define hidden noundef i64 @mk.main.B(",
			"define noundef i64 @XExport(", "@mk.main.G1 = hidden global", "@mk.main.G2 = hidden global"}},
		{types.X86_64Linux, types.Release, want{"define internal noundef i64 @mk.main.A(", "define hidden noundef i64 @mk.main.B(",
			"define noundef i64 @XExport(", "@mk.main.G1 = internal global", "@mk.main.G2 = hidden global"}},
		{types.X86_64Windows, types.Dev, want{"define internal noundef i64 @mk.main.A(", "define dso_local noundef i64 @mk.main.B(",
			"define dso_local noundef i64 @XExport(", "@mk.main.G1 = dso_local global", "@mk.main.G2 = dso_local global"}},
		{types.X86_64Windows, types.Release, want{"define internal noundef i64 @mk.main.A(", "define dso_local noundef i64 @mk.main.B(",
			"define dso_local noundef i64 @XExport(", "@mk.main.G1 = internal global", "@mk.main.G2 = dso_local global"}},
	}
	for _, c := range cases {
		t.Run(c.target.Name+map[types.Profile]string{types.Dev: "/dev", types.Release: "/release"}[c.prof], func(t *testing.T) {
			e := newTestEnv()
			ll := emit(t, e, linkUnit(e), c.target, c.prof)
			for _, s := range []string{c.w.a, c.w.b, c.w.x, c.w.g1, c.w.g2} {
				if !strings.Contains(ll, s) {
					t.Errorf("no %q in\n%s", s, ll)
				}
			}
			if strings.Contains(ll, "G2 = internal") || strings.Contains(ll, "@XExport = ") {
				t.Error("a cross-pack global or the export is internal")
			}
			clangC(t, c.target, t.TempDir(), "link", ll, "-O2")
		})
	}
	// The entry and the support routines are external with default
	// visibility.
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		s := supportLL(t, target)
		for _, def := range []string{`define (dso_local )?ptr @memcpy\(`, `define (dso_local )?i32 @memcmp\(`} {
			if !regexp.MustCompile(def).MatchString(s) {
				t.Errorf("%s support pack: no %s", target.Name, def)
			}
		}
		entry := "define void @mk_start()"
		if target.OS == types.OSLinux {
			entry = "define void @mk_start(ptr"
			if !strings.Contains(s, ".globl _start") {
				t.Errorf("%s: _start is not global", target.Name)
			}
		}
		if !regexp.MustCompile(regexp.QuoteMeta("define ") + `(dso_local )?` + regexp.QuoteMeta(strings.TrimPrefix(entry, "define "))).MatchString(s) {
			t.Errorf("%s support pack: no external %q", target.Name, entry)
		}
	}
}

// TestLocalReferencedElsewhere: a Local function or global
// referenced from another pack is the driver's error, reported as an ICE.
func TestLocalReferencedElsewhere(t *testing.T) {
	e := newTestEnv()
	u := linkUnit(e)
	// Move B and XExport to pack 1; A stays Local to pack 0.
	u.Packs = [][]int{{0}, {1, 2}}
	u.Funcs[1].Pack, u.Funcs[2].Pack = 1, 1
	var b bytes.Buffer
	_, err := EmitPack(u, 1, e, Config{Target: types.X86_64Linux}, &b)
	var ie *InternalError
	if !errors.As(err, &ie) || !strings.Contains(ie.IE.What, "Local to pack 0") {
		t.Fatalf("err = %v, want an ICE naming the Local function", err)
	}
}

// TestSymbols: symbols are Func.Symbol printed through llvm.Quote, and
// the IR does not depend on interning order: the same program in
// a table where unrelated types and items were interned first gives the
// same bytes.
func TestSymbols(t *testing.T) {
	for _, c := range emitCases {
		t.Run(c.name, func(t *testing.T) {
			a := caseIR(t, c, types.X86_64Linux)
			e := newTestEnv()
			e.structT("Unrelated", types.I8, types.U16)
			e.errItem("Noise", types.Int)
			e.tt.Slice(types.U16)
			e.tt.Optional(e.tt.Ref(types.F32))
			e.intConst(types.Int, 12345)
			bodies, bodiless := c.build(e)
			b := emit(t, e, unitOf(e, bodies, bodiless...), types.X86_64Linux, types.Dev)
			if a != b {
				t.Error("the IR depends on interning order")
			}
		})
	}
	e := newTestEnv()
	f := e.body(e.fn("Adds", nil, types.Void, 0))
	f.ret(f.at(1, 1))
	u := unitOf(e, []*mir.Body{f.b})
	u.Funcs[0].Symbol = `mk.main#test("adds")`
	ll := emit(t, e, u, types.X86_64Linux, types.Dev)
	if !strings.Contains(ll, `@"mk.main#test(\22adds\22)"(`) {
		t.Errorf("test symbol not quoted:\n%s", ll)
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "sym", ll, "-O0")
}

// TestVarGlobalWritable: a var global is a writable global, never
// constant or unnamed_addr, and a write through it is read back natively.
func TestVarGlobalWritable(t *testing.T) {
	e := newTestEnv()
	ll := emit(t, e, linkUnit(e), types.X86_64Linux, types.Release)
	for _, line := range strings.Split(ll, "\n") {
		if strings.HasPrefix(line, "@mk.main.G") && (strings.Contains(line, "constant") || strings.Contains(line, "unnamed_addr")) {
			t.Errorf("var global is not writable: %s", line)
		}
	}
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			if _, why := hostRunner(target); why != "" {
				skipRunner(t, why)
			}
			e := newTestEnv()
			ll := emit(t, e, linkUnit(e), target, types.Release)
			dir := t.TempDir()
			osIR, kernel := harnessOS(target)
			h := osIR + harnessRT + `
declare i64 @XExport(i64)
@mk.main.G2 = external global i64

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  %g0 = load i64, ptr @mk.main.G2, align 8
  call void @h_num(i64 %g0)
  call void @h_write(ptr @s.nl, i64 1)
  %r = call i64 @XExport(i64 41)
  call void @h_num(i64 %r)
  call void @h_write(ptr @s.nl, i64 1)
  %g = load i64, ptr @mk.main.G2, align 8
  call void @h_num(i64 %g)
  call void @h_write(ptr @s.nl, i64 1)
  call void @h_exit(i32 0)
  unreachable
}
`
			if target.OS == types.OSWindows {
				h = strings.Replace(h, "@mk.main.G2 = external global i64", "@mk.main.G2 = external dso_local global i64", 1)
			}
			objs := []string{clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
				clangC(t, target, dir, "pack", ll, "-O2"), clangC(t, target, dir, "harness", h, "-O0")}
			out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel, release: true}), "")
			if code != 0 || out != "7\n41\n41\n" {
				t.Fatalf("exit %d, output %q; want 7, 41, 41", code, out)
			}
		})
	}
}

// TestUnnamedAddr: every private const, string and site row of every
// golden is a private unnamed_addr constant (Mk exposes no const address).
func TestUnnamedAddr(t *testing.T) {
	re := regexp.MustCompile(`^@"?mk\.(c|s|pos)\.`)
	n := 0
	for _, c := range emitCases {
		for _, line := range strings.Split(caseIR(t, c, types.X86_64Linux), "\n") {
			if !re.MatchString(line) {
				continue
			}
			n++
			if !strings.Contains(line, "= private unnamed_addr constant ") {
				t.Errorf("%s: %s", c.name, line)
			}
		}
	}
	if n == 0 {
		t.Fatal("no private constants in the goldens")
	}
}

// floatConst interns an f32 or f64 const.
func (e *testEnv) floatConst(t types.TypeID, bits uint64) types.ConstID {
	s, a := e.sa(t)
	b := make([]byte, s)
	types.StoreUint(b, int(s), bits)
	return e.consts.Intern(types.ConstValue{Type: t, Align: a, Bytes: b})
}

// TestConstants: instruction constants are decimal integers and
// double hex (a float as the hex of the double equal to it); a string
// literal is [n x i8] with no terminating NUL.
func TestConstants(t *testing.T) {
	e := newTestEnv()
	ret := func(name string, ty types.TypeID, o mir.Operand) *mir.Body {
		f := e.body(e.fn(name, nil, ty, 0))
		f.assign(place(0), use(o), f.at(2, 3))
		f.ret(f.at(2, 3))
		return f.b
	}
	bodies := []*mir.Body{
		ret("F32", types.F32, mir.Const(types.F32, e.floatConst(types.F32, uint64(math.Float32bits(0.1))))),
		ret("NaN", types.F64, mir.Const(types.F64, e.floatConst(types.F64, 0x7FF8000000000000))),
		ret("NegZero", types.F64, mir.Const(types.F64, e.floatConst(types.F64, math.Float64bits(math.Copysign(0, -1))))),
		ret("MinusOne", types.I64, mir.Imm(types.I64, math.MaxUint64)),
		ret("Big", types.U64, mir.Imm(types.U64, math.MaxUint64)),
		ret("Hello", types.Str, strConst(e, "héllo\n")),
	}
	ll := emit(t, e, unitOf(e, bodies), types.X86_64Linux, types.Dev)
	for _, w := range []string{
		"store float 0x3FB99999A0000000,",
		"store double 0x7FF8000000000000,",
		"store double 0x8000000000000000,",
		"store i64 -1,",
		`[7 x i8] c"h\C3\A9llo\0A"`,
	} {
		if !strings.Contains(ll, w) {
			t.Errorf("no %q in\n%s", w, ll)
		}
	}
	if strings.Contains(ll, `[8 x i8] c"h`) {
		t.Error("a string literal ends in NUL")
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "consts", ll, "-O0")
}
