// Why: fixes for float %, the unit's symbol index and cast switches each need
// a test that fails if they regress.
// What: TestFloatRem* (float % calls std.math.Rem64/Rem32 through
// the unit; a build without them is the ICE "missing runtime entry"),
// TestUnitIndexShared and TestCastSwitchWeights.
// TestSignedEnumTag is in run_test.go, beside the other casts.
// How: remBody stands in for std.math's Rem; packsOf spreads bodies over
// packs, and the IR is checked and run.

package codegen

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// stdFnDecl adds a std function of module mod that has a body (not
// bodiless, not an intrinsic): a std function codegen calls by key.
func (e *testEnv) stdFnDecl(mod, name string, params []types.TypeID, result types.TypeID) types.ItemID {
	sig := types.Signature{Name: e.in.Intern(name), Result: result}
	for i, p := range params {
		sig.Params = append(sig.Params, types.Param{Name: e.in.Intern("p" + string(rune('a'+i))), Type: p})
	}
	id := e.itemIn(mod, types.ItemKey(mod, name), name, types.KindFunc, &types.FuncDecl{Sig: sig})
	e.items.Get(id).Decl.(*types.FuncDecl).Sig.Item = id
	return id
}

// remBody is a stand-in for std.math.Rem64 (or Rem32 over f32/i32):
// a - f(trunc(a / b)) * b, exact for the small operands of these tests.
func remBody(e *testEnv, ft, it types.TypeID) *mir.Body {
	name := "Rem64"
	if ft == types.F32 {
		name = "Rem32"
	}
	f := e.body(e.stdFnDecl("std.math", name, []types.TypeID{ft, ft}, ft))
	q, qi, qf, m := f.local(ft), f.local(it), f.local(ft), f.local(ft)
	f.assign(place(q), bin(mir.Div, mir.Copy(place(1)), mir.Copy(place(2))), f.at(2, 3))
	f.assign(place(qi), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastChecked, A: mir.Copy(place(q)), Type: it}, f.at(2, 5))
	f.assign(place(qf), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastChecked, A: mir.Copy(place(qi)), Type: ft}, f.at(2, 7))
	f.assign(place(m), bin(mir.Mul, mir.Copy(place(qf)), mir.Copy(place(2))), f.at(2, 9))
	f.assign(place(0), bin(mir.Sub, mir.Copy(place(1)), mir.Copy(place(m))), f.at(2, 11))
	f.ret(f.at(3, 1))
	return f.b
}

// fremTen is fn FRemTen(a: f64, b: f64, k: f64): int { return
// Cast[int]((a % b) * k) } (float immediates are consts in MIR, so the
// harness passes 7.5, 2.0 and 10.0, giving 15).
func fremTen(e *testEnv) *mir.Body {
	f := e.body(e.fn("FRemTen", []types.TypeID{types.F64, types.F64, types.F64}, types.Int, 0))
	r, s := f.local(types.F64), f.local(types.F64)
	f.assign(place(r), bin(mir.Rem, mir.Copy(place(1)), mir.Copy(place(2))), f.at(4, 9))
	f.assign(place(s), bin(mir.Mul, mir.Copy(place(r)), mir.Copy(place(3))), f.at(4, 20))
	f.assign(place(0), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastChecked, A: mir.Copy(place(s)), Type: types.Int}, f.at(4, 30))
	f.ret(f.at(5, 1))
	return f.b
}

// fremF32 is fn FRem32(a: f32, b: f32): f32 { return a % b }.
func fremF32(e *testEnv) *mir.Body {
	return binFn(e, "FRem32", mir.Rem, types.F32, types.F32, mir.Copy(place(2)))
}

// packsOf is a unit of the given packs of bodies (symbols mk.<key>).
func packsOf(e *testEnv, packs ...[]*mir.Body) *Unit {
	u := &Unit{Packs: make([][]int, len(packs))}
	for p, bs := range packs {
		for _, b := range bs {
			u.Packs[p] = append(u.Packs[p], len(u.Funcs))
			it := e.items.Get(b.Item)
			u.Funcs = append(u.Funcs, Func{Symbol: "mk." + it.Key.String(), Key: it.Key.String(), Item: b.Item, Body: b, Pack: p})
		}
	}
	return u
}

var (
	reDefRem64  = regexp.MustCompile(`(?m)^define [^\n]*@mk\.std\.math\.Rem64\(`)
	reDeclRem64 = regexp.MustCompile(`(?m)^declare [^\n]*@mk\.std\.math\.Rem64\(`)
	reCallRem64 = regexp.MustCompile(`call [^\n]*@mk\.std\.math\.Rem64\(`)
)

// TestFloatRemCallsStd: float % calls the unit's std.math.Rem64
// (f64) or Rem32 (f32) instance through funcDecl. In the pack that holds
// Rem64's body the call targets that one definition (no declaration, no
// "already defined" ICE); in another pack the call shares one declaration.
// No frem anywhere. Each pack compiles with clang -O0 and -O2.
func TestFloatRemCallsStd(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows, types.Aarch64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			ten, r64, f32, r32 := fremTen(e), remBody(e, types.F64, types.I64), fremF32(e), remBody(e, types.F32, types.I32)
			// One pack holding the caller and Rem64's body.
			same := emitP(t, e, packsOf(e, []*mir.Body{ten, r64, f32, r32}), 0, target, types.Dev)
			if n := len(reDefRem64.FindAllString(same, -1)); n != 1 {
				t.Errorf("same pack: %d definitions of Rem64, want 1", n)
			}
			if reDeclRem64.MatchString(same) {
				t.Error("same pack: Rem64 is also declared")
			}
			if !reCallRem64.MatchString(fnText(same, "mk.main.FRemTen")) {
				t.Errorf("FRemTen does not call Rem64:\n%s", fnText(same, "mk.main.FRemTen"))
			}
			if !strings.Contains(fnText(same, "mk.main.FRem32"), "@mk.std.math.Rem32(") {
				t.Errorf("FRem32 does not call Rem32:\n%s", fnText(same, "mk.main.FRem32"))
			}
			// The caller in pack 0, Rem64 and Rem32 in pack 1.
			other := emitP(t, e, packsOf(e, []*mir.Body{ten, f32}, []*mir.Body{r64, r32}), 0, target, types.Dev)
			if reDefRem64.MatchString(other) || len(reDeclRem64.FindAllString(other, -1)) != 1 {
				t.Errorf("other pack: want one declaration and no definition of Rem64:\n%s", other)
			}
			for _, ll := range []string{same, other} {
				if strings.Contains(ll, " frem ") {
					t.Error("frem emitted")
				}
			}
			dir := t.TempDir()
			for i, ll := range []string{same, other} {
				clangC(t, target, dir, fmt.Sprint("o0-", i), ll, "-O0")
				clangC(t, target, dir, fmt.Sprint("o2-", i), ll, "-O2")
			}
		})
	}
}

// TestFloatRemMissingRuntimeEntry: without std.math.Rem64 in the
// build, or with the item but no instance in the unit (not a mono root),
// emitting a float % is the E0000 ICE "missing runtime entry", not an
// undefined symbol at link.
func TestFloatRemMissingRuntimeEntry(t *testing.T) {
	for _, withItem := range []bool{false, true} {
		t.Run(fmt.Sprint("item=", withItem), func(t *testing.T) {
			e := newTestEnv()
			u := packsOf(e, []*mir.Body{fremTen(e)})
			if withItem {
				e.stdFnDecl("std.math", "Rem64", []types.TypeID{types.F64, types.F64}, types.F64)
			}
			var b strings.Builder
			_, err := EmitPack(u, 0, e, Config{Target: types.X86_64Linux, Profile: types.Dev}, &b)
			ie, ok := err.(*InternalError)
			if !ok || !strings.Contains(ie.IE.What, "missing runtime entry std.math.Rem64") {
				t.Fatalf("EmitPack: %v; want the ICE missing runtime entry std.math.Rem64", err)
			}
		})
	}
}

// TestFloatRemRun: natively, dev (-O0) and release (-O2), Windows
// and WSL Linux, FRemTen calls the Rem64 of another pack and returns 15.
func TestFloatRemRun(t *testing.T) {
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				u := packsOf(e, []*mir.Body{fremTen(e)}, []*mir.Body{remBody(e, types.F64, types.I64)})
				prof := types.Dev
				if opt == "-O2" {
					prof = types.Release
				}
				dir := t.TempDir()
				var objs []string
				for p := range u.Packs {
					objs = append(objs, clangC(t, target, dir, fmt.Sprint("pack", p), emitP(t, e, u, p, target, prof), opt))
				}
				osIR, kernel := harnessOS(target)
				h := osIR + harnessRT + `
declare i64 @mk.main.FRemTen(double, double, double)

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  %r = call i64 @mk.main.FRemTen(double 7.5, double 2.0, double 10.0)
  call void @h_num(i64 %r)
  call void @h_write(ptr @s.nl, i64 1)
  call void @h_exit(i32 0)
  unreachable
}
`
				objs = append(objs, clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "harness", h, "-O0"))
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
				if code != 0 || out != "15\n" {
					t.Fatalf("exit %d, output %q; want 0 and \"15\\n\"", code, out)
				}
			})
		}
	}
}

// TestUnitIndexShared: the exact (Item, Args) index is built once
// per Unit and shared by every EmitPack, so the allocations of one
// EmitPack do not grow with len(Unit.Funcs).
func TestUnitIndexShared(t *testing.T) {
	cfg := Config{Target: types.X86_64Linux, Profile: types.Dev}
	unit := func(extra int) (*testEnv, *Unit) {
		e := newTestEnv()
		u := packsOf(e, []*mir.Body{binFn(e, "Add", mir.Add, types.I64, types.I64, mir.Copy(place(2)))})
		for i := 0; i < extra; i++ {
			id := e.fn(fmt.Sprint("F", i), nil, types.Int, 0)
			u.Funcs = append(u.Funcs, Func{Symbol: fmt.Sprint("mk.main.F", i), Item: id, Pack: -1})
		}
		return e, u
	}
	allocs := func(e *testEnv, u *Unit) float64 {
		return testing.AllocsPerRun(5, func() {
			var b strings.Builder
			if _, err := EmitPack(u, 0, e, cfg, &b); err != nil {
				t.Fatal(err)
			}
		})
	}
	es, us := unit(10)
	el, ul := unit(10000)
	if small, large := allocs(es, us), allocs(el, ul); large > small+50 {
		t.Errorf("EmitPack allocates %.0f times with 10 extra Funcs and %.0f with 10,000", small, large)
	}
	before := ul.index()
	var b strings.Builder
	if _, err := EmitPack(ul, 0, el, cfg, &b); err != nil {
		t.Fatal(err)
	}
	if after := ul.index(); after != before {
		t.Error("the unit's index was rebuilt between two emissions")
	}
}

// TestCastSwitchWeights: the PanicCast switch of Cast[AB](x) for
// enum AB: u8 {A, B} carries !prof branch weights with the trap default
// first at 1 and each case at 1048575, and its default is the trap.
func TestCastSwitchWeights(t *testing.T) {
	var c emitCase
	for _, ec := range emitCases {
		if ec.name == "casts" {
			c = ec
		}
	}
	ll := caseIR(t, c, types.X86_64Linux)
	for _, fn := range []string{"mk.main.IntToAB", "mk.main.IntToSE"} {
		text := fnText(ll, fn)
		m := regexp.MustCompile(`switch i8 %\w+, label %(\w+) \[[^\]]*\], !prof (![0-9]+)`).FindStringSubmatch(text)
		if m == nil {
			t.Errorf("%s: no switch with !prof:\n%s", fn, text)
			continue
		}
		if !strings.Contains(ll, "\n"+m[2]+` = !{!"branch_weights", i32 1, i32 1048575, i32 1048575}`) {
			t.Errorf("%s: %s is not the weights {1, 1048575, 1048575}", fn, m[2])
		}
		trap := regexp.MustCompile(`(?s)\n` + m[1] + `:\n\s*call void @mk\.std\.rt\.PanicAt\(i32 noundef ` + fmt.Sprint(int(types.PanicCast)) + `,`)
		if !trap.MatchString(text) {
			t.Errorf("%s: the switch default %s is not the PanicCast trap", fn, m[1])
		}
	}
}
