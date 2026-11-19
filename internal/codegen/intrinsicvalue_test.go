// Why: a std intrinsic may be used as a fn value ("pass T.Get in namespace
// form, a plain fn"), so codegen needs a real function to point at.
// What: tests that an intrinsic used as a fn value, by RvFnRef or by a
// RelocFn in a const, and an s.go of an intrinsic, call the pack's wrapper
// of the intrinsic instead of raising "intrinsic used as a function" or
// "s.go of the intrinsic"; TestIntrinsicAsFnValueRun runs them.
// How: hand-built bodies are checked in the IR, then linked with a stub and
// a driver and run natively.

package codegen

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// fnValueBodies, over the spawn std surface of spawn_test.go:
//
//	LenOf(): int { f := str.Len; return f("abcd") }          (RvFnRef)
//	LenC(): int { return T.f("abcde") } with const T = str.Len (RelocFn)
//	RunC(io: Io) { f := Scope.Cancel; scope s := io.Scope() { f(&s) } }
//	RunS(io: Io) { f := Io.Scope; scope s := f(io) { } }
//	RunO(io: Io) { f := Io.ScopeOf[TooBig]; scope s := f(io) { } }
//	RunG(io: Io) { scope s := io.Scope() { s.go Scope.Cancel(&s) } }
func fnValueBodies(e *testEnv) ([]*mir.Body, []types.ItemID) {
	s := newSpawnStd(e)
	strLen := e.fnIn("std.core", "str", "Len", []types.TypeID{types.Str}, types.Int, true)
	lenT := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: types.Str}}, Result: types.Int})

	lo := e.body(e.fn("LenOf", nil, types.Int, 0))
	fv := lo.local(lenT)
	lo.assign(place(fv), mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: strLen}, Type: lenT}, lo.at(2, 5))
	lo.callTo(mir.Callee{Kind: mir.CallValue, Value: mir.Copy(place(fv))}, place(0), mir.NoLocal, lo.at(3, 12), strConst(e, "abcd"))
	lo.ret(lo.at(3, 5))

	lc := e.body(e.fn("LenC", nil, types.Int, 0))
	cf := e.consts.Intern(types.ConstValue{Type: lenT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: strLen}}}})
	lc.callTo(mir.Callee{Kind: mir.CallValue, Value: mir.Const(lenT, cf)}, place(0), mir.NoLocal, lc.at(5, 12), strConst(e, "abcde"))
	lc.ret(lc.at(5, 5))

	ref := e.tt.Ref(s.scope)
	cancelT := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: ref}}, Result: types.Void})
	rc := e.body(e.fn("RunC", []types.TypeID{s.io}, types.Void, 0))
	cv := rc.local(cancelT)
	sc := rc.scopeLocal(s.scope)
	r := rc.local(ref)
	v := rc.local(types.Void)
	rc.assign(place(cv), mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: s.cancel}, Type: cancelT}, rc.at(7, 5))
	rc.callTo(direct(s.ioScope), place(sc), mir.NoLocal, rc.at(8, 8), mir.Copy(place(1)))
	rc.assign(place(r), mir.Rvalue{Kind: mir.RvRef, Place: place(sc)}, rc.at(9, 5))
	rc.callTo(mir.Callee{Kind: mir.CallValue, Value: mir.Copy(place(cv))}, place(v), mir.NoLocal, rc.at(9, 5), mir.Copy(place(r)))
	rc.join(sc, false, rc.at(10, 3))
	rc.ret(rc.at(10, 3))

	scopeT := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: s.io}}, Result: s.scope})
	rs := e.body(e.fn("RunS", []types.TypeID{s.io}, types.Void, 0))
	sv := rs.local(scopeT)
	ss := rs.scopeLocal(s.scope)
	rs.assign(place(sv), mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: s.ioScope}, Type: scopeT}, rs.at(12, 5))
	rs.callTo(mir.Callee{Kind: mir.CallValue, Value: mir.Copy(place(sv))}, place(ss), mir.NoLocal, rs.at(13, 8), mir.Copy(place(1)))
	rs.join(ss, false, rs.at(14, 3))
	rs.ret(rs.at(14, 3))

	// Io.ScopeOf[E] is generic: its declared signature is [E](self): Scope[E].
	of := e.items.Get(s.ioScopeOf).Decl.(*types.FuncDecl)
	of.Sig.Generics = []types.GenericParam{{Name: e.in.Intern("E")}}
	of.Sig.OwnGenerics = 1
	pset := e.tt.ErrorSet([]types.ErrorMember{{IsParam: true, Param: 0}})
	of.Sig.Result = e.tt.Named(e.tt.Get(s.scope).Item, []types.GenericArg{types.TypeArg(e.tt.ErrorSetType(pset))})
	tooBig := e.errItem("TooBig", types.Int)
	set := e.tt.ErrorSet([]types.ErrorMember{{Item: tooBig}})
	scopeE := e.tt.Named(e.tt.Get(s.scope).Item, []types.GenericArg{types.TypeArg(e.tt.ErrorSetType(set))})
	e.noFreeze[scopeE] = true
	ofT := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: s.io}}, Result: scopeE})
	ro := e.body(e.fn("RunO", []types.TypeID{s.io}, types.Void, 0))
	ov := ro.local(ofT)
	so := ro.scopeLocal(scopeE)
	ro.assign(place(ov), mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: s.ioScopeOf,
		Args: ro.b.AddGenericArgs(types.TypeArg(e.tt.ErrorSetType(set)))}, Type: ofT}, ro.at(16, 5))
	ro.callTo(mir.Callee{Kind: mir.CallValue, Value: mir.Copy(place(ov))}, place(so), mir.NoLocal, ro.at(17, 8), mir.Copy(place(1)))
	ro.join(so, false, ro.at(18, 3))
	ro.ret(ro.at(18, 3))

	rg := e.body(e.fn("RunG", []types.TypeID{s.io}, types.Void, 0))
	sg := rg.scopeLocal(s.scope)
	rr := rg.local(ref)
	vg := rg.local(types.Void)
	rg.callTo(direct(s.ioScope), place(sg), mir.NoLocal, rg.at(20, 8), mir.Copy(place(1)))
	rg.assign(place(rr), mir.Rvalue{Kind: mir.RvRef, Place: place(sg)}, rg.at(21, 5))
	rg.callTo(direct(s.cancel), place(vg), sg, rg.at(21, 5), mir.Copy(place(rr)))
	rg.join(sg, false, rg.at(22, 3))
	rg.ret(rg.at(22, 3))

	return []*mir.Body{lo.b, lc.b, rc.b, rs.b, ro.b, rg.b}, s.bodiless
}

// fnValueGlobal adds var G: fn(str): int = str.Len to unit u's pack 0: its
// initializer is emitted by finish, after every body.
func fnValueGlobal(e *testEnv, u *Unit) {
	strLen := mustItemE(e, "std.core.str:Len")
	lenT := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: types.Str}}, Result: types.Int})
	v := e.consts.Intern(types.ConstValue{Type: lenT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: strLen}}}})
	g := e.item("G", types.KindGlobal, &types.GlobalDecl{Type: lenT, Value: v, HasValue: true})
	u.Globals = append(u.Globals, Global{Item: g, Pack: 0})
}

func mustItemE(e *testEnv, key string) types.ItemID {
	id, ok := e.items.ByKey(types.KeyFromString(key))
	if !ok {
		panic("no item " + key)
	}
	return id
}

// TestWrapperRows: every wrapper and thunk has its FuncTable row, also
// a wrapper that only a var global's initializer makes (a pack with no
// other use of str.Len), and wrappers emit under --pgo-gen and --pgo=
// with no counters of their own.
func TestWrapperRows(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, mode := range []string{"dev", "pgo-gen", "pgo-use"} {
			t.Run(target.Name+"/"+mode, func(t *testing.T) {
				e := newTestEnv()
				bodies, bodiless := fnValueBodies(e)
				u := unitOf(e, bodies[2:], bodiless...) // no LenOf or LenC: only G names str.Len
				fnValueGlobal(e, u)
				cfg := Config{Target: target}
				switch mode {
				case "pgo-gen":
					cfg = Config{Target: target, Profile: types.Release, PGOGen: true}
				case "pgo-use":
					cfg = fuzzPGOConfig(u, target, []byte{1, 2, 3, 4})
				}
				var b strings.Builder
				if _, err := EmitPack(u, 0, e, cfg, &b); err != nil {
					t.Fatal(err)
				}
				ll := b.String()
				for _, w := range []string{"mk.std.core.str:Len.fn", "mk.std.core.Scope:Cancel.fn", "mk.std.core.Io:Scope.fn",
					"mk.std.core.Io:ScopeOf.fn.1", "mk.std.core.Scope:Cancel.fn.go"} {
					if !strings.Contains(ll, `@"mk.fr.`+w+`" = private`) {
						t.Errorf("no FuncTable row for %s", w)
					}
					if strings.Contains(ll, `@"`+w+`.pgo"`) {
						t.Errorf("a wrapper or thunk %s has PGO counters", w)
					}
				}
				if !strings.Contains(ll, `@mk.main.G = `) || !strings.Contains(ll, `ptr @"mk.std.core.str:Len.fn"`) {
					t.Errorf("the var global does not name the wrapper:\n%s", ll)
				}
				clangC(t, target, t.TempDir(), "pack", ll, "-O2")
			})
		}
	}
}

// TestIntrinsicAsFnValue: the IR of every case has an internal wrapper
// per intrinsic and generic instance, and compiles for every target in dev
// and release.
func TestIntrinsicAsFnValue(t *testing.T) {
	for _, target := range types.Targets() {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(fmt.Sprintf("%s/%d", target.Name, prof), func(t *testing.T) {
				e := newTestEnv()
				bodies, bodiless := fnValueBodies(e)
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, prof)
				for _, w := range []string{`@"mk.std.core.str:Len.fn"(`, `@"mk.std.core.Scope:Cancel.fn"(`,
					`@"mk.std.core.Io:Scope.fn"(`, `@"mk.std.core.Io:ScopeOf.fn.1"(`, `@"mk.std.core.Scope:Cancel.fn.go"(`} {
					if n := defines(ll, w); n != 1 {
						t.Errorf("%d internal definitions of %s", n, w)
					}
				}
				if !strings.Contains(fnText(ll, "mk.main.LenOf"), `ptr @"mk.std.core.str:Len.fn"`) {
					t.Errorf("LenOf does not take the wrapper's address:\n%s", fnText(ll, "mk.main.LenOf"))
				}
				if !strings.Contains(ll, `ptr @"mk.std.core.str:Len.fn"`) {
					t.Error("the const relocation does not name the wrapper")
				}
				clangC(t, target, t.TempDir(), "pack", ll, "-O2")
			})
		}
	}
}

// defines counts the internal definitions in ll whose line names w.
func defines(ll, w string) int {
	n := 0
	for _, line := range strings.Split(ll, "\n") {
		if strings.HasPrefix(line, "define internal ") && strings.Contains(line, w) {
			n++
		}
	}
	return n
}

// wrapperStub is a stub std.task: Enter prints 100 and err_size and records
// the record's address; Join prints 400 and 1 when its scope is the record
// Enter saw (the wrapper wrote the scope in place, never a moved copy);
// SpawnAlloc returns a static block; Spawn runs the thunk at once and
// prints 300; Cancel prints 500 and 1 when its scope is Enter's.
func wrapperStub(target types.Target) string {
	enter := `define void @mk.std.task.Enter(ptr %s, ptr %t, i64 %f, i64 %limit, i8 %stack, i64 %es, ptr %site) nounwind {`
	if target.OS == types.OSWindows {
		enter = `define void @mk.std.task.Enter(ptr %s, ptr %io, i64 %limit, i8 %stack, i64 %es, ptr %site) nounwind {`
	}
	return `
@blk = internal global [64 x i8] zeroinitializer, align 16
@rec = internal global ptr null, align 8

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}

define internal void @h_same(ptr %s) nounwind {
  %r = load ptr, ptr @rec, align 8
  %eq = icmp eq ptr %r, %s
  %e64 = zext i1 %eq to i64
  call void @h_pn(i64 %e64)
  ret void
}

` + enter + `
  call void @h_pn(i64 100)
  call void @h_pn(i64 %es)
  store ptr %s, ptr @rec, align 8
  ret void
}

define ptr @mk.std.task.SpawnAlloc(ptr %s, i64 %size, i64 %align, ptr %site) nounwind {
  ret ptr @blk
}

define void @mk.std.task.Spawn(ptr %s, ptr %thunk, ptr %b) nounwind {
  %r = call i1 %thunk(ptr null, ptr %b, ptr null)
  call void @h_pn(i64 300)
  ret void
}

define void @mk.std.task.Join(ptr %s, i1 zeroext %c) nounwind {
  call void @h_pn(i64 400)
  call void @h_same(ptr %s)
  ret void
}

define void @mk.std.task.Cancel(ptr %s) nounwind {
  call void @h_pn(i64 500)
  call void @h_same(ptr %s)
  ret void
}
`
}

// fnValueDriver calls LenOf, LenC, RunC, RunS, RunO and RunG.
func fnValueDriver(target types.Target) string {
	decl, call := "ptr", "ptr %io"
	if target.OS != types.OSWindows {
		decl, call = "ptr, i64", "ptr null, i64 0"
	}
	io := ""
	if target.OS == types.OSWindows {
		io = "  %io = alloca [16 x i8], align 8\n  store i64 0, ptr %io, align 8\n  %io8 = getelementptr inbounds i8, ptr %io, i64 8\n  store i64 0, ptr %io8, align 8\n"
	}
	s := `
declare i64 @mk.main.LenOf()
declare i64 @mk.main.LenC()
`
	for _, f := range []string{"RunC", "RunS", "RunO", "RunG"} {
		s += "declare void @mk.main." + f + "(" + decl + ")\n"
	}
	s += `
define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
` + io + `  %a = call i64 @mk.main.LenOf()
  call void @h_pn(i64 %a)
  %b = call i64 @mk.main.LenC()
  call void @h_pn(i64 %b)
`
	for _, f := range []string{"RunC", "RunS", "RunO", "RunG"} {
		s += "  call void @mk.main." + f + "(" + call + ")\n"
	}
	return s + `  call void @h_exit(i32 0)
  unreachable
}
`
}

// TestIntrinsicAsFnValueRun runs the bodies natively, -O0 and -O2:
// str.Len through a fn value gives 4 and through a const 5; Scope.Cancel
// through a value cancels the scope Enter linked; Io.Scope and
// Io.ScopeOf[TooBig] through a value write the scope in place (Join sees
// Enter's record; err_size 0 and 16); s.go Scope.Cancel(&s) runs the
// wrapper from the thunk.
func TestIntrinsicAsFnValueRun(t *testing.T) {
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				bodies, bodiless := fnValueBodies(e)
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				objs := []string{
					clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "pack", ll, opt),
					clangC(t, target, dir, "harness", osIR+harnessRT+wrapperStub(target)+fnValueDriver(target), "-O0"),
				}
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
				want := strings.Join([]string{
					"4", "5",
					"100", "0", "500", "1", "400", "1", // RunC
					"100", "0", "400", "1", // RunS
					"100", "16", "400", "1", // RunO
					"100", "0", "500", "1", "300", "400", "1", // RunG
				}, "\n") + "\n"
				if code != 0 || out != want {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want)
				}
			})
		}
	}
}
