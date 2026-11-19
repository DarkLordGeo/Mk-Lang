// Why: the fold hash and Map.New's iteration order are Mk code that must
// match their Go model, at compile time and at run time alike.
// What: std's Hasher, HashOf and Map.New iteration order against the model
// of std-hash_model_test.go, and NewKeyed against SipHash-1-3 (sip13k).
// How: each call as a const evaluated at compile time and run natively, dev
// and release; the fixture core/tests_hashfix.mk lives only in a std copy.

package std

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// hashFixWords is the str key set: 260 distinct words of 1 to 72 bytes, so
// every tail length, 0 to 4 word pairs and both lanes are covered.
func hashFixWords() []string {
	var ws []string
	for i := 0; i < 260; i++ {
		n := 1 + i%72
		var b strings.Builder
		fmt.Fprintf(&b, "%d", i)
		for b.Len() < n {
			b.WriteByte(byte('a' + (i*7+b.Len())%26))
		}
		ws = append(ws, b.String())
	}
	return ws
}

var hashFixCore = `import std.raw

const HfHeapBytes = 131072
const HfHeapTable = AllocTable(alloc: HfHeapAlloc, resize: HfHeapResize, free: HfHeapFree)

// A bump heap over a caller's byte array: 16-byte units, the count in the
// first three bytes; never frees. The same code runs at compile time.
HfHeapAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is the fixture's byte array" {
        used := int(raw.Read(ctx)) + int(raw.Read(raw.Offset(ctx, 1))) * 256 + int(raw.Read(raw.Offset(ctx, 2))) * 65536
        units := (size + 15) / 16
        if 16 + (used + units) * 16 > HfHeapBytes {
            return raw.Null[u8]()
        }
        n := used + units
        raw.Write(ctx, Trunc[u8](n))
        raw.Write(raw.Offset(ctx, 1), Trunc[u8](n / 256))
        raw.Write(raw.Offset(ctx, 2), Trunc[u8](n / 65536))
        return raw.Offset(ctx, 16 + used * 16)
    }
}

HfHeapResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

HfHeapFree(ctx: *u8, p: *u8, size: int, align: int) {
}

HfFnv(h: u64, x: u64): u64 {
    return (h ^ x) *% 1099511628211
}

const HfWords = HFWORDS

HfInput(): [96]u8 {
    var b: [96]u8 = [0; 96]
    for i in 0..96 {
        b[i] = Trunc[u8](i)
    }
    return b
}

// HfLong is the lengths whose every split is checked beyond 0..23: both
// lanes (SH-2), each side of the 32- and 64-byte steps.
const HfLong: [12]int = [31, 32, 33, 40, 47, 48, 49, 63, 64, 65, 79, 80]

// The fold Hasher: every length 0..95 whole, every split of lengths 0..23
// and HfLong into two Bytes calls, three pieces of 80 bytes, U64 after
// 0..7 tail bytes and between two pieces, six U64 after 0..7 bytes (pairs
// into both lanes through Word); then HashOf of ints and of every word.
export HfHasher(): u64 {
    b := HfInput()
    var r: u64 = 14695981039346656037
    for n in 0..96 {
        var h := NewHasher()
        h.Bytes(b[0..n])
        r = HfFnv(r, HashFinish(mut h))
    }
    for n in 0..24 {
        for k in 0..n + 1 {
            var h := NewHasher()
            h.Bytes(b[0..k])
            h.Bytes(b[k..n])
            r = HfFnv(r, HashFinish(mut h))
        }
    }
    for n in HfLong {
        for k in 0..n + 1 {
            var h := NewHasher()
            h.Bytes(b[0..k])
            h.Bytes(b[k..n])
            r = HfFnv(r, HashFinish(mut h))
        }
    }
    for k in 0..24 {
        var h := NewHasher()
        h.Bytes(b[0..k])
        h.Bytes(b[k..k + 9])
        h.Bytes(b[k + 9..80])
        r = HfFnv(r, HashFinish(mut h))
    }
    for k in 0..8 {
        var h := NewHasher()
        h.Bytes(b[0..k])
        h.U64(0x1716151413121110)
        h.Bytes(b[k..k + 13])
        h.U64(42)
        r = HfFnv(r, HashFinish(mut h))
    }
    for k in 0..8 {
        var h := NewHasher()
        h.Bytes(b[0..k])
        for j in 0..6 {
            h.U64(Trunc[u64](j) *% 0x9e3779b97f4a7c15)
        }
        h.Bytes(b[k..k + 5])
        r = HfFnv(r, HashFinish(mut h))
    }
    for k in -300..300 {
        x := k * 1_000_003
        r = HfFnv(r, HashOf(&x))
    }
    var ws := HfWords.Words()
    for w := ws.Next() {
        r = HfFnv(r, HashOf(&w))
    }
    return r
}

// NewHasherKeyed is SipHash-1-3: lengths 0..63 under the reference key and
// under the key bytes 0x10..0x1f.
export HfKeyed(): u64 {
    b := HfInput()
    var r: u64 = 14695981039346656037
    for n in 0..64 {
        var h := NewHasherKeyed(FixedK0, FixedK1)
        h.Bytes(b[0..n])
        r = HfFnv(r, HashFinish(mut h))
        var g := NewHasherKeyed(0x1716151413121110, 0x1f1e1d1c1b1a1918)
        g.Bytes(b[0..n])
        r = HfFnv(r, HashFinish(mut g))
    }
    return r
}

struct HfPt {
    x: int
    y: int
}

impl Eq for HfPt {
    Eq(&self, other: &HfPt): bool {
        return self.x == other.x && self.y == other.y
    }
}

impl Hash for HfPt {
    Hash(&self, h: mut Hasher) {
        self.x.Hash(h)
        self.y.Hash(h)
    }
}

// HfMapInt, HfMapStr and HfMapPt: a Map.New under seeded inserts and
// removes (every fifth key removed again), then the digest of the values in
// iteration order (each value names its key) and the length. The value of
// a key is its index in the sequence, so the Go model replays it.
export HfMapInt(): u64 {
    var mem: [131072]u8 = [0; 131072]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&HfHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var m := Map[int, int].New(&a)
        var s: u64 = 11
        for i in 0..400 {
            s = s *% 6364136223846793005 +% 1442695040888963407
            k := Trunc[int](s >> 20)
            m.Set(k, i) else {
                a.Free()
                return 2
            }
            if i % 5 == 3 {
                _ = m.Remove(&k)
            }
        }
        r := HfDigest(&m)
        a.Free()
        return r
    }
}

HfDigest[K: Hash + Eq](m: &Map[K, int]): u64 {
    var h: u64 = 14695981039346656037
    var it := m.Iter()
    for e := it.Next() {
        h = HfFnv(h, Trunc[u64](*e.value))
    }
    return HfFnv(h, Trunc[u64](m.Len()))
}

export HfMapStr(): u64 {
    var mem: [131072]u8 = [0; 131072]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&HfHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var m := Map[str, int].New(&a)
        var i := 0
        var ws := HfWords.Words()
        for w := ws.Next() {
            m.Set(w, i) else {
                a.Free()
                return 2
            }
            if i % 5 == 3 {
                _ = m.Remove(&w)
            }
            i += 1
        }
        r := HfDigest(&m)
        a.Free()
        return r
    }
}

export HfMapPt(): u64 {
    var mem: [131072]u8 = [0; 131072]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&HfHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var m := Map[HfPt, int].New(&a)
        for i in 0..400 {
            k := HfPt(x: i % 37, y: (i / 37) * 1_000_003 - 5)
            m.Set(k, i) else {
                a.Free()
                return 2
            }
            if i % 5 == 3 {
                _ = m.Remove(&k)
            }
        }
        r := HfDigest(&m)
        a.Free()
        return r
    }
}

const HfHasherC = HfHasher() fuel 100_000_000
const HfKeyedC = HfKeyed() fuel 100_000_000
const HfMapIntC = HfMapInt() fuel 100_000_000
const HfMapStrC = HfMapStr() fuel 100_000_000
const HfMapPtC = HfMapPt() fuel 100_000_000

// The compile-time values beside the same calls run natively.
export HfAll(): [10]u64 {
    return [HfHasherC, HfHasher(), HfKeyedC, HfKeyed(), HfMapIntC, HfMapInt(), HfMapStrC, HfMapStr(), HfMapPtC, HfMapPt()]
}
`

const hashFixModule = `import std.core

export All(): [10]u64 {
    return core.HfAll()
}
`

// hashFixWant is the Go side of HfAll's five digests.
func hashFixWant() []uint64 {
	in := make([]byte, 96)
	for i := range in {
		in[i] = byte(i)
	}
	r := uint64(fnvStart)
	for n := 0; n < 96; n++ {
		r = fnvStep(r, foldHash(in[:n]))
	}
	for n := 0; n < 24; n++ {
		for k := 0; k <= n; k++ {
			r = fnvStep(r, foldHash(in[:n]))
		}
	}
	for _, n := range []int{31, 32, 33, 40, 47, 48, 49, 63, 64, 65, 79, 80} {
		for k := 0; k <= n; k++ {
			r = fnvStep(r, foldHash(in[:n]))
		}
	}
	for k := 0; k < 24; k++ {
		r = fnvStep(r, foldHash(in[:80]))
	}
	for k := 0; k < 8; k++ {
		var all []byte
		all = append(all, in[:k]...)
		all = binary.LittleEndian.AppendUint64(all, 0x1716151413121110)
		all = append(all, in[k:k+13]...)
		all = binary.LittleEndian.AppendUint64(all, 42)
		r = fnvStep(r, foldHash(all))
	}
	for k := 0; k < 8; k++ {
		var all []byte
		all = append(all, in[:k]...)
		for j := uint64(0); j < 6; j++ {
			all = binary.LittleEndian.AppendUint64(all, j*0x9e3779b97f4a7c15)
		}
		all = append(all, in[k:k+5]...)
		r = fnvStep(r, foldHash(all))
	}
	for k := -300; k < 300; k++ {
		r = fnvStep(r, foldHashInt(k*1_000_003))
	}
	words := hashFixWords()
	for _, w := range words {
		r = fnvStep(r, foldHashStr(w))
	}
	hasher := r

	r = fnvStart
	for n := 0; n < 64; n++ {
		r = fnvStep(r, sip13k(0x0706050403020100, 0x0f0e0d0c0b0a0908, in[:n]))
		r = fnvStep(r, sip13k(0x1716151413121110, 0x1f1e1d1c1b1a1918, in[:n]))
	}
	keyed := r

	// The maps: the model's keys are the sequence indices' keys, by index
	// of first insertion; hashFn gives the real key's fold hash.
	type op struct {
		id     int  // the key's id (equal keys, equal ids)
		remove bool // remove it again after the Set
	}
	run := func(ops []op, hash func(id int) uint64) uint64 {
		t := &swissModel{hashFn: hash}
		for i, o := range ops {
			h := hash(o.id)
			if at, free := t.lookup(h, o.id); at < 0 {
				j := t.place(h, free)
				t.key[j], t.val[j] = o.id, i
			} else {
				t.val[at] = i
			}
			if o.remove {
				if j := t.find(h, o.id); j >= 0 {
					t.erase(j)
				}
			}
		}
		d := uint64(fnvStart)
		for i := 0; i < t.cap; i++ {
			if t.ctrl[i] < mDeleted {
				d = fnvStep(d, uint64(t.val[i]))
			}
		}
		return fnvStep(d, uint64(t.used))
	}
	var ints []int
	var iops []op
	ids := map[int]int{}
	s := uint64(11)
	for i := 0; i < 400; i++ {
		s = s*6364136223846793005 + 1442695040888963407
		k := int(s >> 20)
		if _, ok := ids[k]; !ok {
			ids[k] = len(ints)
			ints = append(ints, k)
		}
		iops = append(iops, op{ids[k], i%5 == 3})
	}
	mapInt := run(iops, func(id int) uint64 { return foldHashInt(ints[id]) })

	var sops []op
	for i := range words {
		sops = append(sops, op{i, i%5 == 3})
	}
	mapStr := run(sops, func(id int) uint64 { return foldHashStr(words[id]) })

	type pt struct{ x, y int }
	var pts []pt
	var pops []op
	for i := 0; i < 400; i++ {
		pts = append(pts, pt{i % 37, (i/37)*1_000_003 - 5})
		pops = append(pops, op{i, i%5 == 3})
	}
	mapPt := run(pops, func(id int) uint64 {
		return foldHashPair(uint64(int64(pts[id].x)), uint64(int64(pts[id].y)))
	})
	return []uint64{hasher, keyed, mapInt, mapStr, mapPt}
}

// TestStdHash_EvalAndNativeMatchModel: each digest's compile-time value
// equals its native value and the Go model's, at dev and at release.
// Hasher and HashOf against foldHash; NewHasherKeyed against sip13k;
// Map.New's iteration order for int, str and two-field keys against the Go
// Swiss table model under the fold hash, which is the reproducible order
// the reference promises, checked at run time and in the evaluator.
func TestStdHash_EvalAndNativeMatchModel(t *testing.T) {
	t.Parallel()
	words := hashFixWords()
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w] || strings.ContainsAny(w, " \t\n") {
			t.Fatalf("hashFixWords: %q repeated or holds a space", w)
		}
		seen[w] = true
	}
	src := `import std.hashfix

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for x in hashfix.All() {
        try Print(mut out, "{x}\n", (x))
    }
}
`
	var want strings.Builder
	for _, w := range hashFixWant() {
		fmt.Fprintf(&want, "%d\n%d\n", w, w)
	}
	core := strings.Replace(hashFixCore, "HFWORDS", mkString(strings.Join(words, " ")), 1)
	extra := map[string]string{"core/tests_hashfix.mk": core, "hashfix/hashfix.mk": hashFixModule}
	for _, flags := range [][]string{nil, {"--release"}} {
		got := collExec(t, collBuildWith(t, extra, src, flags...))
		diffLines(t, got, want.String())
	}
}

// TestStdHash_ModelSelfCheck pins the Go model to the vectors the Mk
// test blocks of tests_map.mk carry, and checks its closed forms
// (foldHashU64, foldHashPair) against the stream form.
func TestStdHash_ModelSelfCheck(t *testing.T) {
	in := make([]byte, 64)
	for i := range in {
		in[i] = byte(i)
	}
	want := map[int]uint64{0: 12733740897008279252, 1: 10099875023774034710, 7: 2310436164859541822, 8: 2141061075543806254,
		15: 1690340452080105924, 16: 14185856542041807614, 63: 807221478985217149}
	for n, w := range want {
		if got := foldHash(in[:n]); got != w {
			t.Errorf("foldHash(0..%d) = %d, tests_map.mk says %d", n, got, w)
		}
	}
	if foldHashStr("a str key") != 5828005871986082735 || foldHashInt(-1) != 4553538096044278647 {
		t.Error("foldHashStr or foldHashInt differs from the tests_map.mk vectors")
	}
	for _, x := range []uint64{0, 1, 1 << 63, 0xFFFFFFFFFFFFFFFF, foldF0, foldF1} {
		if foldHashU64(x) != foldHash(binary.LittleEndian.AppendUint64(nil, x)) {
			t.Errorf("foldHashU64(%#x) differs from the stream form", x)
		}
		for _, y := range []uint64{0, 7, 1 << 40} {
			b := binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, x), y)
			if foldHashPair(x, y) != foldHash(b) {
				t.Errorf("foldHashPair(%#x, %#x) differs from the stream form", x, y)
			}
		}
	}
}
