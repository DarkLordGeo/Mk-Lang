// Why: ordered atomics, frame pointers, coverage and hardware math each
// change the IR, and LLVM must accept each form on every target.
// What: TestUnorderedLoadStore, LoweringsCompile, FramePointerPrologue,
// CoverCounters and HardwareMathIR.
// How: hand-built bodies are emitted and compiled with clang; the native
// runs are std/math's and std/mem's intrinsic test blocks.

package codegen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// TestUnorderedLoadStore: a LoadOrd or StoreOrd of order 5 (Unordered)
// lowers to LLVM's monotonic, never unordered, because LLVM's loop idiom
// turns counted unordered loops into
// __llvm_memcpy_element_unordered_atomic_<n> calls no Mk link provides; and
// the IR compiles.
func TestUnorderedLoadStore(t *testing.T) {
	e := newTestEnv()
	at := e.structIn("std.raw", "Atomic", types.I64)
	ref := e.tt.Ref(at)
	load := e.stdFn("std.raw", "Atomic", "LoadOrd", 1, []types.TypeID{ref, types.U8}, types.I64)
	store := e.stdFn("std.raw", "Atomic", "StoreOrd", 1, []types.TypeID{ref, types.I64, types.U8}, types.Void)
	ga := []types.GenericArg{types.TypeArg(types.I64)}
	f := e.body(e.fn("Un", []types.TypeID{ref}, types.I64, 0))
	f.call(store, ga, types.Void, mir.Copy(place(1)), mir.Imm(types.I64, 9), mir.Imm(types.U8, 5))
	v := f.call(load, ga, types.I64, mir.Copy(place(1)), mir.Imm(types.U8, 5))
	f.assign(place(0), use(mir.Copy(place(v))), f.at(6, 1))
	f.ret(f.at(6, 1))
	ll := emit(t, e, unitOf(e, []*mir.Body{f.b}), types.X86_64Linux, types.Release)
	for _, want := range []string{"store atomic i64 9, ptr", "monotonic, align 8", "load atomic i64, ptr"} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %q in:\n%s", want, fnText(ll, "mk.main.Un"))
		}
	}
	if fn := fnText(ll, "mk.main.Un"); strings.Count(fn, " monotonic") != 2 || strings.Contains(fn, "unordered") {
		t.Errorf("want two monotonic accesses and no unordered one in:\n%s", fn)
	}
}

// TestLoweringsCompile: the lowerings of intrinsic_extra.go
// emit IR that clang compiles at -O0 and -O2 on x86_64 Linux and Windows
// and on aarch64, whose DivWide is the shift-subtract form and Pause
// `yield`; the native runs are std/math's and std/mem's intrinsic test blocks.
func TestLoweringsCompile(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows, types.Aarch64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			p0 := e.tt.Param(0)
			rec := func(a, b string, ta, tb types.TypeID) types.TypeID {
				return e.tt.Record([]types.RecordField{{Name: e.in.Intern(a), Type: ta}, {Name: e.in.Intern(b), Type: tb}})
			}
			ov := rec("overflow", "value", types.Bool, p0)
			add := e.stdFn("std.math", "", "AddOverflow", 1, []types.TypeID{p0, p0}, ov)
			mul := e.stdFn("std.math", "", "MulOverflow", 1, []types.TypeID{p0, p0}, ov)
			wrap := e.stdFn("std.math", "", "WrapI32", 0, []types.TypeID{types.F64}, types.I32)
			sat := e.stdFn("std.math", "", "CastSat", 1, []types.TypeID{types.F64}, p0)
			qr := rec("q", "r", types.U64, types.U64)
			div := e.stdFn("std.math", "", "DivWide", 0, []types.TypeID{types.U64, types.U64, types.U64}, qr)
			cyc := e.stdFn("std.runtime", "", "Cycles", 0, nil, types.U64)
			pause := e.stdFn("std.atomic", "", "Pause", 0, nil, types.Void)
			pu32 := e.tt.RawPtr(types.U32)
			cp := e.stdFn("std.raw", "", "AtomicCopy", 1, []types.TypeID{e.tt.RawPtr(p0), e.tt.RawPtr(p0), types.Int}, types.Void)
			fill := e.stdFn("std.raw", "", "AtomicFill", 1, []types.TypeID{e.tt.RawPtr(p0), p0, types.Int}, types.Void)
			i8 := []types.GenericArg{types.TypeArg(types.I8)}
			u16 := []types.GenericArg{types.TypeArg(types.U16)}
			u32 := []types.GenericArg{types.TypeArg(types.U32)}
			f := e.body(e.fn("Use", []types.TypeID{types.U64, types.F64, pu32}, types.U64, 0))
			f.call(add, i8, e.tt.Record([]types.RecordField{{Name: e.in.Intern("overflow"), Type: types.Bool}, {Name: e.in.Intern("value"), Type: types.I8}}),
				mir.Imm(types.I8, 100), mir.Imm(types.I8, 100))
			f.call(mul, u16, e.tt.Record([]types.RecordField{{Name: e.in.Intern("overflow"), Type: types.Bool}, {Name: e.in.Intern("value"), Type: types.U16}}),
				mir.Imm(types.U16, 300), mir.Imm(types.U16, 300))
			f.call(wrap, nil, types.I32, mir.Copy(place(2)))
			f.call(sat, u16, types.U16, mir.Copy(place(2)))
			f.call(div, nil, qr, mir.Imm(types.U64, 1), mir.Copy(place(1)), mir.Imm(types.U64, 7))
			f.call(cyc, nil, types.U64)
			f.call(pause, nil, types.Void)
			f.call(cp, u32, types.Void, mir.Copy(place(3)), mir.Copy(place(3)), mir.Imm(types.Int, 4))
			f.call(fill, u32, types.Void, mir.Copy(place(3)), mir.Imm(types.U32, 9), mir.Imm(types.Int, 4))
			f.assign(place(0), use(mir.Copy(place(1))), f.at(21, 1))
			f.ret(f.at(21, 1))
			u := unitOf(e, []*mir.Body{f.b})
			ll := emit(t, e, u, target, types.Dev)
			wants := []string{"@llvm.sadd.with.overflow.i8", "@llvm.umul.with.overflow.i16", "@llvm.fptoui.sat.i16.f64", "@llvm.readcyclecounter", "load atomic i32", "store atomic i32"}
			if target.Arch == types.ArchX64 {
				wants = append(wants, "divq", `"pause"`)
			} else {
				wants = append(wants, `"yield"`)
			}
			for _, want := range wants {
				if !strings.Contains(ll, want) {
					t.Errorf("no %q in:\n%s", want, fnText(ll, "mk.main.Use"))
				}
			}
			dir := t.TempDir()
			clangC(t, target, dir, "o0", ll, "-O0")
			clangC(t, target, dir, "o2", ll, "-O2")
		})
	}
}

// TestFramePointerPrologue: with Config.FramePointers every Mk function
// carries "frame-pointer"="non-leaf", so a function that calls sets up rbp
// at -O2 (System V: push rbp; mov rbp, rsp; Windows x64: push rbp, then
// rbp at rsp plus the offset its unwind information records), and a leaf
// does not (it links no chain entry, which hello's 64 KiB budget needs);
// without it neither uses rbp; with Config.Size every function carries
// minsize and optsize; and each setting changes the pack key (PackFacts)
// only when on.
func TestFramePointerPrologue(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			f := e.body(e.fn("Leaf", []types.TypeID{types.I64}, types.I64, 0))
			f.assign(place(0), use(mir.Copy(place(1))), f.at(2, 1))
			f.ret(f.at(2, 1))
			// Caller calls a function defined elsewhere twice, so the first
			// call is no tail call and is never inlined.
			ext := e.fn("Ext", []types.TypeID{types.I64}, types.I64, 0)
			c := e.body(e.fn("Caller", []types.TypeID{types.I64}, types.I64, 0))
			v := c.call(ext, nil, types.I64, mir.Copy(place(1)))
			w := c.call(ext, nil, types.I64, mir.Copy(place(v)))
			c.assign(place(0), use(mir.Copy(place(w))), c.at(4, 1))
			c.ret(c.at(4, 1))
			u := unitOf(e, []*mir.Body{f.b, c.b}, ext)
			on := emitCfg(t, e, u, 0, Config{Target: target, Profile: types.Release, FramePointers: true, Size: true})
			off := emitCfg(t, e, u, 0, Config{Target: target, Profile: types.Release})
			for _, want := range []string{`"frame-pointer"="non-leaf"`, "minsize", "optsize"} {
				if !strings.Contains(on, want) {
					t.Errorf("no %s with the settings on:\n%s", want, fnText(on, "mk.main.Leaf"))
				}
				if strings.Contains(off, want) {
					t.Errorf("%s with the settings off", want)
				}
			}
			if strings.Contains(on, `"frame-pointer"="all"`) {
				t.Errorf(`"frame-pointer"="all" with the settings on`)
			}
			bin := needLLVM(t)
			dir := t.TempDir()
			dis := func(name, ll, fn string) string {
				d := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", clangC(t, target, dir, name, ll, "-O2"))
				// The function's own listing: from its label to the next.
				i := strings.Index(d, "<"+fn+">:")
				if i < 0 {
					t.Fatalf("no %s in:\n%s", fn, d)
				}
				d = d[i+len(fn)+3:]
				if j := strings.Index(d, ">:"); j >= 0 {
					d = d[:j]
				}
				return d
			}
			if d := dis("on", on, "mk.main.Caller"); !strings.Contains(d, "pushq\t%rbp") || target.OS == types.OSLinux && !strings.Contains(d, "movq\t%rsp, %rbp") {
				t.Errorf("frame pointers on, the caller has no rbp prologue:\n%s", d)
			}
			if d := dis("on", on, "mk.main.Leaf"); strings.Contains(d, "%rbp") {
				t.Errorf("frame pointers on (non-leaf), the leaf uses rbp:\n%s", d)
			}
			for _, fn := range []string{"mk.main.Leaf", "mk.main.Caller"} {
				if d := dis("off", off, fn); strings.Contains(d, "%rbp") {
					t.Errorf("frame pointers off, %s uses rbp:\n%s", fn, d)
				}
			}
			var keys []source.Hash
			for _, cfg := range []Config{{Target: target, Profile: types.Release}, {Target: target, Profile: types.Release, FramePointers: true},
				{Target: target, Profile: types.Release, Size: true}} {
				h := source.NewHasher("t")
				if !PackFacts(u, 0, e, cfg, h) {
					t.Fatal("no pack facts")
				}
				keys = append(keys, h.Sum())
			}
			if keys[0] == keys[1] || keys[0] == keys[2] || keys[1] == keys[2] {
				t.Errorf("pack keys %v: each setting must change the key", keys)
			}
		})
	}
}

// TestCoverCounters: with Config.Cover a package function gets one
// counter per MIR block, incremented by a relaxed load and store when the
// block is entered, and a descriptor row; both sit in their own sections,
// tied to the function (ELF mk_cnts and mk_cover with !associated, COFF
// .mkcc$M and .mkcv$M in its comdat), and the objects compile with those
// sections. Without Cover nothing is added.
func TestCoverCounters(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			f := e.body(e.fn("Pick", []types.TypeID{types.Bool}, types.I64, 0))
			yes, no, join := f.newBlock(), f.newBlock(), f.newBlock()
			f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(1)), Cases: f.b.AddCases(mir.SwitchCase{Value: 1, Target: yes}), Default: no, Span: f.at(2, 1)})
			f.cur = yes
			f.assign(place(0), use(mir.Imm(types.I64, 1)), f.at(3, 1))
			f.term(mir.Terminator{Kind: mir.TermGoto, Target: join, Span: f.at(3, 1)})
			f.cur = no
			f.assign(place(0), use(mir.Imm(types.I64, 2)), f.at(5, 1))
			f.term(mir.Terminator{Kind: mir.TermGoto, Target: join, Span: f.at(5, 1)})
			f.cur = join
			f.ret(f.at(6, 1))
			u := unitOf(e, []*mir.Body{f.b})
			on := emitCfg(t, e, u, 0, Config{Target: target, Profile: types.Dev, Cover: true})
			off := emitCfg(t, e, u, 0, Config{Target: target, Profile: types.Dev})
			cnts, rows := `private global [4 x i64] zeroinitializer, section "mk_cnts"`, `section "mk_cover"`
			if target.OS == types.OSWindows {
				// The counters key their own comdat, which holds the row.
				cnts, rows = `internal global [4 x i64] zeroinitializer, section ".mkcc$M", comdat`, `section ".mkcv$M", comdat($mk.main.Pick.cov)`
			}
			for _, want := range []string{"@mk.main.Pick.cov = " + cnts, rows,
				"load atomic i64, ptr", "monotonic, align 8", "@llvm.compiler.used"} {
				if !strings.Contains(on, want) {
					t.Errorf("no %q in:\n%s", want, on)
				}
			}
			if n := strings.Count(fnText(on, "mk.main.Pick"), "store atomic i64"); n != 4 {
				t.Errorf("%d counter stores, want one per block (4)", n)
			}
			if strings.Contains(off, ".cov") {
				t.Error("counters without Config.Cover")
			}
			dir := t.TempDir()
			obj := clangC(t, target, dir, "on", on, "-O2")
			h := runTool(t, tool(needLLVM(t), "llvm-objdump"), "-h", obj)
			secs := []string{"mk_cnts", "mk_cover"}
			if target.OS == types.OSWindows {
				secs = []string{".mkcc", ".mkcv"}
			}
			for _, want := range secs {
				if !strings.Contains(h, want) {
					t.Errorf("no section %s in:\n%s", want, h)
				}
			}
		})
	}
}

// TestHardwareMathIR (hwmath.go): a call of std.math's
// rounding functions and Fma from a function whose features hold the
// instruction is inline (llvm.floor and the rest, then the NaN select),
// on x86_64's hosted baseline (sse4.1; Fma needs fma, so it stays a call)
// and on aarch64, and the IR compiles at -O0 and -O2; the bit-identity is
// std/math's and std/mem's intrinsic test blocks, natively.
func TestHardwareMathIR(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Linux, types.X86_64Windows, types.Aarch64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			var items []types.ItemID
			for _, n := range []string{"Floor", "Ceil", "RoundToZero", "Round", "RoundEven"} {
				items = append(items, e.fnIn("std.math", "", n, []types.TypeID{types.F64}, types.F64, false))
			}
			r32 := e.fnIn("std.math", "", "RoundF32", []types.TypeID{types.F32}, types.F32, false)
			fma := e.fnIn("std.math", "", "Fma", []types.TypeID{types.F64, types.F64, types.F64}, types.F64, false)
			f := e.body(e.fn("Use", []types.TypeID{types.F64, types.F32}, types.F64, 0))
			for _, it := range items {
				f.call(it, nil, types.F64, mir.Copy(place(1)))
			}
			f.call(r32, nil, types.F32, mir.Copy(place(2)))
			uses := append(items, r32)
			if target.Arch == types.ArchArm64 {
				f.call(fma, nil, types.F64, mir.Copy(place(1)), mir.Copy(place(1)), mir.Copy(place(1)))
				uses = append(uses, fma)
			}
			f.assign(place(0), use(mir.Copy(place(1))), f.at(9, 1))
			f.ret(f.at(9, 1))
			u := unitOf(e, []*mir.Body{f.b})
			for _, id := range uses {
				u.Funcs = append(u.Funcs, Func{Symbol: "-", Key: e.items.Get(id).Key.String(), Item: id, Pack: -1})
			}
			ll := emit(t, e, u, target, types.Release)
			body := fnText(ll, "mk.main.Use")
			wants := []string{"@llvm.floor.f64", "@llvm.ceil.f64", "@llvm.trunc.f64", "@llvm.roundeven.f64", "@llvm.copysign.f32", "fcmp uno"}
			if target.Arch == types.ArchArm64 {
				wants = append(wants, "@llvm.fma.f64")
			}
			for _, w := range wants {
				if !strings.Contains(body, w) {
					t.Errorf("no %q in:\n%s", w, body)
				}
			}
			if strings.Contains(body, "call double @mk.std.math") || strings.Contains(body, "call float @mk.std.math") {
				t.Errorf("a rounding call was not inlined:\n%s", body)
			}
			dir := t.TempDir()
			clangC(t, target, dir, "o0", ll, "-O0")
			clangC(t, target, dir, "o2", ll, "-O2")
		})
	}
}
