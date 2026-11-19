// Why: the purge and the kill read a per-body holders index instead of
// scanning every held set, and that shortcut must never lose a loan.
// What: TestHoldersIndexSuperset (after any transfer, every root holding a
// loan rooted at x is in holders(x)) and TestPurgeEqualsFullScrub (the purge
// and the kill over the index give the same held sets as subtracting from
// every held set), on generated bodies.
// How: runs the fuzz generator over fixed seeds (genSeeds), solves each body
// and compares every state (eachState, equalHeld).

package borrow

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// genSeeds is a fixed set of generator inputs: the fuzz seeds and n
// pseudo-random byte strings.
func genSeeds(n int) [][]byte {
	r := rand.New(rand.NewPCG(1, 2))
	out := [][]byte{{0, 1, 1, 0, 5, 3, 4, 8, 0}, {3, 3, 4, 6, 7, 9, 2, 8, 1, 5}, {1, 0, 9, 0, 5, 6, 0, 7}}
	for range n {
		b := make([]byte, 8+r.IntN(56))
		for i := range b {
			b[i] = byte(r.IntN(256))
		}
		out = append(out, b)
	}
	return out
}

// solvedCheckers yields a checker that has run, for every generated body
// that validates.
func solvedCheckers(t *testing.T, n int, f func(c *checker)) int {
	t.Helper()
	ran := 0
	for _, data := range genSeeds(n) {
		k := newKit(t)
		m := genBody(k, data)
		if m.b.Validate(k.mirEnv(m.sig)) != nil {
			continue
		}
		c := newChecker(m.b, k.env(m.sig))
		c.run()
		c.report = false
		f(c)
		ran++
	}
	return ran
}

// eachState replays every reachable block from its final entry state, with
// reporting off, and calls f on the state after every statement and on every
// out-state of the terminator.
func eachState(c *checker, f func(st *state)) {
	for _, b := range c.rpo {
		in := c.entries[b]
		if in == nil {
			continue
		}
		st := in.clone()
		f(st)
		blk := &c.body.Blocks[b]
		clear(c.lastDiscr)
		for i := range blk.Stmts {
			ctx := stmtCtx{b: b, i: i, span: blk.Stmts[i].Span}
			c.statement(st, &ctx, &blk.Stmts[i])
			f(st)
		}
		ctx := stmtCtx{b: b, i: len(blk.Stmts), span: blk.Term.Span, term: true}
		for _, o := range c.terminator(st, &ctx, &blk.Term) {
			f(o.st)
		}
	}
}

func TestHoldersIndexSuperset(t *testing.T) {
	states := 0
	ran := solvedCheckers(t, 400, func(c *checker) {
		eachState(c, func(st *state) {
			states++
			for h := range c.roots {
				for _, l := range c.sets.get(st.get(rootID(h)).held) {
					lo := &c.loans[l]
					if lo.place == noPath {
						continue
					}
					x := c.paths.get(lo.place).root
					if !slices.Contains(c.holdersOf(x), rootID(h)) {
						t.Fatalf("root %d holds loan %d rooted at %d but is not in holders(%d) = %v",
							h, l, x, x, c.holdersOf(x))
					}
				}
			}
		})
	})
	if ran < 100 || states == 0 {
		t.Fatalf("only %d generated bodies validated (%d states)", ran, states)
	}
}

func TestPurgeEqualsFullScrub(t *testing.T) {
	compared := 0
	solvedCheckers(t, 400, func(c *checker) {
		eachState(c, func(st *state) {
			for x := range c.roots {
				// Purge over the index against the scrub of every held set.
				a := st.clone()
				c.purge(a, rootID(x))
				b := st.clone()
				rs := c.rootedSet(rootID(x))
				for h := range c.roots {
					if h == x {
						continue
					}
					e := b.get(rootID(h))
					e.held = c.sets.minus(e.held, rs)
					b.set(rootID(h), e)
				}
				b.set(rootID(x), entry{})
				if !equalHeld(c, a, b) {
					t.Fatalf("purge of root %d differs from the full scrub", x)
				}
				compared++
			}
			for p := range c.paths.paths {
				a := st.clone()
				c.kill(a, pathID(p))
				b := st.clone()
				ks := c.killSet(pathID(p))
				for h := range c.roots {
					e := b.get(rootID(h))
					e.held = c.sets.minus(e.held, ks)
					b.set(rootID(h), e)
				}
				if !equalHeld(c, a, b) {
					t.Fatalf("kill of path %d differs from the full scrub", p)
				}
				compared++
			}
		})
	})
	if compared == 0 {
		t.Fatal("nothing compared")
	}
}

// equalHeld compares two states root by root, leaving out on: the candidate
// filter is a superset, and a scrub that bypasses put does not maintain it.
func equalHeld(c *checker, a, b *state) bool {
	for gi, g := range a.groups {
		h := b.groups[gi]
		if g == h {
			continue
		}
		for ci := range groupSize {
			x, y := chunkAt(g, ci), chunkAt(h, ci)
			if x == y {
				continue
			}
			for j := range chunkSize {
				e, f := x[j], y[j]
				e.on, f.on = emptySet, emptySet
				if e != f {
					return false
				}
			}
		}
	}
	return true
}
