// Why: the loan pass must stay fast on very large functions: one function of
// up to 64,000 expanded operations in at most 1 s (a target, not a
// measurement).
// What: generated worst-case bodies (copyChainLoop, borrowChainScopeEnds,
// scopeEndsWideLive, storeFanout, typicalBody), TestBudget64kOneSecond and
// the benchmarks over them.
// How: builds each shape as hand-built MIR, validates it, then times Check.

package borrow

import (
	"runtime"
	"testing"
	"time"

	"mk/internal/mir"
	"mk/internal/types"
)

// copyChainLoop is the worst shape for the loan pass: a loop whose body is the
// chain x1 := &x0; x2 := x1; ...; xn := x(n-1), then Use(xn), which keeps the
// chain live, with ops statements in the chain.
func copyChainLoop(k *kit, ops int) *mb {
	ri := refI32(k)
	m := k.body(k.sig("CopyChain", []types.Param{k.param("c", types.Bool)}, types.Void, types.EmptyErrors))
	x0 := m.user("x0", types.I32, false)
	m.set(x0, use(imm(types.I32, 0)))
	header, body, exit := m.block(), m.block(), m.block()
	m.b.Loops = append(m.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Holder: mir.NoLocal, Span: m.sp()})
	m.jump(header)
	m.at(header)
	m.branch(cp(m.local(1)), body, exit)
	m.at(body)
	xs := make([]mir.LocalID, ops)
	for i := range xs {
		xs[i] = m.user("x", ri, false)
	}
	m.set(xs[0], ref(m.local(x0)))
	for i := 1; i < ops; i++ {
		m.set(xs[i], use(cp(m.local(xs[i-1]))))
	}
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(xs[ops-1])))
	for i := ops - 1; i >= 0; i-- {
		m.end(xs[i])
	}
	m.jump(header)
	m.at(exit)
	m.end(x0)
	m.ret(mir.ExitNormal)
	return m
}

// borrowChainScopeEnds is a shape that stresses the purge: x_i := &x_(i-1)
// for ops statements (every x_i borrowed, each held set distinct), Use(x_n),
// then the scope end of every x_i, innermost first.
func borrowChainScopeEnds(k *kit, ops int) *mb {
	m := k.body(k.sig("BorrowChain", nil, types.Void, types.EmptyErrors))
	ty := types.I32
	xs := make([]mir.LocalID, ops+1)
	xs[0] = m.user("x", ty, false)
	m.set(xs[0], use(imm(types.I32, 0)))
	for i := 1; i <= ops; i++ {
		ty = k.tab.Ref(ty)
		xs[i] = m.user("x", ty, false)
		m.set(xs[i], ref(m.local(xs[i-1])))
	}
	m.callv("Use", []types.TypeID{k.tab.Get(ty).Elem}, argCopy(m.local(xs[ops])))
	for i := ops; i >= 0; i-- {
		m.end(xs[i])
	}
	m.ret(mir.ExitNormal)
	return m
}

// scopeEndsWideLive is a slow-case shape for scope ends: wide locals live to
// the end of the body (one array literal reads them all), then narrow
// borrowed inner locals each ending while all the others are live.
func scopeEndsWideLive(k *kit, wide, narrow int) *mb {
	m := k.body(k.sig("WideLive", nil, types.Void, types.EmptyErrors))
	outer := make([]mir.Operand, wide)
	for i := range outer {
		o := m.user("o", types.I32, false)
		m.set(o, use(imm(types.I32, uint64(i))))
		outer[i] = cp(m.local(o))
	}
	z := m.user("z", types.I32, true)
	for j := 0; j < narrow; j++ {
		b := m.user("b", types.I32, false)
		r := m.user("r", refI32(k), false)
		m.set(b, use(imm(types.I32, 1)))
		m.set(r, ref(m.local(b)))
		m.set(z, use(cp(m.pl(r, deref(types.I32)))))
		m.end(r, b)
	}
	at := k.tab.Array(k.lenArg(uint64(wide)), types.I32)
	arr := m.user("arr", at, false)
	m.set(arr, m.agg(mir.AggArray, types.NoItem, at, outer...))
	m.ret(mir.ExitNormal)
	return m
}

// storeFanout is 1,000 into calls into one parameter inside a loop.
func storeFanout(k *kit, calls int) *mb {
	k.lib()
	ri := refI32(k)
	cellT := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(ri)})
	m := k.body(k.sig("Fanout", []types.Param{k.param("c", k.tab.Ref(cellT)), {Name: k.in.Intern("v"), Type: ri, Into: 1},
		k.param("go", types.Bool)}, types.Void, types.EmptyErrors))
	header, body, exit := m.block(), m.block(), m.block()
	m.b.Loops = append(m.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Holder: mir.NoLocal, Span: m.sp()})
	m.jump(header)
	m.at(header)
	m.branch(cp(m.local(3)), body, exit)
	m.at(body)
	for i := 0; i < calls; i++ {
		m.callv("Cell.Store", []types.TypeID{ri}, argCopy(m.local(1)), argCopy(m.local(2)))
	}
	m.jump(header)
	m.at(exit)
	m.ret(mir.ExitNormal)
	return m
}

// The shapes validate (on small instances: Validate's type walk is
// quadratic in the nesting of borrowChainScopeEnds' reference types) and
// are accepted.
func TestBenchShapesValidate(t *testing.T) {
	for name, build := range map[string]func(*kit) *mb{
		"copychain":   func(k *kit) *mb { return copyChainLoop(k, 50) },
		"borrowchain": func(k *kit) *mb { return borrowChainScopeEnds(k, 50) },
		"widelive":    func(k *kit) *mb { return scopeEndsWideLive(k, 20, 20) },
		"fanout":      func(k *kit) *mb { return storeFanout(k, 20) },
		"typical":     func(k *kit) *mb { return typicalBody(k, 2000) },
	} {
		k := newKit(t)
		m := build(k)
		r := m.check(t)
		if r.Diags != nil {
			t.Fatalf("%s: %v", name, got(r))
		}
		if name == "typical" {
			if stmtCount(m.b) < 2000 {
				t.Fatalf("typical: %d statements", stmtCount(m.b))
			}
			t.Logf("typical: %d statements and terminators, %d blocks, %d locals", stmtCount(m.b), len(m.b.Blocks), len(m.b.Locals))
		}
	}
}

func timeCheck(k *kit, m *mb) (time.Duration, *Result) {
	env := k.env(m.sig)
	start := time.Now()
	r := Check(m.b, env)
	return time.Since(start), r
}

// TestBudget64kOneSecond is the budget test: the 64,000-operation
// copy chain in a loop and the 64,000-statement borrow chain with its scope
// ends each check in at most 1 s. It logs the numbers.
func TestBudget64kOneSecond(t *testing.T) {
	const ops = 64000
	for _, tc := range []struct {
		name  string
		build func(*kit) *mb
	}{
		{"CopyChainLoop", func(k *kit) *mb { return copyChainLoop(k, ops) }},
		{"BorrowChainScopeEnds", func(k *kit) *mb { return borrowChainScopeEnds(k, ops) }},
	} {
		k := newKit(t)
		m := tc.build(k)
		d, r := timeCheck(k, m)
		t.Logf("%s: %d operations, %v (GOOS=%s GOARCH=%s, %d CPUs)", tc.name, ops, d, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
		if r.Diags != nil {
			t.Fatalf("%s: unexpected diagnostics %v", tc.name, got(r)[:min(5, len(r.Diags))])
		}
		if !timingRun() {
			t.Logf("%s: %s: the 1 s budget is not asserted (took %v)", tc.name, timingOff, d)
		} else if d > time.Second {
			t.Errorf("%s took %v, over the 1 s budget", tc.name, d)
		}
	}
}

func benchCheck(b *testing.B, build func(*kit) *mb) {
	k := newKit(b)
	m := build(k)
	env := k.env(m.sig)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Check(m.b, env)
	}
}

func BenchmarkCopyChainLoop(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return copyChainLoop(k, 64000) })
}

func BenchmarkBorrowChainScopeEnds(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return borrowChainScopeEnds(k, 64000) })
}

func BenchmarkScopeEndsWideLive(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return scopeEndsWideLive(k, 32000, 32000) })
}

// typicalBody is a typical body: at least n statements made of the accept
// shapes of the unit tests, each segment over fresh locals so the body is
// clean: a borrow and its use, disjoint field borrows and a write, an element
// borrow behind lower's bounds check, `for x in mut xs`, `try list.Push(a, r)`
// with its error edge and into flow, acquire and close, a scope with a spawn
// and a later read, Exchange's stores and a branch that assigns.
func typicalBody(k *kit, n int) *mb {
	k.lib()
	k.taskLib()
	hl := k.named("HeapList", k.Res)
	ri := refI32(k)
	mi := k.tab.Mut(types.I32)
	m := k.body(k.sig("Typical", []types.Param{k.param("c", types.Bool), k.param("xs", k.tab.MutSlice(types.I32)),
		k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(hl)), k.param("io", k.Io)}, types.Void, k.errs("OutOfMemory")))
	for seg := 0; stmtCount(m.b) < n; seg++ {
		switch seg % 9 {
		case 0: // x := 1; r := &x; Use(r)
			x, r := m.user("x", types.I32, false), m.user("r", ri, false)
			m.set(x, use(imm(types.I32, 1)))
			m.set(r, ref(m.local(x)))
			m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(r)))
			m.end(r, x)
		case 1: // var p := MakePoint(); q := &p.x; w := mut p.y; *w = 1; Use(q)
			p, q, w := m.user("p", k.Point, true), m.user("q", ri, false), m.user("w", mi, false)
			nx := m.block()
			m.call(k.item("MakePoint"), nil, nil, m.local(p), nx)
			m.at(nx)
			m.set(q, ref(m.pl(p, fld(0, types.I32))))
			m.set(w, mutRef(m.pl(p, fld(1, types.I32))))
			m.assign(m.pl(w, deref(types.I32)), use(imm(types.I32, 1)))
			m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(q)))
			m.end(w, q, p)
		case 2: // e := mut xs[i]; *e = 1
			i, e := m.temp(types.Int), m.user("e", mi, false)
			m.set(i, use(imm(types.Int, uint64(seg%4))))
			m.boundsCheck(m.local(2), i)
			m.set(e, mutRef(m.pl(2, index(i, types.I32))))
			m.assign(m.pl(e, deref(types.I32)), use(imm(types.I32, 1)))
			m.end(e, i)
		case 3: // for x in mut xs { *x = 1 }
			m.mutLoop(2, func(x mir.LocalID) {
				m.assign(m.pl(x, deref(types.I32)), use(imm(types.I32, 1)))
			})
		case 4: // r := Acquire("a"); onerr r.Close(); try list.Push(a, r)
			r := m.user("r", k.Res, false)
			m.acquire(r)
			m.tryPush(k.Res, 4, 3, r, func() { m.close(r) }) // onerr r.Close()
			m.end(r)
		case 5: // r := Acquire("a"); r.Close()
			r := m.user("r", k.Res, false)
			m.acquire(r)
			m.close(r)
			m.end(r)
		case 6: // var v := 0; scope s := io.Scope() { s.go Bump(mut v) }; Use(&v)
			v := m.user("v", types.I32, true)
			m.set(v, use(imm(types.I32, 0)))
			s := m.openScope()
			tm := m.temp(mi)
			m.set(tm, mutRef(m.local(v)))
			m.spawn(s, "Bump", argMove(m.local(tm)))
			m.end(tm)
			m.join(s)
			m.end(s)
			m.useRef(m.local(v), types.I32)
			m.end(v)
		case 7: // var x := &a1; var y := &b1; Exchange(mut x, mut y); Use(x)
			a1, b1 := m.user("a1", types.I32, false), m.user("b1", types.I32, false)
			x, y := m.user("x", ri, true), m.user("y", ri, true)
			m.set(a1, use(imm(types.I32, 1)))
			m.set(b1, use(imm(types.I32, 2)))
			m.set(x, ref(m.local(a1)))
			m.set(y, ref(m.local(b1)))
			mx, my := m.temp(k.tab.Mut(ri)), m.temp(k.tab.Mut(ri))
			m.set(mx, mutRef(m.local(x)))
			m.set(my, mutRef(m.local(y)))
			m.callv("Exchange", []types.TypeID{ri}, argMove(m.local(mx)), argMove(m.local(my)))
			m.end(my, mx)
			m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(x)))
			m.end(y, x, b1, a1)
		case 8: // n: i32; if c { n = 1 } else { n = 2 }; Use(&n)
			v := m.user("n", types.I32, false)
			yes, no, join := m.block(), m.block(), m.block()
			m.branch(cp(m.local(1)), yes, no)
			m.at(yes)
			m.set(v, use(imm(types.I32, 1)))
			m.jump(join)
			m.at(no)
			m.set(v, use(imm(types.I32, 2)))
			m.jump(join)
			m.at(join)
			m.useRef(m.local(v), types.I32)
			m.end(v)
		}
	}
	m.okVoid()
	return m
}

func BenchmarkTypicalBody(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return typicalBody(k, 2000) })
}

func BenchmarkStoreFanout(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return storeFanout(k, 1000) })
}
