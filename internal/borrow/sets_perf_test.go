// Why: the set store's hashed intern index and the fast paths of unite and
// minus must give exactly the sets plain sorted slices give.
// What: TestSetInternCollisionsChain (collisions are chained, never merged)
// and TestSetOpsMatchReference against refUnite and refMinus.
// How: forces hash collisions, then compares random sets (randSet) with the
// reference.

package borrow

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestSetInternCollisionsChain forces every set into one hash chain and
// checks that distinct sets keep distinct IDs and equal sets one ID.
func TestSetInternCollisionsChain(t *testing.T) {
	s := newSetStore()
	s.hashMask = 0
	sets := [][]uint32{{1}, {2}, {1, 2}, {2, 3}, {1, 2, 3}, {7}, {0}}
	ids := make([]setID, len(sets))
	for i, xs := range sets {
		ids[i] = s.intern(xs)
	}
	for i := range sets {
		for j := range sets {
			if (ids[i] == ids[j]) != (i == j) {
				t.Fatalf("sets %v and %v: IDs %d and %d", sets[i], sets[j], ids[i], ids[j])
			}
		}
		if again := s.intern(slices.Clone(sets[i])); again != ids[i] {
			t.Fatalf("set %v interned again as %d, first %d", sets[i], again, ids[i])
		}
		if !slices.Equal(s.get(ids[i]), sets[i]) {
			t.Fatalf("set %d holds %v, want %v", ids[i], s.get(ids[i]), sets[i])
		}
	}
	if len(s.index) != 1 {
		t.Fatalf("hashMask 0 must give one chain, got %d", len(s.index))
	}
}

func refUnite(a, b []uint32) []uint32 {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

func refMinus(a, b []uint32) []uint32 {
	var out []uint32
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func randSet(r *rand.Rand, n, universe int) []uint32 {
	xs := make([]uint32, 0, n)
	for range n {
		xs = append(xs, uint32(r.IntN(universe)))
	}
	slices.Sort(xs)
	return slices.Compact(xs)
}

// TestSetOpsMatchReference compares unite, minus and meet with slice
// references over random sets of very different sizes (so both arms of
// firstShared and both unite fast paths run), with the normal hash and with
// every set in one chain.
func TestSetOpsMatchReference(t *testing.T) {
	for _, mask := range []uint64{^uint64(0), 0} {
		s := newSetStore()
		s.hashMask = mask
		r := rand.New(rand.NewPCG(1, 2))
		var pool []setID
		for range 400 {
			n := []int{0, 1, 2, 5, 40, 300}[r.IntN(6)]
			pool = append(pool, s.intern(randSet(r, n, 64+r.IntN(400))))
		}
		for range 4000 {
			a, b := pool[r.IntN(len(pool))], pool[r.IntN(len(pool))]
			xa, xb := slices.Clone(s.get(a)), slices.Clone(s.get(b))
			u, d, m := s.unite(a, b), s.minus(a, b), s.meet(a, b)
			if !slices.Equal(s.get(u), refUnite(xa, xb)) {
				t.Fatalf("mask %x: %v ∪ %v = %v", mask, xa, xb, s.get(u))
			}
			if !slices.Equal(s.get(d), refMinus(xa, xb)) {
				t.Fatalf("mask %x: %v minus %v = %v", mask, xa, xb, s.get(d))
			}
			if !slices.Equal(s.get(m), refMinus(xa, refMinus(xa, xb))) {
				t.Fatalf("mask %x: %v ∩ %v = %v", mask, xa, xb, s.get(m))
			}
			// Canonical: every result has the one ID of its contents.
			for _, id := range []setID{u, d, m} {
				if s.intern(slices.Clone(s.get(id))) != id {
					t.Fatalf("mask %x: result %v is not canonical", mask, s.get(id))
				}
			}
			pool = append(pool, u, d)
		}
	}
}
