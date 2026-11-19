// Why: Sort is pdqsort, and every branch, and the input built to defeat
// quicksort, must match its model and stay O(n log n).
// What: TestStdColl_SortAdversarialMatchesGo and mkSortModel (Go's
// pdqsort_func with ipnsort's Lomuto partition).
// How: the comparison count and the order of equal keys must equal the model's
// on every pattern and on McIlroy's killer adversary.

package std

import (
	"fmt"
	"math/bits"
	"strings"
	"testing"
)

type sortCase struct {
	name string
	keys []int
}

func sortCases(n int) []sortCase {
	gen := func(f func(i int) int) []int {
		xs := make([]int, n)
		for i := range xs {
			xs[i] = f(i)
		}
		return xs
	}
	var s uint64 = 99
	rnd := func(i int) int {
		s = s*6364136223846793005 + 1442695040888963407
		return int(s >> 33)
	}
	nearly := gen(func(i int) int { return i })
	for i := 0; i < n/100; i++ {
		a, b := rnd(0)%n, rnd(0)%n
		nearly[a], nearly[b] = nearly[b], nearly[a]
	}
	swap10 := gen(func(i int) int { return i })
	for i := 0; i < 10; i++ {
		a, b := rnd(0)%n, rnd(0)%n
		swap10[a], swap10[b] = swap10[b], swap10[a]
	}
	tail := gen(func(i int) int { return i })
	for i := n - n/100; i < n; i++ {
		tail[i] = rnd(0) % n
	}
	return []sortCase{
		{"random", gen(func(i int) int { return rnd(i) % 1000000000 })},
		{"sorted", gen(func(i int) int { return i })},
		{"reversed", gen(func(i int) int { return n - i })},
		{"equal", gen(func(i int) int { return 7 })},
		{"organ-pipe", gen(func(i int) int { return min(i, n-i) })},
		{"sawtooth", gen(func(i int) int { return i % 100 })},
		{"few-distinct", gen(func(i int) int { return rnd(i) % 3 })},
		{"nearly-sorted", nearly},
		{"swap10", swap10},
		{"random-tail", tail},
		{"min-last", gen(func(i int) int { return (i + 1) % n })},
	}
}

// goCountedSort sorts (key, id) pairs by key with the Go model of Mk's
// Sort (mkSortModel; the Mk pair is 16 bytes) and returns the number of
// less calls and the ids in sorted order.
func goCountedSort(keys []int) (int, []int) {
	type p struct{ k, id int }
	xs := make([]p, len(keys))
	for i, k := range keys {
		xs[i] = p{k, i}
	}
	n := 0
	mkSortModel(xs, 16, func(a, b *p) bool { n++; return a.k < b.k })
	ids := make([]int, len(xs))
	for i, x := range xs {
		ids[i] = x.id
	}
	return n, ids
}

// idDigest is FNV-1a over the ids.
func idDigest(ids []int) uint64 {
	h := uint64(14695981039346656037)
	for _, id := range ids {
		h = (h ^ uint64(id)) * 1099511628211
	}
	return h
}

// goAdversary is McIlroy's adversary ("A Killer Adversary for Quicksort",
// 1999) against the Go model of Mk's Sort (mkSortModel, 8-byte elements)
// of 0..n-1: it returns the comparison count.
func goAdversary(n int) int {
	val := make([]int, n)
	gas := n
	for i := range val {
		val[i] = gas
	}
	nsolid, cand, ncmp := 0, 0, 0
	freeze := func(x int) { val[x] = nsolid; nsolid++ }
	cmp := func(x, y int) int {
		ncmp++
		if val[x] == gas && val[y] == gas {
			if x == cand {
				freeze(x)
			} else {
				freeze(y)
			}
		}
		if val[x] == gas {
			cand = x
		} else if val[y] == gas {
			cand = y
		}
		return val[x] - val[y]
	}
	xs := make([]int, n)
	for i := range xs {
		xs[i] = i
	}
	mkSortModel(xs, 8, func(a, b *int) bool { return cmp(*a, *b) < 0 })
	return ncmp
}

const sortProgramHead = `struct P {
    k: int
    id: int
}

ByKey(c: &Cell[int], a: &P, b: &P): bool {
    c.Store(c.Load() + 1)
    return a.k < b.k
}

Run(out: mut Writer, a: &Arena, keys: []int): OutOfMemory|WriteError!void {
    n := keys.Len()
    var xs := try a.Slice[P](n)
    for i in 0..n {
        xs[i] = P(k: keys[i], id: i)
    }
    c := Cell[int].New(0)
    xs.Sort(fn(p: &P, q: &P): bool { return ByKey(&c, p, q) })
    var h: u64 = 14695981039346656037
    var sorted := true
    for i in 0..n {
        h = (h ^ Trunc[u64](xs[i].id)) *% 1099511628211
        if i > 0 && xs[i].k < xs[i - 1].k {
            sorted = false
        }
    }
    cmps := c.Load()
    try Print(out, "{cmps} {h} {sorted}\n", (cmps, h, sorted))
}

view struct Adv {
    val: []Cell[int]
    nsolid: Cell[int]
    cand: Cell[int]
    ncmp: Cell[int]
    gas: int
}

Freeze(adv: &Adv, x: int) {
    adv.val[x].Store(adv.nsolid.Load())
    adv.nsolid.Store(adv.nsolid.Load() + 1)
}

AdvLess(adv: &Adv, a: &int, b: &int): bool {
    x := *a
    y := *b
    adv.ncmp.Store(adv.ncmp.Load() + 1)
    if adv.val[x].Load() == adv.gas && adv.val[y].Load() == adv.gas {
        if x == adv.cand.Load() {
            Freeze(adv, x)
        } else {
            Freeze(adv, y)
        }
    }
    if adv.val[x].Load() == adv.gas {
        adv.cand.Store(x)
    } else if adv.val[y].Load() == adv.gas {
        adv.cand.Store(y)
    }
    return adv.val[x].Load() < adv.val[y].Load()
}

Killer(out: mut Writer, a: &Arena, n: int): OutOfMemory|WriteError!void {
    var cells := List[Cell[int]].New(a)
    for _ in 0..n {
        try cells.Push(Cell[int].New(n))
    }
    val := cells.Done()
    adv := Adv(val: val[..], nsolid: Cell[int].New(0), cand: Cell[int].New(0), ncmp: Cell[int].New(0), gas: n)
    var xs := try a.Slice[int](n)
    for i in 0..n {
        xs[i] = i
    }
    xs.Sort(fn(x: &int, y: &int): bool { return AdvLess(&adv, x, y) })
    var sorted := true
    for i in 1..n {
        if adv.val[xs[i]].Load() < adv.val[xs[i - 1]].Load() {
            sorted = false
        }
    }
    cmps := adv.ncmp.Load()
    try Print(out, "killer {cmps} {sorted}\n", (cmps, sorted))
}
`

// TestStdColl_SortAdversarialMatchesGo: Sort with a named comparator and
// with a capturing literal, over the inputs that reach each branch of
// pdqsort (sorted, reversed, all equal, organ pipe, sawtooth, few distinct
// keys, nearly sorted, sorted with 10 swaps, sorted with a random tail, one
// misplaced minimum) and McIlroy's killer adversary, which builds the worst
// input for any quicksort while it runs. The model's cost against Go's own
// sort.Slice is checked in std-defects_sort_test.go. Sizes: n = 20,000 and
// 1,000 for every pattern of sortCases, plus the killer adversary at 1,000,
// 20,000 and 200,000 elements.
func TestStdColl_SortAdversarialMatchesGo(t *testing.T) {
	t.Parallel()
	var src strings.Builder
	var want strings.Builder
	var calls strings.Builder
	k := 0
	for _, n := range []int{1000, 20000} {
		for _, c := range sortCases(n) {
			fmt.Fprintf(&src, "const K%d: [%d]int = %s\n", k, n, mkIntArray(c.keys))
			fmt.Fprintf(&calls, "    try Run(mut out, sys.arena, K%d[..])\n", k)
			cmps, ids := goCountedSort(c.keys)
			fmt.Fprintf(&want, "%d %d true\n", cmps, idDigest(ids))
			k++
		}
	}
	killers := []int{1000, 20000, 200000}
	for _, n := range killers {
		fmt.Fprintf(&calls, "    try Killer(mut out, sys.arena, %d)\n", n)
		g := goAdversary(n)
		fmt.Fprintf(&want, "killer %d true\n", g)
		bound := 4 * n * bits.Len(uint(n))
		t.Logf("killer adversary, n = %d: the model of Mk's Sort makes %d comparisons (%.2f n log2 n); bound %d", n, g, float64(g)/float64(n*bits.Len(uint(n))), bound)
		if g > bound {
			t.Errorf("the model's count %d is above the bound %d: the heapsort fallback does not hold", g, bound)
		}
	}
	src.WriteString(sortProgramHead)
	src.WriteString("\nexport Main(sys: &System): OutOfMemory|WriteError!void {\n    var out := sys.Stdout()\n")
	src.WriteString(calls.String())
	src.WriteString("}\n")
	diffLines(t, collRun(t, src.String(), "--release"), want.String())
}
