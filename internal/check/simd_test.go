// Why: std.simd's Vec and Mask are compiler-known types with their own
// layout, kinds, operators, lane classes and CPU feature rules.
// What: the std.simd checks (TestVecFileInstalled to TestCpuCallNeedsUnsafe), TestMmio and TestCompoundAssign.
// How: adds the declaration file internal/check/testdata/simd/vec.mk to the
// test world as std.simd; the tests that need the evaluator run mk over the
// std overlay (overlay_test.go).

package check

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// vecText is the test copy of std/simd/vec.mk.
func vecText(t testing.TB) string {
	t.Helper()
	b, err := os.ReadFile("testdata/simd/vec.mk")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// simdWorld is a prelude world with std.simd added from vecText.
func simdWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, true)
	w.add("std.simd", opt{std: true}, vecText(t)).wantClean(t)
	return w
}

// simdCases runs body cases over simdWorld; each source imports std.simd.
func simdCases(t *testing.T, cases []bodyCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBody(t, simdWorld(t), tc.want, "import std.simd\n\n"+tc.src)
		})
	}
}

// TestVecFileInstalled: std/simd/vec.mk, when present, is this
// testdata file byte for byte.
func TestVecFileInstalled(t *testing.T) {
	inst, err := os.ReadFile("../../std/simd/vec.mk")
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("std/simd/vec.mk is not installed yet")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inst, []byte(vecText(t))) {
		t.Fatal("std/simd/vec.mk differs from internal/check/testdata/simd/vec.mk: change both together")
	}
}

// TestLaneComputed: Lane is the integer types, int, uint, f32 and
// f64; bool, char, a struct and a reference are no lanes (E0701);
// an impl of Lane is E0704 reason computed.
func TestLaneComputed(t *testing.T) {
	var ok string
	for _, l := range []string{"i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "int", "uint", "f32", "f64"} {
		ok += "\tvar v" + l + ": simd.Vec[" + l + ", 4]\n\tvar m" + l + ": simd.Mask[" + l + ", 4]\n"
	}
	simdCases(t, []bodyCase{
		{"every lane type", "F() {\n" + ok + "}\n", ""},
		{"bool", "F(v: simd.Vec[bool, 4]) {\n}\n", "E0701@3:6"},
		{"char", "F(v: simd.Mask[char, 4]) {\n}\n", "E0701@3:6"},
		{"reference", "F(v: simd.Vec[&i32, 4]) {\n}\n", "E0701@3:6"},
		{"generic lane", "F[T: simd.Lane](v: simd.Vec[T, 4]) {\n}\n", ""},
		{"generic unbounded", "F[T](v: simd.Vec[T, 4]) {\n}\n", "E0701@3:9"},
	})
	t.Run("impl", func(t *testing.T) {
		w := simdWorld(t)
		b := w.add("m", opt{}, "import std.simd\n\nstruct B {\n\tx: int\n}\n\nimpl simd.Lane for B {\n}\n")
		ds := b.diags()
		if got := codes(w, ds); got != "E0704@7:1" {
			t.Fatalf("got %q\n%s", got, dump(w, ds))
		}
		if fact(ds[0], "reason") != "computed" {
			t.Fatalf("reason %q", fact(ds[0], "reason"))
		}
	})
}

// TestLaneCountRequires: every function carries the lane-count rule:
// N 3 or 0 at a call is E0706; a generic caller over N restates the
// clause, else E0706; the type itself is well formed
// and zero-valid for any N. The clause is judged by the evaluator, so this
// runs mk over the overlay (overlay_test.go).
func TestLaneCountRequires(t *testing.T) {
	t.Parallel()
	clause := " requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64"
	for _, tc := range []struct{ name, src, want string }{
		{"16 lanes", "F(x: u8): simd.Vec[u8, 16] {~    return simd.Vec[u8, 16].Splat(x)~}~", ""},
		{"3 lanes", "F(x: u8): simd.Vec[u8, 3] {~    return simd.Vec[u8, 3].Splat(x)~}~", "main.mk:4:12: E0706"},
		{"0 lanes", "F(x: u8): simd.Vec[u8, 0] {~    return simd.Vec[u8, 0].Splat(x)~}~", "main.mk:4:12: E0706"},
		{"any N as a type", "F(): int {~    return SizeOf[simd.Vec[u8, 3]]() + SizeOf[simd.Vec[f64, 0]]()~}~", ""},
		{"generic restates", "G[const N: int](x: u8): simd.Vec[u8, N]" + clause + " {~    return simd.Vec[u8, N].Splat(x)~}~", ""},
		{"generic does not", "G[const N: int](x: u8): simd.Vec[u8, N] {~    return simd.Vec[u8, N].Splat(x)~}~", "main.mk:4:12: E0706"},
		// An operator is a call of its intrinsic, so the clause is
		// restated for operators too (binary, comparison, unary, compound).
		{"operator restates", "G[const N: int](a: simd.Vec[u8, N], b: simd.Vec[u8, N]): simd.Vec[u8, N]" + clause + " {~    return a +% b~}~", ""},
		{"operator does not", "G[const N: int](a: simd.Vec[u8, N], b: simd.Vec[u8, N]): simd.Vec[u8, N] {~    return a +% b~}~", "main.mk:4:12: E0706"},
		{"comparison does not", "G[const N: int](a: simd.Vec[f32, N]): bool {~    return (a < a).Any()~}~", "main.mk:4:13: E0706"},
		{"unary does not", "G[const N: int](a: simd.Vec[f32, N]): simd.Vec[f32, N] {~    return -a~}~", "main.mk:4:12: E0706"},
		{"compound does not", "G[const N: int](a: simd.Vec[u8, N]): simd.Vec[u8, N] {~    var r := a~    r +%= a~    return r~}~", "main.mk:5:5: E0706"},
		{"concrete operator", "F(a: simd.Vec[u8, 16]): simd.Vec[u8, 16] {~    return a +% a~}~", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "import std.simd~~" + tc.src + "~export Main(sys: &System) {~}~"
			out, code := mkOverlay(t, strings.ReplaceAll(src, "~", string(rune(10))), "check")
			if tc.want == "" && code != 0 || tc.want != "" && (code == 0 || !strings.Contains(out, tc.want)) {
				t.Fatalf("exit %d, want %q:%c%s", code, tc.want, rune(10), out)
			}
		})
	}
}

// TestVecLayoutTable: Vec[T, N] is [N]T in memory, aligned as T, for
// every lane type and every lane count of the rule; Mask[T, N] has Vec[T,
// N]'s size and alignment.
func TestVecLayoutTable(t *testing.T) {
	w := simdWorld(t)
	vec, mask := w.mods["std.simd"].item("Vec"), w.mods["std.simd"].item("Mask")
	lanes := map[types.TypeID]uint64{types.I8: 1, types.I16: 2, types.I32: 4, types.I64: 8, types.U8: 1, types.U16: 2,
		types.U32: 4, types.U64: 8, types.Int: 8, types.Uint: 8, types.F32: 4, types.F64: 8}
	for lt, size := range lanes {
		for _, n := range []int64{2, 4, 8, 16, 32, 64} {
			args := []types.GenericArg{types.TypeArg(lt), types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: w.c.intConst(n)})}
			for _, item := range []types.ItemID{vec, mask} {
				l := w.c.Layout(w.tab.Named(item, args))
				if l.Shape.Size != uint64(n)*size || uint64(l.Shape.Align) != size {
					t.Errorf("%s: size %d align %d, want %d and %d", w.tab.TypeString(w.tab.Named(item, args), nil), l.Shape.Size, l.Shape.Align, uint64(n)*size, size)
				}
			}
		}
	}
}

// TestVecKinds: Vec and Mask are Plain, Freeze, Send and Share, and
// neither carriers nor views.
func TestVecKinds(t *testing.T) {
	w := simdWorld(t)
	vec, mask := w.mods["std.simd"].item("Vec"), w.mods["std.simd"].item("Mask")
	args := []types.GenericArg{types.TypeArg(types.F32), types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: w.c.intConst(8)})}
	for _, item := range []types.ItemID{vec, mask} {
		nt := w.tab.Named(item, args)
		f := w.c.Facts(nt)
		if f.Kind != types.Plain || !f.Freeze || !f.Send || !f.Share || f.Carrier || f.View {
			t.Errorf("%s: facts %+v", w.tab.TypeString(nt, nil), f)
		}
	}
}

// TestOperatorTable: the operator table over both lane classes:
// each accepted operator is recorded as a call of its std.simd intrinsic,
// and each refused one is E0308 (with its mapping) or E0301.
func TestOperatorTable(t *testing.T) {
	pre := "F(a: simd.Vec[i32, 4], b: simd.Vec[i32, 4], f: simd.Vec[f32, 4], g: simd.Vec[f32, 4], k: u8) {\n"
	simdCases(t, []bodyCase{
		{"integer wrapping, bitwise, shifts, compares", pre + "\tc := a +% b -% a *% b\n\td := (c & b) | (c ^ ~a)\n\te := d << k >> 2\n\tm := e == a\n\tn := e != a\n\to := e < a\n\tp := e <= a\n\tq := e > a\n\tr := e >= a\n}\n", ""},
		{"float arithmetic, negation, compares", pre + "\th := -f * g / f + g - f\n\tm := h == g\n\tn := h < g\n}\n", ""},
		{"integer +", pre + "\tc := a + b\n}\n", "E0308@4:7"},
		{"integer -", pre + "\tc := a - b\n}\n", "E0308@4:7"},
		{"integer *", pre + "\tc := a * b\n}\n", "E0308@4:7"},
		{"integer /", pre + "\tc := a / b\n}\n", "E0308@4:7"},
		{"integer %", pre + "\tc := a % b\n}\n", "E0308@4:7"},
		{"integer unary -", pre + "\tc := -a\n}\n", "E0308@4:7"},
		{"float +%", pre + "\tc := f +% g\n}\n", "E0308@4:7"},
		{"float &", pre + "\tc := f & g\n}\n", "E0308@4:7"},
		{"float %", pre + "\tc := f % g\n}\n", "E0308@4:7"},
		{"float ~", pre + "\tc := ~f\n}\n", "E0308@4:7"},
		{"float <<", pre + "\tc := f << k\n}\n", "E0308@4:7"},
		{"scalar mixed", pre + "\tc := a +% 1\n}\n", "E0301@4:12"},
		{"two vector types", pre + "\tc := a +% f\n}\n", "E0301@4:12"},
		{"vector amount", pre + "\tc := a << b\n}\n", "E0308@4:7"},
		{"three lanes", "F(a: simd.Vec[u8, 3]) {\n\tc := a +% a\n}\n", "E0706@4:7"},
	})
	t.Run("recorded as the intrinsic", func(t *testing.T) {
		w := simdWorld(t)
		b := checkBody(t, w, "", "import std.simd\n\nF(a: simd.Vec[u8, 16], k: u8): simd.Mask[u8, 16] {\n\treturn (a +% a << k) == a\n}\n")
		var names []string
		for _, u := range b.bodyUses(t) {
			if u.Kind == UseMethod {
				names = append(names, w.items.Get(u.Item).Key.String())
			}
		}
		got := strings.Join(names, " ")
		for _, want := range []string{"std.simd.Vec:AddWrap", "std.simd.Vec:Shl", "std.simd.Vec:Eq"} {
			if !strings.Contains(got, want) {
				t.Errorf("uses %q lack %s", got, want)
			}
		}
	})
	t.Run("fix for +", func(t *testing.T) {
		w := simdWorld(t)
		b := w.add("m", opt{}, "import std.simd\n\nF(a: simd.Vec[i32, 4]): simd.Vec[i32, 4] {\n\treturn a + a\n}\n")
		ds := b.bodyDiags(t)
		if len(ds) != 1 || len(ds[0].Fixes) != 1 || ds[0].Fixes[0].Class != diag.Compiles || ds[0].Fixes[0].Edits[0].Text != " +% " || len(ds[0].Alternatives) != 1 {
			t.Fatalf("want E0308 with the compiles fix +%% and AddSat:\n%s", dump(w, ds))
		}
	})
}

// TestMaskOperators: a Mask takes & | ^ and !; arithmetic and && on a
// mask are E0308; a mask as a condition is E0301.
func TestMaskOperators(t *testing.T) {
	pre := "F(m: simd.Mask[f64, 2], n: simd.Mask[f64, 2]) {\n"
	simdCases(t, []bodyCase{
		{"bitwise and not", pre + "\tc := (m & n) | (m ^ !n)\n}\n", ""},
		{"arithmetic", pre + "\tc := m +% n\n}\n", "E0308@4:7"},
		{"logical and", pre + "\tc := m && n\n}\n", "E0301@4:12 E0301@4:7"},
		{"logical or with a bool", pre + "\tc := true || m\n}\n", "E0301@4:15"},
		{"condition", pre + "\tif m {\n\t\treturn\n\t}\n}\n", "E0301@4:5"},
		{"loop condition", pre + "\tfor m {\n\t\tbreak\n\t}\n}\n", "E0301@4:6"},
	})
	t.Run("alternatives", func(t *testing.T) {
		w := simdWorld(t)
		b := w.add("m", opt{}, "import std.simd\n\nF(m: simd.Mask[f64, 2]) {\n\tif m {\n\t\treturn\n\t}\n\tc := m || true\n}\n")
		ds := b.bodyDiags(t)
		if len(ds) != 2 {
			t.Fatalf("%d diagnostics, want two E0301", len(ds))
		}
		for _, d := range ds {
			var alts []string
			for _, a := range d.Alternatives {
				alts = append(alts, a)
			}
			if got := strings.Join(alts, " | "); d.Code != diag.E0301 || !strings.Contains(got, ".All()") || !strings.Contains(got, ".Any()") {
				t.Errorf("%v %s: alternatives %q", d.Code, d.Message, got)
			}
		}
	})
}

// bodyUses checks every unit of b's module and returns their Uses.
func (b *built) bodyUses(t *testing.T) []Use {
	t.Helper()
	var out []Use
	for _, id := range b.m.Units() {
		body := b.w.c.Check(b.m, id)
		out = append(out, body.Uses...)
	}
	return out
}

// TestLaneClasses: the reference's lane classes at the call (E0701 with
// `lanes`), the widening class, Trunc's narrower integer, and E0703 for a T
// bounded only by Lane unless narrowed with `if T is f32`; over the overlay,
// since each call's lane-count requires is evaluated
// (testdata/simd/laneclass.mk).
func TestLaneClasses(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("testdata/simd/laneclass.mk")
	if err != nil {
		t.Fatal(err)
	}
	out, code := mkOverlay(t, string(src), "check")
	want := []string{"main.mk:4:12: E0701", "main.mk:5:12: E0701", "main.mk:6:12: E0701", "main.mk:7:12: E0701",
		"main.mk:8:12: E0701", "main.mk:9:12: E0701", "main.mk:19:14: E0703"}
	if code == 0 {
		t.Fatalf("want errors:\n%s", out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %s", w)
		}
	}
	if n := strings.Count(out, ": E07"); n != len(want) {
		t.Errorf("%d E07xx diagnostics, want %d:\n%s", n, len(want), out)
	}
	// Trunc's rule is a narrower integer, not the widening class (line 9).
	if !strings.Contains(out, "`Trunc` cannot make `i64` lanes from `i32` lanes: the result is an integer lane no wider than the source's") {
		t.Errorf("Trunc's E0701 message:\n%s", out)
	}
}

// TestVecInExternE1107: a Vec or Mask, or a struct, record or array
// holding one by value, as a parameter or result of an extern or export
// extern is E1107 at it; a raw pointer to a Vec, and a Vec in a plain Mk
// function, are accepted.
func TestVecInExternE1107(t *testing.T) {
	simdCases(t, []bodyCase{
		{"export extern parameter", "export extern Total(v: simd.Vec[f32, 4]): f32 {\n\treturn 0.0\n}\n", "E1107@3:21"},
		{"bodiless extern result", "extern Make(): simd.Mask[u8, 16]\n", "E1107@3:16"},
		{"record holding one", "extern Pair(p: (a: simd.Vec[u8, 16], n: int)): int\n", "E1107@3:13"},
		{"array of them", "extern Many(p: [2]simd.Vec[u8, 16]): int\n", "E1107@3:13"},
		{"struct holding one", "struct Box {\n\tv: simd.Vec[u8, 16]\n}\n\nextern Boxed(b: Box): int\n", "E1107@7:14"},
		{"raw pointer: accepted", "extern ByPtr(p: *simd.Vec[f32, 4]): int\n", ""},
		{"Mk function: accepted", "Twice(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] {\n\treturn v + v\n}\n", ""},
	})
}

// TestShuffleIndexE0706: a Shuffle index outside 0 to 2N - 1 is E0706
// at the index, judged by check over the literal; the
// indices in range check clean and are recorded as one const.
func TestShuffleIndexE0706(t *testing.T) {
	pre := "import std.simd\n\nF(a: simd.Vec[u32, 4]): simd.Vec[u32, 4] {\n"
	for _, tc := range []struct{ name, idx, want string }{
		{"in range", "[0, 7, 2, 4]", ""},
		{"past 2N - 1", "[0, 1, 8, 3]", "main.mk:4:29: E0706"},
		{"negative", "[0, -1, 2, 3]", "main.mk:4:26: E0706"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := mkOverlay(t, pre+"\treturn a.Shuffle(a, "+tc.idx+")\n}\n", "check")
			if tc.want == "" {
				if code != 0 {
					t.Fatalf("mk check: exit %d\n%s", code, out)
				}
				return
			}
			if code == 0 || !strings.Contains(out, tc.want) || strings.Count(out, ": E0706 ") != 1 {
				t.Fatalf("want one %s, got exit %d:\n%s", tc.want, code, out)
			}
		})
	}
}

// TestCpuClause: a cpu clause's names come from the feature table
// (E1106 `feature` at the clause); a bodiless extern's clause is E1106
// `no-body`, an impl method's E1106 `impl`; the closure is the signature's
// CPU, sorted.
func TestCpuClause(t *testing.T) {
	simdCases(t, []bodyCase{
		{"known names", "F(x: int): int cpu \"avx2, fma\" {\n\treturn x\n}\n", ""},
		{"a level name", "F(x: int): int cpu \"x86-64-v3\" {\n\treturn x\n}\n", ""},
		{"another architecture's name", "F(x: int): int cpu \"neon\" {\n\treturn x\n}\n", ""},
		{"unknown name", "Count(xs: []u32): int cpu \"avx9\" {\n\treturn xs.Len()\n}\n", "E1106@3:27"},
		{"bodiless extern", "extern Ext(x: int): int cpu \"avx2\"\n", "E1106@3:29"},
		{"impl method", "interface Twice {\n\tTwice(&self): int\n}\n\nstruct S {\n\tx: int\n}\n\nimpl Twice for S {\n\tTwice(&self): int cpu \"avx2\" {\n\t\treturn self.x\n\t}\n}\n", "E1106@12:24"},
	})
	t.Run("closure recorded", func(t *testing.T) {
		w := simdWorld(t)
		w.add("m", opt{}, "F(x: int): int cpu \"avx2\" {\n\treturn x\n}\n").wantClean(t)
		id, ok := w.items.ByKey(types.ItemKey("m", "F"))
		if !ok {
			t.Fatal("no m.F")
		}
		got := strings.Join(w.items.Get(id).Decl.(*types.FuncDecl).Sig.CPU, ",")
		if got != "avx,avx2,sse,sse2,sse3,sse4.1,sse4.2,ssse3" {
			t.Fatalf("Signature.CPU = %s", got)
		}
	})
}

// TestCpuCallNeedsUnsafe: calling, or naming as a value, a function
// whose features the caller's context (the target's baseline and its own
// clause) lacks is E1101 outside unsafe; inside unsafe, from a function
// with the features, or below the baseline, it is accepted.
func TestCpuCallNeedsUnsafe(t *testing.T) {
	pre := "Double8(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu \"avx2\" {\n\treturn v + v\n}\n\n"
	simdCases(t, []bodyCase{
		{"call", pre + "Call(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {\n\treturn Double8(v)\n}\n", "E1101@8:9"},
		{"value", pre + "Pick(): fn(simd.Vec[f32, 8]): simd.Vec[f32, 8] {\n\treturn Double8\n}\n", "E1101@8:9"},
		{"in unsafe", pre + "Call(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] {\n\tunsafe \"Cpu() reports avx2\" {\n\t\treturn Double8(v)\n\t}\n}\n", ""},
		{"from a function with the features", pre + "Call(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu \"avx2\" {\n\treturn Double8(v)\n}\n", ""},
		{"from a level that implies them", pre + "Call(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu \"x86-64-v3\" {\n\treturn Double8(v)\n}\n", ""},
		{"inside the baseline", "Fast(x: int): int cpu \"sse4.2\" {\n\treturn x\n}\n\nCall(x: int): int {\n\treturn Fast(x)\n}\n", ""},
	})
}

// TestFeatureNeeds: Fma needs fma in the function's features and the
// rounding methods sse4.1 (E1106 `needs`; the hosted baseline holds
// sse4.1); CpuFeatures.Of and Has take names of the table (E1106).
func TestFeatureNeeds(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"Fma at the baseline", "F(a: simd.Vec[f32, 4]): simd.Vec[f32, 4] {\n\treturn a.Fma(a, a)\n}\n", "main.mk:4:11: E1106"},
		{"Fma with fma", "F(a: simd.Vec[f32, 4]): simd.Vec[f32, 4] cpu \"fma\" {\n\treturn a.Fma(a, a)\n}\n", ""},
		{"Floor at the hosted baseline", "F(a: simd.Vec[f32, 4]): simd.Vec[f32, 4] {\n\treturn a.Floor()\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := mkOverlay(t, "import std.simd\n\n"+tc.src, "check")
			if tc.want == "" && code != 0 || tc.want != "" && (code == 0 || !strings.Contains(out, tc.want)) {
				t.Fatalf("want %q, got exit %d:\n%s", tc.want, code, out)
			}
		})
	}
	simdCases(t, []bodyCase{
		{"Of a known list", "F(): simd.CpuFeatures {\n\treturn simd.CpuFeatures.Of(\"avx2, bmi2\")\n}\n", ""},
		{"Of an unknown name", "F(): simd.CpuFeatures {\n\treturn simd.CpuFeatures.Of(\"avx2, sse9\")\n}\n", "E1106@4:29"},
		{"Has an unknown name", "F(c: &simd.CpuFeatures): bool {\n\treturn c.Has(\"avx9\")\n}\n", "E1106@4:15"},
		{"Has another architecture's name", "F(c: &simd.CpuFeatures): bool {\n\treturn c.Has(\"neon\")\n}\n", ""},
	})
}

// TestMmio: every std.mmio call needs unsafe (E1101 `mmio`); T is an
// integer type, int, uint or a raw pointer (E0701 for f64, bool or a
// struct); inside unsafe, in a module unsafe_modules lists, a register
// read and a load check clean.
func TestMmio(t *testing.T) {
	mmio, err := os.ReadFile("../../std/mmio/mmio.mk")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, src, want string }{
		{"load outside unsafe", "Status(p: *u32): u32 {\n\treturn mmio.Load(p)\n}\n", "E1101@4:9"},
		{"register outside unsafe", "Ver(): u32 {\n\treg := mmio.Reg[u32].New(0xFEE00030)\n\treturn reg.Read()\n}\n", "E1101@4:9 E1101@5:9"},
		{"fence outside unsafe", "F() {\n\tmmio.Fence()\n}\n", "E1101@4:2"},
		{"in unsafe", "Ver(p: *u64): u32 {\n\tunsafe \"the APIC page is mapped uncached\" {\n\t\tmmio.Store(p, mmio.Load(p))\n\t\tmmio.Fence()\n\t\treg := mmio.Reg[u32].New(0xFEE00030)\n\t\treg.Write(reg.Read())\n\t\treturn reg.Read() & 0xFF\n\t}\n}\n", ""},
		{"raw pointer lanes", "F(p: **u8): *u8 {\n\tunsafe \"r\" {\n\t\treturn mmio.Load(p)\n\t}\n}\n", ""},
		{"float", "F(p: *f64): f64 {\n\tunsafe \"r\" {\n\t\treturn mmio.Load(p)\n\t}\n}\n", "E0701@5:10"},
		{"bool", "F(p: *bool): bool {\n\tunsafe \"r\" {\n\t\treturn mmio.Load(p)\n\t}\n}\n", "E0701@5:10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true)
			w.add("std.mmio", opt{std: true}, string(mmio)).wantClean(t)
			checkBody(t, w, tc.want, "import std.mmio\n\n"+tc.src)
		})
	}
}

// TestCompoundAssign: a compound assignment on a Vec or Mask follows
// its operator: accepted forms are recorded on the statement as the
// operator's intrinsic; rejected ones are E0308 naming the compound
// operator.
func TestCompoundAssign(t *testing.T) {
	pre := "F(a: simd.Vec[i32, 4], f: simd.Vec[f32, 4], m: simd.Mask[i32, 4], k: u8) {\n\tvar x := a\n\tvar y := f\n\tvar z := m\n"
	simdCases(t, []bodyCase{
		{"integer wrapping, bitwise, shifts", pre + "\tx +%= a\n\tx -%= a\n\tx *%= a\n\tx &= a\n\tx |= a\n\tx ^= a\n\tx <<= k\n\tx >>= 1\n}\n", ""},
		{"float arithmetic", pre + "\ty += f\n\ty -= f\n\ty *= f\n\ty /= f\n}\n", ""},
		{"mask bitwise", pre + "\tz &= m\n\tz |= m\n\tz ^= m\n}\n", ""},
		{"integer +=", pre + "\tx += a\n}\n", "E0308@7:2"},
		{"integer /=", pre + "\tx /= a\n}\n", "E0308@7:2"},
		{"float +%=", pre + "\ty +%= f\n}\n", "E0308@7:2"},
		{"mask +%=", pre + "\tz +%= m\n}\n", "E0308@7:2"},
		{"scalar right side", pre + "\tx +%= 1\n}\n", "E0301@7:8"},
	})
	t.Run("recorded as the intrinsic", func(t *testing.T) {
		w := simdWorld(t)
		b := checkBody(t, w, "", "import std.simd\n\nF(a: simd.Vec[u8, 16]): simd.Vec[u8, 16] {\n\tvar x := a\n\tx +%= a\n\tx <<= 3\n\tx >>= 1\n\treturn x\n}\n")
		var names []string
		for _, u := range b.bodyUses(t) {
			if u.Kind == UseMethod {
				names = append(names, w.items.Get(u.Item).Key.String())
			}
		}
		got := strings.Join(names, " ")
		for _, want := range []string{"std.simd.Vec:AddWrap", "std.simd.Vec:Shl", "std.simd.Vec:Shr"} {
			if !strings.Contains(got, want) {
				t.Errorf("uses %q lack %s", got, want)
			}
		}
	})
}
