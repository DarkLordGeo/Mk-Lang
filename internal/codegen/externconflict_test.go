// Why: an extern's symbol is its C name, so one name can arrive with two
// types in one pack, and an accepted input must never cause an ICE.
// What: one C name declared twice, by two Mk externs (Sleep(u32) and
// Sleep(u64)) or by a Mk extern and codegen's own memcmp of str comparison,
// is one LLVM declaration and calls with each declaration's own type, never
// an llvm.Module panic; adapters and thunks share the symbol too.
// How: IR checks, and native runs against the support pack's memcmp.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func externIn(e *testEnv, mod, name string, params []types.TypeID, result types.TypeID) types.ItemID {
	id := e.fnIn(mod, "", name, params, result, false)
	fd := e.items.Get(id).Decl.(*types.FuncDecl)
	fd.Sig.Extern, fd.Bodiless = true, true
	return id
}

// conflictSleep: modules a and b declare extern Sleep(ms: u32) and
// Sleep(ms: u64); Both() { a.Sleep(1); b.Sleep(2) }.
func conflictSleep(e *testEnv) ([]*mir.Body, []types.ItemID) {
	sa := externIn(e, "a", "Sleep", []types.TypeID{types.U32}, types.Void)
	sb := externIn(e, "b", "Sleep", []types.TypeID{types.U64}, types.Void)
	f := e.body(e.fn("Both", nil, types.Void, 0))
	v := f.local(types.Void)
	f.callTo(direct(sa), place(v), mir.NoLocal, f.at(2, 3), mir.Imm(types.U32, 1))
	f.callTo(direct(sb), place(v), mir.NoLocal, f.at(3, 3), mir.Imm(types.U64, 2))
	f.ret(f.at(4, 1))
	return []*mir.Body{f.b}, []types.ItemID{sa, sb}
}

// conflictMemcmp: extern memcmp(a: int, b: int, n: int): int in main, and
//
//	First(): bool { c := memcmp(0, 0, 0); return "ab" < "ac" }   (the extern first)
//	Second(): bool { b := "ac" < "ab"; return b }                  (str compare first)
//	Third(): int { return memcmp(0, 0, 0) }
//
// with First before Second in one unit, or Second first when swap.
func conflictMemcmp(e *testEnv, swap bool) ([]*mir.Body, []types.ItemID) {
	mc := externIn(e, "main", "memcmp", []types.TypeID{types.Int, types.Int, types.Int}, types.Int)
	first := e.body(e.fn("First", nil, types.Bool, 0))
	c := first.local(types.Int)
	first.callTo(direct(mc), place(c), mir.NoLocal, first.at(2, 3), mir.Imm(types.Int, 0), mir.Imm(types.Int, 0), mir.Imm(types.Int, 0))
	first.assign(place(0), bin(mir.Lt, strConst(e, "ab"), strConst(e, "ac")), first.at(3, 3))
	first.ret(first.at(3, 3))
	second := e.body(e.fn("Second", nil, types.Bool, 0))
	second.assign(place(0), bin(mir.Lt, strConst(e, "ac"), strConst(e, "ab")), second.at(5, 3))
	second.ret(second.at(5, 3))
	third := e.body(e.fn("Third", nil, types.Int, 0))
	third.callTo(direct(mc), place(0), mir.NoLocal, third.at(7, 3), mir.Imm(types.Int, 0), mir.Imm(types.Int, 0), mir.Imm(types.Int, 0))
	third.ret(third.at(7, 3))
	bodies := []*mir.Body{first.b, second.b, third.b}
	if swap {
		bodies = []*mir.Body{second.b, first.b, third.b}
	}
	return bodies, []types.ItemID{mc}
}

func TestExternSymbolConflict(t *testing.T) {
	declares := regexp.MustCompile(`(?m)^declare .*@(Sleep|memcmp)\(`)
	for _, target := range types.Targets() {
		t.Run(target.Name+"/sleep", func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := conflictSleep(e)
			ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
			if n := len(declares.FindAllString(ll, -1)); n != 1 {
				t.Errorf("%d declarations of Sleep:\n%s", n, ll)
			}
			both := fnText(ll, "mk.main.Both")
			if !regexp.MustCompile(`call void @Sleep\(i32 [^)]*1\)`).MatchString(both) ||
				!regexp.MustCompile(`call void @Sleep\(i64 [^)]*2\)`).MatchString(both) {
				t.Errorf("the two calls do not use their own declarations' types:\n%s", both)
			}
			clangC(t, target, t.TempDir(), "pack", ll, "-O2")
		})
		for _, swap := range []bool{false, true} {
			name := target.Name + "/memcmp-extern-first"
			if swap {
				name = target.Name + "/memcmp-compare-first"
			}
			t.Run(name, func(t *testing.T) {
				e := newTestEnv()
				bodies, bodiless := conflictMemcmp(e, swap)
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
				if n := len(declares.FindAllString(ll, -1)); n != 1 {
					t.Errorf("%d declarations of memcmp:\n%s", n, ll)
				}
				if !strings.Contains(ll, "call i32 @memcmp(ptr") {
					t.Errorf("no str comparison through memcmp's C type:\n%s", ll)
				}
				if !regexp.MustCompile(`call noundef i64 @memcmp\(i64`).MatchString(ll) {
					t.Errorf("no call of the extern with its own type:\n%s", ll)
				}
				clangC(t, target, t.TempDir(), "pack", ll, "-O2")
			})
		}
	}
}

// TestExternSymbolConflictRun: the memcmp units run natively, -O0 and
// -O2, against the support pack's memcmp: First is "ab" < "ac" (1), Second
// "ac" < "ab" (0), Third memcmp(0, 0, 0) through the Mk extern (0).
func TestExternSymbolConflictRun(t *testing.T) {
	for _, target := range runTargets {
		for _, swap := range []bool{false, true} {
			for _, opt := range []string{"-O0", "-O2"} {
				name := target.Name + opt
				if swap {
					name += "-swapped"
				}
				t.Run(name, func(t *testing.T) {
					if _, why := hostRunner(target); why != "" {
						skipRunner(t, why)
					}
					e := newTestEnv()
					bodies, bodiless := conflictMemcmp(e, swap)
					ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
					dir := t.TempDir()
					osIR, kernel := harnessOS(target)
					h := osIR + harnessRT + `
declare zeroext i1 @mk.main.First()
declare zeroext i1 @mk.main.Second()
declare i64 @mk.main.Third()

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  %a = call zeroext i1 @mk.main.First()
  %a64 = zext i1 %a to i64
  call void @h_pn(i64 %a64)
  %b = call zeroext i1 @mk.main.Second()
  %b64 = zext i1 %b to i64
  call void @h_pn(i64 %b64)
  %c = call i64 @mk.main.Third()
  call void @h_pn(i64 %c)
  call void @h_exit(i32 0)
  unreachable
}
`
					objs := []string{
						clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
						clangC(t, target, dir, "pack", ll, opt),
						clangC(t, target, dir, "harness", h, "-O0"),
					}
					out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
					if code != 0 || out != "1\n0\n0\n" {
						t.Fatalf("exit %d, output %q; want 1, 0, 0", code, out)
					}
				})
			}
		}
	}
}

// TestSharedSymbolAdaptersAndThunks: two externs of one C name in one
// pack, with the system stack in the build, get distinct adapters for their
// direct calls and distinct thunks for their s.go sites (mk once failed on
// such a Sleep program with "AddFunction: symbol @mk.cs.Sleep already
// defined").
func TestSharedSymbolAdaptersAndThunks(t *testing.T) {
	for _, target := range types.Targets() {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := conflictSleep(e)
			s := newSpawnStd(e)
			sysStack := e.structIn("std.task", "SysStack", e.tt.RawPtr(types.U8), types.U64, types.U64, types.U64)
			bodiless = append(append(bodiless, s.bodiless...), e.fnIn("std.task", "", "SystemStack", nil, e.tt.RawPtr(sysStack), false),
				e.fnIn("std.task", "", "SystemStackDone", nil, types.Void, false))
			sa, sb := mustItemE(e, "a.Sleep"), mustItemE(e, "b.Sleep")
			g := e.body(e.fn("GoBoth", []types.TypeID{s.io}, types.Void, 0))
			sl := g.scopeLocal(s.scope)
			v := g.local(types.Void)
			g.callTo(direct(s.ioScope), place(sl), mir.NoLocal, g.at(9, 8), mir.Copy(place(1)))
			g.callTo(direct(sa), place(v), sl, g.at(10, 5), mir.Imm(types.U32, 1))
			g.callTo(direct(sb), place(v), sl, g.at(11, 5), mir.Imm(types.U64, 2))
			g.join(sl, false, g.at(12, 3))
			g.ret(g.at(12, 3))
			ll := emit(t, e, unitOf(e, append(bodies, g.b), bodiless...), target, types.Dev)
			for _, want := range []string{"@mk.cs.Sleep(", "@mk.cs.Sleep.1(", "@Sleep.go(", "@Sleep.go.1("} {
				if defines(ll, want) != 1 {
					t.Errorf("want one definition of %s:\n%s", want, ll)
				}
			}
			clangC(t, target, t.TempDir(), "pack", ll, "-O2")
		})
	}
}
