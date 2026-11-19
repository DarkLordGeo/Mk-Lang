// Why: misuse of the builder must fail loudly here, not as bad IR in clang.
// What: tests of foreign metadata and refs, column
// clamps, invalid IR as an ICE, module flags once, vector overflow intrinsics.
// How: misuse panics with "llvm: "; modules that must be valid are compiled by
// every clang of the smoke test (smoke_test.go), checking exact intrinsic
// types.

package llvm

import (
	"strings"
	"testing"
)

// mustPanicLLVM runs fn and fails unless it panics with an "llvm: " string.
func mustPanicLLVM(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if s, ok := r.(string); !ok || !strings.HasPrefix(s, "llvm: ") {
			t.Errorf("%s: recovered %v, want an llvm: panic", name, r)
		}
	}()
	fn()
}

// smokeAll compiles build's module with clang for every smoke target and
// with WSL's clang for x86_64-linux, as TestLLVMSyntaxSmoke does.
func smokeAll(t *testing.T, build func(tg target) *Module) {
	t.Helper()
	host := findHostClang(t)
	for _, tg := range smokeTriples {
		t.Run("clang/"+tg.name, func(t *testing.T) {
			t.Parallel()
			r := resolve(t, host, tg)
			if out, ok := compile(host, r, build(r)); !ok {
				t.Error(out)
			}
		})
	}
	t.Run("wsl-clang/x86_64-linux", func(t *testing.T) {
		t.Parallel()
		w := findWSLClang(t)
		r := resolve(t, w, smokeTriples[1])
		if out, ok := compile(w, r, build(r)); !ok {
			t.Error(out)
		}
	})
}

// TestMetadataOfAnotherModule: an MD of one module used in another
// panics, as an attachment, as a function's !dbg or !prof, as a block's
// location and as an operand of a node, a tuple or named metadata.
func TestMetadataOfAnotherModule(t *testing.T) {
	t.Parallel()
	m1 := NewModule("m1", "", "")
	i8 := m1.I(8)
	r := m1.Metadata().Range(i8, RangePairs(8, []uint64{0, 1}))
	file1 := m1.Metadata().DIFile("a.mk", ".")
	sp1 := m1.Metadata().DISubprogram("F", "F", file1, 1, m1.Metadata().DICompileUnit(file1, false), false)

	m2 := NewModule("m2", "", "")
	j8 := m2.I(8)
	for i := 0; i < 8; i++ { // m2 has a node of every number m1 has
		m2.Metadata().Range(j8, []int64{0, int64(i + 1)})
	}
	f := m2.AddFunction("loadbool", FuncSig{Ret: j8, Params: []Type{m2.Ptr()}})
	b := f.NewBlock("b0")
	mustPanicLLVM(t, "Load !range of m1", func() { b.Load(j8, f.Param(0), 1, Range(r)) })
	mustPanicLLVM(t, "!dbg of m1", func() { b.Load(j8, f.Param(0), 1, Dbg(m1.Metadata().DILocation(1, 1, sp1))) })
	mustPanicLLVM(t, "SetDbg of m1", func() { f.SetDbg(sp1) })
	mustPanicLLVM(t, "SetProf of m1", func() { f.SetProf(m1.Metadata().EntryCount(3)) })
	mustPanicLLVM(t, "SetDebugLoc of m1", func() { b.SetDebugLoc(sp1) })
	mustPanicLLVM(t, "DILocation scope of m1", func() { m2.Metadata().DILocation(1, 1, sp1) })
	mustPanicLLVM(t, "Tuple ref of m1", func() { m2.Metadata().Tuple(MDRef(r)) })
	mustPanicLLVM(t, "Tuple value of m1", func() { m2.Metadata().Tuple(MDVal(m1.ConstInt(i8, 1))) })
	mustPanicLLVM(t, "Named of m1", func() { m2.Metadata().Named("mk.x", r) })
	mustPanicLLVM(t, "Range type of m1", func() { m2.Metadata().Range(i8, []int64{0, 2}) })

	// The zero MD still clears, and m2's own nodes are accepted.
	b.SetDebugLoc(MD{})
	f.SetDbg(MD{})
	own := m2.Metadata().Range(j8, RangePairs(8, []uint64{0, 1}))
	v := b.Load(j8, f.Param(0), 1, Range(own))
	b.Ret(&v)
	if !strings.Contains(m2.String(), "!range "+own.ref()) {
		t.Errorf("m2's own !range missing:\n%s", m2)
	}
}

// TestColumnClamp: a column above 65535 prints as 0 and the module
// compiles; a column at the limit is kept.
func TestColumnClamp(t *testing.T) {
	m := NewModule("t", "", "")
	sp := m.Metadata().DistinctNode("DISubprogram", FStr("name", "F"))
	for _, c := range []struct {
		line, col int
		want      string
	}{
		{1, 70000, "line: 1, column: 0,"},
		{2, MaxColumn, "line: 2, column: 65535,"},
		{3, -4, "line: 3, column: 0,"},
		{-1, 5, "line: 0, column: 5,"},
		{1 << 40, 5, "line: 4294967295, column: 5,"},
	} {
		m.Metadata().DILocation(c.line, c.col, sp)
		mt := m.Metadata()
		if text := string(mt.appendNode(nil, len(mt.nodes)-1)); !strings.Contains(text, c.want) {
			t.Errorf("DILocation(%d, %d) = %s, want %s", c.line, c.col, text, c.want)
		}
	}
	smokeAll(t, func(tg target) *Module {
		m := NewModule("r02.mk", tg.triple, tg.layout)
		mt := m.Metadata()
		file := mt.DIFile("r02.mk", ".")
		cu := mt.DICompileUnit(file, false)
		mt.DebugFlags(tg.os == "windows")
		sp := mt.DISubprogram("F", "mk.r02.F", file, 1, cu, false)
		f := m.AddFunction("mk.r02.F", FuncSig{Ret: m.Void(), FnAttrs: Attrs{}.Flag("nounwind")})
		f.SetDbg(sp)
		b := f.NewBlock("b0")
		b.Alloca(m.I(64), 8, Dbg(mt.DILocation(1, 65535, sp)))
		b.Ret(nil, Dbg(mt.DILocation(1, 70000, sp)))
		return m
	})
}

// TestRefOfAnotherModule: the address of a function or global of
// another module panics instead of naming an undefined symbol.
func TestRefOfAnotherModule(t *testing.T) {
	t.Parallel()
	m1 := NewModule("m1", "", "")
	f1 := m1.Declare("only.in.m1", FuncSig{Ret: m1.Void()})
	g1 := m1.AddGlobal("g.only.in.m1", GlobalAttrs{Type: m1.I(8), Init: m1.ConstInt(m1.I(8), 0)})
	m2 := NewModule("m2", "", "")
	f := m2.AddFunction("f", FuncSig{Ret: m2.Void()})
	b := f.NewBlock("b0")
	mustPanicLLVM(t, "FuncRef", func() { m2.FuncRef(f1) })
	mustPanicLLVM(t, "GlobalRef", func() { m2.GlobalRef(g1) })
	mustPanicLLVM(t, "CallFunc", func() { b.CallFunc(f1, nil) })
	if m1.FuncRef(f1).Ref() != "@only.in.m1" || m1.GlobalRef(g1).Ref() != "@g.only.in.m1" {
		t.Error("own refs changed")
	}
}

// TestInvalidIRIsICE: casts, atomic orderings, compares, switch
// cases and branches that LLVM rejects panic; the valid neighbours do not.
func TestInvalidIRIsICE(t *testing.T) {
	r04Module(goldenTarget, func(name string, fn func()) { mustPanicLLVM(t, name, fn) })
	smokeAll(t, func(tg target) *Module { return r04Module(tg, nil) })
}

// r04Module builds the valid neighbours of the invalid forms that
// TestInvalidIRIsICE tries; with a non-nil mustPanic it first tries
// every invalid form on the same block.
func r04Module(tg target, mustPanic func(name string, fn func())) *Module {
	m := NewModule("r04.mk", tg.triple, tg.layout)
	i1, i8, i32, i64, p, f32, f64 := m.I(1), m.I(8), m.I(32), m.I(64), m.Ptr(), m.Float(), m.Double()
	v2f64, v2i32, v4i32 := m.Vector(2, f64), m.Vector(2, i32), m.Vector(4, i32)
	f := m.AddFunction("f", FuncSig{Ret: m.Void(), Params: []Type{i8, p, i64, v2f64, v2i32, f64, i32, i1}})
	entry := f.NewBlock("entry")
	b := f.NewBlock("b1")
	x8, ptr, x64, vd, vi, d, x32, c := f.Param(0), f.Param(1), f.Param(2), f.Param(3), f.Param(4), f.Param(5),
		f.Param(6), f.Param(7)

	for _, bad := range []struct {
		name string
		fn   func()
	}{
		{"trunc i8 to i64", func() { b.Cast(Trunc, x8, i64) }},
		{"trunc i8 to i8", func() { b.Cast(Trunc, x8, i8) }},
		{"zext i64 to i8", func() { b.Cast(ZExt, x64, i8) }},
		{"sext f64 to i64", func() { b.Cast(SExt, d, i64) }},
		{"fptrunc f32 to f64", func() { b.Cast(FPTrunc, m.ConstF32(0), f64) }},
		{"fpext f64 to f32", func() { b.Cast(FPExt, d, f32) }},
		{"fptosi i64 to i32", func() { b.Cast(FPToSI, x64, i32) }},
		{"sitofp f64 to f32", func() { b.Cast(SIToFP, d, f32) }},
		{"ptrtoint i64", func() { b.Cast(PtrToInt, x64, i64) }},
		{"inttoptr ptr", func() { b.Cast(IntToPtr, ptr, p) }},
		{"bitcast ptr to i64", func() { b.Cast(BitCast, ptr, i64) }},
		{"bitcast i64 to ptr", func() { b.Cast(BitCast, x64, p) }},
		{"bitcast i32 to i64", func() { b.Cast(BitCast, x32, i64) }},
		{"zext lanes", func() { b.Cast(ZExt, vi, m.Vector(4, i64)) }},
		{"atomicrmw unordered", func() { b.AtomicRMW(RMWAdd, ptr, x64, Unordered, 8) }},
		{"atomicrmw add f64", func() { b.AtomicRMW(RMWAdd, ptr, d, SeqCst, 8) }},
		{"atomicrmw fadd i64", func() { b.AtomicRMW(RMWFAdd, ptr, x64, SeqCst, 8) }},
		{"atomicrmw i1", func() { b.AtomicRMW(RMWXchg, ptr, c, SeqCst, 1) }},
		{"atomic load release", func() { b.AtomicLoad(i64, ptr, Release, 8) }},
		{"atomic load acq_rel", func() { b.AtomicLoad(i64, ptr, AcqRel, 8) }},
		{"atomic load no align", func() { b.AtomicLoad(i64, ptr, SeqCst, 0) }},
		{"atomic store acquire", func() { b.AtomicStore(x64, ptr, Acquire, 8) }},
		{"atomic store acq_rel", func() { b.AtomicStore(x64, ptr, AcqRel, 8) }},
		{"cmpxchg unordered", func() { b.CmpXchg(ptr, x64, x64, Unordered, Monotonic, 8) }},
		{"cmpxchg fail release", func() { b.CmpXchg(ptr, x64, x64, SeqCst, Release, 8) }},
		{"cmpxchg fail acq_rel", func() { b.CmpXchg(ptr, x64, x64, SeqCst, AcqRel, 8) }},
		{"cmpxchg f64", func() { b.CmpXchg(ptr, d, d, SeqCst, SeqCst, 8) }},
		{"fence monotonic", func() { b.Fence(Monotonic) }},
		{"fence unordered", func() { b.Fence(Unordered) }},
		{"ordering out of range", func() { b.Fence(SeqCst + 1) }},
		{"icmp <2 x double>", func() { b.ICmp(IEQ, vd, vd) }},
		{"icmp double", func() { b.ICmp(IEQ, d, d) }},
		{"switch duplicate", func() {
			b.Switch(x32, b, []Case{{m.ConstInt(i32, 1), b}, {m.ConstSInt(i32, 1), b}})
		}},
		{"switch non-constant case", func() { b.Switch(x32, b, []Case{{x32, b}}) }},
		{"br to entry", func() { b.Br(entry) }},
		{"condbr to entry", func() { b.CondBr(c, b, entry) }},
		{"switch default entry", func() { b.Switch(x32, entry, nil) }},
		{"switch case entry", func() { b.Switch(x32, b, []Case{{m.ConstInt(i32, 0), entry}}) }},
	} {
		if mustPanic != nil {
			mustPanic(bad.name, bad.fn)
		}
	}
	if mustPanic != nil && (b.Terminated() || len(b.text) != 0) {
		mustPanic("a refused instruction was emitted", func() {})
	}

	// Long switches use the map path.
	var many []Case
	for i := 0; i < 40; i++ {
		many = append(many, Case{m.ConstInt(i32, uint64(i)), b})
	}
	if mustPanic != nil {
		mustPanic("long switch duplicate", func() {
			b.Switch(x32, b, append(many[:len(many):len(many)], Case{m.ConstInt(i32, 7), b}))
		})
	}

	// The valid neighbours.
	b.Cast(BitCast, ptr, p)
	b.Cast(BitCast, vi, i64)
	b.Cast(BitCast, d, i64)
	b.Cast(ZExt, vi, m.Vector(2, i64))
	b.Cast(SIToFP, vi, v2f64)
	b.ICmp(IEQ, vi, vi)
	b.ICmp(IULT, ptr, ptr)
	b.AtomicRMW(RMWXchg, ptr, ptr, Monotonic, 8)
	b.AtomicRMW(RMWXchg, ptr, d, Monotonic, 8)
	b.CmpXchg(ptr, ptr, ptr, Monotonic, Monotonic, 8)
	b.CmpXchg(ptr, x64, x64, Release, Monotonic, 8)
	b.AtomicLoad(i64, ptr, Unordered, 8)
	b.AtomicStore(d, ptr, Unordered, 8)
	b.ICmp(IEQ, m.ZeroInit(v4i32), m.ZeroInit(v4i32))
	entry.Br(b)
	b.Switch(x32, b, many)
	return m
}

// TestModuleFlagsOnce: DebugFlags twice prints each flag once, the
// module compiles, and one key with two values panics.
func TestModuleFlagsOnce(t *testing.T) {
	m := NewModule("t", "", "")
	m.Metadata().DebugFlags(false)
	m.Metadata().DebugFlags(false)
	m.Metadata().ModuleFlag(FlagMax, "Dwarf Version", 4)
	if text := m.String(); !strings.Contains(text, "!llvm.module.flags = !{!0, !1}\n") {
		t.Errorf("module flags repeated:\n%s", text)
	}
	mustPanicLLVM(t, "same key, other value", func() { m.Metadata().ModuleFlag(FlagMax, "Dwarf Version", 5) })
	mustPanicLLVM(t, "same key, other behaviour", func() { m.Metadata().ModuleFlag(FlagWarning, "Dwarf Version", 4) })
	smokeAll(t, func(tg target) *Module {
		m := NewModule("r05.mk", tg.triple, tg.layout)
		mt := m.Metadata()
		file := mt.DIFile("r05.mk", ".")
		cu := mt.DICompileUnit(file, false)
		mt.DebugFlags(tg.os == "windows")
		mt.DebugFlags(tg.os == "windows")
		f := m.AddFunction("mk.r05.F", FuncSig{Ret: m.Void(), FnAttrs: Attrs{}.Flag("nounwind")})
		sp := mt.DISubprogram("F", "mk.r05.F", file, 1, cu, false)
		f.SetDbg(sp)
		f.NewBlock("b0").Ret(nil, Dbg(mt.DILocation(1, 1, sp)))
		return m
	})
}

// TestVectorOverflow: a vector overload of *.with.overflow has a
// per-lane i1 vector flag, and a call to each family compiles.
func TestVectorOverflow(t *testing.T) {
	m := NewModule("t", "", "")
	v4 := m.Vector(4, m.I(32))
	if got, want := m.IntrinsicFn("sadd.with.overflow", v4).Type().String(),
		"{ <4 x i32>, <4 x i1> } (<4 x i32>, <4 x i32>)"; got != want {
		t.Errorf("sadd.with.overflow.v4i32 type %s, want %s", got, want)
	}
	if got, want := m.IntrinsicFn("sadd.with.overflow", m.I(32)).Type().String(),
		"{ i32, i1 } (i32, i32)"; got != want {
		t.Errorf("sadd.with.overflow.i32 type %s, want %s", got, want)
	}
	smokeAll(t, func(tg target) *Module {
		m := NewModule("r06.mk", tg.triple, tg.layout)
		v4 := m.Vector(4, m.I(32))
		f := m.AddFunction("mk.r06.F", FuncSig{Ret: m.Vector(4, m.I(1)), Params: []Type{v4, v4},
			FnAttrs: Attrs{}.Flag("nounwind")})
		b := f.NewBlock("b0")
		var acc Value
		for _, name := range []string{"sadd.with.overflow", "uadd.with.overflow", "ssub.with.overflow",
			"usub.with.overflow", "smul.with.overflow", "umul.with.overflow"} {
			r := b.CallFunc(m.IntrinsicFn(name, v4), []Value{f.Param(0), f.Param(1)})
			of := b.ExtractValue(r, 1)
			if acc.IsZero() {
				acc = of
			} else {
				acc = b.Bin(Or, acc, of, 0)
			}
		}
		b.Ret(&acc)
		return m
	})
}
