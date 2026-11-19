// Why: the IR codegen prints is the compiler's output; a change in it must be
// seen, and every case must stay valid for each target and for LLVM 21.
// What: TestEmitGolden (testdata/<case>.ll.golden, x86_64-linux),
// TestEmitCompiles (clang for the four targets, the verifier as oracle),
// TestGoldensParseWithLLVM21 and TestNoSyntaxNewerThan21.
// How: hand-built MIR cases in a table are emitted and compared with their
// goldens; LLVM 21 is the oldest LLVM Mk supports (toolchain.MinMajor).

package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// emitCase is one golden: a builder of bodies (the first is the function
// under test) and the bodiless functions it calls.
type emitCase struct {
	name  string
	build func(e *testEnv) ([]*mir.Body, []types.ItemID)
}

func bin(op mir.BinOp, a, b mir.Operand) mir.Rvalue {
	return mir.Rvalue{Kind: mir.RvBinary, Bin: op, A: a, B: b}
}

func use(o mir.Operand) mir.Rvalue { return mir.Rvalue{Kind: mir.RvUse, A: o} }

// binFn is fn name(a: t, b: t): r { return a op b }.
func binFn(e *testEnv, name string, op mir.BinOp, t, r types.TypeID, b mir.Operand) *mir.Body {
	f := e.body(e.fn(name, []types.TypeID{t, t}, r, 0))
	if b.Kind == mir.OpCopy {
		b = mir.Copy(place(2))
	}
	f.assign(place(0), bin(op, mir.Copy(place(1)), b), f.at(2, 5))
	f.ret(f.at(3, 1))
	return f.b
}

func castFn(e *testEnv, name string, from, to types.TypeID, kind mir.CastKind) *mir.Body {
	f := e.body(e.fn(name, []types.TypeID{from}, to, 0))
	f.assign(place(0), mir.Rvalue{Kind: mir.RvCast, Cast: kind, A: mir.Copy(place(1)), Type: to}, f.at(4, 9))
	f.ret(f.at(5, 1))
	return f.b
}

var emitCases = []emitCase{
	{"arith", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		cp := mir.Copy(place(2))
		return []*mir.Body{
			binFn(e, "AddI32", mir.Add, types.I32, types.I32, cp),
			binFn(e, "SubU8", mir.Sub, types.U8, types.U8, cp),
			binFn(e, "MulInt", mir.Mul, types.Int, types.Int, cp),
			binFn(e, "WrapAdd", mir.AddWrap, types.I64, types.I64, cp),
			binFn(e, "DivI64", mir.Div, types.I64, types.I64, cp),
			binFn(e, "RemU32", mir.Rem, types.U32, types.U32, cp),
			binFn(e, "DivBy7", mir.Div, types.I32, types.I32, mir.Imm(types.I32, 7)),
			binFn(e, "DivByM1", mir.Div, types.I32, types.I32, mir.Imm(types.I32, 0xffffffff)),
			binFn(e, "ShlU32", mir.Shl, types.U32, types.U32, cp),
			binFn(e, "ShrI16", mir.Shr, types.I16, types.I16, cp),
			binFn(e, "LtI8", mir.Lt, types.I8, types.Bool, cp),
			binFn(e, "GeU64", mir.Ge, types.U64, types.Bool, cp),
			binFn(e, "FAdd", mir.Add, types.F64, types.F64, cp),
			binFn(e, "FNe", mir.Ne, types.F32, types.Bool, cp),
			binFn(e, "FLe", mir.Le, types.F64, types.Bool, cp),
			binFn(e, "StrEq", mir.Eq, types.Str, types.Bool, cp),
			binFn(e, "StrLt", mir.Lt, types.Str, types.Bool, cp),
		}, nil
	}},
	{"casts", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		ab := e.enumT("AB", types.U8, []uint64{0, 1})
		se := e.enumT("SE", types.I8, []uint64{0xFF, 0})
		return []*mir.Body{
			castFn(e, "I32ToU8", types.I32, types.U8, mir.CastChecked),
			castFn(e, "U64ToI64", types.U64, types.I64, mir.CastChecked),
			castFn(e, "I8ToU16", types.I8, types.U16, mir.CastChecked),
			castFn(e, "F64ToI32", types.F64, types.I32, mir.CastChecked),
			castFn(e, "F64ToU32", types.F64, types.U32, mir.CastChecked),
			castFn(e, "F64ToI64", types.F64, types.I64, mir.CastChecked),
			castFn(e, "F64ToF32", types.F64, types.F32, mir.CastChecked),
			castFn(e, "U32ToChar", types.U32, types.Char, mir.CastChecked),
			castFn(e, "IntToAB", types.Int, ab, mir.CastChecked),
			castFn(e, "I16ToI64", types.I16, types.I64, mir.CastLossless),
			castFn(e, "U8ToF64", types.U8, types.F64, mir.CastLossless),
			castFn(e, "I64ToU8", types.I64, types.U8, mir.CastTrunc),
			// enum SE: i8 { A = -1, B = 0 }, discriminants as
			// the tag's bit patterns (check's masked Discr).
			castFn(e, "SEToInt", se, types.Int, mir.CastLossless),
			castFn(e, "IntToSE", types.Int, se, mir.CastChecked),
		}, nil
	}},
	{"control", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		// fn Max(a: int, b: int): int { if a < b { return b } return a }
		f := e.body(e.fn("Max", []types.TypeID{types.Int, types.Int}, types.Int, 0))
		c := f.local(types.Bool)
		f.assign(place(c), bin(mir.Lt, mir.Copy(place(1)), mir.Copy(place(2))), f.at(2, 8))
		yes, no, done := f.newBlock(), f.newBlock(), f.newBlock()
		f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)),
			Cases:   f.b.AddCases(mir.SwitchCase{Value: 1, Target: yes}, mir.SwitchCase{Value: 0, Target: no}),
			Default: mir.NoBlock, Span: f.at(2, 5)})
		f.cur = yes
		f.assign(place(0), use(mir.Copy(place(2))), f.at(2, 20))
		f.term(mir.Terminator{Kind: mir.TermGoto, Target: done, Span: f.at(2, 20)})
		f.cur = no
		f.assign(place(0), use(mir.Copy(place(1))), f.at(3, 5))
		f.term(mir.Terminator{Kind: mir.TermGoto, Target: done, Span: f.at(3, 5)})
		f.cur = done
		f.ret(f.at(3, 5))
		// fn Day(d: Day): int { match d { A => 1, B => 2, C => 3 } }, u8 tags {0,1,200}
		day := e.enumT("Day", types.U8, []uint64{0, 1, 200})
		g := e.body(e.fn("Pick", []types.TypeID{day}, types.Int, 0))
		dl := g.local(types.U64)
		g.assign(place(dl), mir.Rvalue{Kind: mir.RvDiscriminant, Place: place(1)}, g.at(6, 3))
		var cases []mir.SwitchCase
		var arms []mir.BlockID
		for _, d := range []uint64{0, 1, 200} {
			blk := g.newBlock()
			arms = append(arms, blk)
			cases = append(cases, mir.SwitchCase{Value: d, Target: blk})
		}
		g.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(dl)), Cases: g.b.AddCases(cases...),
			Default: mir.NoBlock, Span: g.at(6, 3)})
		for i, blk := range arms {
			g.cur = blk
			g.assign(place(0), use(mir.Imm(types.Int, uint64(i+1))), g.at(7, 3))
			g.ret(g.at(7, 3))
		}
		// fn Copy(d: &Day): Day { return *d } loads the tag with !range.
		h := e.body(e.fn("Load", []types.TypeID{e.tt.Ref(day)}, day, 0))
		h.assign(place(0), use(mir.Copy(h.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: day}))), h.at(9, 3))
		h.ret(h.at(9, 3))
		// fn Never(): never { unreachable }
		n := e.body(e.fn("Stop", nil, types.Never, 0))
		n.term(mir.Terminator{Kind: mir.TermUnreachable, Span: n.at(11, 3)})
		return []*mir.Body{f.b, g.b, h.b, n.b}, nil
	}},
	{"aggregates", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		pt := e.structT("Point", types.I32, types.I64)
		// fn Make(x: i32, y: i64): Point { return Point{x, y} } (sret on Win64, two regs on SysV)
		f := e.body(e.fn("Make", []types.TypeID{types.I32, types.I64}, pt, 0))
		f.assign(place(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: e.tt.Get(pt).Item,
			Ops: f.b.AddOperands(mir.Copy(place(1)), mir.Copy(place(2)))}, f.at(2, 3))
		f.ret(f.at(2, 3))
		// fn Y(p: &Point): i64 { return p.y }
		g := e.body(e.fn("Y", []types.TypeID{e.tt.Ref(pt)}, types.I64, 0))
		g.assign(place(0), use(mir.Copy(g.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: pt},
			mir.Projection{Kind: mir.ProjField, A: 1, Type: types.I64}))), g.at(4, 3))
		g.ret(g.at(4, 3))
		// fn SetY(p: mut Point, v: i64) { p.y = v }
		s := e.body(e.fn("SetY", []types.TypeID{e.tt.Mut(pt), types.I64}, types.Void, 0))
		s.assign(s.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: pt},
			mir.Projection{Kind: mir.ProjField, A: 1, Type: types.I64}), use(mir.Copy(place(2))), s.at(6, 3))
		s.ret(s.at(6, 3))
		// fn Wrap(p: &int): ?&int { return Some(p) } (niche) and fn Opt(x: i32): ?i32
		w := e.body(e.fn("Wrap", []types.TypeID{e.tt.Ref(types.Int)}, e.tt.Optional(e.tt.Ref(types.Int)), 0))
		w.assign(place(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggSome, Ops: w.b.AddOperands(mir.Copy(place(1)))}, w.at(8, 3))
		w.ret(w.at(8, 3))
		o := e.body(e.fn("Opt", []types.TypeID{types.I32}, e.tt.Optional(types.I32), 0))
		o.assign(place(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggSome, Ops: o.b.AddOperands(mir.Copy(place(1)))}, o.at(9, 3))
		o.ret(o.at(9, 3))
		// fn Sum(xs: []i64, i: int): i64 { return xs[i] } (lower checks the bound)
		x := e.body(e.fn("At", []types.TypeID{e.tt.Slice(types.I64), types.Int}, types.I64, 0))
		x.assign(place(0), use(mir.Copy(x.b.Place(1, mir.Projection{Kind: mir.ProjIndex, A: 2, Type: types.I64}))), x.at(11, 3))
		x.ret(x.at(11, 3))
		// fn Len(xs: []i64): int; fn Sub(s: str, a: int, b: int): str { return s[a..b] }
		l := e.body(e.fn("Len", []types.TypeID{e.tt.Slice(types.I64)}, types.Int, 0))
		l.assign(place(0), mir.Rvalue{Kind: mir.RvLen, Place: place(1)}, l.at(13, 3))
		l.ret(l.at(13, 3))
		ss := e.body(e.fn("Sub", []types.TypeID{types.Str, types.Int, types.Int}, types.Str, 0))
		ss.assign(place(0), mir.Rvalue{Kind: mir.RvSlice, Place: place(1), HasLo: true, A: mir.Copy(place(2)),
			HasHi: true, B: mir.Copy(place(3))}, ss.at(15, 3))
		ss.ret(ss.at(15, 3))
		// fn Zeros(): [16]u8 { return [0; 16] }
		arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 16)}, types.U8)
		z := e.body(e.fn("Zeros", nil, arr, 0))
		z.assign(place(0), mir.Rvalue{Kind: mir.RvRepeat, A: mir.Imm(types.U8, 0),
			Len: types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 16)}}, z.at(17, 3))
		z.ret(z.at(17, 3))
		return []*mir.Body{f.b, g.b, s.b, w.b, o.b, x.b, l.b, ss.b, z.b}, nil
	}},
	{"calls", func(e *testEnv) ([]*mir.Body, []types.ItemID) {
		notFound := e.errItem("NotFound")
		tooBig := e.errItem("TooBig", types.I64)
		set := e.tt.ErrorSet([]types.ErrorMember{{Item: notFound}, {Item: tooBig}})
		// fn Get(k: int): (NotFound | TooBig)!int; bodiless here, called through its declaration
		get := e.fn("Get", []types.TypeID{types.Int}, types.Int, set)
		// fn Use(k: int): int { v := Get(k) else e { return -1 }; return v + 1 }
		f := e.body(e.fn("Use", []types.TypeID{types.Int}, types.Int, 0))
		v := f.local(types.Int)
		er := f.local(e.tt.ErrorSetType(set))
		okB, errB := f.newBlock(), f.newBlock()
		f.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: get}},
			Args: f.b.AddArgs(mir.Arg{Operand: mir.Copy(place(1))}), Dest: place(v), Target: okB, HasErr: true,
			Err: mir.ErrorEdge{Kind: mir.EdgeElse, Block: errB, Error: er}, Spawn: mir.NoLocal, Span: f.at(2, 8)})
		f.cur = okB
		f.assign(place(0), bin(mir.Add, mir.Copy(place(v)), mir.Imm(types.Int, 1)), f.at(3, 10))
		f.ret(f.at(3, 3))
		f.cur = errB
		f.assign(place(0), use(mir.Imm(types.Int, ^uint64(0))), f.at(2, 30))
		f.ret(f.at(2, 30))
		// fn Must(k: int): int { return Get(k)! } panics with the error (PanicMust)
		m := e.body(e.fn("MustGet", []types.TypeID{types.Int}, types.Int, 0))
		er2 := m.local(e.tt.ErrorSetType(set))
		ok2, err2 := m.newBlock(), m.newBlock()
		m.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: get}},
			Args: m.b.AddArgs(mir.Arg{Operand: mir.Copy(place(1))}), Dest: place(0), Target: ok2, HasErr: true,
			Err: mir.ErrorEdge{Kind: mir.EdgeMust, Block: err2, Error: er2}, Spawn: mir.NoLocal, Span: m.at(5, 10)})
		m.cur = ok2
		m.ret(m.at(5, 3))
		m.cur = err2
		m.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicMust, HasMsg: true, Msg: mir.Move(place(er2)), Span: m.at(5, 10)})
		// fn Twice(f: fn(int): int, x: int): int { return f(f(x)) }
		ft := e.tt.Fn(types.FnType{Params: []types.FnParam{{Type: types.Int}}, Result: types.Int})
		tw := e.body(e.fn("Twice", []types.TypeID{ft, types.Int}, types.Int, 0))
		t1 := tw.local(types.Int)
		b1, b2 := tw.newBlock(), tw.newBlock()
		tw.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallValue, Value: mir.Copy(place(1))},
			Args: tw.b.AddArgs(mir.Arg{Operand: mir.Copy(place(2))}), Dest: place(t1), Target: b1, Spawn: mir.NoLocal, Span: tw.at(7, 3)})
		tw.cur = b1
		tw.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallValue, Value: mir.Copy(place(1))},
			Args: tw.b.AddArgs(mir.Arg{Operand: mir.Copy(place(t1))}), Dest: place(0), Target: b2, Spawn: mir.NoLocal, Span: tw.at(7, 3)})
		tw.cur = b2
		tw.ret(tw.at(7, 3))
		// fn Msg() { panic("boom") } with a str message
		pm := e.body(e.fn("Boom", nil, types.Never, 0))
		pm.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicExplicit, HasMsg: true, Msg: strConst(e, "boom"), Span: pm.at(9, 3)})
		return []*mir.Body{f.b, m.b, tw.b, pm.b}, []types.ItemID{get}
	}},
}

// strConst is an OpConst of type str holding s.
func strConst(e *testEnv, s string) mir.Operand {
	arr := e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, uint64(len(s)))}, types.U8)
	data := e.consts.Intern(types.ConstValue{Type: arr, Align: 1, Bytes: []byte(s)})
	b := make([]byte, 16)
	types.StoreUint(b[8:], 8, uint64(len(s)))
	c := e.consts.Intern(types.ConstValue{Type: types.Str, Align: 8, Bytes: b,
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: data}}})
	return mir.Const(types.Str, c)
}

func caseIR(t *testing.T, c emitCase, target types.Target) string {
	e := newTestEnv()
	bodies, bodiless := c.build(e)
	return emit(t, e, unitOf(e, bodies, bodiless...), target, types.Dev)
}

func TestEmitGolden(t *testing.T) {
	for _, c := range emitCases {
		t.Run(c.name, func(t *testing.T) {
			got := caseIR(t, c, types.X86_64Linux)
			if again := caseIR(t, c, types.X86_64Linux); again != got {
				t.Fatal("two emissions differ")
			}
			path := filepath.Join("testdata", c.name+".ll.golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if string(want) != got {
				t.Errorf("%s differs from %s (run with -update and read the diff)", c.name, path)
			}
		})
	}
}

// TestEmitCompiles compiles every case for every target with clang -O0 and
// -O2 and the IR verifier on, and the x86_64-linux IR, in dev and release,
// with WSL's clang 21 too (the LLVM 21 floor).
func TestEmitCompiles(t *testing.T) {
	for _, c := range emitCases {
		for _, target := range types.Targets() {
			t.Run(c.name+"/"+target.Name, func(t *testing.T) {
				ll := caseIR(t, c, target)
				dir := t.TempDir()
				clangC(t, target, dir, "o0", ll, "-O0")
				clangC(t, target, dir, "o2", ll, "-O2")
			})
		}
		t.Run(c.name+"/wsl-clang-x86_64-linux", func(t *testing.T) {
			e := newTestEnv()
			bodies, bodiless := c.build(e)
			u := unitOf(e, bodies, bodiless...)
			wslClang(t, emit(t, e, u, types.X86_64Linux, types.Dev), "-O0")
			wslClang(t, emit(t, e, u, types.X86_64Linux, types.Release), "-O2")
		})
	}
}

// wslClang compiles x86_64-linux IR with WSL's clang 21 (wslClangPath, the
// LLVM 21 floor) and the verifier on.
func wslClang(t *testing.T, ll, opt string) {
	t.Helper()
	wslClangRun(t, ll, opt)
}

// goldenFiles is every IR golden, sorted.
func goldenFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "*.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no goldens: %v", err)
	}
	sort.Strings(files)
	return files
}

// TestGoldensParseWithLLVM21 compiles every golden with the discovered
// clang, which must be LLVM 21, the floor (toolchain.MinMajor), with
// the verifier on: LLVM 21 itself is the judge of what LLVM 21 parses. A
// clang of another major version proves nothing about the floor, so the
// test does not run on one.
func TestGoldensParseWithLLVM21(t *testing.T) {
	bin := needLLVM(t)
	out := runTool(t, tool(bin, "clang"), "--version")
	if !strings.Contains(out, "clang version 21.") {
		notRun(t, "the discovered clang is not LLVM 21 (%s); the floor needs LLVM 21 to judge it", strings.SplitN(out, "\n", 2)[0])
	}
	triple := regexp.MustCompile(`(?m)^target triple = "([^"]+)"`)
	dir := t.TempDir()
	for _, f := range goldenFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		m := triple.FindSubmatch(b)
		if m == nil {
			t.Errorf("%s: no target triple", f)
			continue
		}
		src := filepath.Join(dir, filepath.Base(f)+".ll")
		if err := os.WriteFile(src, b, 0o644); err != nil {
			t.Fatal(err)
		}
		runTool(t, tool(bin, "clang"), "--target="+string(m[1]), "-x", "ir", "-c", "-O0", "-Wno-override-module",
			"-fverify-intermediate-code", "-o", filepath.Join(dir, filepath.Base(f)+".o"), src)
	}
}

// TestNoSyntaxNewerThan21 scans every golden for IR spellings that LLVM 22
// or later added and LLVM 21 refuses, so a golden written by hand or
// updated on a newer clang cannot raise the floor unseen. The parse
// by LLVM 21 above is the full check; this list is the fast one.
func TestNoSyntaxNewerThan21(t *testing.T) {
	bad := regexp.MustCompile(`\bptrtoaddr\b`)
	for _, f := range goldenFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if bad.MatchString(line) {
				t.Errorf("%s:%d: %s", f, i+1, line)
			}
		}
	}
}
