// Why: std.coll's containers have many edge states (ring wrap-around, node
// merges, free-list reuse) that a few hand-written tests miss.
// What: differential tests of Deque, HeapDeque, PriorityQueue, BTree and
// HeapBTree against Go models (TestSTDCOLL_*Diff), dev and release.
// How: a native program and a Go model run the same seeded splitmix64
// operation sequence, growing then shrinking; outputs must match line for line.

package std

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// c2Gold and c2Mix are splitmix64 (Steele, Lea and Flood 2014), as the
// programs' Mix.
const c2Gold = 0x9E3779B97F4A7C15

func c2Mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

const c2MixMk = `
const GOLD: u64 = 0x9E3779B97F4A7C15

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

Opt(o: ?&int): int {
    if o.IsSome() {
        return *o.Must()
    }
    return -1
}
`

// c2Program puts the consts and Mix after the program's imports.
func c2Program(src string, consts map[string]string) string {
	var b strings.Builder
	keys := make([]string, 0, len(consts))
	for k := range consts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "const %s = %s\n", k, consts[k])
	}
	b.WriteString(c2MixMk)
	return strings.Replace(src, "// CONSTS\n", b.String(), 1)
}

var c2Flags = [][]string{nil, {"--release"}}

func c2FlagName(flags []string) string {
	if len(flags) == 0 {
		return "dev"
	}
	return "release"
}

// ---- Deque and HeapDeque against a Go slice ----

const c2DequeProgram = `import std.coll

// CONSTS

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    h := sys.heap.Alloc()
    var d := coll.Deque[int].New(sys.arena)
    var q := coll.HeapDeque[int].New(h)
    onerr q.Free(h)
    var s: u64 = SEED
    for op in 0..OPS {
        s = s +% GOLD
        r := Mix(s)
        var what := Trunc[int](r & 7)
        if (op / PHASE) % 2 == 1 && what < 3 {
            what = 4 + (what & 1)
        }
        v := Trunc[int]((r >> 8) & 0xFFFFF)
        if what < 2 {
            try d.Push(v)
            try q.Push(h, v)
            try Print(mut out, "B {v}\n", (v))
        } else if what < 4 {
            try d.PushFront(v)
            try q.PushFront(h, v)
            try Print(mut out, "F {v}\n", (v))
        } else if what == 4 {
            a := d.PopFront() else { -1 }
            b := q.PopFront() else { -1 }
            try Print(mut out, "PF {a} {b}\n", (a, b))
        } else if what == 5 {
            a := d.Pop() else { -1 }
            b := q.Pop() else { -1 }
            try Print(mut out, "PB {a} {b}\n", (a, b))
        } else {
            n := d.Len()
            m := q.Len()
            i := Trunc[int]((r >> 40) % Trunc[u64](n + 2)) - 1
            a := Opt(d.Get(i))
            b := Opt(q.Get(i))
            try Print(mut out, "G {i} {n} {m} {a} {b}\n", (i, n, m, a, b))
        }
    }
    n := d.Len()
    var sum := 0
    for i in 0..n {
        sum = sum *% 31 +% Opt(d.Get(i)) +% Opt(q.Get(i))
    }
    try Print(mut out, "E {n} {sum}\n", (n, sum))
    q.Free(h)
}
`

func c2DequeModel(seed uint64, ops, phase int) string {
	var b strings.Builder
	var d []int
	s := seed
	for op := 0; op < ops; op++ {
		s += c2Gold
		r := c2Mix(s)
		what := int(r & 7)
		if (op/phase)%2 == 1 && what < 3 {
			what = 4 + (what & 1)
		}
		v := int((r >> 8) & 0xFFFFF)
		switch {
		case what < 2:
			d = append(d, v)
			fmt.Fprintf(&b, "B %d\n", v)
		case what < 4:
			d = append([]int{v}, d...)
			fmt.Fprintf(&b, "F %d\n", v)
		case what == 4:
			a := -1
			if len(d) > 0 {
				a, d = d[0], d[1:]
			}
			fmt.Fprintf(&b, "PF %d %d\n", a, a)
		case what == 5:
			a := -1
			if len(d) > 0 {
				a, d = d[len(d)-1], d[:len(d)-1]
			}
			fmt.Fprintf(&b, "PB %d %d\n", a, a)
		default:
			n := len(d)
			i := int((r>>40)%uint64(n+2)) - 1
			a := -1
			if i >= 0 && i < n {
				a = d[i]
			}
			fmt.Fprintf(&b, "G %d %d %d %d %d\n", i, n, n, a, a)
		}
	}
	sum := 0
	for _, x := range d {
		sum = sum*31 + x + x
	}
	fmt.Fprintf(&b, "E %d %d\n", len(d), sum)
	return b.String()
}

// TestSTDCOLL_DequeDiff: Deque and HeapDeque against a Go slice over
// seeded pushes and pops at both ends and Get at every index from -1 to
// Len(), in a growing and a shrinking phase (wrap-around at every
// capacity, empty pops, refill after empty).
func TestSTDCOLL_DequeDiff(t *testing.T) {
	t.Parallel()
	for _, seed := range []uint64{1, 0xDEADBEEF} {
		const ops, phase = 30000, 3000
		src := c2Program(c2DequeProgram, map[string]string{
			"SEED: u64": fmt.Sprint(seed), "OPS": fmt.Sprint(ops), "PHASE": fmt.Sprint(phase)})
		want := c2DequeModel(seed, ops, phase)
		for _, flags := range c2Flags {
			t.Run(fmt.Sprintf("seed-%x/%s", seed, c2FlagName(flags)), func(t *testing.T) {
				t.Parallel()
				diffLines(t, collRun(t, src, flags...), want)
			})
		}
	}
}

// ---- PriorityQueue against container/heap ----

const c2PQProgram = `import std.coll

// CONSTS

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var q := coll.PriorityQueue[int].New(sys.arena)
    var s: u64 = SEED
    for op in 0..OPS {
        s = s +% GOLD
        r := Mix(s)
        var what := Trunc[int](r & 7)
        if (op / PHASE) % 2 == 1 && what < 3 {
            what = 4
        }
        v := Trunc[int]((r >> 8) % VALS)
        if what < 4 {
            try q.Push(v)
        } else if what < 6 {
            a := q.Pop() else { -1 }
            try Print(mut out, "O {a}\n", (a))
        } else if what == 6 {
            a := Opt(q.Peek())
            try Print(mut out, "K {a}\n", (a))
        } else {
            n := q.Len()
            try Print(mut out, "L {n}\n", (n))
        }
    }
    var sum := 0
    var prev := -1
    var sorted := true
    for x := q.Pop() {
        if x < prev {
            sorted = false
        }
        prev = x
        sum = sum *% 31 +% x
    }
    try Print(mut out, "E {sum} {sorted}\n", (sum, sorted))
}
`

type c2IntHeap []int

func (h c2IntHeap) Len() int           { return len(h) }
func (h c2IntHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h c2IntHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *c2IntHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *c2IntHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func c2PQModel(seed uint64, ops, phase int, vals uint64) string {
	var b strings.Builder
	h := &c2IntHeap{}
	s := seed
	for op := 0; op < ops; op++ {
		s += c2Gold
		r := c2Mix(s)
		what := int(r & 7)
		if (op/phase)%2 == 1 && what < 3 {
			what = 4
		}
		v := int((r >> 8) % vals)
		switch {
		case what < 4:
			heap.Push(h, v)
		case what < 6:
			a := -1
			if h.Len() > 0 {
				a = heap.Pop(h).(int)
			}
			fmt.Fprintf(&b, "O %d\n", a)
		case what == 6:
			a := -1
			if h.Len() > 0 {
				a = (*h)[0]
			}
			fmt.Fprintf(&b, "K %d\n", a)
		default:
			fmt.Fprintf(&b, "L %d\n", h.Len())
		}
	}
	sum := 0
	for h.Len() > 0 {
		sum = sum*31 + heap.Pop(h).(int)
	}
	fmt.Fprintf(&b, "E %d true\n", sum)
	return b.String()
}

// TestSTDCOLL_PriorityQueueDiff: PriorityQueue against container/heap
// (a binary min-heap: equal ints are indistinguishable, so the pop order is
// determined) with many ties (100 values) and few (2^40), in a growing and a
// shrinking phase, and a final drain checked sorted.
func TestSTDCOLL_PriorityQueueDiff(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		seed uint64
		vals uint64
	}{{7, 100}, {0xC0FFEE, 1 << 40}} {
		const ops, phase = 40000, 5000
		src := c2Program(c2PQProgram, map[string]string{
			"SEED: u64": fmt.Sprint(c.seed), "OPS": fmt.Sprint(ops), "PHASE": fmt.Sprint(phase),
			"VALS: u64": fmt.Sprint(c.vals)})
		want := c2PQModel(c.seed, ops, phase, c.vals)
		for _, flags := range c2Flags {
			t.Run(fmt.Sprintf("vals-%d/%s", c.vals, c2FlagName(flags)), func(t *testing.T) {
				t.Parallel()
				diffLines(t, collRun(t, src, flags...), want)
			})
		}
	}
}

// ---- BTree and HeapBTree against a Go sorted map ----

const c2BTreeProgram = `import std.coll

// CONSTS
// KEYDEFS
// VALDEFS

OptVal(o: ?VTYPE): int {
    if x := o {
        return ValInt(&x)
    }
    return -1
}

OptRef(o: ?&VTYPE): int {
    if x := o {
        return ValInt(x)
    }
    return -1
}

// Key is the key of a set: random, ascending, descending or sawtooth.
Key(op: int, r: u64): int {
    if MODE == 1 {
        return op
    } else if MODE == 2 {
        return OPS - op
    } else if MODE == 3 {
        return (op % 64) * 1000 + op / 64
    }
    return Trunc[int]((r >> 12) % KEYS)
}

Scan(it: mut coll.BTreeIter[KTYPE, VTYPE]): (n: int, chk: int, ok: bool) {
    var n := 0
    var chk := 0
    var prev := -1
    var ok := true
    for e := it.Next() {
        k := KeyInt(e.key)
        if k <= prev {
            ok = false
        }
        prev = k
        n += 1
        chk = chk *% 31 +% k *% 7 +% ValInt(e.value)
    }
    return (n: n, chk: chk, ok: ok)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    h := sys.heap.Alloc()
    var t := coll.BTree[KTYPE, VTYPE].New(sys.arena)
    var u := coll.HeapBTree[KTYPE, VTYPE].New(h)
    onerr u.Free(h)
    var s: u64 = SEED
    for op in 0..OPS {
        s = s +% GOLD
        r := Mix(s)
        var what := Trunc[int](r & 15)
        shrink := (op / PHASE) % 2 == 1
        if shrink && what < 7 {
            what = 7
        }
        rk := Trunc[int]((r >> 12) % KEYS)
        if what < 7 {
            k := Key(op, r)
            v := Trunc[int]((r >> 40) & 0xFFFF)
            try t.Set(MkKey(k), MkVal(v))
            try u.Set(h, MkKey(k), MkVal(v))
        } else if what < 10 {
            var k := rk
            if MODE != 0 {
                k = Trunc[int]((r >> 12) % Trunc[u64](OPS + 1))
            }
            kk := MkKey(k)
            a := OptVal(t.Remove(&kk))
            b := OptVal(u.Remove(&kk))
            try Print(mut out, "R {k} {a} {b}\n", (k, a, b))
        } else if what == 10 {
            rkk := MkKey(rk)
            a := t.Has(&rkk)
            b := u.Has(&rkk)
            try Print(mut out, "H {rk} {a} {b}\n", (rk, a, b))
        } else if what == 11 {
            rkk := MkKey(rk)
            var a := -1
            if p := t.GetMut(&rkk) {
                a = Bump(p)
            }
            var b := -1
            if p := u.GetMut(&rkk) {
                b = Bump(p)
            }
            try Print(mut out, "M {rk} {a} {b}\n", (rk, a, b))
        } else if what < 13 {
            rkk := MkKey(rk)
            a := OptRef(t.Get(&rkk))
            b := OptRef(u.Get(&rkk))
            try Print(mut out, "G {rk} {a} {b}\n", (rk, a, b))
        } else if what == 13 {
            lo := rk
            hi := lo + Trunc[int]((r >> 52) % 400) - 40
            lok := MkKey(lo)
            hik := MkKey(hi)
            var i := t.Range(&lok, &hik)
            x := Scan(mut i)
            var j := u.Range(&lok, &hik)
            y := Scan(mut j)
            xn := x.n
            xc := x.chk
            xo := x.ok
            yn := y.n
            yc := y.chk
            try Print(mut out, "Q {lo} {hi} {xn} {xc} {xo} {yn} {yc}\n", (lo, hi, xn, xc, xo, yn, yc))
        } else if what == 14 {
            a := t.Len()
            b := u.Len()
            try Print(mut out, "L {a} {b}\n", (a, b))
        } else if op % 8 == 0 {
            var i := t.Iter()
            x := Scan(mut i)
            var j := u.Iter()
            y := Scan(mut j)
            xn := x.n
            xc := x.chk
            xo := x.ok
            yn := y.n
            yc := y.chk
            try Print(mut out, "I {xn} {xc} {xo} {yn} {yc}\n", (xn, xc, xo, yn, yc))
        }
    }
    var i := t.Iter()
    x := Scan(mut i)
    xn := x.n
    xc := x.chk
    try Print(mut out, "E {xn} {xc}\n", (xn, xc))
    u.Free(h)
}
`

func c2BTreeModel(seed uint64, ops, phase, mode int, keys uint64) string {
	var b strings.Builder
	m := map[int]int{}
	var sorted []int
	set := func(k, v int) {
		if _, ok := m[k]; !ok {
			i := sort.SearchInts(sorted, k)
			sorted = append(sorted, 0)
			copy(sorted[i+1:], sorted[i:])
			sorted[i] = k
		}
		m[k] = v
	}
	remove := func(k int) int {
		v, ok := m[k]
		if !ok {
			return -1
		}
		delete(m, k)
		i := sort.SearchInts(sorted, k)
		sorted = append(sorted[:i], sorted[i+1:]...)
		return v
	}
	scan := func(lo, hi int, all bool) (int, int) {
		n, chk := 0, 0
		i := 0
		if !all {
			if lo >= hi {
				return 0, 0
			}
			i = sort.SearchInts(sorted, lo)
		}
		for ; i < len(sorted) && (all || sorted[i] < hi); i++ {
			k := sorted[i]
			n++
			chk = chk*31 + k*7 + m[k]
		}
		return n, chk
	}
	key := func(op int, r uint64) int {
		switch mode {
		case 1:
			return op
		case 2:
			return ops - op
		case 3:
			return (op%64)*1000 + op/64
		}
		return int((r >> 12) % keys)
	}
	s := seed
	for op := 0; op < ops; op++ {
		s += c2Gold
		r := c2Mix(s)
		what := int(r & 15)
		if (op/phase)%2 == 1 && what < 7 {
			what = 7
		}
		rk := int((r >> 12) % keys)
		switch {
		case what < 7:
			set(key(op, r), int((r>>40)&0xFFFF))
		case what < 10:
			k := rk
			if mode != 0 {
				k = int((r >> 12) % uint64(ops+1))
			}
			a := remove(k)
			fmt.Fprintf(&b, "R %d %d %d\n", k, a, a)
		case what == 10:
			_, ok := m[rk]
			fmt.Fprintf(&b, "H %d %v %v\n", rk, ok, ok)
		case what == 11:
			a := -1
			if v, ok := m[rk]; ok {
				m[rk] = v + 1
				a = v + 1
			}
			fmt.Fprintf(&b, "M %d %d %d\n", rk, a, a)
		case what < 13:
			a := -1
			if v, ok := m[rk]; ok {
				a = v
			}
			fmt.Fprintf(&b, "G %d %d %d\n", rk, a, a)
		case what == 13:
			lo := rk
			hi := lo + int((r>>52)%400) - 40
			n, chk := scan(lo, hi, false)
			fmt.Fprintf(&b, "Q %d %d %d %d true %d %d\n", lo, hi, n, chk, n, chk)
		case what == 14:
			fmt.Fprintf(&b, "L %d %d\n", len(m), len(m))
		case op%8 == 0:
			n, chk := scan(0, 0, true)
			fmt.Fprintf(&b, "I %d %d true %d %d\n", n, chk, n, chk)
		}
	}
	n, chk := scan(0, 0, true)
	fmt.Fprintf(&b, "E %d %d\n", n, chk)
	return b.String()
}

// c2Keys are the key types of the BTree program: int (8 bytes, so BtB
// 16 with an int value) and Wide (24 bytes, a user Ord, so BtB 6).
var c2Keys = []struct{ name, typ, defs string }{
	{"int", "int", `
MkKey(k: int): int {
    return k
}

KeyInt(x: &int): int {
    return *x
}
`},
	{"wide", "Wide", `
struct Wide {
    hi: int
    k: int
    tag: int
}

impl Ord for Wide {
    Less(&self, other: &Wide): bool {
        if self.hi != other.hi {
            return self.hi < other.hi
        }
        return self.k < other.k
    }
}

MkKey(k: int): Wide {
    return Wide(hi: k >> 10, k: k, tag: 7)
}

KeyInt(x: &Wide): int {
    return x.k
}
`},
}

// c2Vals are the value types: int (an int key's slot is 16 bytes, BtB
// 16), Pair (16 bytes: slot 24, so the slot budget gives BtB 10) and Fat
// (248 bytes: slot 256, so BtB is BtLo, 6). ValInt checks a
// value's redundant fields, so a slot torn by a shift or a split shows.
var c2Vals = []struct{ name, typ, defs string }{
	{"int", "int", `
MkVal(v: int): int {
    return v
}

ValInt(x: &int): int {
    return *x
}

Bump(x: mut int): int {
    *x += 1
    return *x
}
`},
	{"pair", "Pair", `
struct Pair {
    v: int
    w: int
}

MkVal(v: int): Pair {
    return Pair(v: v, w: v ^ 0x5A5A)
}

ValInt(x: &Pair): int {
    if x.w != x.v ^ 0x5A5A {
        return -7
    }
    return x.v
}

Bump(x: mut Pair): int {
    x.v += 1
    x.w = x.v ^ 0x5A5A
    return x.v
}
`},
	{"fat", "Fat", `
struct Fat {
    v: int
    pad: [30]int
}

MkVal(v: int): Fat {
    var f := Fat(v: v, pad: [0; 30])
    f.pad[29] = v
    f.pad[0] = 3
    return f
}

ValInt(x: &Fat): int {
    if x.pad[29] != x.v || x.pad[0] != 3 {
        return -7
    }
    return x.v
}

Bump(x: mut Fat): int {
    x.v += 1
    x.pad[29] = x.v
    return x.v
}
`},
}

// TestSTDCOLL_BTreeDiff: BTree and HeapBTree against a Go map with a
// sorted key slice over seeded Set (new and present keys), Remove (present
// and absent), Get, Has, GetMut adding 1 in place, Range (lo <
// hi, lo >= hi, bounds absent and past both ends) and full Iter, with
// random keys over a small and a large key space, and ascending,
// descending and sawtooth insertion orders (the split patterns), in
// growing and shrinking phases (merges down to the empty tree, and refill
// from the free list). Keys int and Wide with int values, and int keys
// with the Pair and Fat values, whose B the slot budget sets.
func TestSTDCOLL_BTreeDiff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seed uint64
		mode int
		keys uint64
	}{
		{"random-small", 3, 0, 300},
		{"random-large", 0xFEED, 0, 20000},
		{"ascending", 5, 1, 30000},
		{"descending", 6, 2, 30000},
		{"sawtooth", 8, 3, 70000},
	}
	type combo struct{ key, val int }
	var combos []combo
	for k := range c2Keys {
		combos = append(combos, combo{k, 0})
	}
	combos = append(combos, combo{0, 1}, combo{0, 2})
	for _, cb := range combos {
		key, val := c2Keys[cb.key], c2Vals[cb.val]
		for _, c := range cases {
			if val.name == "fat" && (c.name == "random-small" || c.name == "descending") {
				continue
			}
			const ops, phase = 24000, 4000
			src := c2Program(c2BTreeProgram, map[string]string{
				"SEED: u64": fmt.Sprint(c.seed), "OPS": fmt.Sprint(ops), "PHASE": fmt.Sprint(phase),
				"MODE": fmt.Sprint(c.mode), "KEYS: u64": fmt.Sprint(c.keys)})
			src = strings.ReplaceAll(strings.Replace(src, "// KEYDEFS\n", key.defs, 1), "KTYPE", key.typ)
			src = strings.ReplaceAll(strings.Replace(src, "// VALDEFS\n", val.defs, 1), "VTYPE", val.typ)
			want := c2BTreeModel(c.seed, ops, phase, c.mode, c.keys)
			for _, flags := range c2Flags {
				t.Run(key.name+"-"+val.name+"/"+c.name+"/"+c2FlagName(flags), func(t *testing.T) {
					t.Parallel()
					diffLines(t, collRun(t, src, flags...), want)
				})
			}
		}
	}
}

// ---- Bitset against []bool ----

const c2BitsetProgram = `import std.coll

// CONSTS

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var s: u64 = SEED
    for si in 0..SIZES.Len() {
        size := SIZES[si]
        var b := try coll.Bitset.New(sys.arena, size)
        l := b.Len()
        c0 := b.Count()
        try Print(mut out, "N {size} {l} {c0}\n", (size, l, c0))
        if size == 0 {
            continue
        }
        for op in 0..OPS {
            s = s +% GOLD
            r := Mix(s)
            i := Trunc[int]((r >> 8) % Trunc[u64](size))
            what := Trunc[int](r & 3)
            if what < 2 {
                b.Set(i)
            } else if what == 2 {
                b.Remove(i)
            } else {
                x := b.Has(i)
                try Print(mut out, "H {i} {x}\n", (i, x))
            }
            if op % 37 == 0 {
                c := b.Count()
                try Print(mut out, "C {c}\n", (c))
            }
        }
        var n := 0
        for i in 0..size {
            if b.Has(i) {
                n += 1
            }
        }
        c := b.Count()
        try Print(mut out, "E {n} {c}\n", (n, c))
    }
}
`

func c2BitsetModel(seed uint64, ops int, sizes []int) string {
	var b strings.Builder
	s := seed
	for _, size := range sizes {
		bits := make([]bool, size)
		fmt.Fprintf(&b, "N %d %d 0\n", size, size)
		if size == 0 {
			continue
		}
		count := func() int {
			n := 0
			for _, x := range bits {
				if x {
					n++
				}
			}
			return n
		}
		for op := 0; op < ops; op++ {
			s += c2Gold
			r := c2Mix(s)
			i := int((r >> 8) % uint64(size))
			switch what := int(r & 3); {
			case what < 2:
				bits[i] = true
			case what == 2:
				bits[i] = false
			default:
				fmt.Fprintf(&b, "H %d %v\n", i, bits[i])
			}
			if op%37 == 0 {
				fmt.Fprintf(&b, "C %d\n", count())
			}
		}
		fmt.Fprintf(&b, "E %d %d\n", count(), count())
	}
	return b.String()
}

// TestSTDCOLL_BitsetDiff: Bitset against []bool over seeded Set, Remove and
// Has at lengths around every word edge (0, 1, 63, 64, 65, 127, 128, 129,
// 1000, 4097), with Count checked along the way and against a Has scan.
func TestSTDCOLL_BitsetDiff(t *testing.T) {
	t.Parallel()
	sizes := []int{0, 1, 63, 64, 65, 127, 128, 129, 1000, 4097}
	const ops = 3000
	const seed = 11
	src := c2Program(c2BitsetProgram, map[string]string{
		"SEED: u64": fmt.Sprint(seed), "OPS": fmt.Sprint(ops),
		fmt.Sprintf("SIZES: [%d]int", len(sizes)): mkIntArray(sizes)})
	want := c2BitsetModel(seed, ops, sizes)
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, collRun(t, src, flags...), want)
		})
	}
}
