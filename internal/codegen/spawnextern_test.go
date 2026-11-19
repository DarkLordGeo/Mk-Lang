// Why: spawned calls of externs differ from direct ones: a variadic extern
// needs its call's own signature, and a C function may need a large stack.
// What: s.go of a variadic extern stores the extra arguments in the block
// and calls the extern with the call's variadic signature, one thunk per
// call signature; s.go of an extern declared outside std runs it through
// SystemStack and mk_call_system from the thunk, as a direct call does.
// How: IR checks, then native runs with a harness, so a C function with a
// large frame survives a small task stack.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// variadicBodies: extern MkRec(n: int, ...) outside std, and
//
//	RunR(io: Io) { scope s := io.Scope() { s.go MkRec(1, 7, 9); s.go MkRec(2, 5) } }
func variadicBodies(e *testEnv) ([]*mir.Body, []types.ItemID) {
	s := newSpawnStd(e)
	rec := externIn(e, "main", "MkRec", []types.TypeID{types.Int}, types.Void)
	e.items.Get(rec).Decl.(*types.FuncDecl).Sig.Variadic = true
	r := e.body(e.fn("RunR", []types.TypeID{s.io}, types.Void, 0))
	sl := r.scopeLocal(s.scope)
	v := r.local(types.Void)
	r.callTo(direct(s.ioScope), place(sl), mir.NoLocal, r.at(2, 8), mir.Copy(place(1)))
	r.callTo(direct(rec), place(v), sl, r.at(3, 5), mir.Imm(types.Int, 1), mir.Imm(types.Int, 7), mir.Imm(types.Int, 9))
	r.callTo(direct(rec), place(v), sl, r.at(4, 5), mir.Imm(types.Int, 2), mir.Imm(types.Int, 5))
	r.join(sl, false, r.at(5, 3))
	r.ret(r.at(5, 3))
	return []*mir.Body{r.b}, append(s.bodiless, rec)
}

func TestSpawnVariadicExtern(t *testing.T) {
	three := regexp.MustCompile(`call void \(i64, \.\.\.\) @MkRec\(i64 [^,]*%v\d+, i64 [^,]*%v\d+, i64 [^,]*%v\d+\)`)
	two := regexp.MustCompile(`call void \(i64, \.\.\.\) @MkRec\(i64 [^,]*%v\d+, i64 [^,]*%v\d+\)`)
	for _, target := range types.Targets() {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := variadicBodies(e)
			ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
			if defines(ll, "@MkRec.go.") != 2 {
				t.Errorf("want two thunks of MkRec, one per call signature:\n%s", ll)
			}
			if !three.MatchString(ll) || !two.MatchString(ll) {
				t.Errorf("the thunks do not call MkRec with the variadic call signature:\n%s", ll)
			}
			clangC(t, target, t.TempDir(), "pack", ll, "-O2")
		})
	}
}

// variadicDriver calls RunR; MkRec (a fixed-arity stand-in: integer variadic
// arguments travel in the same registers) prints n, the next argument, and
// the third when n is 1.
func variadicDriver(target types.Target) string {
	decl, call, io := "ptr", "ptr %io", "  %io = alloca [16 x i8], align 8\n  call void @llvm.memset.p0.i64(ptr %io, i8 0, i64 16, i1 false)\n"
	if target.OS != types.OSWindows {
		decl, call, io = "ptr, i64", "ptr null, i64 0", ""
	}
	return `
declare void @mk.main.RunR(` + decl + `)
declare void @llvm.memset.p0.i64(ptr, i8, i64, i1)

define void @MkRec(i64 %n, i64 %a, i64 %b) nounwind {
  call void @h_pn(i64 %n)
  call void @h_pn(i64 %a)
  %one = icmp eq i64 %n, 1
  br i1 %one, label %third, label %done
third:
  call void @h_pn(i64 %b)
  br label %done
done:
  ret void
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
` + io + `  call void @mk.main.RunR(` + call + `)
  call void @h_exit(i32 0)
  unreachable
}
`
}

func TestSpawnVariadicExternRun(t *testing.T) {
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				bodies, bodiless := variadicBodies(e)
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				objs := []string{
					clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "pack", ll, opt),
					clangC(t, target, dir, "harness", osIR+harnessRT+wrapperStub(target)+variadicDriver(target), "-O0"),
				}
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
				want := strings.Join([]string{"100", "0", "1", "7", "9", "300", "2", "5", "300", "400", "1"}, "\n") + "\n"
				if code != 0 || out != want {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want)
				}
			})
		}
	}
}

// systemStackBodies: extern BigStack(n: int): int outside std, SystemStack and
// SystemStackDone in the build, and
//
//	GoBig(io: Io) { scope s := io.Scope() { s.go BigStack(41) } }
func systemStackBodies(e *testEnv) ([]*mir.Body, []types.ItemID) {
	s := newSpawnStd(e)
	sysStack := e.structIn("std.task", "SysStack", e.tt.RawPtr(types.U8), types.U64, types.U64, types.U64)
	ss := []types.ItemID{e.fnIn("std.task", "", "SystemStack", nil, e.tt.RawPtr(sysStack), false),
		e.fnIn("std.task", "", "SystemStackDone", nil, types.Void, false)}
	big := externIn(e, "main", "BigStack", []types.TypeID{types.Int}, types.Int)
	g := e.body(e.fn("GoBig", []types.TypeID{s.io}, types.Void, 0))
	sl := g.scopeLocal(s.scope)
	v := g.local(types.Int)
	g.callTo(direct(s.ioScope), place(sl), mir.NoLocal, g.at(2, 8), mir.Copy(place(1)))
	g.callTo(direct(big), place(v), sl, g.at(3, 5), mir.Imm(types.Int, 41))
	g.join(sl, false, g.at(4, 3))
	g.ret(g.at(4, 3))
	return []*mir.Body{g.b}, append(append(s.bodiless, ss...), big)
}

func TestSpawnExternOnSystemStack(t *testing.T) {
	for _, target := range types.Targets() {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := systemStackBodies(e)
			ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
			th := fnText(ll, "BigStack.go")
			for _, want := range []string{"@mk.std.task.SystemStack()", "@mk_call_system(ptr @mk.cs.BigStack", "@mk.std.task.SystemStackDone()", "@BigStack("} {
				if !strings.Contains(th, want) {
					t.Errorf("the thunk of an outside-std extern has no %s:\n%s", want, th)
				}
			}
			clangC(t, target, t.TempDir(), "pack", ll, "-O2")
		})
	}
}

// systemStackHarness is sysHarness's scheduler stand-in with GoBig on the task:
// Start switches to a task on a 64 KiB stack, which runs GoBig (its s.go
// thunk runs at once, on the task stack, through wrapperStub's Spawn) and
// prints what BigStack saw; then Start prints 777.
func systemStackHarness(target types.Target) string {
	h := sysHarness(target)
	h = strings.Replace(h, "declare i64 @mk.main.CallBig(i64)", "@seen = external global i64\ndeclare void @mk.main.GoBig("+map[bool]string{true: "ptr", false: "ptr, i64"}[target.OS == types.OSWindows]+")", 1)
	h = strings.Replace(h, `define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}`, "", 1)
	call := "  %io = alloca [16 x i8], align 8\n  store i64 0, ptr %io, align 8\n  %io8 = getelementptr inbounds i8, ptr %io, i64 8\n  store i64 0, ptr %io8, align 8\n  call void @mk.main.GoBig(ptr %io)\n"
	if target.OS != types.OSWindows {
		call = "  call void @mk.main.GoBig(ptr null, i64 0)\n"
	}
	h = strings.Replace(h, "  %r = call i64 @mk.main.CallBig(i64 41)\n", call+"  %r = load i64, ptr @seen, align 8\n", 1)
	h = strings.Replace(h, "  %r0 = call i64 @mk.main.CallBig(i64 1)\n  call void @h_pn(i64 %r0)\n", "", 1)
	return h
}

// systemStackC is the stand-in C library: BigStack(n) touches both ends of a
// 512 KiB frame and records n + 1 in @seen.
const systemStackC = `
@seen = global i64 0, align 8

define i64 @BigStack(i64 %n) nounwind {
  %buf = alloca [524288 x i8], align 16
  store volatile i8 1, ptr %buf, align 16
  %hi = getelementptr inbounds i8, ptr %buf, i64 524287
  store volatile i8 0, ptr %hi, align 1
  %v = load volatile i8, ptr %buf, align 16
  %v64 = zext i8 %v to i64
  %r = add i64 %n, %v64
  store i64 %r, ptr @seen, align 8
  ret i64 %r
}
`

// TestSpawnExternOnSystemStackRun: the thunk's call of BigStack from a
// task on a 64 KiB stack runs on the system stack (SystemStackDone prints
// 900) and BigStack sees 41; without the routing it faults on both systems.
func TestSpawnExternOnSystemStackRun(t *testing.T) {
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				bodies, bodiless := systemStackBodies(e)
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				objs := []string{
					clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "pack", ll, opt),
					clangC(t, target, dir, "harness", osIR+harnessRT+wrapperStub(target)+systemStackHarness(target), "-O0"),
					clangC(t, target, dir, "clib", systemStackC, "-O0"),
				}
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
				want := strings.Join([]string{"100", "0", "900", "300", "400", "1", "42", "777"}, "\n") + "\n"
				if code != 0 || out != want {
					t.Fatalf("exit %d, output %q; want %q", code, out, want)
				}
			})
		}
	}
}
