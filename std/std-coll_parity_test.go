// Why: std.coll code must give the same results at compile time, in the
// evaluator, as natively.
// What: TestStdColl_EvalParity: Sort, Map insertion, removal and iteration,
// Hasher, HashOf, Words, Lines and Split.
// How: each runs as a const initializer and natively in one program; both must
// equal a Go model (sort.Slice, swissModel, SipHash-1-3).

package std

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

const parityText = "alpha beta\u3000gamma,,delta\r\nepsilon\n\nzeta,eta;theta\r iota\tkappa,\n"

// collParityCore is written to std/core/tests_collparity.mk: four digests,
// each a const (evaluated at compile time, with enough fuel) and the same
// call made at run time; Map runs over an Arena on a bump heap in a local
// byte array, so the same code runs in both.
var collParityCore = strings.Replace(`import std.raw

const ParityHeapBytes = 65536
const ParityHeapTable = AllocTable(alloc: ParityHeapAlloc, resize: ParityHeapResize, free: ParityHeapFree)

// A bump heap over a caller's byte array: 16-byte units, the count in the
// first two bytes; never frees. The same code runs at compile time.
ParityHeapAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is the fixture's byte array" {
        used := int(raw.Read(ctx)) + int(raw.Read(raw.Offset(ctx, 1))) * 256
        units := (size + 15) / 16
        if 16 + (used + units) * 16 > ParityHeapBytes {
            return raw.Null[u8]()
        }
        n := used + units
        raw.Write(ctx, Trunc[u8](n))
        raw.Write(raw.Offset(ctx, 1), Trunc[u8](n / 256))
        return raw.Offset(ctx, 16 + used * 16)
    }
}

ParityHeapResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

ParityHeapFree(ctx: *u8, p: *u8, size: int, align: int) {
}

Fnv(h: u64, x: u64): u64 {
    return (h ^ x) *% 1099511628211
}

ParityPairLess(a: &(k: int, id: int), b: &(k: int, id: int)): bool {
    return a.k < b.k
}

// Sort of 400 seeded (key, id) pairs with many ties, by key only, so the
// permutation of ties is pdqsort's own: the digest of the ids in order.
export CollParitySort(): u64 {
    var xs: [400](k: int, id: int) = [(k: 0, id: 0); 400]
    var s: u64 = 99
    for i in 0..400 {
        s = s *% 6364136223846793005 +% 1442695040888963407
        xs[i] = (k: Trunc[int](s >> 33) % 37, id: i)
    }
    xs.Sort(ParityPairLess)
    var h: u64 = 14695981039346656037
    for p in xs {
        h = Fnv(h, Trunc[u64](p.id))
    }
    return h
}

// Map[int, int] under 300 seeded inserts and 120 removes (tombstones, one
// growth path), then the iteration order and the values: the digest of
// (key, value) in iteration order and the length.
export CollParityMap(): u64 {
    var mem: [65536]u8 = [0; 65536]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ParityHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var m := Map[int, int].New(&a)
        var s: u64 = 7
        for i in 0..300 {
            s = s *% 6364136223846793005 +% 1442695040888963407
            k := Trunc[int](s >> 40)
            m.Set(k, i) else {
                a.Free()
                return 2
            }
            if i % 5 == 2 {
                _ = m.Remove(&k)
            }
        }
        var h: u64 = 14695981039346656037
        var it := m.Iter()
        for e := it.Next() {
            h = Fnv(Fnv(h, Trunc[u64](*e.key)), Trunc[u64](*e.value))
        }
        h = Fnv(h, Trunc[u64](m.Len()))
        a.Free()
        return h
    }
}

const ParityText = PARITYTEXT

// Hasher over bytes in pieces and words, HashOf of str and int keys.
export CollParityHash(): u64 {
    var h := NewHasher()
    b := ParityText.AsBytes()
    h.Bytes(b[0..3])
    h.U64(0x0123456789ABCDEF)
    h.Bytes(b[3..b.Len()])
    h.U64(42)
    var r := HashFinish(mut h)
    for k in 0..20 {
        r = Fnv(r, HashOf(&k))
    }
    t := ParityText
    r = Fnv(r, HashOf(&t))
    return r
}

// Words, Lines, Split (one byte and longer) over ParityText: the digest of
// the piece lengths and first bytes.
export CollParityText(): u64 {
    var h: u64 = 14695981039346656037
    var ws := ParityText.Words()
    for w := ws.Next() {
        h = Fnv(Fnv(h, Trunc[u64](w.Len())), u64(w.Byte(0)))
    }
    h = Fnv(h, 1)
    var ls := ParityText.Lines()
    for l := ls.Next() {
        h = Fnv(h, Trunc[u64](l.Len()))
    }
    h = Fnv(h, 2)
    var sp := ParityText.Split(",")
    for p := sp.Next() {
        h = Fnv(h, Trunc[u64](p.Len()))
    }
    h = Fnv(h, 3)
    var sq := ParityText.Split("\r\n")
    for p := sq.Next() {
        h = Fnv(h, Trunc[u64](p.Len()))
    }
    return h
}

const ParitySortC = CollParitySort() fuel 100_000_000
const ParityMapC = CollParityMap() fuel 100_000_000
const ParityHashC = CollParityHash()
const ParityTextC = CollParityText()

// The compile-time values beside the same calls run natively.
export CollParityAll(): [8]u64 {
    return [ParitySortC, CollParitySort(), ParityMapC, CollParityMap(), ParityHashC, CollParityHash(), ParityTextC, CollParityText()]
}
`, "PARITYTEXT", mkString(parityText), 1)

const collParityModule = `import std.core

export All(): [8]u64 {
    return core.CollParityAll()
}
`

func fnvStep(h, x uint64) uint64 { return (h ^ x) * 1099511628211 }

const fnvStart = 14695981039346656037

// paritySortWant is the digest of the ids after Go's sort.Slice by key.
func paritySortWant() uint64 {
	type pair struct{ k, id int }
	xs := make([]pair, 400)
	s := uint64(99)
	for i := range xs {
		s = s*6364136223846793005 + 1442695040888963407
		xs[i] = pair{int(s>>33) % 37, i}
	}
	mkSortModel(xs, 16, func(a, b *pair) bool { return a.k < b.k })
	h := uint64(fnvStart)
	for _, p := range xs {
		h = fnvStep(h, uint64(p.id))
	}
	return h
}

// parityMapWant runs the same Set and Remove sequence on the Go table model.
func parityMapWant() uint64 {
	t := &swissModel{}
	s := uint64(7)
	for i := 0; i < 300; i++ {
		s = s*6364136223846793005 + 1442695040888963407
		k := int(s >> 40)
		hash := modelHash(k)
		if at, free := t.lookup(hash, k); at < 0 {
			j := t.place(hash, free)
			t.key[j], t.val[j] = k, i
		} else {
			t.val[at] = i
		}
		if i%5 == 2 {
			if j := t.find(hash, k); j >= 0 {
				t.erase(j)
			}
		}
	}
	h := uint64(fnvStart)
	for i := 0; i < t.cap; i++ {
		if t.ctrl[i] < mDeleted {
			h = fnvStep(fnvStep(h, uint64(t.key[i])), uint64(t.val[i]))
		}
	}
	return fnvStep(h, uint64(t.used))
}

func parityHashWant() uint64 {
	b := []byte(parityText)
	var all []byte
	all = append(all, b[:3]...)
	all = binary.LittleEndian.AppendUint64(all, 0x0123456789ABCDEF)
	all = append(all, b[3:]...)
	all = binary.LittleEndian.AppendUint64(all, 42)
	r := foldHash(all)
	for k := 0; k < 20; k++ {
		r = fnvStep(r, modelHash(k))
	}
	return fnvStep(r, foldHashStr(parityText))
}

func parityTextWant() uint64 {
	h := uint64(fnvStart)
	for _, w := range goWordsModel(parityText) {
		h = fnvStep(fnvStep(h, uint64(len(w))), uint64(w[0]))
	}
	h = fnvStep(h, 1)
	for _, l := range goLinesModel(parityText) {
		h = fnvStep(h, uint64(len(l)))
	}
	h = fnvStep(h, 2)
	for _, p := range strings.Split(parityText, ",") {
		h = fnvStep(h, uint64(len(p)))
	}
	h = fnvStep(h, 3)
	for _, p := range strings.Split(parityText, "\r\n") {
		h = fnvStep(h, uint64(len(p)))
	}
	return h
}

// TestStdColl_EvalParity: each digest's compile-time value equals its native
// value and the Go model's, at dev and at release.
func TestStdColl_EvalParity(t *testing.T) {
	t.Parallel()
	src := `import std.collpar

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for x in collpar.All() {
        try Print(mut out, "{x}\n", (x))
    }
}
`
	var want strings.Builder
	for _, w := range []uint64{paritySortWant(), parityMapWant(), parityHashWant(), parityTextWant()} {
		fmt.Fprintf(&want, "%d\n%d\n", w, w)
	}
	extra := map[string]string{"core/tests_collparity.mk": collParityCore, "collpar/collpar.mk": collParityModule}
	for _, flags := range [][]string{nil, {"--release"}} {
		got := collExec(t, collBuildWith(t, extra, src, flags...))
		diffLines(t, got, want.String())
	}
}
