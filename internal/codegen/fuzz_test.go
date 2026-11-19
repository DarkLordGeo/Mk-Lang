// Why: EmitPack must never raise an ICE on well-formed MIR, and its output
// must be deterministic.
// What: FuzzEmit: on any well-formed scalar body built from the input
// (arithmetic, compares, casts, unary operators, branches, calls of a
// sibling), EmitPack raises no ICE on any target and two emissions give the
// same bytes; TestFuzzEmitCompiles compiles the seeds with clang.
// How: fuzzBody decodes the input into two functions; fuzzPGOConfig adds a
// profile so the PGO paths are fuzzed too.

package codegen

import (
	"bytes"
	"sort"
	"testing"

	"mk/internal/mir"
	"mk/internal/pgo"
	"mk/internal/types"
)

var fuzzScalars = []types.TypeID{types.I8, types.I16, types.I32, types.I64, types.U8, types.U16, types.U32,
	types.U64, types.Int, types.Uint, types.F32, types.F64, types.Bool, types.Char}

// fuzzBody builds F(a: T1, b: T2): R and G(x: T1): T1 from data.
func fuzzBody(e *testEnv, data []byte) []*mir.Body {
	pos := 0
	next := func() int {
		if pos >= len(data) {
			return 0
		}
		pos++
		return int(data[pos-1])
	}
	pick := func() types.TypeID { return fuzzScalars[next()%len(fuzzScalars)] }
	t1, t2, rt := pick(), pick(), pick()
	g := e.body(e.fn("G", []types.TypeID{t1}, t1, 0))
	g.assign(place(0), use(mir.Copy(place(1))), g.at(1, 1))
	g.ret(g.at(1, 1))

	f := e.body(e.fn("F", []types.TypeID{t1, t2}, rt, 0))
	pool := map[types.TypeID][]mir.LocalID{t1: {1}, t2: {2}}
	if t1 == t2 {
		pool[t1] = []mir.LocalID{1, 2}
	}
	var order []types.TypeID // insertion order of pool's types (determinism)
	order = append(order, t1)
	if t2 != t1 {
		order = append(order, t2)
	}
	add := func(t types.TypeID, l mir.LocalID) {
		if _, ok := pool[t]; !ok {
			order = append(order, t)
		}
		pool[t] = append(pool[t], l)
	}
	of := func(t types.TypeID) mir.LocalID { ls := pool[t]; return ls[next()%len(ls)] }
	kind := func(t types.TypeID) types.TypeKind { return e.tt.Kind(t) }
	signed := func(t types.TypeID) bool { return e.tt.Get(t).Signed }
	bits := func(t types.TypeID) int {
		s, _ := e.sa(t)
		return int(s) * 8
	}
	line := uint32(2)
	sp := func() mir.SpanID { line++; return f.at(line, uint32(next()%80+1)) }
	for steps := next() % 10; steps > 0; steps-- {
		t := order[next()%len(order)]
		a := of(t)
		k := kind(t)
		switch op := next() % 6; {
		case op == 0 && (k == types.KInt || k == types.KFloat):
			ops := []mir.BinOp{mir.Add, mir.Sub, mir.Mul, mir.Div, mir.Rem}
			if k == types.KInt {
				ops = append(ops, mir.AddWrap, mir.SubWrap, mir.MulWrap, mir.Shl, mir.Shr, mir.BitAnd, mir.BitOr, mir.BitXor)
			}
			bo := ops[next()%len(ops)]
			b := mir.Copy(place(of(t)))
			if k == types.KInt && next()%2 == 0 {
				b = mir.Imm(t, uint64(next()))
			}
			l := f.local(t)
			f.assign(place(l), bin(bo, mir.Copy(place(a)), b), sp())
			add(t, l)
		case op == 1:
			ops := []mir.BinOp{mir.Eq, mir.Ne}
			if k != types.KBool {
				ops = append(ops, mir.Lt, mir.Le, mir.Gt, mir.Ge)
			}
			l := f.local(types.Bool)
			f.assign(place(l), bin(ops[next()%len(ops)], mir.Copy(place(a)), mir.Copy(place(of(t)))), sp())
			add(types.Bool, l)
		case op == 2 && k != types.KBool:
			to := pick()
			tk := kind(to)
			if tk == types.KBool || (k == types.KChar && tk == types.KFloat) || (k == types.KChar && tk == types.KChar) {
				continue
			}
			ck := mir.CastChecked
			switch {
			case k == types.KInt && tk == types.KInt && bits(to) < bits(t) && next()%2 == 0:
				ck = mir.CastTrunc
			case k == types.KInt && tk == types.KInt && bits(to) > bits(t) && signed(to) == signed(t):
				ck = mir.CastLossless
			}
			l := f.local(to)
			f.assign(place(l), mir.Rvalue{Kind: mir.RvCast, Cast: ck, A: mir.Copy(place(a)), Type: to}, sp())
			add(to, l)
		case op == 3:
			var un mir.UnOp
			switch {
			case k == types.KBool:
				un = mir.Not
			case k == types.KFloat || (k == types.KInt && signed(t) && next()%2 == 0):
				un = mir.Neg
			case k == types.KInt:
				un = mir.BitNot
			default:
				continue
			}
			l := f.local(t)
			f.assign(place(l), mir.Rvalue{Kind: mir.RvUnary, Un: un, A: mir.Copy(place(a))}, sp())
			add(t, l)
		case op == 4 && len(pool[types.Bool]) > 0:
			// if c { l = a } else { l = <another of t> }
			c := of(types.Bool)
			l := f.local(t)
			yes, no, join := f.newBlock(), f.newBlock(), f.newBlock()
			f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)), Cases: f.b.AddCases(
				mir.SwitchCase{Value: 1, Target: yes}, mir.SwitchCase{Value: 0, Target: no}), Default: mir.NoBlock, Span: sp()})
			f.cur = yes
			f.assign(place(l), use(mir.Copy(place(a))), sp())
			f.term(mir.Terminator{Kind: mir.TermGoto, Target: join, Span: sp()})
			f.cur = no
			f.assign(place(l), use(mir.Copy(place(of(t)))), sp())
			f.term(mir.Terminator{Kind: mir.TermGoto, Target: join, Span: sp()})
			f.cur = join
			add(t, l)
		case op == 5 && t == t1:
			l := f.local(t1)
			f.callTo(direct(g.item), place(l), mir.NoLocal, sp(), mir.Copy(place(a)))
			add(t1, l)
		}
	}
	if ls := pool[rt]; len(ls) > 0 {
		f.assign(place(0), use(mir.Copy(place(ls[next()%len(ls)]))), sp())
	} else {
		var o mir.Operand
		switch kind(rt) {
		case types.KFloat:
			o = mir.Const(rt, e.floatConst(rt, 0))
		default:
			o = mir.Imm(rt, 0)
		}
		f.assign(place(0), use(o), sp())
	}
	f.ret(sp())
	return []*mir.Body{f.b, g.b}
}

// fuzzStd adds the std functions a generated body may call by key: float %
// calls std.math.Rem64 and Rem32, which a real build holds as
// RootRuntime instances; here they are remBody's stand-ins in the pack.
func fuzzStd(e *testEnv, bodies []*mir.Body) []*mir.Body {
	return append(bodies, remBody(e, types.F64, types.I64), remBody(e, types.F32, types.I32))
}

func FuzzEmit(f *testing.F) {
	for _, seed := range [][]byte{
		nil, {0, 1, 2}, {3, 3, 13, 9, 0, 0, 1, 1, 2, 5, 4, 4, 4, 0, 0},
		{10, 11, 12, 7, 2, 2, 7, 5, 4, 1, 3, 0, 9, 9, 4, 0, 1, 2, 3},
		{8, 8, 12, 9, 1, 1, 1, 4, 3, 0, 2, 13, 4, 4, 1, 5, 0, 0, 0, 0},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		e := newTestEnv()
		bodies := fuzzStd(e, fuzzBody(e, data))
		for _, b := range bodies {
			if err := b.Validate(&mir.Env{Types: e.tt}); err != nil {
				t.Fatalf("the generator built an invalid body: %v\n%s", err, (&mir.Printer{Types: e.tt}).Print(b))
			}
		}
		u := unitOf(e, bodies)
		for _, target := range types.Targets() {
			var x, y bytes.Buffer
			if _, err := EmitPack(u, 0, e, Config{Target: target}, &x); err != nil {
				t.Fatalf("%s: %v", target.Name, err)
			}
			if _, err := EmitPack(u, 0, e, Config{Target: target, Profile: types.Release}, &y); err != nil {
				t.Fatalf("%s release: %v", target.Name, err)
			}
			var z bytes.Buffer
			if _, err := EmitPack(u, 0, e, Config{Target: target}, &z); err != nil || !bytes.Equal(x.Bytes(), z.Bytes()) {
				t.Fatalf("%s: two emissions differ (%v)", target.Name, err)
			}
			var p bytes.Buffer
			if _, err := EmitPack(u, 0, e, fuzzPGOConfig(u, target, data), &p); err != nil {
				t.Fatalf("%s pgo: %v", target.Name, err)
			}
		}
	})
}

// TestFuzzEmitCompiles compiles the fuzzer's seeds and a sweep of generated
// bodies with clang, so the generator's bodies are known to be valid IR,
// not only ICE-free.
func TestFuzzEmitCompiles(t *testing.T) {
	for i := 0; i < 24; i++ {
		data := make([]byte, 40)
		for j := range data {
			data[j] = byte((i*37 + j*11 + j*j*3) % 251)
		}
		e := newTestEnv()
		u := unitOf(e, fuzzStd(e, fuzzBody(e, data)))
		ll := emit(t, e, u, types.X86_64Linux, types.Dev)
		clangC(t, types.X86_64Linux, t.TempDir(), "fz", ll, "-O1")
		var p bytes.Buffer
		if _, err := EmitPack(u, 0, e, fuzzPGOConfig(u, types.X86_64Linux, data), &p); err != nil {
			t.Fatal(err)
		}
		clangC(t, types.X86_64Linux, t.TempDir(), "fzpgo", p.String(), "-O1")
	}
}

// fuzzPGOConfig is a release --pgo-gen build that also applies a profile
// with a record per member: counts from data, every other one with a wrong
// length or checksum (no ICE, no index past n).
func fuzzPGOConfig(u *Unit, target types.Target, data []byte) Config {
	var recs []pgo.Record
	for i, f := range u.Funcs {
		if f.Body == nil {
			continue
		}
		n := planCounters(f.Body).n
		switch i % 3 {
		case 1:
			n++ // wrong length: stale
		case 2:
			n = 0
		}
		c := make([]uint64, n)
		for k := range c {
			if len(data) > 0 {
				c[k] = uint64(data[(i+k)%len(data)]) << (k % 40)
			}
		}
		sum := pgoChecksum(f.Body)
		if i%4 == 3 {
			sum[0] ^= 1
		}
		recs = append(recs, pgo.Record{Key: f.Symbol, Checksum: sum, Counts: c})
	}
	sort.Slice(recs, func(a, b int) bool { return recs[a].Key < recs[b].Key })
	return Config{Target: target, Profile: types.Release, PGOGen: true, PGO: &pgo.Profile{Records: recs}}
}
