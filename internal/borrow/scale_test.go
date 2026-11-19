// Why: a fuzz input of thousands of nested `scope s := io.Scope() { s.go
// Bump(mut n); ... }` over one local was very slow, because every access
// scanned holders(root) for every candidate loan and built a diagnostic for
// every live conflicting loan.
// What: tests and benchmarks of that shape (nestedScopeSpawns), of sequential
// mut borrows (seqMutBorrows) and of the fuzz input itself
// (testdata/fuzz/FuzzCheckNoICE/0dcd84ef0b55382b).
// How: pins that the per-loan holders index and the one-winner rule change no
// result, and that each shape meets the 1 s budget.

package borrow

import (
	"math/rand/v2"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// nestedScopeSpawns is the slow fuzz input's shape at a chosen depth:
//
//	var n: i32 = 0
//	scope s := io.Scope() { s.go Bump(mut n)
//	  scope s := io.Scope() { s.go Bump(mut n) // <- E0501
//	    ... } }
//
// Each level is six operations (the scope call, `tm = mut n`, the spawn,
// the end of tm, the join, the end of s); every level but the outermost
// borrows n while each outer task holds `mut n`.
func nestedScopeSpawns(k *kit, depth int) *mb {
	k.taskLib()
	m := k.body(k.sig("Nest", []types.Param{k.param("io", k.Io)}, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	m.set(n, use(imm(types.I32, 0)))
	scopes := make([]mir.LocalID, depth)
	for i := range scopes {
		scopes[i] = m.openScope()
		tm := m.temp(k.tab.Mut(types.I32))
		m.set(tm, mutRef(m.local(n)))
		m.spawn(scopes[i], "Bump", argMove(m.local(tm)))
		m.end(tm)
	}
	for i := depth - 1; i >= 0; i-- {
		m.join(scopes[i])
		m.end(scopes[i])
	}
	m.end(n)
	m.ret(mir.ExitNormal)
	return m
}

func checkNested(t testing.TB, r *Result, depth int) {
	t.Helper()
	if len(r.Diags) != depth-1 {
		t.Fatalf("depth %d: %d diagnostics, want %d E0501", depth, len(r.Diags), depth-1)
	}
	for _, d := range r.Diags {
		if d.Code != diag.E0501 {
			t.Fatalf("depth %d: %v, want only E0501", depth, got(r)[:min(5, len(r.Diags))])
		}
	}
}

func TestNestedScopeSpawnsVerdict(t *testing.T) {
	for _, depth := range []int{1, 2, 5} {
		k := newKit(t)
		m := nestedScopeSpawns(k, depth)
		checkNested(t, m.check(t), depth)
	}
}

// TestBudgetNestedScopeSpawns holds the shape, at the 64,000 operations of
// the per-function budget, to its 1 s target. Each access stops at its first
// live conflict in loan order instead of running a live-loan search on every
// live exclusive loan on n to pick the least message. The check allocates
// heavily here, so on a loaded machine one run can be slow for reasons
// outside it: a run over 1 s is tried twice more and the best counts, every
// run logged (the target is for one function on an uncontended core).
func TestBudgetNestedScopeSpawns(t *testing.T) {
	const depth = 64000 / 6
	k := newKit(t)
	m := nestedScopeSpawns(k, depth)
	ops := stmtCount(m.b)
	var d time.Duration
	var r *Result
	for try := range 3 {
		di, ri := timeCheck(k, m)
		t.Logf("NestedScopeSpawns: depth %d, %d operations, %v (run %d; GOOS=%s GOARCH=%s, %d CPUs)", depth, ops, di, try+1, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
		if try == 0 || di < d {
			d, r = di, ri
		}
		if d <= time.Second || !timingRun() {
			break
		}
	}
	checkNested(t, r, depth)
	if !timingRun() {
		t.Logf("%s: the 1 s budget is not asserted (took %v)", timingOff, d)
	} else if d > time.Second {
		t.Errorf("NestedScopeSpawns took %v, over the 1 s budget", d)
	}
}

func BenchmarkNestedScopeSpawns(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return nestedScopeSpawns(k, 12000/6) })
}

func BenchmarkNestedScopeSpawns64k(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return nestedScopeSpawns(k, 64000/6) })
}

// seqMutBorrows is n ops/3 sequential `UseMut(mut n)` calls through a
// temporary, each ended before the next: no conflict, but every access of n
// visits every exclusive loan on n (the candidates, cached per path
// and column), so the cost grows with the square of the borrow count.
func seqMutBorrows(k *kit, ops int) *mb {
	k.lib()
	m := k.body(k.sig("Seq", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	m.set(n, use(imm(types.I32, 0)))
	for range ops / 3 {
		tm := m.temp(k.tab.Mut(types.I32))
		m.set(tm, mutRef(m.local(n)))
		m.callv("UseMut", []types.TypeID{types.I32}, argMove(m.local(tm)))
		m.end(tm)
	}
	m.end(n)
	m.ret(mir.ExitNormal)
	return m
}

func TestSeqMutBorrowsClean(t *testing.T) {
	k := newKit(t)
	if r := seqMutBorrows(k, 30).check(t); r.Diags != nil {
		t.Fatalf("%v", got(r))
	}
}

func BenchmarkSeqMutBorrows64k(b *testing.B) {
	benchCheck(b, func(k *kit) *mb { return seqMutBorrows(k, 64000) })
}

// gateInput is the slow fuzz input, read from its corpus file.
func gateInput(t testing.TB) []byte {
	raw, err := os.ReadFile("testdata/fuzz/FuzzCheckNoICE/0dcd84ef0b55382b")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	s, err := strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(lines[1], "[]byte("), ")"))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(s)
}

// TestGateFuzzInputBudget runs the slow fuzz input itself and holds it to the
// 1 s target (it is 11,694 operations, under the 64,000 of the budget).
func TestGateFuzzInputBudget(t *testing.T) {
	k := newKit(t)
	m := genBody(k, gateInput(t))
	if err := m.b.Validate(k.mirEnv(m.sig)); err != nil {
		t.Fatalf("the gate input no longer validates: %v", err)
	}
	d, r := timeCheck(k, m)
	t.Logf("gate input: %d operations, %d blocks, %d locals, %d diagnostics, %v", stmtCount(m.b), len(m.b.Blocks), len(m.b.Locals), len(r.Diags), d)
	if !timingRun() {
		t.Logf("%s: the 1 s budget is not asserted (took %v)", timingOff, d)
	} else if d > time.Second {
		t.Errorf("the gate input took %v, over the 1 s budget", d)
	}
}

// scaleSeeds is genSeeds plus long inputs, which draw many conflicting
// loans per access, and the slow fuzz input.
func scaleSeeds(t testing.TB) [][]byte {
	out := genSeeds(400)
	r := rand.New(rand.NewPCG(3, 4))
	for range 40 {
		b := make([]byte, 200+r.IntN(1800))
		for i := range b {
			b[i] = byte(r.IntN(256))
		}
		out = append(out, b)
	}
	return append(out, gateInput(t)[:600])
}

// TestAccessWinnerEqualsFullScan: the lazy walk of access, which stops at
// the first live candidate of the on filter in loan order, gives the same
// diagnostics, facts and locations included, as visiting every candidate,
// unfiltered, and keeping the live one with the least loan ID.
func TestAccessWinnerEqualsFullScan(t *testing.T) {
	ran, diags := 0, 0
	for _, data := range scaleSeeds(t) {
		k := newKit(t)
		m := genBody(k, data)
		if m.b.Validate(k.mirEnv(m.sig)) != nil {
			continue
		}
		ran++
		a := newChecker(m.b, k.env(m.sig)).run()
		ref := newChecker(m.b, k.env(m.sig))
		ref.fullScan = true
		b := ref.run()
		if len(a.Diags) != len(b.Diags) {
			t.Fatalf("%d diagnostics, %d with the full scan\n%v\n%v", len(a.Diags), len(b.Diags), got(a), got(b))
		}
		for i := range a.Diags {
			if !reflect.DeepEqual(a.Diags[i], b.Diags[i]) {
				t.Fatalf("diagnostic %d differs:\n%+v\nwith the full scan:\n%+v", i, *a.Diags[i], *b.Diags[i])
			}
		}
		diags += len(a.Diags)
	}
	if ran < 300 || diags == 0 {
		t.Fatalf("only %d bodies, %d diagnostics", ran, diags)
	}
	t.Logf("%d bodies, %d diagnostics", ran, diags)
}

// TestLoanHoldersSeedSameAsRootScan: in every state, the roots of
// loanHolders[l] that hold l are exactly, and in the same order, the roots
// of holders(root(l)) that hold l, so liveHolder's seeds are those of the
// scan it replaced.
func TestLoanHoldersSeedSameAsRootScan(t *testing.T) {
	states, seeded := 0, 0
	ran := solvedCheckers(t, 400, func(c *checker) {
		eachState(c, func(st *state) {
			states++
			for l := range c.loans {
				lo := &c.loans[l]
				if lo.place == noPath {
					continue
				}
				var scan, idx []rootID
				for _, h := range c.holdersOf(c.paths.get(lo.place).root) {
					if c.sets.has(c.held(st, h), uint32(l)) {
						scan = append(scan, h)
					}
				}
				for _, h := range c.loanHoldersOf(loanID(l)) {
					if c.sets.has(c.held(st, h), uint32(l)) {
						idx = append(idx, h)
					}
				}
				if !slices.Equal(scan, idx) {
					t.Fatalf("loan %d: holders scan gives %v, loanHolders gives %v", l, scan, idx)
				}
				seeded += len(idx)
			}
		})
	})
	if ran < 100 || states == 0 || seeded == 0 {
		t.Fatalf("only %d bodies, %d states, %d seeds", ran, states, seeded)
	}
}
