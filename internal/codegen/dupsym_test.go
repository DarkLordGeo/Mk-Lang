// Why: a symbol declared or defined twice in one pack makes LLVM refuse the
// module, with errors like "@mk.std.rt.Panics already defined".
// What: TestVarGlobalDeclaredOnce (a pack that references a var global
// it does not define declares it once) and TestHookInDefiningPack (a
// panic hook is defined in its own pack, declared once elsewhere).
// How: two-pack units are emitted and the declarations are counted.

package codegen

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// readG is name(): int { return G }, through the static &G.
func readG(e *testEnv, name string, g types.ItemID, ln uint32) *mir.Body {
	f := e.body(e.fn(name, nil, types.Int, 0))
	p := f.local(e.tt.Ref(types.Int))
	f.assign(place(p), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g}, f.at(ln, 3))
	f.assign(place(0), use(mir.Copy(f.b.Place(p, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}))), f.at(ln, 3))
	f.ret(f.at(ln, 3))
	return f.b
}

// emitP is pack p's IR, failing the test on any error.
func emitP(t *testing.T, e *testEnv, u *Unit, p int, target types.Target, profile types.Profile) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := EmitPack(u, p, e, Config{Target: target, Profile: profile}, &b); err != nil {
		t.Fatalf("pack %d: %v", p, err)
	}
	return b.String()
}

// count is the number of lines of ll matching re.
func count(ll, re string) int {
	return len(regexp.MustCompile(`(?m)`+re).FindAllString(ll, -1))
}

// TestVarGlobalDeclaredOnce: G is defined by pack 0 (Def reads it);
// pack 1's R1 and R2 each read it, R2 twice. Pack 1 declares G once and
// pack 0 defines it once (never "@mk.std.rt.Panics already defined").
func TestVarGlobalDeclaredOnce(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				e := newTestEnv()
				g := e.item("G", types.KindGlobal, &types.GlobalDecl{Type: types.Int, HasValue: true, Value: e.intConst(types.Int, 7)})
				def := readG(e, "Def", g, 2)
				r1 := readG(e, "R1", g, 3)
				r2 := e.body(e.fn("R2", nil, types.Int, 0))
				for i := uint32(0); i < 2; i++ {
					p := r2.local(e.tt.Ref(types.Int))
					r2.assign(place(p), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g}, r2.at(4+i, 3))
					r2.assign(place(0), use(mir.Copy(r2.b.Place(p, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}))), r2.at(4+i, 3))
				}
				r2.ret(r2.at(6, 3))
				u := unitOf(e, []*mir.Body{def, r1, r2.b})
				u.Packs = [][]int{{0}, {1, 2}}
				u.Funcs[1].Pack, u.Funcs[2].Pack = 1, 1
				u.Globals = []Global{{Item: g, Pack: 0}}
				ll1 := emitP(t, e, u, 1, target, prof)
				if n := count(ll1, `^@mk\.main\.G = `); n != 1 {
					t.Errorf("pack 1 has %d lines for @mk.main.G, want 1:\n%s", n, ll1)
				}
				if !regexp.MustCompile(`(?m)^@mk\.main\.G = external (dso_local |hidden )*global `).MatchString(ll1) {
					t.Errorf("pack 1 does not declare G:\n%s", ll1)
				}
				ll0 := emitP(t, e, u, 0, target, prof)
				if n := count(ll0, `^@mk\.main\.G = `); n != 1 || strings.Contains(ll0, "@mk.main.G = external") {
					t.Errorf("pack 0 has %d lines for @mk.main.G, want one definition:\n%s", n, ll0)
				}
				dir := t.TempDir()
				clangC(t, target, dir, "p1", ll1, "-O2")
				clangC(t, target, dir, "p0", ll0, "-O2")
			})
		}
	}
}

// hookUnit is std.rt.PanicAt(code: u32, site: &u8): never, whose body
// panics (so its own pack calls the hook it defines, as std/rt does), and
// main.AddI32, whose overflow check calls the hook, and main.Direct, which
// calls PanicAt by MIR. hookPack is PanicAt's pack; the others are in
// pack 0.
func hookUnit(e *testEnv, hookPack int) *Unit {
	pi := e.itemIn("std.rt", types.ItemKey("std.rt", "PanicAt"), "PanicAt", types.KindFunc,
		&types.FuncDecl{Sig: types.Signature{Name: e.in.Intern("PanicAt"), Result: types.Never,
			Params: []types.Param{{Name: e.in.Intern("code"), Type: types.U32}, {Name: e.in.Intern("site"), Type: e.tt.Ref(types.U8)}}}})
	e.items.Get(pi).Decl.(*types.FuncDecl).Sig.Item = pi
	ph := e.body(pi)
	ph.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicUnreachable, Span: ph.at(9, 1)})
	add := binFn(e, "AddI32", mir.Add, types.I32, types.I32, mir.Copy(place(2)))
	d := e.body(e.fn("Direct", []types.TypeID{types.U32, e.tt.Ref(types.U8)}, types.Never, 0))
	d.callTo(direct(pi), place(0), mir.NoLocal, d.at(7, 3), mir.Copy(place(1)), mir.Copy(place(2)))
	d.term(mir.Terminator{Kind: mir.TermUnreachable, Span: d.at(7, 3)})
	u := unitOf(e, []*mir.Body{ph.b, add, d.b})
	if hookPack != 0 {
		u.Packs = [][]int{{1, 2}, {0}}
		u.Funcs[0].Pack = hookPack
	}
	return u
}

// TestHookInDefiningPack: in the pack that defines std.rt.PanicAt,
// every check and panic calls that definition and nothing declares it (never
// "Declare: symbol @mk.std.rt.PanicAt already defined"); in
// another pack the checks and a direct MIR call share one declaration.
func TestHookInDefiningPack(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+prof.String(), func(t *testing.T) {
				dir := t.TempDir()
				e := newTestEnv()
				ll := emitP(t, e, hookUnit(e, 0), 0, target, prof)
				if n := count(ll, `^define .*@mk\.std\.rt\.PanicAt\(`); n != 1 {
					t.Errorf("%d definitions of PanicAt, want 1:\n%s", n, ll)
				}
				if n := count(ll, `^declare .*@mk\.std\.rt\.PanicAt\(`); n != 0 {
					t.Errorf("PanicAt declared in its defining pack:\n%s", ll)
				}
				for _, fn := range []string{"mk.std.rt.PanicAt", "mk.main.AddI32", "mk.main.Direct"} {
					if !strings.Contains(fnText(ll, fn), "@mk.std.rt.PanicAt(") {
						t.Errorf("%s does not call PanicAt:\n%s", fn, fnText(ll, fn))
					}
				}
				clangC(t, target, dir, "same", ll, "-O2")

				e = newTestEnv()
				u := hookUnit(e, 1)
				ll = emitP(t, e, u, 0, target, prof)
				if n := count(ll, `^declare .*@mk\.std\.rt\.PanicAt\(`); n != 1 || strings.Contains(ll, "define hidden void @mk.std.rt.PanicAt") {
					t.Errorf("%d declarations of PanicAt in the other pack, want 1:\n%s", n, ll)
				}
				clangC(t, target, dir, "other", ll, "-O2")
				ll = emitP(t, e, u, 1, target, prof)
				if n := count(ll, `^define .*@mk\.std\.rt\.PanicAt\(`); n != 1 || count(ll, `^declare .*@mk\.std\.rt\.PanicAt\(`) != 0 {
					t.Errorf("hook pack:\n%s", ll)
				}
				clangC(t, target, dir, "hook", ll, "-O2")
			})
		}
	}
}
