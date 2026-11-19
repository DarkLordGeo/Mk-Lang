// Why: every documented std.coll receiver needs its results, lengths and
// capacities checked against a model, edges included.
// What: TestStdColl_Section18Ops, with Go models of List, HeapList, Map,
// HeapMap, Set and TextMap.
// How: seeded operations run in lockstep in Mk and the models; every result,
// length, capacity and content digest is compared after each.

package std

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// opsKeys are the TextMap keys and HeapText pieces: the empty key, short
// and long ASCII, keys that differ in one byte, and multi-byte scalars:
// the last holds the first and last scalar of each UTF-8 length and the
// two scalars around the surrogates.
var opsKeys = []string{"", "a", "b", "ab", "ba", "key-00000000", "key-00000001", "a longer key past sixteen bytes", "é", "€uro", "　x", "z", string([]rune{0x7F, 0x80, 0x7FF, 0x800, 0xD7FF, 0xE000, 0xFFFF, 0x10000, 0x1F600, 0x10FFFF})}

const opsFixCore = `// Fixture of TestStdColl_Section18Ops: list capacities, and the first
// byte at which Words may end a word inside an 8-byte step.

import std.raw

export CollFixWordStop(w: u64): int {
    m := WordStops8(w)
    if m == 0 {
        return 8
    }
    return raw.Ctz64(m) / 8
}

export CollFixListCap(l: &List[int]): int {
    return l.cap
}

export CollFixHeapListCap(l: &HeapList[int]): int {
    return l.cap
}
`

const opsFixModule = `import std.core

export ListCap(l: &List[int]): int {
    return core.CollFixListCap(l)
}

export HeapListCap(l: &HeapList[int]): int {
    return core.CollFixHeapListCap(l)
}

export WordStop(w: u64): int {
    return core.CollFixWordStop(w)
}
`

const opsProgram = `import std.opsfix

struct Rng {
    s: u64
}

Rng.Next(mut self, n: int): int {
    self.s = self.s *% 6364136223846793005 +% 1442695040888963407
    return Trunc[int](self.s >> 33) % n
}

B(b: bool): int {
    if b {
        return 1
    }
    return 0
}

Fnv(h: u64, x: int): u64 {
    return (h ^ Trunc[u64](x)) *% 1099511628211
}

Digest(xs: []int): u64 {
    var h: u64 = 14695981039346656037
    for i in 0..xs.Len() {
        h = Fnv(h, xs[i])
    }
    return h
}

FnvStr(s: str): u64 {
    var h: u64 = 14695981039346656037
    b := s.AsBytes()
    for i in 0..b.Len() {
        h = (h ^ u64(b[i])) *% 1099511628211
    }
    return h
}

// Content of a map of int: the wrapping sum over entries of
// key * 1000003 + value (order-free), and the order of the keys.
Content(it: mut MapIter[int, int]): (sum: u64, order: u64) {
    var s: u64 = 0
    var o: u64 = 14695981039346656037
    for e := it.Next() {
        s = s +% Trunc[u64](*e.key) *% 1000003 +% Trunc[u64](*e.value)
        o = Fnv(o, *e.key)
    }
    return (sum: s, order: o)
}

TextContent(m: &TextMap[int]): (sum: u64, order: u64) {
    var s: u64 = 0
    var o: u64 = 14695981039346656037
    var it := m.Iter()
    for e := it.Next() {
        s = s +% FnvStr(e.key) *% 1000003 +% Trunc[u64](*e.value)
        o = o *% 31 +% FnvStr(e.key)
    }
    return (sum: s, order: o)
}

Lists(out: mut Writer, l: mut List[int], h: mut HeapList[int], a: &Alloc, ops: int, seed: u64): OutOfMemory|WriteError!void {
    var rng := Rng(s: seed)
    for op in 0..ops {
        r := rng.Next(16)
        n := l.Len()
        var what := "push"
        var rl := 0
        var rh := 0
        if r == 5 {
            what = "pop"
            rl = l.Pop() else { -1 }
            rh = h.Pop() else { -1 }
        } else if r == 6 {
            what = "insert"
            i := rng.Next(n + 1)
            try l.Insert(i, op)
            try h.Insert(a, i, op)
            rl = i
            rh = i
        } else if r == 7 {
            what = "remove"
            rl = -1
            rh = -1
            if n > 0 {
                i := rng.Next(n)
                rl = l.Remove(i)
                rh = h.Remove(i)
            }
        } else if r == 8 {
            what = "get"
            i := rng.Next(n + 2) - 1
            rl = -1
            rh = -1
            if v := l.Get(i) {
                rl = *v
            }
            if v := h.Get(i) {
                rh = *v
            }
        } else if r == 9 {
            what = "getmut"
            i := rng.Next(n + 2) - 1
            if p := l.GetMut(i) {
                *p = op + 1000000
                rl = 1
            }
            if p := h.GetMut(i) {
                *p = op + 1000000
                rh = 1
            }
        } else if r == 10 {
            what = "swap"
            rl = -1
            rh = -1
            if n > 0 {
                i := rng.Next(n)
                j := rng.Next(n)
                l.Swap(i, j)
                h.Swap(i, j)
                rl = i * 1000 + j
                rh = rl
            }
        } else if r == 11 {
            what = "reverse"
            l.Reverse()
            h.Reverse()
        } else if r == 12 {
            what = "index"
            x := rng.Next(op + 2)
            rl = l.Index(&x) else { -1 }
            rh = h.Index(&x) else { -1 }
            if l.Has(&x) != (rl >= 0) || h.Has(&x) != (rh >= 0) {
                Panic("Has disagrees with Index")
            }
        } else if r == 13 && rng.Next(4) == 0 {
            what = "clear"
            l.Clear()
            h.Clear()
        } else {
            try l.Push(op)
            try h.Push(a, op)
        }
        nl := l.Len()
        nh := h.Len()
        cl := opsfix.ListCap(&*l)
        ch := opsfix.HeapListCap(&*h)
        dl := Digest(l.Items())
        dh := Digest(h.Items())
        try Print(out, "L {op} {what} {rl} {nl} {cl} {dl}\n", (op, what, rl, nl, cl, dl))
        try Print(out, "H {op} {what} {rh} {nh} {ch} {dh}\n", (op, what, rh, nh, ch, dh))
    }
}

Maps(out: mut Writer, m: mut Map[int, int], hm: mut HeapMap[int, int], s: mut Set[int], tm: mut TextMap[int], a: &Alloc, ops: int, seed: u64): OutOfMemory|WriteError!void {
    var rng := Rng(s: seed)
    for op in 0..ops {
        r := rng.Next(12)
        k := rng.Next(64)
        key := Keys[k % Keys.Len()]
        var what := "has"
        var r1 := 0
        var r2 := 0
        var r3 := 0
        var r4 := 0
        var i1 := m.Iter()
        before1 := Content(mut i1).order
        var i2 := hm.Iter()
        before2 := Content(mut i2).order
        before4 := TextContent(&*tm).order
        if r < 3 {
            what = "set"
            try m.Set(k, op)
            try hm.Set(a, k, op)
            r3 = B(try s.Add(k))
            p := try tm.Ensure(a, key, op)
            r4 = *p
            *p = op
        } else if r < 5 {
            what = "ensure"
            p1 := try m.Ensure(k, op)
            r1 = *p1
            *p1 += 1
            p2 := try hm.Ensure(a, k, op)
            r2 = *p2
            *p2 += 1
            r3 = B(s.Has(&k))
            p4 := try tm.Ensure(a, key, op)
            r4 = *p4
            *p4 += 1
        } else if r == 5 {
            what = "get"
            r1 = -1
            r2 = -1
            r4 = -1
            if v := m.Get(&k) {
                r1 = *v
            }
            if v := hm.Get(&k) {
                r2 = *v
            }
            r3 = B(s.Has(&k))
            if v := tm.Get(key) {
                r4 = *v
            }
            if m.Has(&k) != (r1 >= 0) {
                Panic("Map.Has disagrees with Map.Get")
            }
        } else if r == 6 {
            what = "getmut"
            if p := m.GetMut(&k) {
                *p = op + 500000
                r1 = 1
            }
            if p := hm.GetMut(&k) {
                *p = op + 500000
                r2 = 1
            }
            r3 = B(s.Has(&k))
            if p := tm.GetMut(key) {
                *p = op + 500000
                r4 = 1
            }
        } else if r < 9 {
            what = "remove"
            r1 = m.Remove(&k) else { -1 }
            r2 = hm.Remove(&k) else { -1 }
            r3 = B(s.Remove(&k))
            r4 = tm.Remove(a, key) else { -1 }
        } else {
            r1 = B(m.Has(&k))
            r2 = B(hm.Get(&k).IsSome())
            r3 = B(s.Has(&k))
            r4 = B(tm.Get(key).IsSome())
        }
        var j1 := m.Iter()
        c1 := Content(mut j1)
        var j2 := hm.Iter()
        c2 := Content(mut j2)
        c4 := TextContent(&*tm)
        // Inserting or removing may move entries; a write through GetMut or
        // Ensure, or a lookup, may not.
        if what == "getmut" || what == "get" || what == "has" {
            if c1.order != before1 || c2.order != before2 || c4.order != before4 {
                Panic("iteration order changed without an insert or remove")
            }
        }
        n1 := m.Len()
        n2 := hm.Len()
        n4 := tm.Len()
        s1 := c1.sum
        s2 := c2.sum
        s4 := c4.sum
        try Print(out, "{op} {what} {k} {r1} {r2} {r3} {r4} {n1} {n2} {n4} {s1} {s2} {s4}\n", (op, what, k, r1, r2, r3, r4, n1, n2, n4, s1, s2, s4))
    }
}

Arrays(out: mut Writer): WriteError!void {
    var xs: [9]int = [5, 3, 8, 1, 9, 2, 7, 4, 6]
    xs.Reverse()
    xs.Swap(0, 8)
    xs.Swap(4, 4)
    g := B(xs.Get(9).IsNone()) + B(xs.Get(-1).IsNone()) + B(xs.GetMut(9).IsNone()) + B(xs.GetMut(-1).IsNone())
    if p := xs.GetMut(3) {
        *p = 100
    }
    sub := mut xs[2..7]
    sub.Reverse()
    sub.Swap(0, 4)
    var e: [0]int = []
    e.Reverse()
    one := mut xs[4..5]
    one.Reverse()
    d := Digest(xs[..])
    x0 := xs[0]
    x8 := xs[8]
    try Print(out, "A {g} {x0} {x8} {d}\n", (g, x0, x8, d))
    for n in 0..10 {
        var ys: [9]int = [0, 1, 2, 3, 4, 5, 6, 7, 8]
        part := mut ys[0..n]
        part.Reverse()
        dn := Digest(ys[..])
        try Print(out, "R {n} {dn}\n", (n, dn))
    }
}

Texts(out: mut Writer, a: &Alloc): OutOfMemory|WriteError!void {
    var t := HeapText.New(a)
    for i in 0..Keys.Len() {
        t.Push(a, Keys[i]) else {
            t.Free(a)
            fail OutOfMemory
        }
        t.Push(a, "") else {
            t.Free(a)
            fail OutOfMemory
        }
    }
    n := t.Str().Len()
    d := FnvStr(t.Str())
    var sc := t.Str().Scalars()
    var count := 0
    var sd: u64 = 14695981039346656037
    for c := sc.Next() {
        count += 1
        sd = (sd ^ u64(u32(c))) *% 1099511628211
    }
    t.Free(a)
    try Print(out, "T {n} {d} {count} {sd}\n", (n, d, count, sd))
    for c in WordCases {
        try Print(out, "W", ())
        var ws := c.Words()
        for w := ws.Next() {
            wn := w.Len()
            try Print(out, " {wn}", (wn))
        }
        try Print(out, "\n", ())
    }
    for c in Cases {
        try Print(out, "S", ())
        var it := c.Scalars()
        for ch := it.Next() {
            v := u32(ch)
            try Print(out, " {v}", (v))
        }
        try Print(out, "\n", ())
    }
}

Stops(out: mut Writer): WriteError!void {
    var bad := 0
    var first := -1
    for i in 0..StopWords.Len() {
        if opsfix.WordStop(StopWords[i]) != StopWant[i] {
            bad += 1
            if first < 0 {
                first = i
            }
        }
    }
    n := StopWords.Len()
    try Print(out, "P {n} {bad} {first}\n", (n, bad, first))
}

export Main(sys: &System) {
    var out := sys.Stdout()
    a := sys.heap.Alloc()
    var l := List[int].New(sys.arena)
    var h := HeapList[int].New(a)
    Lists(mut out, mut l, mut h, a, OPS, SEED) else {
        h.Free(a)
        Panic("Lists failed")
    }
    h.Free(a)
    done := l.Done()
    dd := Digest(done[..])
    dn := done.Len()
    Print(mut out, "D {dn} {dd}\n", (dn, dd)) else {
        Panic("print failed")
    }
    var m := Map[int, int].New(sys.arena)
    var s := Set[int].New(sys.arena)
    var hm := HeapMap[int, int].New(a)
    var tm := TextMap[int].New(a)
    Maps(mut out, mut m, mut hm, mut s, mut tm, a, OPS, SEED) else {
        hm.Free(a)
        tm.Free(a)
        Panic("Maps failed")
    }
    hm.Free(a)
    tm.Free(a)
    Arrays(mut out) else {
        Panic("Arrays failed")
    }
    Texts(mut out, a) else {
        Panic("Texts failed")
    }
    Stops(mut out) else {
        Panic("Stops failed")
    }
}
`

// opsRng is the program's Rng.
type opsRng struct{ s uint64 }

func (r *opsRng) next(n int) int {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return int(r.s>>33) % n
}

const opsFnvOffset uint64 = 14695981039346656037

func opsFnv(h uint64, x int) uint64 { return (h ^ uint64(x)) * 1099511628211 }

func opsDigest(xs []int) uint64 {
	h := opsFnvOffset
	for _, x := range xs {
		h = opsFnv(h, x)
	}
	return h
}

func opsFnvStr(s string) uint64 {
	h := opsFnvOffset
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return h
}

func opsB2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// opsNextCap is the lists' growth: max(4, 2 cap, need).
func opsNextCap(c, need int) int {
	c *= 2
	if c < 4 {
		c = 4
	}
	if c < need {
		c = need
	}
	return c
}

// opsListModel is std's List and HeapList, with their capacity growth.
func opsListModel(w *strings.Builder, ops int, seed uint64) []int {
	r := &opsRng{s: seed}
	var xs []int
	c := 0
	grow := func() {
		if len(xs) == c {
			c = opsNextCap(c, len(xs)+1)
		}
	}
	for op := 0; op < ops; op++ {
		k := r.next(16)
		n := len(xs)
		what, res := "push", 0
		push := func() {
			grow()
			xs = append(xs, op)
		}
		switch {
		case k == 5:
			what, res = "pop", -1
			if n > 0 {
				res = xs[n-1]
				xs = xs[:n-1]
			}
		case k == 6:
			what = "insert"
			i := r.next(n + 1)
			grow()
			xs = append(xs[:i], append([]int{op}, xs[i:]...)...)
			res = i
		case k == 7:
			what, res = "remove", -1
			if n > 0 {
				i := r.next(n)
				res = xs[i]
				xs = append(xs[:i], xs[i+1:]...)
			}
		case k == 8:
			what, res = "get", -1
			i := r.next(n+2) - 1
			if i >= 0 && i < n {
				res = xs[i]
			}
		case k == 9:
			what = "getmut"
			i := r.next(n+2) - 1
			if i >= 0 && i < n {
				xs[i] = op + 1000000
				res = 1
			}
		case k == 10:
			what, res = "swap", -1
			if n > 0 {
				i, j := r.next(n), r.next(n)
				xs[i], xs[j] = xs[j], xs[i]
				res = i*1000 + j
			}
		case k == 11:
			what = "reverse"
			for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
				xs[i], xs[j] = xs[j], xs[i]
			}
		case k == 12:
			what, res = "index", -1
			x := r.next(op + 2)
			for i, y := range xs {
				if y == x {
					res = i
					break
				}
			}
		case k == 13 && r.next(4) == 0:
			what = "clear"
			xs = xs[:0]
		default:
			push()
		}
		for _, tag := range []string{"L", "H"} {
			fmt.Fprintf(w, "%s %d %s %d %d %d %d\n", tag, op, what, res, len(xs), c, opsDigest(xs))
		}
	}
	return xs
}

func opsSum(m map[int]int) uint64 {
	var s uint64
	for k, v := range m {
		s += uint64(k)*1000003 + uint64(v)
	}
	return s
}

func opsTextSum(m map[string]int) uint64 {
	var s uint64
	for k, v := range m {
		s += opsFnvStr(k)*1000003 + uint64(v)
	}
	return s
}

// opsMapModel is std's Map, HeapMap, Set and TextMap: Set replaces,
// Ensure keeps a present value, Add is true only for a new member, Remove
// returns the value or none.
func opsMapModel(w *strings.Builder, ops int, seed uint64) {
	r := &opsRng{s: seed}
	m, hm, tm := map[int]int{}, map[int]int{}, map[string]int{}
	set := map[int]bool{}
	for op := 0; op < ops; op++ {
		kind := r.next(12)
		k := r.next(64)
		key := opsKeys[k%len(opsKeys)]
		what := "has"
		var r1, r2, r3, r4 int
		ensure := func(mp map[int]int) int {
			if _, ok := mp[k]; !ok {
				mp[k] = op
			}
			v := mp[k]
			mp[k]++
			return v
		}
		switch {
		case kind < 3:
			what = "set"
			m[k], hm[k] = op, op
			r3 = opsB2i(!set[k])
			set[k] = true
			if _, ok := tm[key]; !ok {
				tm[key] = op
			}
			r4 = tm[key]
			tm[key] = op
		case kind < 5:
			what = "ensure"
			r1, r2 = ensure(m), ensure(hm)
			r3 = opsB2i(set[k])
			if _, ok := tm[key]; !ok {
				tm[key] = op
			}
			r4 = tm[key]
			tm[key]++
		case kind == 5:
			what, r1, r2, r4 = "get", -1, -1, -1
			if v, ok := m[k]; ok {
				r1 = v
			}
			if v, ok := hm[k]; ok {
				r2 = v
			}
			r3 = opsB2i(set[k])
			if v, ok := tm[key]; ok {
				r4 = v
			}
		case kind == 6:
			what = "getmut"
			if _, ok := m[k]; ok {
				m[k], r1 = op+500000, 1
			}
			if _, ok := hm[k]; ok {
				hm[k], r2 = op+500000, 1
			}
			r3 = opsB2i(set[k])
			if _, ok := tm[key]; ok {
				tm[key], r4 = op+500000, 1
			}
		case kind < 9:
			what, r1, r2, r4 = "remove", -1, -1, -1
			if v, ok := m[k]; ok {
				r1 = v
				delete(m, k)
			}
			if v, ok := hm[k]; ok {
				r2 = v
				delete(hm, k)
			}
			r3 = opsB2i(set[k])
			delete(set, k)
			if v, ok := tm[key]; ok {
				r4 = v
				delete(tm, key)
			}
		default:
			_, a := m[k]
			_, b := hm[k]
			_, d := tm[key]
			r1, r2, r3, r4 = opsB2i(a), opsB2i(b), opsB2i(set[k]), opsB2i(d)
		}
		fmt.Fprintf(w, "%d %s %d %d %d %d %d %d %d %d %d %d %d\n", op, what, k, r1, r2, r3, r4,
			len(m), len(hm), len(tm), opsSum(m), opsSum(hm), opsTextSum(tm))
	}
}

func opsArrayModel(w *strings.Builder) {
	rev := func(xs []int) {
		for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
			xs[i], xs[j] = xs[j], xs[i]
		}
	}
	xs := []int{5, 3, 8, 1, 9, 2, 7, 4, 6}
	rev(xs)
	xs[0], xs[8] = xs[8], xs[0]
	xs[3] = 100
	sub := xs[2:7]
	rev(sub)
	sub[0], sub[4] = sub[4], sub[0]
	fmt.Fprintf(w, "A 4 %d %d %d\n", xs[0], xs[8], opsDigest(xs))
	for n := 0; n < 10; n++ {
		ys := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
		rev(ys[:min(n, 9)])
		fmt.Fprintf(w, "R %d %d\n", n, opsDigest(ys))
	}
}

// opsWordCases are the iterator cases and texts for Words' word scan:
// controls that are not White_Space (0x01, DEL), NEL, NBSP and other
// non-ASCII White_Space, words longer than 8 bytes at every alignment,
// and text of spaces only.
func opsWordCases() []string {
	cases := iterCases()
	cases = append(cases,
		string([]rune{'a', 0x01, 'b', 0x7F, 'c', 0x85, 'd', 0xA0, 'e', 0x1680, 'f', 0x2028, 'g', 0x3000, 'h', 0x202F, 'i'}),
		strings.Repeat("abcdefghij", 5)+" x\t\n\v\f\r "+strings.Repeat("y", 17)+"\u00e9"+strings.Repeat("z", 9),
		"   \t ", "", "a", " a", "a ", "abcdefgh", "abcdefg ", " abcdefgh ")
	for k := 0; k < 9; k++ {
		cases = append(cases, strings.Repeat(" ", k)+strings.Repeat("w", 7+k)+" "+strings.Repeat("v", 8+k))
	}
	return cases
}

// opsStopWords are little-endian 8-byte words, each byte drawn mostly from
// the edges of 0x21..0x7F (0x00, 0x20, 0x21, 0x7E, 0x7F, 0x80, 0xFF), and
// for each the index of its first byte outside 0x21..0x7F, or 8.
func opsStopWords() ([]uint64, []int) {
	r := rand.New(rand.NewSource(11))
	edges := []byte{0x00, 0x20, 0x21, 0x7E, 0x7F, 0x80, 0xFF, 'a', 'z'}
	var words []uint64
	var stops []int
	for k := 0; k < 3000; k++ {
		var w uint64
		stop := 8
		for i := 0; i < 8; i++ {
			var c byte
			if r.Intn(3) == 0 {
				c = byte(r.Intn(256))
			} else if r.Intn(4) == 0 {
				c = edges[r.Intn(len(edges))]
			} else {
				c = byte(0x21 + r.Intn(0x5F))
			}
			w |= uint64(c) << (8 * i)
			if stop == 8 && (c < 0x21 || c > 0x7F) {
				stop = i
			}
		}
		words = append(words, w)
		stops = append(stops, stop)
	}
	return words, stops
}

func opsTextModel(w *strings.Builder, cases []string) {
	all := strings.Join(opsKeys, "")
	sd, count := opsFnvOffset, 0
	for _, r := range all {
		count++
		sd = (sd ^ uint64(r)) * 1099511628211
	}
	fmt.Fprintf(w, "T %d %d %d %d\n", len(all), opsFnvStr(all), count, sd)
	for _, c := range opsWordCases() {
		fmt.Fprintf(w, "W%s\n", lens(goWordsModel(c)))
	}
	for _, c := range cases {
		w.WriteString("S")
		for _, r := range c {
			fmt.Fprintf(w, " %d", r)
		}
		w.WriteString("\n")
	}
}

// TestStdColl_Section18Ops: seeded operation sequences over List on an Arena
// and HeapList in lockstep, and over Map, HeapMap, Set and TextMap in
// lockstep, against Go models, with every result, length, capacity (max(4, 2
// cap, need), exact) and content digest compared after every operation. The
// edges are drawn on purpose: Get and GetMut at -1 and at Len(), Pop and
// Remove on an empty list, Swap with i == j, Reverse at every length, Clear
// then refill (capacity kept), Set on a present key (replaces), Ensure on a
// present key (keeps, drops the argument), Set.Add of a member (false), Remove
// of an absent key (none), the empty TextMap key, and a GetMut or Ensure that
// writes through the result (iteration order unchanged). Then the Slice[T]
// functions on a var array and on mut sub-slices, HeapText.Push and Str, and
// Scalars.Next over the iterator cases against Go's range over a string. User
// code only: each call goes through check's Slice[T] routing. Dev and release,
// two seeds each.
func TestStdColl_Section18Ops(t *testing.T) {
	t.Parallel()
	const ops = 4000
	cases := iterCases()
	var consts strings.Builder
	fmt.Fprintf(&consts, "const Keys: [%d]str = [", len(opsKeys))
	for i, k := range opsKeys {
		if i > 0 {
			consts.WriteString(", ")
		}
		consts.WriteString(mkString(k))
	}
	fmt.Fprintf(&consts, "]\n\nconst Cases: [%d]str = [", len(cases))
	for i, c := range cases {
		if i > 0 {
			consts.WriteString(",\n    ")
		}
		consts.WriteString(mkString(c))
	}
	wcases := opsWordCases()
	fmt.Fprintf(&consts, "]\n\nconst WordCases: [%d]str = [", len(wcases))
	for i, c := range wcases {
		if i > 0 {
			consts.WriteString(",\n    ")
		}
		consts.WriteString(mkString(c))
	}
	consts.WriteString("]\n\n")
	words, stops := opsStopWords()
	fmt.Fprintf(&consts, "const StopWords: [%d]u64 = [", len(words))
	for i, w := range words {
		if i > 0 {
			consts.WriteString(", ")
		}
		fmt.Fprintf(&consts, "%d", w)
	}
	fmt.Fprintf(&consts, "]\n\nconst StopWant: [%d]int = %s\n\n", len(stops), mkIntArray(stops))
	extra := map[string]string{"core/tests_collops.mk": opsFixCore, "opsfix/opsfix.mk": opsFixModule}
	for _, seed := range []uint64{1, 0x9E3779B97F4A7C15} {
		prog := strings.Replace(opsProgram, "import std.opsfix\n", "import std.opsfix\n\n"+consts.String()+fmt.Sprintf("const OPS = %d\nconst SEED: u64 = %d\n", ops, seed), 1)
		var want strings.Builder
		final := opsListModel(&want, ops, seed)
		fmt.Fprintf(&want, "D %d %d\n", len(final), opsDigest(final))
		opsMapModel(&want, ops, seed)
		opsArrayModel(&want)
		opsTextModel(&want, cases)
		fmt.Fprintf(&want, "P %d 0 -1\n", len(words))
		for _, flags := range [][]string{nil, {"--release"}} {
			t.Run(fmt.Sprintf("seed-%x%v", seed, flags), func(t *testing.T) {
				got := collExec(t, collBuildWith(t, extra, prog, flags...))
				diffLines(t, got, want.String())
				opsCoverage(t, got)
			})
		}
	}
}

// opsCoverage fails unless the run reached each edge the file comment
// names at least once.
func opsCoverage(t *testing.T, got string) {
	t.Helper()
	seen := map[string]int{}
	for _, line := range strings.Split(got, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 7 && f[0] == "L":
			n := f[4]
			switch f[2] {
			case "pop", "remove", "get", "index":
				if f[3] == "-1" {
					seen[f[2]+" none"]++
				} else {
					seen[f[2]+" some"]++
				}
			case "getmut":
				seen["getmut "+f[3]]++
			case "swap":
				var res int
				if _, err := fmt.Sscan(f[3], &res); err == nil && res >= 0 && res/1000 == res%1000 {
					seen["swap i == j"]++
				}
			case "clear":
				if n == "0" {
					seen["clear"]++
				}
			}
		case len(f) == 13 && f[0] != "L" && f[0] != "H" && f[0] != "S":
			switch f[1] {
			case "set":
				seen["set add "+f[5]]++
			case "remove":
				if f[3] == "-1" {
					seen["map remove none"]++
				} else {
					seen["map remove some"]++
				}
			case "getmut":
				seen["map getmut "+f[3]]++
			case "ensure":
				seen["ensure"]++
			}
		}
	}
	var missing []string
	for _, e := range []string{"pop none", "pop some", "remove none", "remove some", "get none", "get some",
		"index none", "index some", "getmut 0", "getmut 1", "swap i == j", "clear",
		"set add 0", "set add 1", "map remove none", "map remove some", "map getmut 0", "map getmut 1", "ensure"} {
		if seen[e] == 0 {
			missing = append(missing, e)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("edges never reached: %v", missing)
	}
}
