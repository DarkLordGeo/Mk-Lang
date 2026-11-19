// Why: a C function may need far more stack than a task has, so externs
// declared outside std run on the system stack.
// What: the sysstack golden, TestSystemStackRouting (which externs go
// through SystemStack and mk_call_system; variadic calls name the variadic
// type) and TestSystemStackTIB (an extern using 512 KiB of stack called
// from a task on a 64 KiB stack).
// How: on Windows the main thread's stack grows through its guard page only
// when NT_TIB names it, so mk_call_system's swap is what makes the call work.

package codegen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func init() {
	emitCases = append(emitCases, emitCase{"sysstack", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		b, bl := sysBodies(e, true)
		return b, bl
	}})
}

// sysBodies:
//
//	extern BigStack(n: int): int                    (outside std: system stack)
//	extern Printf(f: *u8, ...): i32                 (variadic, outside std)
//	extern std.sys.Raw(n: int): int                 (a std extern: called directly)
//	CallBig(n: int): int { return BigStack(n) }
//	CallVar(f: *u8): i32 { return Printf(f, 1, 2.5) }
//	CallStd(n: int): int { return std.sys.Raw(n) }
func sysBodies(e *testEnv, withSS bool) ([]*mir.Body, []types.ItemID) {
	ext := func(id types.ItemID) types.ItemID {
		fd := e.items.Get(id).Decl.(*types.FuncDecl)
		fd.Sig.Extern, fd.Bodiless = true, true
		return id
	}
	sysStack := e.structIn("std.task", "SysStack", e.tt.RawPtr(types.U8), types.U64, types.U64, types.U64)
	var bodiless []types.ItemID
	if withSS {
		bodiless = append(bodiless, e.fnIn("std.task", "", "SystemStack", nil, e.tt.RawPtr(sysStack), false),
			e.fnIn("std.task", "", "SystemStackDone", nil, types.Void, false))
	}
	big := ext(e.fn("BigStack", []types.TypeID{types.Int}, types.Int, 0))
	pf := ext(e.fn("Printf", []types.TypeID{e.tt.RawPtr(types.U8)}, types.I32, 0))
	e.items.Get(pf).Decl.(*types.FuncDecl).Sig.Variadic = true
	raw := ext(e.fnIn("std.sys", "", "Raw", []types.TypeID{types.Int}, types.Int, false))

	cb := e.body(e.fn("CallBig", []types.TypeID{types.Int}, types.Int, 0))
	cb.callTo(direct(big), place(0), mir.NoLocal, cb.at(2, 10), mir.Copy(place(1)))
	cb.ret(cb.at(2, 3))
	cv := e.body(e.fn("CallVar", []types.TypeID{e.tt.RawPtr(types.U8)}, types.I32, 0))
	cv.callTo(direct(pf), place(0), mir.NoLocal, cv.at(4, 10), mir.Copy(place(1)), mir.Imm(types.Int, 1),
		mir.Const(types.F64, e.floatConst(types.F64, 0x4004000000000000)))
	cv.ret(cv.at(4, 3))
	cs := e.body(e.fn("CallStd", []types.TypeID{types.Int}, types.Int, 0))
	cs.callTo(direct(raw), place(0), mir.NoLocal, cs.at(6, 10), mir.Copy(place(1)))
	cs.ret(cs.at(6, 3))
	return []*mir.Body{cb.b, cv.b, cs.b}, append(bodiless, big, pf, raw)
}

// TestSystemStackRouting: only externs declared outside std go through
// SystemStack and mk_call_system; a variadic call names the variadic type;
// without std.task.SystemStack in the build every extern is called directly.
func TestSystemStackRouting(t *testing.T) {
	e := newTestEnv()
	bodies, bodiless := sysBodies(e, true)
	ll := emit(t, e, unitOf(e, bodies, bodiless...), types.X86_64Windows, types.Dev)
	for _, f := range []string{"mk.main.CallBig", "mk.main.CallVar"} {
		txt := fnText(ll, f)
		if !strings.Contains(txt, "@mk.std.task.SystemStack()") || !strings.Contains(txt, "@mk_call_system(") ||
			!strings.Contains(txt, "@mk.std.task.SystemStackDone()") {
			t.Errorf("%s does not go through the system stack:\n%s", f, txt)
		}
	}
	if txt := fnText(ll, "mk.main.CallStd"); strings.Contains(txt, "SystemStack") || !strings.Contains(txt, "@Raw(") {
		t.Errorf("a std extern is not called directly:\n%s", txt)
	}
	if !strings.Contains(ll, "call noundef i32 (ptr, ...) @Printf(ptr noundef") {
		t.Errorf("no variadic call of Printf:\n%s", fnText(ll, "mk.main.CallVar"))
	}
	if !strings.Contains(ll, "define internal void @mk.cs.BigStack(ptr noundef") {
		t.Error("no adapter for BigStack")
	}
	// Without SystemStack: direct.
	e2 := newTestEnv()
	b2, bl2 := sysBodies(e2, false)
	ll2 := emit(t, e2, unitOf(e2, b2, bl2...), types.X86_64Linux, types.Dev)
	if strings.Contains(ll2, "mk_call_system") {
		t.Error("an extern goes through mk_call_system without std.task.SystemStack")
	}
}

// sysHarness is TestSystemStackTIB's Start: CallBig(1) from the main stack
// (SystemStack null: direct), then a task on a 64 KiB stack calls
// CallBig(41) (SystemStack: the main stack below its saved frame, with its
// NT_TIB words), prints, and switches back.
func sysHarness(target types.Target) string {
	tib := ""
	if target.OS == types.OSWindows {
		tib = `
  store ptr getelementptr inbounds (i8, ptr @taskStack, i64 65536), ptr getelementptr inbounds (i8, ptr @taskCtx, i64 8), align 8
  store ptr @taskStack, ptr getelementptr inbounds (i8, ptr @taskCtx, i64 16), align 8
  store ptr @taskStack, ptr getelementptr inbounds (i8, ptr @taskCtx, i64 24), align 8
`
	}
	return `
@mainCtx = internal global [8 x i64] zeroinitializer, align 16
@taskCtx = internal global [8 x i64] zeroinitializer, align 16
@taskStack = internal global [65536 x i8] zeroinitializer, align 16
@ss = internal global [4 x i64] zeroinitializer, align 16
@onTask = internal global i32 0, align 4

declare i64 @mk.main.CallBig(i64)
declare void @mk_switch(ptr, ptr) nounwind
declare void @"mk.support.InitContext"(ptr, ptr, ptr, ptr) nounwind

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}

define ptr @mk.std.task.SystemStack() nounwind {
  %on = load i32, ptr @onTask, align 4
  %t = icmp ne i32 %on, 0
  br i1 %t, label %task, label %none
none:
  ret ptr null
task:
  %sp = load i64, ptr @mainCtx, align 8
  %sp1 = sub i64 %sp, 256
  %sp2 = and i64 %sp1, -16
  store i64 %sp2, ptr @ss, align 8
  %w1 = load i64, ptr getelementptr inbounds (i8, ptr @mainCtx, i64 8), align 8
  store i64 %w1, ptr getelementptr inbounds (i8, ptr @ss, i64 8), align 8
  %w2 = load i64, ptr getelementptr inbounds (i8, ptr @mainCtx, i64 16), align 8
  store i64 %w2, ptr getelementptr inbounds (i8, ptr @ss, i64 16), align 8
  %w3 = load i64, ptr getelementptr inbounds (i8, ptr @mainCtx, i64 24), align 8
  store i64 %w3, ptr getelementptr inbounds (i8, ptr @ss, i64 24), align 8
  ret ptr @ss
}

define void @mk.std.task.SystemStackDone() nounwind {
  call void @h_pn(i64 900)
  ret void
}

define internal void @task_entry(ptr %arg) nounwind {
  store i32 1, ptr @onTask, align 4
  %r = call i64 @mk.main.CallBig(i64 41)
  call void @h_pn(i64 %r)
  store i32 0, ptr @onTask, align 4
  call void @mk_switch(ptr @taskCtx, ptr @mainCtx)
  unreachable
}

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  %r0 = call i64 @mk.main.CallBig(i64 1)
  call void @h_pn(i64 %r0)
  call void @"mk.support.InitContext"(ptr @taskCtx, ptr getelementptr inbounds (i8, ptr @taskStack, i64 65536), ptr @task_entry, ptr null)
` + tib + `
  call void @mk_switch(ptr @mainCtx, ptr @taskCtx)
  call void @h_pn(i64 777)
  call void @h_exit(i32 0)
  unreachable
}
`
}

// bigStackC is the stand-in C library: BigStack(n) touches both ends of a
// 512 KiB frame and returns n + 1.
const bigStackC = `
define i64 @BigStack(i64 %n) nounwind {
  %buf = alloca [524288 x i8], align 16
  store volatile i8 1, ptr %buf, align 16
  %hi = getelementptr inbounds i8, ptr %buf, i64 524287
  store volatile i8 0, ptr %hi, align 1
  %v = load volatile i8, ptr %buf, align 16
  %v64 = zext i8 %v to i64
  %r = add i64 %n, %v64
  ret i64 %r
}

define i32 @Printf(ptr %f, ...) nounwind {
  ret i32 0
}

define i64 @Raw(i64 %n) nounwind {
  ret i64 %n
}
`

func TestSystemStackTIB(t *testing.T) {
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				bodies, bodiless := sysBodies(e, true)
				ll := emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
				dir := t.TempDir()
				osIR, kernel := harnessOS(target)
				objs := []string{
					clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
					clangC(t, target, dir, "pack", ll, opt),
					clangC(t, target, dir, "harness", osIR+harnessRT+sysHarness(target), "-O0"),
					clangC(t, target, dir, "clib", bigStackC, "-O0"),
				}
				out, code := runExe(t, target, linkExe(t, target, dir, objs, linkOpts{kernel: kernel}), "")
				if code != 0 || out != "2\n900\n42\n777\n" {
					t.Fatalf("exit %d, output %q; want 2, 900, 42, 777", code, out)
				}
			})
		}
	}
}
