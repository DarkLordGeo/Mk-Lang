// Why: generic code reaches compile-time evaluation through instance bodies
// that mono substitutes and the driver serves through Program.Body.
// What: tests of generic evaluation: Max over two types, an unrolled fields
// loop, const arguments, Mutex.New at compile time, stage stops, reuse.
// How: eval's tests may not import mono, so the bodies are hand-built in the
// shape mono's Instantiate gives: GenericCount 0 and concrete types, an
// interface call resolved to the impl method, a fields loop unrolled per
// field, and const arguments as CAValue or CASizeOf.

package eval

import (
	"fmt"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// genericFn declares a function with n type parameters T0..; its parameter
// and result types may name them as w.tab.Param(i).
func (w *world) genericFn(mod, name string, n int, result types.TypeID, params ...types.TypeID) types.ItemID {
	f := w.fn(mod, name, result, params...)
	fd := w.items.Get(f).Decl.(*types.FuncDecl)
	for i := 0; i < n; i++ {
		fd.Sig.Generics = append(fd.Sig.Generics, types.GenericParam{Name: w.in.Intern(fmt.Sprintf("T%d", i))})
	}
	return f
}

// method declares the method owner.name of module mod (key mod.owner:name).
func (w *world) method(mod, owner, name string, n int, result types.TypeID, params ...types.TypeID) types.ItemID {
	fd := &types.FuncDecl{}
	fd.Sig.Result = result
	for i, p := range params {
		fd.Sig.Params = append(fd.Sig.Params, types.Param{Name: w.in.Intern(fmt.Sprintf("p%d", i)), Type: p})
	}
	for i := 0; i < n; i++ {
		fd.Sig.Generics = append(fd.Sig.Generics, types.GenericParam{Name: w.in.Intern(fmt.Sprintf("T%d", i))})
	}
	id := w.items.AddItem(&types.Item{Module: w.module(mod), Name: w.in.Intern(name), Kind: types.KindFunc,
		Key: types.MemberKey(mod, owner, name), Decl: fd, Span: w.span()})
	fd.Sig.Item = id
	return id
}

// genericStruct declares a struct whose fields may name its parameters.
func (w *world) genericStruct(mod, name string, n int, fields ...types.TypeID) types.ItemID {
	d := &types.StructDecl{}
	for i := 0; i < n; i++ {
		d.Generics = append(d.Generics, types.GenericParam{Name: w.in.Intern(fmt.Sprintf("T%d", i))})
	}
	for i, f := range fields {
		d.Fields = append(d.Fields, types.FieldDecl{Name: w.in.Intern(fmt.Sprintf("f%d", i)), Type: f})
	}
	return w.item(mod, name, types.KindStruct, d)
}

// instBody starts an instance body of fn: concrete result and parameters.
func (w *world) instBody(fn types.ItemID, ret types.TypeID, params ...types.TypeID) *bb {
	x := &bb{w: w, b: mir.NewBody(fn, w.span(), ret)}
	for i, p := range params {
		x.b.AddParam(p, w.in.Intern(fmt.Sprintf("p%d", i)), x.sp())
	}
	x.b.AddBlock(mir.OriginBody, mir.NoDefer)
	return x
}

func targs(ts ...types.TypeID) []types.GenericArg {
	var as []types.GenericArg
	for _, t := range ts {
		as = append(as, types.TypeArg(t))
	}
	return as
}

// lessFn declares lib.<name>(a: t, b: t): bool, a < b: the Ord impl method
// mono resolves Max's interface call to.
func (w *world) lessFn(name string, t types.TypeID) types.ItemID {
	f := w.fn("lib", name, types.Bool, t, t)
	x := w.body(f, types.Bool, t, t)
	x.assign(0, lp(0), bin(mir.Lt, cp(1), cp(2)))
	x.ret(0)
	x.done()
	return f
}

// maxGeneric declares lib.Max[T: Ord](a: T, b: T): T and serves its
// instances for int and i8.
func (w *world) maxGeneric() types.ItemID {
	p := w.tab.Param(0)
	max := w.genericFn("lib", "Max", 1, p, p, p)
	for _, c := range []struct {
		t    types.TypeID
		less string
	}{{types.Int, "IntLess"}, {types.I8, "I8Less"}} {
		less := w.lessFn(c.less, c.t)
		x := w.instBody(max, c.t, c.t, c.t)
		l := x.local(types.Bool)
		b1, yes, no := x.block(), x.block(), x.block()
		x.call(0, less, lp(l), b1, cp(1), cp(2))
		x.branch(b1, cp(l), yes, no)
		x.assign(yes, lp(0), use(cp(2)))
		x.ret(yes)
		x.assign(no, lp(0), use(cp(1)))
		x.ret(no)
		w.setInstance(max, targs(c.t), x.b)
	}
	return max
}

// constMax declares const mod.name: t = Max[t](a, b).
func (w *world) constMax(mod, name string, max types.ItemID, t types.TypeID, a, b int64) types.ItemID {
	k := w.constItem(mod, name, t, 0)
	x := w.body(k, t)
	b1 := x.block()
	x.callG(0, max, []types.TypeID{t}, lp(0), b1, imm(t, a), imm(t, b))
	x.ret(b1)
	x.done()
	return k
}

// TestGenericMaxConst: a generic Max[T: Ord] reached from consts with two
// type arguments runs each instance body Program.Body serves for exactly
// those arguments; each is entered as its own body (ReadBody of lib.Max with
// the instance's hash), at either stage, because lib is a lower module.
func TestGenericMaxConst(t *testing.T) {
	w := newWorld(t)
	max := w.maxGeneric()
	a := w.constMax("main", "A", max, types.Int, 3, 7)
	b := w.constMax("main", "B", max, types.I8, -5, -9)
	e := w.evaluator(0)
	for _, st := range []types.Stage{types.StageSignatures, types.StageBodies} {
		ra := e.Eval(constReq(w, a, st))
		rb := e.Eval(constReq(w, b, st))
		if ra.Err != nil || rb.Err != nil || w.intOf(ra.Value) != 7 || w.intOf(rb.Value) != -5 {
			t.Fatalf("stage %v: A %+v, B %+v; want 7 and -5", st, ra, rb)
		}
		if got := w.consts.Get(rb.Value).Type; got != types.I8 {
			t.Errorf("B's value has type %d, want i8", got)
		}
		for _, c := range []struct {
			r    types.EvalResult
			targ types.TypeID
		}{{ra, types.Int}, {rb, types.I8}} {
			want := types.Read{Kind: types.ReadBody, Key: w.items.Get(max).Key,
				Hash: source.HashString("test.inst", instKey(max, targs(c.targ)))}
			if !hasExactRead(c.r.Reads, want) {
				t.Errorf("stage %v: reads %v lack the instance's %v", st, c.r.Reads.Sorted(), want)
			}
		}
	}
	// Program.Body was asked for Max only with arguments.
	for _, f := range w.bodyCalls {
		if f == max {
			return
		}
	}
	t.Errorf("Program.Body never asked for Max")
}

func hasExactRead(rs types.ReadSet, want types.Read) bool {
	for _, r := range rs.Sorted() {
		if r == want {
			return true
		}
	}
	return false
}

// sumFields declares lib.SumFields[B](b: &B): int, whose generic body is
// `for f in fields(b) { acc += b.(f); n += f.Name().Len() }; acc*100 + n`,
// and serves its instance for B = lib.Triple{f0, f1, f2: int} unrolled as
// mono unrolls it; and lib.SumTriple(): int calling it on Triple(1, 2, 3).
func (w *world) sumFields() types.ItemID {
	triple, tt := w.structType("lib", "Triple", types.Int, types.Int, types.Int)
	sum := w.genericFn("lib", "SumFields", 1, types.Int, w.tab.Ref(w.tab.Param(0)))
	x := w.instBody(sum, types.Int, w.tab.Ref(tt))
	acc, tmp, nm, ln, n := x.local(types.Int), x.local(types.Int), x.local(types.Str), x.local(types.Int), x.local(types.Int)
	x.assign(0, lp(acc), use(imm(types.Int, 0)))
	x.assign(0, lp(n), use(imm(types.Int, 0)))
	copies := []mir.BlockID{x.block(), x.block(), x.block()}
	exit := x.block()
	x.gotoB(0, copies[0])
	for i, blk := range copies {
		field := x.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: tt}, mir.Projection{Kind: mir.ProjField, A: uint32(i), Type: types.Int})
		x.assign(blk, lp(tmp), use(mir.Copy(field)))
		x.assign(blk, lp(acc), bin(mir.Add, cp(acc), cp(tmp)))
		x.assign(blk, lp(nm), use(mir.Const(types.Str, w.strConst(fmt.Sprintf("f%d", i)))))
		x.assign(blk, lp(ln), mir.Rvalue{Kind: mir.RvLen, Place: lp(nm)})
		x.assign(blk, lp(n), bin(mir.Add, cp(n), cp(ln)))
		next := exit
		if i+1 < len(copies) {
			next = copies[i+1]
		}
		x.gotoB(blk, next)
	}
	x.assign(exit, lp(tmp), bin(mir.Mul, cp(acc), imm(types.Int, 100)))
	x.assign(exit, lp(0), bin(mir.Add, cp(tmp), cp(n)))
	x.ret(exit)
	w.setInstance(sum, targs(tt), x.b)

	caller := w.fn("lib", "SumTriple", types.Int)
	y := w.body(caller, types.Int)
	s, r := y.local(tt), y.local(w.tab.Ref(tt))
	b1 := y.block()
	y.assign(0, lp(s), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: triple,
		Ops: y.b.AddOperands(imm(types.Int, 1), imm(types.Int, 2), imm(types.Int, 3))})
	y.assign(0, lp(r), mir.Rvalue{Kind: mir.RvRef, Place: lp(s)})
	y.callG(0, sum, []types.TypeID{tt}, lp(0), b1, cp(r))
	y.ret(b1)
	y.done()
	return caller
}

// TestFieldsLoopUnrolled: the instance of a fields loop runs as mono
// unrolls it, field names included: 6·100 + 6.
func TestFieldsLoopUnrolled(t *testing.T) {
	w := newWorld(t)
	f := w.sumFields()
	r := w.call(w.evaluator(0), f, 0)
	if r.Err != nil || w.intOf(r.Value) != 606 {
		t.Fatalf("SumTriple() = %+v, want 606", r)
	}
}

// constArgs declares lib.Cap[N: int, T](): int and serves its instance for
// N = 5, T = lib.Triple: the length of a local [N]int, the const argument N
// itself, and SizeOf[T]() kept as CASizeOf: 5 + 5 + 24.
func (w *world) constArgs() types.ItemID {
	_, tt := w.structType("lib", "Triple", types.Int, types.Int, types.Int)
	capF := w.genericFn("lib", "Cap", 2, types.Int)
	fd := w.items.Get(capF).Decl.(*types.FuncDecl)
	fd.Sig.Generics[0].Const, fd.Sig.Generics[0].Type = true, types.Int
	five := types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 5)}
	args := []types.GenericArg{{IsConst: true, Const: five}, types.TypeArg(tt)}
	x := w.instBody(capF, types.Int)
	arr, a, b, c := x.local(w.tab.Array(five, types.Int)), x.local(types.Int), x.local(types.Int), x.local(types.Int)
	x.assign(0, lp(arr), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.Int, 2), Len: five})
	x.assign(0, lp(a), mir.Rvalue{Kind: mir.RvLen, Place: lp(arr)})
	x.assign(0, lp(b), mir.Rvalue{Kind: mir.RvConstArg, Len: five, Type: types.Int})
	x.assign(0, lp(c), mir.Rvalue{Kind: mir.RvConstArg, Len: types.ConstArg{Kind: types.CASizeOf, Type: tt}, Type: types.Int})
	x.assign(0, lp(a), bin(mir.Add, cp(a), cp(b)))
	x.assign(0, lp(0), bin(mir.Add, cp(a), cp(c)))
	x.ret(0)
	w.setInstance(capF, args, x.b)

	k := w.constItem("main", "K", types.Int, 0)
	y := w.body(k, types.Int)
	b1 := y.block()
	y.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: capF, Args: y.b.AddGenericArgs(args...)}},
		Dest: lp(0), Target: b1, Spawn: mir.NoLocal})
	y.ret(b1)
	y.done()
	return k
}

// TestConstArgsInInstance: a const generic argument reaches the instance
// as an array length, an RvConstArg value and a SizeOf of the type argument.
func TestConstArgsInInstance(t *testing.T) {
	w := newWorld(t)
	k := w.constArgs()
	r := w.evaluator(0).Eval(constReq(w, k, types.StageSignatures))
	if r.Err != nil || w.intOf(r.Value) != 34 {
		t.Fatalf("K = %+v, want 34", r)
	}
}

// mutexNew declares, in the shape of std (std/core/sync_mutex.mk,
// std/raw/raw.mk), raw.Atomic[T] { v: T } with its bodiless intrinsic
// method New, and core.Mutex[T] { state: raw.Atomic[u32], value: T } with
// Mutex[T].New(x: T) = Mutex[T](state: raw.Atomic[u32].New(0), value: x),
// serving New's instance for T = arg. It returns Mutex[arg] and New.
func (w *world) mutexNew(arg types.TypeID) (types.TypeID, types.ItemID) {
	atomic := w.genericStruct("std.raw", "Atomic", 1, w.tab.Param(0))
	at32 := w.tab.Named(atomic, targs(types.U32))
	aNew := w.method("std.raw", "Atomic", "New", 1, w.tab.Named(atomic, targs(w.tab.Param(0))), w.tab.Param(0))
	fd := w.items.Get(aNew).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	mutex := w.genericStruct("std.core", "Mutex", 1, at32, w.tab.Param(0))
	mt := w.tab.Named(mutex, targs(arg))
	mNew := w.method("std.core", "Mutex", "New", 1, w.tab.Named(mutex, targs(w.tab.Param(0))), w.tab.Param(0))
	x := w.instBody(mNew, mt, arg)
	st := x.local(at32)
	b1 := x.block()
	x.callG(0, aNew, []types.TypeID{types.U32}, lp(st), b1, imm(types.U32, 0))
	x.assign(b1, lp(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: mutex, Type: mt,
		Ops: x.b.AddOperands(cp(st), mir.Move(lp(1)))})
	x.ret(b1)
	w.setInstance(mNew, targs(arg), x.b)
	return mt, mNew
}

// TestMutexNewAtCompileTime is doc/memory.md's initializer, `var Latest:
// Mutex[?&i32] = Mutex.New(none)`: the declaration evaluation runs the
// instance body of Mutex[?&i32].New, which calls the intrinsic
// raw.Atomic[u32].New, and gives a 16-byte zero value (a 4-byte lock word,
// padding, and the none of the niche ?&i32) with no reloc.
func TestMutexNewAtCompileTime(t *testing.T) {
	w := newWorld(t)
	opt := w.tab.Optional(w.tab.Ref(types.I32))
	mt, mNew := w.mutexNew(opt)
	g := w.item("main", "Latest", types.KindGlobal, &types.GlobalDecl{Type: mt})
	x := w.body(g, mt)
	o := x.local(opt)
	b1 := x.block()
	x.assign(0, lp(o), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggNone, Type: opt})
	x.callG(0, mNew, []types.TypeID{opt}, lp(0), b1, cp(o))
	x.ret(b1)
	x.done()
	e := w.evaluator(0)
	for _, st := range []types.Stage{types.StageSignatures, types.StageBodies} {
		r := e.Eval(constReq(w, g, st))
		if r.Err != nil || r.Value == types.NoConst {
			t.Fatalf("stage %v: Latest = %+v", st, r)
		}
		v := w.consts.Get(r.Value)
		if v.Type != mt || len(v.Bytes) != 16 || len(v.Relocs) != 0 {
			t.Fatalf("stage %v: Latest = %+v, want 16 zero bytes of Mutex[?&i32]", st, v)
		}
		for _, c := range v.Bytes {
			if c != 0 {
				t.Fatalf("stage %v: Latest bytes %v, want zero", st, v.Bytes)
			}
		}
		want := types.Read{Kind: types.ReadBody, Key: types.MemberKey("std.core", "Mutex", "New"),
			Hash: source.HashString("test.inst", instKey(mNew, targs(opt)))}
		if !hasExactRead(r.Reads, want) {
			t.Errorf("stage %v: reads %v lack %v", st, r.Reads.Sorted(), want)
		}
	}
	if v, ok := e.Value(g); !ok || len(w.consts.Get(v).Bytes) != 16 {
		t.Errorf("Value(Latest) = %v %v", v, ok)
	}
}

// TestSameModuleTypeArgStops: a lower generic instance whose argument is a
// same-module type stops at StageSignatures, through a method of a generic
// type as through a function, and runs at StageBodies.
func TestSameModuleTypeArgStops(t *testing.T) {
	w := newWorld(t)
	loc, lt := w.structType("main", "Local", types.Int)
	mt, mNew := w.mutexNew(lt)
	k := w.constItem("main", "K", mt, 0)
	x := w.body(k, mt)
	l := x.local(lt)
	b1 := x.block()
	x.assign(0, lp(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: loc, Ops: x.b.AddOperands(imm(types.Int, 9))})
	x.callG(0, mNew, []types.TypeID{lt}, lp(0), b1, cp(l))
	x.ret(b1)
	x.done()
	r := w.evaluator(0).Eval(constReq(w, k, types.StageSignatures))
	if r.Err == nil || r.Err.Kind != types.EvalStage {
		t.Fatalf("at StageSignatures: %+v, want E0908", r)
	}
	if got := r.Err.Items[len(r.Err.Items)-1]; got != loc {
		t.Errorf("E0908 items %v end in %v, want Local", r.Err.Items, got)
	}
	r = w.evaluator(0).Eval(constReq(w, k, types.StageBodies))
	if r.Err != nil {
		t.Fatalf("at StageBodies: %+v", r)
	}
	if v := w.consts.Get(r.Value); len(v.Bytes) != 16 || types.LoadUint(v.Bytes[8:], 8) != 9 {
		t.Errorf("K = %v, want value 9 at offset 8", v.Bytes)
	}
}

// TestInstanceCompiledOncePerBody: an instance body served twice (the same
// *mir.Body, as the driver must serve it) is compiled once per Evaluator; the
// second evaluation's steps and value are the first's.
func TestInstanceCompiledOncePerBody(t *testing.T) {
	w := newWorld(t)
	max := w.maxGeneric()
	a := w.constMax("main", "A", max, types.Int, 3, 7)
	b := w.constMax("main", "B", max, types.Int, 8, 1)
	e := w.evaluator(0)
	ra, rb := e.Eval(constReq(w, a, types.StageBodies)), e.Eval(constReq(w, b, types.StageBodies))
	if ra.Err != nil || rb.Err != nil || w.intOf(ra.Value) != 7 || w.intOf(rb.Value) != 8 || ra.Steps != rb.Steps {
		t.Fatalf("A %+v, B %+v", ra, rb)
	}
	e.codeMu.Lock()
	n := 0
	for b := range e.code {
		if b.Item == max {
			n++
		}
	}
	e.codeMu.Unlock()
	if n != 1 {
		t.Errorf("%d compiled bodies of Max, want 1", n)
	}
}

func init() {
	goldenCases = append(goldenCases,
		struct {
			name string
			mk   func(w *world) (types.ItemID, []types.ConstID)
		}{"generic Max[int] instance", func(w *world) (types.ItemID, []types.ConstID) {
			max := w.maxGeneric()
			f := w.fn("lib", "MaxCaller", types.Int)
			x := w.body(f, types.Int)
			b1 := x.block()
			x.callG(0, max, []types.TypeID{types.Int}, lp(0), b1, imm(types.Int, 3), imm(types.Int, 7))
			x.ret(b1)
			x.done()
			return f, nil
		}},
		struct {
			name string
			mk   func(w *world) (types.ItemID, []types.ConstID)
		}{"fields loop unrolled, 3 fields", func(w *world) (types.ItemID, []types.ConstID) {
			return w.sumFields(), nil
		}},
	)
}
