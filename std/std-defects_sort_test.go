// Why: Slice.Sort (std/core/core_sort.mk) switches partitions by element size and
// must neither misorder nor cost much more than Go's sort on any input class.
// What: TestStdDefects_SortElementSizes, TestSortSmallElementsMatchModel,
// TestSortNetworksMatchMk, TestSortNetworksZeroOne, TestNearlySortedWithinGo
// and TestSortCountsWithinGo.
// How: exact permutation and comparison counts against mkSortModel, and
// comparison counts within sortWithinGo of Go's sort.Slice.

package std

// TestStdDefects_SortElementSizes checks Slice.Sort against its Go model
// (mkSortModel) for elements on both sides of PdqLomutoBytes: 32 bytes (the
// Lomuto partition) and 40 bytes (Go's Hoare partition), on random keys
// with many ties and with few, at dev and release, comparing the number of
// less calls and the permutation exactly; TestSortSmallElementsMatchModel
// does the same for elements of 1 to 8 bytes, PdqSmall's path.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const sortSizesProgram = `struct R4 {
    k: int
    id: int
    a: int
    b: int
}

struct R5 {
    k: int
    id: int
    a: int
    b: int
    c: int
}

Key(s: mut u64, m: u64): int {
    *s = *s *% 6364136223846793005 +% 1442695040888963407
    return Cast[int]((*s >> 33) % m)
}

Run4(out: mut Writer, a: &Arena, n: int, m: u64): OutOfMemory|WriteError!void {
    var xs := try a.Slice[R4](n)
    var s: u64 = 7
    for i in 0..n {
        xs[i] = R4(k: Key(mut s, m), id: i, a: 0, b: 0)
    }
    c := Cell[int].New(0)
    xs.Sort(fn(p: &R4, q: &R4): bool {
        c.Store(c.Load() + 1)
        return p.k < q.k
    })
    var h: u64 = 14695981039346656037
    for i in 0..n {
        h = (h ^ Cast[u64](xs[i].id)) *% 1099511628211
    }
    cmps := c.Load()
    try Print(out, "{cmps} {h}\n", (cmps, h))
}

Run5(out: mut Writer, a: &Arena, n: int, m: u64): OutOfMemory|WriteError!void {
    var xs := try a.Slice[R5](n)
    var s: u64 = 7
    for i in 0..n {
        xs[i] = R5(k: Key(mut s, m), id: i, a: 0, b: 0, c: 0)
    }
    c := Cell[int].New(0)
    xs.Sort(fn(p: &R5, q: &R5): bool {
        c.Store(c.Load() + 1)
        return p.k < q.k
    })
    var h: u64 = 14695981039346656037
    for i in 0..n {
        h = (h ^ Cast[u64](xs[i].id)) *% 1099511628211
    }
    cmps := c.Load()
    try Print(out, "{cmps} {h}\n", (cmps, h))
}
`

// sortSizesWant is the model's line for n records of elemBytes bytes with
// keys as Key makes them, mod m.
func sortSizesWant(n int, m uint64, elemBytes int) string {
	type rec struct{ k, id int }
	xs := make([]rec, n)
	var s uint64 = 7
	for i := range xs {
		s = s*6364136223846793005 + 1442695040888963407
		xs[i] = rec{int((s >> 33) % m), i}
	}
	cmps := 0
	mkSortModel(xs, elemBytes, func(a, b *rec) bool { cmps++; return a.k < b.k })
	ids := make([]int, n)
	for i, x := range xs {
		ids[i] = x.id
	}
	return fmt.Sprintf("%d %d\n", cmps, idDigest(ids))
}

func TestStdDefects_SortElementSizes(t *testing.T) {
	t.Parallel()
	var calls, want strings.Builder
	for _, n := range []int{13, 50, 300, 5000} {
		for _, m := range []uint64{3, 1000000} {
			fmt.Fprintf(&calls, "    try Run4(mut out, sys.arena, %d, %d)\n", n, m)
			want.WriteString(sortSizesWant(n, m, 32))
			fmt.Fprintf(&calls, "    try Run5(mut out, sys.arena, %d, %d)\n", n, m)
			want.WriteString(sortSizesWant(n, m, 40))
		}
	}
	src := sortSizesProgram + "\nexport Main(sys: &System): OutOfMemory|WriteError!void {\n    var out := sys.Stdout()\n" +
		calls.String() + "}\n"
	for _, flags := range [][]string{nil, {"--release"}} {
		diffLines(t, collRun(t, src, flags...), want.String())
	}
}

// sortCountRatio sorts a copy of keys with Go's own sort.Slice (pdqsort,
// the outside oracle) and with the Go model of Mk's Sort for an element of
// elemBytes bytes, checks both orders equal, and returns the model's count
// of less calls over Go's.
func sortCountRatio(t *testing.T, keys []int, elemBytes int) float64 {
	t.Helper()
	g := append([]int(nil), keys...)
	gc := 0
	sort.Slice(g, func(i, j int) bool { gc++; return g[i] < g[j] })
	m := append([]int(nil), keys...)
	mc := 0
	mkSortModel(m, elemBytes, func(a, b *int) bool { mc++; return *a < *b })
	for i := range m {
		if m[i] != g[i] {
			t.Fatalf("the model's order differs from sort.Slice's at %d", i)
		}
	}
	return float64(mc) / float64(max(gc, 1))
}

// sortWithinGo is the bound on Mk's comparisons against Go's sort.Slice
// on the same input: pdqsort with a different partition
// is not Go's count, but no input class may cost much more.
const sortWithinGo = 1.25

// sortedSwaps is 0..n-1 with k random pairs swapped.
func sortedSwaps(n, k int) []int {
	xs := make([]int, n)
	for i := range xs {
		xs[i] = i
	}
	var st uint64 = 12345
	for i := 0; i < k; i++ {
		st = st*6364136223846793005 + 1442695040888963407
		a := int(st>>33) % n
		st = st*6364136223846793005 + 1442695040888963407
		b := int(st>>33) % n
		xs[a], xs[b] = xs[b], xs[a]
	}
	return xs
}

// TestNearlySortedWithinGo: sorted input with 10, 100 and 1,000
// random swaps at n = 1,000,000, for an element on the Lomuto path (8 bytes,
// u64; 16, str) and on Hoare's (40). When it also took increasing ranges, the
// Lomuto path made 1.66, 2.96 and 2.13 times Go's comparisons on these
// inputs (counted with the model under that rule): its rotation of the part
// not less than the pivot defeated the partial insertion sort.
func TestNearlySortedWithinGo(t *testing.T) {
	t.Parallel()
	for _, k := range []int{10, 100, 1000} {
		keys := sortedSwaps(1000000, k)
		for _, eb := range []int{8, 16, 40} {
			if r := sortCountRatio(t, keys, eb); r > sortWithinGo {
				t.Errorf("sorted with %d swaps, %d-byte elements: %.2f times Go's comparisons, above %.2f", k, eb, r, sortWithinGo)
			}
		}
	}
}

// sortSmallProgram sorts elements of 8, 4, 2 and 1 bytes, PdqSmall's
// networks and the held-value insertion: an unsigned integer holding a key
// above an id (a 1-byte element is its key alone), ordered by the key, at
// every n to 70 and at 100 and 200, and for 8 and 4 bytes also 1,000 and
// 5,000, under six patterns; each line is the calls of less and the FNV-1a
// digest of the elements in order.
const sortSmallProgram = `SmKey(pat: int, i: int, n: int, s: mut u64, m: u64): u64 {
    if pat == 1 {
        return Cast[u64](i)
    }
    if pat == 2 {
        return Cast[u64](n - i)
    }
    if pat == 3 {
        return 7
    }
    if pat == 4 {
        return Cast[u64](i % 7)
    }
    *s = *s *% 6364136223846793005 +% 1442695040888963407
    return (*s >> 33) % m
}

Digest(h: u64, x: u64): u64 {
    return (h ^ x) *% 1099511628211
}

Run(out: mut Writer, a: &Arena, b: int, n: int, pat: int, m: u64): OutOfMemory|WriteError!void {
    var s: u64 = Cast[u64](n) *% 31 +% Cast[u64](pat) +% 1
    c := Cell[int].New(0)
    var h: u64 = 14695981039346656037
    if b == 8 {
        var xs := try a.Slice[u64](n)
        for i in 0..n {
            xs[i] = (SmKey(pat, i, n, mut s, m) << 32) | Cast[u64](i)
        }
        xs.Sort(fn(p: &u64, q: &u64): bool {
            c.Store(c.Load() + 1)
            return (*p >> 32) < (*q >> 32)
        })
        for x in xs {
            h = Digest(h, x)
        }
    } else if b == 4 {
        var xs := try a.Slice[u32](n)
        for i in 0..n {
            xs[i] = Cast[u32]((SmKey(pat, i, n, mut s, m) << 16) | Cast[u64](i))
        }
        xs.Sort(fn(p: &u32, q: &u32): bool {
            c.Store(c.Load() + 1)
            return (*p >> 16) < (*q >> 16)
        })
        for x in xs {
            h = Digest(h, Cast[u64](x))
        }
    } else if b == 2 {
        var xs := try a.Slice[u16](n)
        for i in 0..n {
            xs[i] = Cast[u16]((SmKey(pat, i, n, mut s, m) << 8) | Cast[u64](i))
        }
        xs.Sort(fn(p: &u16, q: &u16): bool {
            c.Store(c.Load() + 1)
            return (*p >> 8) < (*q >> 8)
        })
        for x in xs {
            h = Digest(h, Cast[u64](x))
        }
    } else {
        var xs := try a.Slice[u8](n)
        for i in 0..n {
            xs[i] = Cast[u8](SmKey(pat, i, n, mut s, m))
        }
        xs.Sort(fn(p: &u8, q: &u8): bool {
            c.Store(c.Load() + 1)
            return *p < *q
        })
        for x in xs {
            h = Digest(h, Cast[u64](x))
        }
    }
    cmps := c.Load()
    try Print(out, "{b} {n} {pat} {m} {cmps} {h}\n", (b, n, pat, m, cmps, h))
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    for b in [8, 4, 2, 1] {
        wide: u64 = if b == 8 { 1000000 } else if b == 4 { 65536 } else { 251 }
        for n in 0..5001 {
            if n > 70 && n != 100 && n != 200 && (b < 4 || (n != 1000 && n != 5000)) {
                continue
            }
            try Run(mut out, sys.arena, b, n, 0, 3)
            try Run(mut out, sys.arena, b, n, 0, wide)
            for pat in 1..5 {
                try Run(mut out, sys.arena, b, n, pat, 3)
            }
        }
    }
}
`

// sortSmallWant is sortSmallProgram's output from the model.
func sortSmallWant() string {
	var w strings.Builder
	key := func(pat, i, n int, s *uint64, m uint64) uint64 {
		switch pat {
		case 1:
			return uint64(i)
		case 2:
			return uint64(n - i)
		case 3:
			return 7
		case 4:
			return uint64(i % 7)
		}
		*s = *s*6364136223846793005 + 1442695040888963407
		return (*s >> 33) % m
	}
	run := func(b, n, pat int, m uint64) {
		s := uint64(n)*31 + uint64(pat) + 1
		xs := make([]uint64, n)
		shift := map[int]uint{8: 32, 4: 16, 2: 8, 1: 0}[b]
		for i := range xs {
			k := key(pat, i, n, &s, m)
			if b == 1 {
				xs[i] = k
			} else {
				xs[i] = k<<shift | uint64(i)
			}
		}
		cmps := 0
		mkSortModel(xs, b, func(p, q *uint64) bool { cmps++; return *p>>shift < *q>>shift })
		h := uint64(14695981039346656037)
		for _, x := range xs {
			h = (h ^ x) * 1099511628211
		}
		fmt.Fprintf(&w, "%d %d %d %d %d %d\n", b, n, pat, m, cmps, h)
	}
	for _, b := range []int{8, 4, 2, 1} {
		wide := map[int]uint64{8: 1000000, 4: 65536}[b]
		if b < 4 {
			wide = 251
		}
		for n := 0; n <= 5000; n++ {
			if n > 70 && n != 100 && n != 200 && (b < 4 || (n != 1000 && n != 5000)) {
				continue
			}
			run(b, n, 0, 3)
			run(b, n, 0, wide)
			for pat := 1; pat < 5; pat++ {
				run(b, n, pat, 3)
			}
		}
	}
	return w.String()
}

// TestSortSmallElementsMatchModel: Slice.Sort of 8-, 4-, 2- and 1-byte
// elements (PdqSmall's networks, the held-value insertion, the spread pivot
// and both Lomuto partitions) makes the model's calls of less and leaves its
// permutation, exactly, at dev and release: the counts and permutations the
// 16-, 32- and 40-byte tests pin, on the small-element path.
func TestSortSmallElementsMatchModel(t *testing.T) {
	t.Parallel()
	want := sortSmallWant()
	for _, flags := range [][]string{nil, {"--release"}} {
		diffLines(t, collRun(t, sortSmallProgram, flags...), want)
	}
}

// TestSortNetworksMatchMk: core_sort.mk's PdqSort9 and PdqSort13 make
// the model's compare-and-swaps in the model's order, and PdqSmallMax is the
// model's mkSmallMax, read from the source.
func TestSortNetworksMatchMk(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("core", "core_sort.mk"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	pair := regexp.MustCompile(`^    PdqSwapIfLess\(p, (\d+), (\d+), less\)$`)
	for _, c := range []struct {
		name string
		net  [][2]int
	}{{"PdqSort9", mkSort9}, {"PdqSort13", mkSort13}} {
		head := c.name + "[T](p: *T, const less: fn(&T, &T): bool) {\n"
		at := strings.Index(src, head)
		if at < 0 {
			t.Fatalf("no %s in core_sort.mk", c.name)
		}
		body, _, _ := strings.Cut(src[at+len(head):], "\n}\n")
		var got [][2]int
		for _, line := range strings.Split(body, "\n") {
			m := pair.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("%s: line %q is not one compare-and-swap", c.name, line)
			}
			i, _ := strconv.Atoi(m[1])
			j, _ := strconv.Atoi(m[2])
			got = append(got, [2]int{i, j})
		}
		if !slices.Equal(got, c.net) {
			t.Errorf("%s's pairs %v differ from the model's %v", c.name, got, c.net)
		}
	}
	if !strings.Contains(src, fmt.Sprintf("\nconst PdqSmallMax = %d\n", mkSmallMax)) {
		t.Errorf("core_sort.mk's PdqSmallMax is not the model's %d", mkSmallMax)
	}
}

// TestSortNetworksZeroOne: the model's sort9 and sort13 networks sort
// every input of 0s and 1s of their length, so by the zero-one principle
// every input (Knuth, TAOCP 5.3.4, exercise 3); the networks swap only, so
// the result is a permutation. TestSortNetworksMatchMk holds the Mk
// bodies to the same pairs.
func TestSortNetworksZeroOne(t *testing.T) {
	t.Parallel()
	for _, net := range [][][2]int{mkSort9, mkSort13} {
		w := 0
		for _, p := range net {
			w = max(w, p[0]+1, p[1]+1)
		}
		for mask := 0; mask < 1<<w; mask++ {
			xs := make([]int, w)
			ones := 0
			for i := range xs {
				xs[i] = (mask >> i) & 1
				ones += xs[i]
			}
			mkNetwork(mkSortSlice[int]{s: xs, less: func(a, b *int) bool { return *a < *b }}, 0, net)
			for i := range xs {
				if want := map[bool]int{true: 1, false: 0}[i >= w-ones]; xs[i] != want {
					t.Fatalf("the %d-element network leaves %v from mask %#x", w, xs, mask)
				}
			}
		}
	}
}

// TestSortCountsWithinGo: every pattern of sortCases at n = 1,000
// and 20,000, for 8-, 16- and 40-byte elements, costs at most sortWithinGo
// times Go's sort.Slice in calls of less. TestStdColl_SortAdversarialMatchesGo
// proves Mk's count equal to the model's, so this bounds Mk's against Go's.
func TestSortCountsWithinGo(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1000, 20000} {
		for _, c := range sortCases(n) {
			for _, eb := range []int{8, 16, 40} {
				if r := sortCountRatio(t, c.keys, eb); r > sortWithinGo {
					t.Errorf("%s, n = %d, %d-byte elements: %.2f times Go's comparisons, above %.2f", c.name, n, eb, r, sortWithinGo)
				}
			}
		}
	}
}
