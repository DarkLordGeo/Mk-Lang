// Why: inline asm, naked functions and section globals reach LLVM in forms
// that are easy to get wrong: a lost clobber, or an asm LLVM deletes.
// What: IR tests of the asm call's constraints, options and attributes, naked
// functions, section and extern globals, general-regs-only builds and
// simd.Cpu's cache; TestCpuidNative runs testdata/asm/hosted natively.
// How: hand-built MIR bodies are emitted and the IR text is checked; each
// module is also compiled by clang at -O2 on both hosted targets.

package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// asmBody appends a StAsm to f's current block.
func (f *fnb) asmBody(a mir.Asm, sp mir.SpanID) {
	a.Span = sp
	id := f.b.AddAsm(a)
	blk := &f.b.Blocks[f.cur]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAsm, Asm: id, Span: sp})
}

// TestAsmIR: outputs then inputs then clobbers, an out early-clobbered,
// an inout's input tied to its output, a discarded explicit output as a
// clobber, clobber_abi less the registers written, the flags and memory
// clobbers, the template's ${n:m}, and the call's flags and attributes.
func TestAsmIR(t *testing.T) {
	for _, tc := range []struct {
		target types.Target
		abi    string // a register only that target's clobber_abi holds
	}{{types.X86_64Linux, "={rsi}"}, {types.X86_64Windows, "={r8}"}} {
		t.Run(tc.target.Name, func(t *testing.T) {
			e := newTestEnv()
			f := e.body(e.fn("A", []types.TypeID{types.U64, types.F64}, types.U64, 0))
			r := f.local(types.U64)
			c := f.local(types.U32)
			f.assign(place(c), use(mir.Imm(types.U32, 0)), f.at(2, 1))
			seven := e.intConst(types.I64, 7)
			f.asmBody(mir.Asm{Template: "mov {0:r}, {2}\nmovq {2}, {3}\nnop # {{x}}", ClobberABI: "C", Operands: []mir.AsmOperand{
				{Kind: mir.AsmOut, Reg: "reg", Out: place(r), Type: types.U64},
				{Kind: mir.AsmInOut, Reg: "eax", Explicit: true, In: mir.Copy(place(c)), Out: place(c), Type: types.U32},
				{Kind: mir.AsmIn, Reg: "reg", In: mir.Copy(place(1)), Type: types.U64},
				{Kind: mir.AsmIn, Reg: "xmm_reg", In: mir.Copy(place(2)), Type: types.F64},
				{Kind: mir.AsmConst, Const: seven, Type: types.I64},
				{Kind: mir.AsmOut, Reg: "rdx", Explicit: true, Out: mir.Place{Local: mir.NoLocal}, Type: types.U64},
			}}, f.at(3, 1))
			f.assign(place(0), use(mir.Copy(place(r))), f.at(4, 1))
			f.ret(f.at(4, 1))
			ll := emit(t, e, unitOf(e, []*mir.Body{f.b}), tc.target, types.Release)
			text := fnText(ll, "mk.main.A")
			for _, want := range []string{
				`asm sideeffect alignstack inteldialect "mov ${0:q}, ${`, `\0Anop # {x}", "=&r,=&{eax},={rcx},`,
				tc.abi, ",1,r,x,~{rdx},", "~{dirflag},~{fpsr},~{flags},~{memory}\"(i32 ",
			} {
				if !strings.Contains(text, want) && !strings.Contains(ll, want) {
					t.Errorf("no %q in:\n%s", want, text)
				}
			}
			if strings.Contains(text, "~{rax}") || strings.Contains(text, "={rax}") {
				t.Errorf("clobber_abi clobbers rax, which an output (eax) writes:\n%s", text)
			}
			clangC(t, tc.target, t.TempDir(), "asm", ll, "-O2")
		})
	}
}

// TestAsmOptionsIR: pure drops sideeffect, nostack alignstack, att
// inteldialect; a pure nomem asm is memory(none) with no memory clobber, a
// non-pure readonly one memory(read, inaccessiblemem: readwrite) and a
// non-pure nomem one memory(inaccessiblemem: readwrite), so LLVM keeps it;
// preserves_flags drops the flags clobbers; noreturn adds the
// attribute, and the block's unreachable follows.
func TestAsmOptionsIR(t *testing.T) {
	e := newTestEnv()
	f := e.body(e.fn("P", []types.TypeID{types.U64}, types.U64, 0))
	r := f.local(types.U64)
	f.asmBody(mir.Asm{Template: "movq {1}, {0}", Options: mir.AsmPure | mir.AsmNomem | mir.AsmNostack | mir.AsmAtt | mir.AsmPreservesFlags,
		Operands: []mir.AsmOperand{
			{Kind: mir.AsmLateOut, Reg: "reg", Out: place(r), Type: types.U64},
			{Kind: mir.AsmIn, Reg: "reg", In: mir.Copy(place(1)), Type: types.U64},
		}}, f.at(2, 1))
	f.asmBody(mir.Asm{Template: "mov {0}, [{0}]", Options: mir.AsmReadonly | mir.AsmNostack,
		Operands: []mir.AsmOperand{{Kind: mir.AsmInLateOut, Reg: "reg", In: mir.Copy(place(r)), Out: place(r), Type: types.U64}}}, f.at(3, 1))
	f.assign(place(0), use(mir.Copy(place(r))), f.at(4, 1))
	f.ret(f.at(4, 1))
	g := e.body(e.fn("H", nil, types.Void, 0))
	g.asmBody(mir.Asm{Template: "hlt", Options: mir.AsmNoreturn | mir.AsmNomem | mir.AsmNostack}, g.at(6, 1))
	g.term(mir.Terminator{Kind: mir.TermUnreachable, Span: g.at(6, 1)})
	ll := emit(t, e, unitOf(e, []*mir.Body{f.b, g.b}), types.X86_64Linux, types.Release)
	p, h := fnText(ll, "mk.main.P"), fnText(ll, "mk.main.H")
	for _, want := range []string{`asm "movq ${1}, ${0}", "=r,r"(i64`, `memory(none)`, `asm sideeffect inteldialect "mov ${0}, [${0}]", "=r,0,~{dirflag},~{fpsr},~{flags}"`, `memory(read, inaccessiblemem: readwrite)`} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in:\n%s", want, p)
		}
	}
	if !strings.Contains(h, `asm sideeffect inteldialect "hlt", "~{dirflag},~{fpsr},~{flags}"() nounwind memory(inaccessiblemem: readwrite) noreturn`) || !strings.Contains(h, "unreachable") {
		t.Errorf("the noreturn asm:\n%s", h)
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "opts", ll, "-O2")
}

// TestInputOnlyAsmSurvives: an asm with no output and nomem or readonly
// but not pure (the kernel's `out dx, al`, `lidt [{0}]` and `cli; hlt`)
// stays in the object at clang -O0 and -O2. With memory(none) or
// memory(read) LLVM deleted all three.
func TestInputOnlyAsmSurvives(t *testing.T) {
	e := newTestEnv()
	f := e.body(e.fn("Out", []types.TypeID{types.U16, types.U8, types.U64}, types.Void, 0))
	f.asmBody(mir.Asm{Template: "out dx, al", Options: mir.AsmNomem | mir.AsmNostack | mir.AsmPreservesFlags,
		Operands: []mir.AsmOperand{
			{Kind: mir.AsmIn, Reg: "dx", Explicit: true, In: mir.Copy(place(1)), Type: types.U16},
			{Kind: mir.AsmIn, Reg: "al", Explicit: true, In: mir.Copy(place(2)), Type: types.U8},
		}}, f.at(2, 1))
	f.asmBody(mir.Asm{Template: "lidt [{0}]", Options: mir.AsmReadonly | mir.AsmNostack | mir.AsmPreservesFlags,
		Operands: []mir.AsmOperand{{Kind: mir.AsmIn, Reg: "reg", In: mir.Copy(place(3)), Type: types.U64}}}, f.at(3, 1))
	f.ret(f.at(4, 1))
	g := e.body(e.fn("Halt", nil, types.Void, 0))
	g.asmBody(mir.Asm{Template: "cli\nhlt", Options: mir.AsmNoreturn | mir.AsmNomem | mir.AsmNostack}, g.at(6, 1))
	g.term(mir.Terminator{Kind: mir.TermUnreachable, Span: g.at(6, 1)})
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		ll := emit(t, e, unitOf(e, []*mir.Body{f.b, g.b}), types.X86_64Linux, prof)
		for _, opt := range []string{"-O0", "-O2"} {
			dir := t.TempDir()
			src := filepath.Join(dir, "a.ll")
			if err := os.WriteFile(src, []byte(ll), 0o644); err != nil {
				t.Fatal(err)
			}
			s := filepath.Join(dir, "a.s")
			runTool(t, tool(needLLVM(t), "clang"), "--target="+types.X86_64Linux.LLVMTriple, "-S", "-masm=intel", opt,
				"-Wno-override-module", "-o", s, src)
			b, err := os.ReadFile(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"out\tdx, al", "lidt", "hlt"} {
				if !strings.Contains(string(b), want) {
					t.Errorf("%v %s: no %q in the assembly:\n%s", prof, opt, want, b)
				}
			}
		}
	}
}

// TestNakedIR: a naked function is naked noinline, with no alloca, no
// stack probe and no unwind table; its body is the asm call (a sym operand
// pasted as the symbol, kept in @llvm.used) and unreachable; its section
// clause puts it in the section and in @llvm.used, never internal.
func TestNakedIR(t *testing.T) {
	e := newTestEnv()
	h := e.body(e.fn("Handler", nil, types.Void, 0))
	h.ret(h.at(1, 1))
	stub := e.fn("Stub", []types.TypeID{types.U64}, types.U64, 0)
	fd := e.items.Get(stub).Decl.(*types.FuncDecl)
	fd.Naked, fd.Section = true, ".text.boot"
	f := e.body(stub)
	f.asmBody(mir.Asm{Template: "call {0}\nret", Options: mir.AsmNoreturn, Operands: []mir.AsmOperand{{Kind: mir.AsmSym, Sym: h.item}}}, f.at(3, 1))
	f.term(mir.Terminator{Kind: mir.TermUnreachable, Span: f.at(3, 1)})
	u := unitOf(e, []*mir.Body{h.b, f.b})
	u.Funcs[1].Local = true // a section function is still never internal
	ll := emit(t, e, u, types.X86_64Linux, types.Release)
	text := fnText(ll, "mk.main.Stub")
	for _, want := range []string{`section ".text.boot"`, `"call mk.main.Handler\0Aret"`, "unreachable"} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	for _, bad := range []string{"alloca", "store", "define internal"} {
		if strings.Contains(text, bad) {
			t.Errorf("%q in the naked function:\n%s", bad, text)
		}
	}
	attrs := attrGroupOf(ll, text)
	if !strings.Contains(attrs, "naked") || !strings.Contains(attrs, "noinline") || strings.Contains(attrs, "probe-stack") || strings.Contains(attrs, "uwtable") {
		t.Errorf("naked attributes: %s", attrs)
	}
	if !strings.Contains(ll, "@llvm.used") || !strings.Contains(ll, "ptr @mk.main.Stub") || !strings.Contains(ll, "ptr @mk.main.Handler") {
		t.Errorf("@llvm.used lacks the section function or the sym target:\n%s", ll)
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "naked", ll, "-O2")
}

// attrGroupOf is the text of the attribute group a function definition
// names (#n), or the inline attributes.
func attrGroupOf(ll, def string) string {
	line, _, _ := strings.Cut(def, "\n")
	i := strings.LastIndex(line, "#")
	if i < 0 {
		return line
	}
	j := i + 1
	for j < len(line) && line[j] >= '0' && line[j] <= '9' {
		j++
	}
	group := "attributes " + line[i:j] + " = {"
	k := strings.Index(ll, group)
	if k < 0 {
		return line
	}
	end := strings.IndexByte(ll[k:], '\n')
	return ll[k : k+end]
}

// TestSectionGlobalsIR: a var and a const with a section clause are
// named globals in their sections and in @llvm.used, never internal (the
// const emitted with no reader); an extern var is an external declaration
// under its own name; a global of an align(64) struct is aligned 64.
func TestSectionGlobalsIR(t *testing.T) {
	e := newTestEnv()
	v := e.item("Boot", types.KindGlobal, &types.GlobalDecl{Type: types.U32, HasValue: true, Value: e.intConst(types.U32, 9), Section: ".data.boot"})
	c := e.item("Header", types.KindConst, &types.ConstDecl{Type: types.U32, HasValue: true, Value: e.intConst(types.U32, 0x1BADB002), Section: ".multiboot"})
	x := e.item("KernelEnd", types.KindGlobal, &types.GlobalDecl{Type: types.U8, Extern: true})
	f := e.body(e.fn("Addr", nil, e.tt.Ref(types.U8), 0))
	f.assign(place(0), mir.Rvalue{Kind: mir.RvGlobalRef, Item: x, Type: e.tt.Ref(types.U8)}, f.at(2, 1))
	f.ret(f.at(2, 1))
	u := unitOf(e, []*mir.Body{f.b})
	u.Globals = []Global{{Item: v, Pack: 0, Local: true}, {Item: c, Pack: 0}, {Item: x, Pack: 0}}
	ll := emit(t, e, u, types.X86_64Linux, types.Release)
	for _, want := range []string{
		`@mk.main.Boot = hidden global <{ [4 x i8] }> <{ [4 x i8] c"\09\00\00\00" }>, section ".data.boot", align 4`,
		`@mk.main.Header = hidden constant <{ [4 x i8] }> <{ [4 x i8] c"\02\B0\AD\1B" }>, section ".multiboot", align 4`,
		`@KernelEnd = external global [1 x i8], align 1`,
		`@llvm.used = appending global`,
	} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %q in:\n%s", want, ll)
		}
	}
	if !strings.Contains(ll, "ptr @mk.main.Boot") || !strings.Contains(ll, "ptr @mk.main.Header") {
		t.Errorf("@llvm.used lacks a section global:\n%s", ll)
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "sect", ll, "-O2")
}

// TestCpuidNative builds testdata/asm/hosted with mk and runs it at
// dev and release on the host: the reference's Cpuid block (its vendor
// string is printable ASCII), const, AT&T, xmm, Vec, clobber_abi with a
// call through sym, and two naked functions.
func TestCpuidNative(t *testing.T) {
	needLLVM(t)
	exe := simdMkExe(t)
	src := filepath.Join("testdata", "asm", "hosted")
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, n := range []string{"main.mk", "pkg.mk"} {
				b, err := os.ReadFile(filepath.Join(src, n))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"run"}
			if prof == "release" {
				args = append(args, "--release")
			}
			cmd := exec.Command(exe, append(args, ".")...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "MK_CACHE="+filepath.Join(dir, ".mk-cache"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("mk %v: %v\n%s", args, err, out)
			}
			lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
			if len(lines) != 4 {
				t.Fatalf("output %q", out)
			}
			var vendor []byte
			for _, w := range strings.Fields(lines[0]) {
				var n uint32
				for _, ch := range w {
					n = n<<4 | uint32(strings.IndexRune("0123456789abcdef", ch))
				}
				vendor = append(vendor, byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
			}
			for _, ch := range vendor {
				if ch < 0x20 || ch > 0x7e {
					t.Fatalf("cpuid vendor %q is not printable (line %q)", vendor, lines[0])
				}
			}
			if len(vendor) != 12 {
				t.Fatalf("cpuid vendor %q", vendor)
			}
			if lines[1] != "29 42 3.75 46" || lines[2] != "7 42 7" {
				t.Errorf("got %q and %q, want \"29 42 3.75 46\" and \"7 42 7\"", lines[1], lines[2])
			}
			// Const parameters as const operands, pasted per instance:
			// 5 << 3, 5 << 1, 50 + (-8).
			if lines[3] != "40 10 42" {
				t.Errorf("const parameter operands: got %q, want \"40 10 42\"", lines[3])
			}
		})
	}
}

// TestGeneralRegsOnlyString pins groFeatures against the clang on
// PATH: -mgeneral-regs-only for x86_64-unknown-none-elf passes exactly
// -x87, -mmx and -sse as target features.
func TestGeneralRegsOnlyString(t *testing.T) {
	dir := needLLVM(t)
	src := filepath.Join(t.TempDir(), "x.c")
	if err := os.WriteFile(src, []byte("int f(void) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tool(dir, "clang"), "-###", "--target=x86_64-unknown-none-elf", "-mgeneral-regs-only", "-c", src).CombinedOutput()
	if err != nil {
		t.Fatalf("clang -###: %v\n%s", err, out)
	}
	var feats []string
	fields := strings.Fields(string(out))
	for i := 0; i+1 < len(fields); i++ {
		if strings.Trim(fields[i], `"`) == "-target-feature" {
			feats = append(feats, strings.Trim(fields[i+1], `"`))
		}
	}
	if got := strings.Join(feats, ","); got != groFeatures {
		t.Errorf("clang's general-regs-only features are %q, codegen writes %q", got, groFeatures)
	}
}

// TestGeneralRegsOnlyIR: in a general-regs-only build every function
// gets groFeatures, and a function whose cpu set holds sse2 gets -x87,-mmx
// with its closure's +sse,+sse2; the module compiles for the freestanding
// triple at -O2.
func TestGeneralRegsOnlyIR(t *testing.T) {
	e := newTestEnv()
	f := e.body(e.fn("Plain", []types.TypeID{types.U64}, types.U64, 0))
	f.assign(place(0), use(mir.Copy(place(1))), f.at(1, 1))
	f.ret(f.at(1, 1))
	sse := e.fn("Saved", []types.TypeID{types.U64}, types.U64, 0)
	e.items.Get(sse).Decl.(*types.FuncDecl).Sig.CPU = []string{"sse", "sse2"}
	g := e.body(sse)
	g.assign(place(0), use(mir.Copy(place(1))), g.at(2, 1))
	g.ret(g.at(2, 1))
	var b strings.Builder
	u := unitOf(e, []*mir.Body{f.b, g.b})
	if _, err := EmitPack(u, 0, e, Config{Target: types.X86_64Linux, Profile: types.Release, GeneralRegsOnly: true}, &b); err != nil {
		t.Fatal(err)
	}
	ll := b.String()
	for sym, want := range map[string]string{"mk.main.Plain": `"target-features"="-x87,-mmx,-sse"`, "mk.main.Saved": `"target-features"="-x87,-mmx,+sse,+sse2"`} {
		if a := attrGroupOf(ll, fnText(ll, sym)); !strings.Contains(a, want) {
			t.Errorf("%s: no %s in %s", sym, want, a)
		}
	}
	dir := needLLVM(t)
	p := filepath.Join(t.TempDir(), "gro.ll")
	if err := os.WriteFile(p, []byte(ll), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(tool(dir, "clang"), "--target=x86_64-unknown-none-elf", "-c", "-O2", "-Wno-override-module", "-o", p+".o", p).CombinedOutput(); err != nil {
		t.Fatalf("clang: %v\n%s", err, out)
	}
}

// TestGeneralRegsOnlySupport: in a general-regs-only bin the support
// pack's functions, memcpy, memmove and memset among them, carry
// groFeatures, and compiled as the driver compiles the support pack (-O3)
// their code names no x87, MMX, SSE or AVX register. The same module
// without GeneralRegsOnly does (the loop vectorizer's movups), so the
// disassembly check can see the bug it guards against.
func TestGeneralRegsOnlySupport(t *testing.T) {
	emitSup := func(gro bool) string {
		var b strings.Builder
		cfg := Config{Target: types.X86_64Freestanding, Profile: types.Release, GeneralRegsOnly: gro}
		if err := EmitSupport(&Unit{}, nil, cfg, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	ll := emitSup(true)
	want := `"target-features"="` + groFeatures + `"`
	defs := 0
	for _, line := range strings.Split(ll, "\n") {
		if !strings.HasPrefix(line, "define ") {
			continue
		}
		defs++
		if a := attrGroupOf(ll, line); !strings.Contains(a, want) {
			t.Errorf("no %s on %s (%s)", want, line, a)
		}
	}
	for _, sym := range []string{symMemcpy, symMemmove, symMemset, symMemcmp, symBcmp} {
		if fnText(ll, sym) == "" {
			t.Errorf("the support pack defines no %s", sym)
		}
	}
	if defs == 0 {
		t.Fatal("the support pack defines no function")
	}
	if host := emitSup(false); strings.Contains(host, `"target-features"`) {
		t.Error("a bin that is not general-regs-only got target-features in its support pack")
	}
	dir := needLLVM(t)
	vecRegs := regexp.MustCompile(`%(?:[xyz]mm[0-9]+\b|mm[0-7]\b|st\([0-7]\)|st\b)`)
	disasm := func(name, ll string) string {
		p := filepath.Join(t.TempDir(), name+".ll")
		if err := os.WriteFile(p, []byte(ll), 0o644); err != nil {
			t.Fatal(err)
		}
		runTool(t, tool(dir, "clang"), "--target="+types.X86_64Freestanding.LLVMTriple, "-c", "-O3", "-Wno-override-module", "-o", p+".o", p)
		return runTool(t, tool(dir, "llvm-objdump"), "-d", "--no-show-raw-insn", p+".o")
	}
	if !vecRegs.MatchString(disasm("host", emitSup(false))) {
		t.Error("the support pack without general-regs-only uses no vector register at -O3 (clang 21.1.8 vectorizes memcpy with movups): the check below can no longer see the bug")
	}
	fn := ""
	for _, line := range strings.Split(disasm("gro", ll), "\n") {
		if strings.HasSuffix(line, ">:") {
			fn = line
		}
		if vecRegs.MatchString(line) {
			t.Errorf("general-regs-only support code uses a vector or x87 register in %s: %s", fn, strings.TrimSpace(line))
		}
	}
}

// TestGeneralRegsOnlyHelpers: a helper a pack emits for
// itself, here simd.Cpu's mk.cpu.detect, carries groFeatures in a
// general-regs-only bin like the instance that needs it.
func TestGeneralRegsOnlyHelpers(t *testing.T) {
	e := newTestEnv()
	feats := e.structIn("std.simd", "CpuFeatures", e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 2)}, types.U64))
	cpu := e.stdFn("std.simd", "", "Cpu", 0, nil, feats)
	f := e.body(e.fn("Read", nil, feats, 0))
	v := f.call(cpu, nil, feats)
	f.assign(place(0), use(mir.Copy(place(v))), f.at(2, 1))
	f.ret(f.at(2, 1))
	var b strings.Builder
	cfg := Config{Target: types.X86_64Freestanding, Profile: types.Release, GeneralRegsOnly: true}
	if _, err := EmitPack(unitOf(e, []*mir.Body{f.b}), 0, e, cfg, &b); err != nil {
		t.Fatal(err)
	}
	ll := b.String()
	want := `"target-features"="` + groFeatures + `"`
	for _, sym := range []string{"mk.main.Read", "mk.cpu.detect"} {
		def := fnText(ll, sym)
		if def == "" {
			t.Fatalf("no %s in:\n%s", sym, ll)
		}
		if a := attrGroupOf(ll, def); !strings.Contains(a, want) || strings.Count(a, `"target-features"`) != 1 {
			t.Errorf("%s: want exactly one %s in %s", sym, want, a)
		}
	}
}

// TestCpuCached: simd.Cpu() reads the pack's cache (an acquire load of
// its ready word, then the two bit words) and runs cpuid and xgetbv only on
// the path where the cache is empty, which stores the bits and then the
// ready word with release order.
func TestCpuCached(t *testing.T) {
	e := newTestEnv()
	feats := e.structIn("std.simd", "CpuFeatures", e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 2)}, types.U64))
	cpu := e.stdFn("std.simd", "", "Cpu", 0, nil, feats)
	f := e.body(e.fn("Read", nil, feats, 0))
	v := f.call(cpu, nil, feats)
	f.assign(place(0), use(mir.Copy(place(v))), f.at(2, 1))
	f.ret(f.at(2, 1))
	ll := emit(t, e, unitOf(e, []*mir.Body{f.b}), types.X86_64Linux, types.Release)
	text := fnText(ll, "mk.main.Read")
	for _, want := range []string{"@mk.cpu.cache = private global [3 x i64] zeroinitializer", "load atomic i64, ptr", "acquire", "store atomic i64 1,", "release", `asm "cpuid"`} {
		if !strings.Contains(ll, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	// The detection is out of line, in the pack's cold noinline
	// mk.cpu.detect, called only on the miss edge (cold by !prof); the
	// reader holds no cpuid.
	if strings.Contains(text, `"cpuid"`) || !strings.Contains(text, "call void @mk.cpu.detect()") {
		t.Errorf("the reader runs cpuid itself, or never calls mk.cpu.detect:\n%s", text)
	}
	det := ll[max(strings.Index(ll, "define internal void @mk.cpu.detect("), 0):]
	det, _, _ = strings.Cut(det, "\n}\n")
	if a := attrGroupOf(ll, det); !strings.Contains(det, `"cpuid"`) || !strings.Contains(det, "store atomic i64 1,") ||
		!strings.Contains(a, "cold") || !strings.Contains(a, "noinline") {
		t.Errorf("mk.cpu.detect is not the cold noinline detection:\n%s\n%s", det, a)
	}
	if i, j := strings.Index(text, "acquire"), strings.Index(text, "@mk.cpu.detect"); i < 0 || j < i {
		t.Errorf("the detection before the cache test:\n%s", text)
	}
	clangC(t, types.X86_64Linux, t.TempDir(), "cpu", ll, "-O2")
}
