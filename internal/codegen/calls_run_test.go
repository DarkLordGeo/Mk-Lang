// Why: calls, error edges and C interop must behave right in the linked
// program, not only in the IR text.
// What: TestFreshCopy (an indirect argument is a new copy per call, so
// a callee writing its parameter never reaches the caller's value),
// TestErrorEdge (the code and the payload reach the else edge),
// TestEnumTagFromCSurvivesRelease, TestReturnReborrow,
// TestImportsOnlyKernel32, TestStaticELFNoInterp.
// How: hand-built bodies are compiled by clang and linked with an IR harness
// and a stand-in library, then run natively; the executables' imports and
// ELF headers are read back.

package codegen

import (
	"debug/elf"
	"debug/pe"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func wrap(op mir.BinOp, a, b mir.Operand) mir.Rvalue { return bin(op, a, b) }

// runBodies are the functions of the native call tests:
//
//	Take(var p: Big): int { p.a = p.a +% 1; return p.a }   (Big is 32 bytes: indirect)
//	Twice(): int { x := Big(5, 0, 0, 0); return Take(x) *% 100 +% Take(x) }
//	Get(k: int): (NotFound | TooBig)!int { k == 0: NotFound; k > 100: TooBig(k); else k *% 2 }
//	ValOf(k: int): int { v := Get(k) else e { return -1 }; return v }
//	CodeOf(k: int): u64 { _ := Get(k) else e { return discr(e) }; return 0 }
//	PayOf(k: int): int { _ := Get(k) else e { if e is TooBig(n) { return n }; return -2 }; return -1 }
//	Check(): int { match CTag() { A => 1, B => 2 } }   (an enum from C; a bad tag is PanicEnumTag)
//	GetX(p: mut Point): mut int { return mut p.x }
//	Reborrow(): int { p := Point(1, 2); r := GetX(mut p); *r = 5; return p.x }
func runBodies(e *testEnv) ([]*mir.Body, []types.ItemID) {
	big := e.structT("Big", types.Int, types.Int, types.Int, types.Int)
	tki := e.fn("Take", []types.TypeID{big}, types.Int, 0)
	// var: only a var parameter may be assigned; a non-var one is
	// readonly and writing it is an ICE.
	e.items.Get(tki).Decl.(*types.FuncDecl).Sig.Params[0].Var = true
	tk := e.body(tki)
	tk.b.Locals[1].Var = true
	fa := tk.b.Place(1, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int})
	tk.assign(fa, wrap(mir.AddWrap, mir.Copy(fa), mir.Imm(types.Int, 1)), tk.at(2, 3))
	tk.assign(place(0), use(mir.Copy(fa)), tk.at(2, 20))
	tk.ret(tk.at(2, 20))

	tw := e.body(e.fn("Twice", nil, types.Int, 0))
	x, r1, r2, m := tw.local(big), tw.local(types.Int), tw.local(types.Int), tw.local(types.Int)
	tw.assign(place(x), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: e.tt.Get(big).Item,
		Ops: tw.b.AddOperands(mir.Imm(types.Int, 5), mir.Imm(types.Int, 0), mir.Imm(types.Int, 0), mir.Imm(types.Int, 0))}, tw.at(4, 3))
	tw.callTo(direct(tk.item), place(r1), mir.NoLocal, tw.at(4, 20), mir.Copy(place(x)))
	tw.callTo(direct(tk.item), place(r2), mir.NoLocal, tw.at(4, 40), mir.Copy(place(x)))
	tw.assign(place(m), wrap(mir.MulWrap, mir.Copy(place(r1)), mir.Imm(types.Int, 100)), tw.at(4, 50))
	tw.assign(place(0), wrap(mir.AddWrap, mir.Copy(place(m)), mir.Copy(place(r2))), tw.at(4, 50))
	tw.ret(tw.at(4, 50))

	notFound := e.errItem("NotFound")
	tooBig := e.errItem("TooBig", types.Int)
	set := e.tt.ErrorSet([]types.ErrorMember{{Item: notFound}, {Item: tooBig}})
	st := e.tt.ErrorSetType(set)
	g := e.body(e.fn("Get", []types.TypeID{types.Int}, types.Int, set))
	c := g.local(types.Bool)
	el := g.local(st)
	g.assign(place(c), bin(mir.Eq, mir.Copy(place(1)), mir.Imm(types.Int, 0)), g.at(6, 3))
	nf, more := g.newBlock(), g.newBlock()
	g.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)), Cases: g.b.AddCases(
		mir.SwitchCase{Value: 1, Target: nf}, mir.SwitchCase{Value: 0, Target: more}), Default: mir.NoBlock, Span: g.at(6, 3)})
	g.cur = nf
	g.assign(place(el), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: notFound}, g.at(6, 10))
	g.assign(place(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggErr, Ops: g.b.AddOperands(mir.Move(place(el)))}, g.at(6, 10))
	g.ret(g.at(6, 10))
	g.cur = more
	g.assign(place(c), bin(mir.Gt, mir.Copy(place(1)), mir.Imm(types.Int, 100)), g.at(7, 3))
	tb, ok := g.newBlock(), g.newBlock()
	g.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)), Cases: g.b.AddCases(
		mir.SwitchCase{Value: 1, Target: tb}, mir.SwitchCase{Value: 0, Target: ok}), Default: mir.NoBlock, Span: g.at(7, 3)})
	g.cur = tb
	g.assign(place(el), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: tooBig, Ops: g.b.AddOperands(mir.Copy(place(1)))}, g.at(7, 10))
	g.assign(place(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggErr, Ops: g.b.AddOperands(mir.Move(place(el)))}, g.at(7, 10))
	g.ret(g.at(7, 10))
	g.cur = ok
	v2 := g.local(types.Int)
	g.assign(place(v2), wrap(mir.MulWrap, mir.Copy(place(1)), mir.Imm(types.Int, 2)), g.at(8, 3))
	g.assign(place(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggOk, Ops: g.b.AddOperands(mir.Copy(place(v2)))}, g.at(8, 3))
	g.ret(g.at(8, 3))

	// fallible calls Get(k) with an else edge into a fresh error local; the
	// ok value goes to dest; onErr fills the error block.
	fallible := func(f *fnb, dest mir.Place, sp mir.SpanID, onErr func(e mir.LocalID)) {
		er := f.local(st)
		okB, errB := f.newBlock(), f.newBlock()
		f.term(mir.Terminator{Kind: mir.TermCall, Callee: direct(g.item), Args: f.b.AddArgs(mir.Arg{Operand: mir.Copy(place(1))}),
			Dest: dest, Target: okB, HasErr: true, Err: mir.ErrorEdge{Kind: mir.EdgeElse, Block: errB, Error: er},
			Spawn: mir.NoLocal, Span: sp})
		f.cur = errB
		onErr(er)
		f.cur = okB
	}
	vo := e.body(e.fn("ValOf", []types.TypeID{types.Int}, types.Int, 0))
	vv := vo.local(types.Int)
	fallible(vo, place(vv), vo.at(10, 8), func(mir.LocalID) {
		vo.assign(place(0), use(mir.Imm(types.Int, ^uint64(0))), vo.at(10, 30))
		vo.ret(vo.at(10, 30))
	})
	vo.assign(place(0), use(mir.Copy(place(vv))), vo.at(11, 3))
	vo.ret(vo.at(11, 3))

	co := e.body(e.fn("CodeOf", []types.TypeID{types.Int}, types.U64, 0))
	cv := co.local(types.Int)
	fallible(co, place(cv), co.at(13, 8), func(er mir.LocalID) {
		co.assign(place(0), mir.Rvalue{Kind: mir.RvDiscriminant, Place: place(er)}, co.at(13, 30))
		co.ret(co.at(13, 30))
	})
	co.assign(place(0), use(mir.Imm(types.U64, 0)), co.at(14, 3))
	co.ret(co.at(14, 3))

	po := e.body(e.fn("PayOf", []types.TypeID{types.Int}, types.Int, 0))
	pv := po.local(types.Int)
	fallible(po, place(pv), po.at(16, 8), func(er mir.LocalID) {
		d := po.local(types.U64)
		po.assign(place(d), mir.Rvalue{Kind: mir.RvDiscriminant, Place: place(er)}, po.at(16, 30))
		is, other := po.newBlock(), po.newBlock()
		po.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(d)), Cases: po.b.AddCases(
			mir.SwitchCase{Value: uint64(types.ErrorCode(types.ItemKey("main", "TooBig"))), Target: is}), Default: other, Span: po.at(16, 30)})
		po.cur = is
		pay := po.b.Place(er, mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelCase, A: uint32(tooBig), Type: st},
			mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int})
		po.assign(place(0), use(mir.Copy(pay)), po.at(16, 40))
		po.ret(po.at(16, 40))
		po.cur = other
		po.assign(place(0), use(mir.Imm(types.Int, ^uint64(1))), po.at(16, 50))
		po.ret(po.at(16, 50))
	})
	po.assign(place(0), use(mir.Imm(types.Int, ^uint64(0))), po.at(17, 3))
	po.ret(po.at(17, 3))

	en := e.enumT("E", types.U8, []uint64{0, 1})
	ct := e.fn("CTag", nil, en, 0)
	e.items.Get(ct).Decl.(*types.FuncDecl).Sig.Extern = true
	e.items.Get(ct).Decl.(*types.FuncDecl).Bodiless = true
	ck := e.body(e.fn("Check", nil, types.Int, 0))
	ev, dv := ck.local(en), ck.local(types.U64)
	ck.callTo(direct(ct), place(ev), mir.NoLocal, ck.at(19, 9))
	ck.assign(place(dv), mir.Rvalue{Kind: mir.RvDiscriminant, Place: place(ev)}, ck.at(19, 3))
	a1, a2, bad := ck.newBlock(), ck.newBlock(), ck.newBlock()
	ck.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(dv)), Cases: ck.b.AddCases(
		mir.SwitchCase{Value: 0, Target: a1}, mir.SwitchCase{Value: 1, Target: a2}), Default: bad, Span: ck.at(19, 3)})
	ck.cur = a1
	ck.assign(place(0), use(mir.Imm(types.Int, 1)), ck.at(20, 3))
	ck.ret(ck.at(20, 3))
	ck.cur = a2
	ck.assign(place(0), use(mir.Imm(types.Int, 2)), ck.at(21, 3))
	ck.ret(ck.at(21, 3))
	ck.cur = bad
	ck.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicEnumTag, Span: ck.at(19, 9)})

	pt := e.structT("Point", types.Int, types.Int)
	gx := e.body(e.fn("GetX", []types.TypeID{e.tt.Mut(pt)}, e.tt.Mut(types.Int), 0))
	gx.assign(place(0), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: gx.b.Place(1,
		mir.Projection{Kind: mir.ProjDeref, Type: pt}, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int})}, gx.at(23, 3))
	gx.ret(gx.at(23, 3))
	rb := e.body(e.fn("Reborrow", nil, types.Int, 0))
	p, mp, r := rb.local(pt), rb.local(e.tt.Mut(pt)), rb.local(e.tt.Mut(types.Int))
	rb.assign(place(p), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: e.tt.Get(pt).Item,
		Ops: rb.b.AddOperands(mir.Imm(types.Int, 1), mir.Imm(types.Int, 2))}, rb.at(25, 3))
	rb.assign(place(mp), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: place(p)}, rb.at(25, 20))
	rb.callTo(direct(gx.item), place(r), mir.NoLocal, rb.at(25, 20), mir.Move(place(mp)))
	rb.assign(rb.b.Place(r, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}), use(mir.Imm(types.Int, 5)), rb.at(25, 40))
	rb.assign(place(0), use(mir.Copy(rb.b.Place(p, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int}))), rb.at(25, 50))
	rb.ret(rb.at(25, 50))

	return []*mir.Body{tk.b, tw.b, g.b, vo.b, co.b, po.b, ck.b, gx.b, rb.b}, []types.ItemID{ct}
}

// runBuild compiles the support pack and the run pack for target; each case
// links its own harness.
type runBuild struct {
	target types.Target
	dir    string
	objs   []string
	ll     string
}

func newRunBuild(t *testing.T, target types.Target, prof types.Profile, opt string) *runBuild {
	e := newTestEnv()
	bodies, bodiless := runBodies(e)
	ll := emit(t, e, unitOf(e, bodies, bodiless...), target, prof)
	dir := t.TempDir()
	return &runBuild{target: target, dir: dir, ll: ll, objs: []string{
		clangC(t, target, dir, "support", supportLL(t, target), "-O2"),
		clangC(t, target, dir, "pack", ll, opt),
	}}
}

const runDecls = `
declare i64 @mk.main.Twice()
declare i64 @mk.main.ValOf(i64)
declare i64 @mk.main.CodeOf(i64)
declare i64 @mk.main.PayOf(i64)
declare i64 @mk.main.Check()
declare i64 @mk.main.Reborrow()

define internal void @h_pn(i64 %v) nounwind {
  call void @h_num(i64 %v)
  call void @h_write(ptr @s.nl, i64 1)
  ret void
}
`

// link links a harness whose Start body is body plus extra IR (its own
// object, like a C library), and returns the executable.
func (rb *runBuild) link(t *testing.T, name, body, extra string, release bool) string {
	t.Helper()
	osIR, kernel := harnessOS(rb.target)
	h := osIR + harnessRT + runDecls + `
define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
` + body + `
  call void @h_exit(i32 0)
  unreachable
}
`
	dir := rb.dir + "/" + name
	if err := mkdir(dir); err != nil {
		t.Fatal(err)
	}
	objs := append(append([]string(nil), rb.objs...), clangC(t, rb.target, dir, "harness", h, "-O0"))
	if extra == "" {
		extra = cTag(0) // the pack references CTag
	}
	objs = append(objs, clangC(t, rb.target, dir, "clib", extra, "-O2"))
	return linkExe(t, rb.target, dir, objs, linkOpts{kernel: kernel, release: release})
}

// cTag is a stand-in C library whose CTag returns tag.
func cTag(tag int) string {
	return fmt.Sprintf("define i8 @CTag() nounwind {\n  ret i8 %d\n}\n", tag)
}

func pn(n int, call string) string {
	return fmt.Sprintf("  %%r%d = call i64 %s\n  call void @h_pn(i64 %%r%d)\n", n, call, n)
}

func TestFreshCopy(t *testing.T) {
	// IR: each indirect argument is its own alloca, filled by a memcpy from
	// the caller's value just before the call (Win64 passes Big by pointer).
	e := newTestEnv()
	bodies, bodiless := runBodies(e)
	ll := emit(t, e, unitOf(e, bodies, bodiless...), types.X86_64Windows, types.Dev)
	tw := fnText(ll, "mk.main.Twice")
	calls := regexp.MustCompile(`call void @llvm\.memcpy\.p0\.p0\.i64\(ptr (%v\d+), ptr (%v\d+), i64 32, i1 false\)[^\n]*\n\s*%v\d+ = call noundef i64 @mk\.main\.Take\(ptr [^%]*(%v\d+)\)`).FindAllStringSubmatch(tw, -1)
	if len(calls) != 2 {
		t.Fatalf("want two copy-then-call sequences in\n%s", tw)
	}
	for _, c := range calls {
		if c[1] != c[3] || c[2] == c[3] {
			t.Errorf("the argument %s is not the fresh copy %s of %s", c[3], c[1], c[2])
		}
	}
	if calls[0][1] == calls[1][1] || calls[0][2] != calls[1][2] {
		t.Errorf("the two calls share a copy (%s, %s) or copy different sources", calls[0][1], calls[1][1])
	}
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				rb := newRunBuild(t, target, types.Dev, opt)
				out, code := runExe(t, target, rb.link(t, "fresh", pn(0, "@mk.main.Twice()"), "", false), "")
				if code != 0 || out != "606\n" {
					t.Fatalf("exit %d, output %q; want 606 (607 would mean the callee wrote the caller's value)", code, out)
				}
			})
		}
	}
}

func TestErrorEdge(t *testing.T) {
	nf := types.ErrorCode(types.ItemKey("main", "NotFound"))
	tb := types.ErrorCode(types.ItemKey("main", "TooBig"))
	body := pn(0, "@mk.main.ValOf(i64 5)") + pn(1, "@mk.main.ValOf(i64 0)") + pn(2, "@mk.main.CodeOf(i64 0)") +
		pn(3, "@mk.main.CodeOf(i64 500)") + pn(4, "@mk.main.CodeOf(i64 5)") + pn(5, "@mk.main.PayOf(i64 500)") +
		pn(6, "@mk.main.PayOf(i64 0)") + pn(7, "@mk.main.PayOf(i64 7)")
	want := fmt.Sprintf("10\n-1\n%d\n%d\n0\n500\n-2\n-1\n", nf, tb)
	for _, target := range runTargets {
		for _, opt := range []string{"-O0", "-O2"} {
			t.Run(target.Name+opt, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				rb := newRunBuild(t, target, types.Dev, opt)
				out, code := runExe(t, target, rb.link(t, "edge", body, "", false), "")
				if code != 0 || out != want {
					t.Fatalf("exit %d, output\n%s\nwant\n%s", code, out, want)
				}
			})
		}
	}
}

// TestEnumTagFromCSurvivesRelease: the RvDiscriminant load of an
// enum returned by a C function carries no !range, so an -O2 build keeps
// the default branch: tag 7 panics with PanicEnumTag; tags 0 and 1 match.
func TestEnumTagFromCSurvivesRelease(t *testing.T) {
	e := newTestEnv()
	bodies, bodiless := runBodies(e)
	ll := emit(t, e, unitOf(e, bodies, bodiless...), types.X86_64Linux, types.Release)
	ck := fnText(ll, "mk.main.Check")
	for _, line := range strings.Split(ck, "\n") {
		if strings.Contains(line, "load i8") && strings.Contains(line, "!range") {
			t.Errorf("the discriminant load has a !range: %s", line)
		}
	}
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			if _, why := hostRunner(target); why != "" {
				skipRunner(t, why)
			}
			rb := newRunBuild(t, target, types.Release, "-O2")
			for tag, want := range map[int]string{0: "1\n", 1: "2\n"} {
				out, code := runExe(t, target, rb.link(t, fmt.Sprint("ok", tag), pn(0, "@mk.main.Check()"), cTag(tag), true), "")
				if code != 0 || out != want {
					t.Errorf("tag %d: exit %d, output %q; want %q", tag, code, out, want)
				}
			}
			out, code := runExe(t, target, rb.link(t, "bad", pn(0, "@mk.main.Check()"), cTag(7), true), "")
			w := fmt.Sprintf("panic: %d at main/t.mk:19:9\n", types.PanicEnumTag)
			if code != 101 || out != w {
				t.Errorf("tag 7: exit %d, output %q; want 101 and %q", code, out, w)
			}
		})
	}
}

// TestReturnReborrow: GetX returns a reborrow of its mut
// parameter; a write through it is seen by the caller in a release build,
// which a noalias on the return would let LLVM break.
func TestReturnReborrow(t *testing.T) {
	for _, target := range runTargets {
		t.Run(target.Name, func(t *testing.T) {
			if _, why := hostRunner(target); why != "" {
				skipRunner(t, why)
			}
			rb := newRunBuild(t, target, types.Release, "-O2")
			if def := regexp.MustCompile(`(?m)^define .*@mk\.main\.GetX\(.*$`).FindString(rb.ll); strings.Contains(strings.SplitN(def, "@", 2)[0], "noalias") {
				t.Errorf("noalias on the return: %s", def)
			}
			out, code := runExe(t, target, rb.link(t, "rb", pn(0, "@mk.main.Reborrow()"), "", true), "")
			if code != 0 || out != "5\n" {
				t.Fatalf("exit %d, output %q; want 5", code, out)
			}
		})
	}
}

// TestImportsOnlyKernel32: an executable of codegen's packs, the support
// pack and a kernel32-only harness imports kernel32.dll and nothing else:
// no pack or support routine pulls in a CRT DLL.
func TestImportsOnlyKernel32(t *testing.T) {
	target := types.X86_64Windows
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		rb := newRunBuild(t, target, prof, map[types.Profile]string{types.Dev: "-O0", types.Release: "-O2"}[prof])
		exe := rb.link(t, fmt.Sprint("imp", prof), pn(0, "@mk.main.Twice()"), "", prof == types.Release)
		f, err := pe.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		// debug/pe's ImportedLibraries returns nothing; ImportedSymbols
		// reads the import directory as "name:dll".
		syms, err := f.ImportedSymbols()
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		libs := map[string]bool{}
		for _, s := range syms {
			if i := strings.LastIndex(s, ":"); i >= 0 {
				libs[strings.ToLower(s[i+1:])] = true
			}
		}
		if len(syms) == 0 || len(libs) != 1 || !libs["kernel32.dll"] {
			t.Errorf("profile %d imports %v from %v, want only kernel32.dll", prof, syms, libs)
		}
	}
}

// TestStaticELFNoInterp: the Linux executable has no PT_INTERP and no
// PT_DYNAMIC segment.
func TestStaticELFNoInterp(t *testing.T) {
	target := types.X86_64Linux
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		rb := newRunBuild(t, target, prof, map[types.Profile]string{types.Dev: "-O0", types.Release: "-O2"}[prof])
		exe := rb.link(t, fmt.Sprint("elf", prof), pn(0, "@mk.main.Twice()"), "", prof == types.Release)
		f, err := elf.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
				t.Errorf("profile %d: segment %v", prof, p.Type)
			}
		}
		if f.Type != elf.ET_EXEC {
			t.Errorf("profile %d: type %v, want ET_EXEC", prof, f.Type)
		}
		f.Close()
	}
}
