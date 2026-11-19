// Why: without a filter, N sequential exclusive borrows of one local cost
// N² candidate tests (scale_test.go's BenchmarkSeqMutBorrows64k shows it).
// What: tests of the state-local candidate filter: each state keeps, per root
// with more than onFilterMin loans, a superset of the loans on it that some
// root holds (entry.on, kept by checker.put); access tests only the cached
// candidates in it.
// How: compares filtered and unfiltered verdicts, checks the superset, and
// times 64,000 operations against the 1 s target.

package borrow

import (
	"reflect"
	"runtime"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// TestOnFilterEqualsUnfiltered: with the filter on every root (onMin 0),
// with the superset path forced (pruneMax 0 and 1) and with the defaults,
// every generated body, the long ones and the slow fuzz input's head included,
// and the sequential and nested shapes of scale_test.go, gives the
// diagnostics, facts and locations of a check with the filter off.
func TestOnFilterEqualsUnfiltered(t *testing.T) {
	type cfg struct{ onMin, pruneMax int }
	cfgs := []cfg{{0, onPruneMax}, {0, 0}, {0, 1}, {onFilterMin, onPruneMax}}
	// The generated bodies seldom have a candidate the filter drops; the
	// sequential and nested shapes have mostly such candidates.
	var builds []func(k *kit) *mb
	for _, data := range scaleSeeds(t) {
		builds = append(builds, func(k *kit) *mb { return genBody(k, data) })
	}
	builds = append(builds,
		func(k *kit) *mb { return seqMutBorrows(k, 300) },
		func(k *kit) *mb { return nestedScopeSpawns(k, 20) },
		func(k *kit) *mb { return copiedThenEnded(k, 12) })
	ran, diags := 0, 0
	for _, build := range builds {
		k := newKit(t)
		m := build(k)
		if m.b.Validate(k.mirEnv(m.sig)) != nil {
			continue
		}
		ran++
		ref := newChecker(m.b, k.env(m.sig))
		ref.noOnFilter = true
		want := ref.run()
		for _, f := range cfgs {
			c := newChecker(m.b, k.env(m.sig))
			c.onMin, c.pruneMax = f.onMin, f.pruneMax
			r := c.run()
			if len(r.Diags) != len(want.Diags) {
				t.Fatalf("%+v: %d diagnostics, %d unfiltered\n%v\n%v", f, len(r.Diags), len(want.Diags), got(r), got(want))
			}
			for i := range r.Diags {
				if !reflect.DeepEqual(r.Diags[i], want.Diags[i]) {
					t.Fatalf("%+v: diagnostic %d differs:\n%+v\nunfiltered:\n%+v", f, i, *r.Diags[i], *want.Diags[i])
				}
			}
			if !reflect.DeepEqual(r.Facts, want.Facts) {
				t.Fatalf("%+v: facts differ", f)
			}
		}
		diags += len(want.Diags)
	}
	if ran < 300 || diags == 0 {
		t.Fatalf("only %d bodies, %d diagnostics", ran, diags)
	}
	t.Logf("%d bodies, %d diagnostics, %d configurations", ran, diags, len(cfgs))
}

// TestOnSuperset: in every state after every statement and on every
// out-state, every loan some root holds is in on of its place root (the
// filter's invariant), with the filter on every root; and with pruning exact
// (pruneMax unbounded) on is exactly the held loans of the root, so the
// superset is only what the bound leaves.
func TestOnSuperset(t *testing.T) {
	for _, pruneMax := range []int{onPruneMax, 1 << 30} {
		states, held := 0, 0
		ran := 0
		for _, data := range genSeeds(400) {
			k := newKit(t)
			m := genBody(k, data)
			if m.b.Validate(k.mirEnv(m.sig)) != nil {
				continue
			}
			c := newChecker(m.b, k.env(m.sig))
			c.onMin, c.pruneMax = 0, pruneMax
			c.run()
			c.report = false
			ran++
			eachState(c, func(st *state) {
				states++
				want := make(map[rootID][]uint32)
				for h := range c.roots {
					for _, l := range c.sets.get(c.held(st, rootID(h))) {
						if pr := c.placeRoot(loanID(l)); pr != noRoot {
							want[pr] = append(want[pr], l)
							held++
						}
					}
				}
				for r := range c.roots {
					on := st.get(rootID(r)).on
					hs := c.sets.fromUnsorted(append([]uint32(nil), want[rootID(r)]...))
					if c.sets.minus(hs, on) != emptySet {
						t.Fatalf("root %d: held loans %v not all in on %v", r, c.sets.get(hs), c.sets.get(on))
					}
					if pruneMax > len(c.loans) && hs != on {
						t.Fatalf("root %d, exact pruning: on %v, held loans %v", r, c.sets.get(on), c.sets.get(hs))
					}
				}
			})
		}
		if ran < 100 || states == 0 || held == 0 {
			t.Fatalf("only %d bodies, %d states, %d held loans", ran, states, held)
		}
	}
}

// TestBudgetSeqMutBorrows64k holds 21,333 sequential `UseMut(mut n)` calls
// (64,000 operations, clean) to the 1 s target for one function.
func TestBudgetSeqMutBorrows64k(t *testing.T) {
	k := newKit(t)
	m := seqMutBorrows(k, 64000)
	ops := stmtCount(m.b)
	d, r := timeCheck(k, m)
	t.Logf("SeqMutBorrows: %d operations, %v (GOOS=%s GOARCH=%s, %d CPUs)", ops, d, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	if r.Diags != nil {
		t.Fatalf("%v", got(r)[:min(5, len(r.Diags))])
	}
	if !timingRun() {
		t.Logf("%s: the 1 s budget is not asserted (took %v)", timingOff, d)
	} else if d > time.Second {
		t.Errorf("SeqMutBorrows took %v, over the 1 s budget", d)
	}
}

// copiedThenEnded is n rounds of `a := &x; b := a; end a; Use(b)` over one
// var x, then `a := &x; b := a; end a; x = 2 // <- E0502; Use(b)`: each loan
// leaves one holder while another still holds it, the case where on must
// keep it.
func copiedThenEnded(k *kit, n int) *mb {
	k.lib()
	ri := refI32(k)
	m := k.body(k.sig("Copied", nil, types.Void, types.EmptyErrors))
	x := m.user("x", types.I32, true)
	m.set(x, use(imm(types.I32, 1)))
	for i := 0; i <= n; i++ {
		a := m.user("a", ri, false)
		b := m.user("b", ri, false)
		m.set(a, ref(m.local(x)))
		m.set(b, use(cp(m.local(a))))
		m.end(a)
		if i == n {
			m.set(x, use(imm(types.I32, 2)))
		}
		m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(b)))
		m.end(b)
	}
	m.end(x)
	m.ret(mir.ExitNormal)
	return m
}

func TestCopiedThenEndedVerdict(t *testing.T) {
	k := newKit(t)
	m := copiedThenEnded(k, 12)
	r := m.check(t)
	if len(r.Diags) != 1 || r.Diags[0].Code != diag.E0502 {
		t.Fatalf("%v, want one E0502", got(r))
	}
}
