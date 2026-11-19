// Why: every std.simd routine has vector paths that a plain loop must agree
// with at every length, offset and position, without reading out of bounds.
// What: TestSTDSIMD_BytesDiff, ValidUtf8Diff, DotSumDiff and Dump.
// How: Mk programs print one hash per routine and length; a Go model over
// bytes and unicode/utf8 prints the same lines from the same splitmix64 input.

// The differential tests of std.simd: every routine of std.simd
// runs natively, at dev and release, over every length 0 to 130 and some
// long ones, every offset 0 to 63, and a match, a set member, a differing
// byte or a bad UTF-8 byte at every position (every 7th past 130), with the
// needle planted just outside the slice on both sides, so an out-of-slice
// read shows. Each output line is a hash per routine and length; a Go model
// computes the same lines with Go's bytes and unicode/utf8 packages (and
// Dot and Sum in the reference's stated order), and the two must agree.
// The inputs come from splitmix64 on both sides.

package std

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// sdLongs are the lengths past 130 (offsets sdLongOffs, positions every 7th
// and the last two): both widths' four-vector blocks and their tails.
var sdLongs = []int{191, 255, 256, 257, 300, 511, 512, 513, 1000, 4099}

var sdLongOffs = []int{0, 1, 15, 33, 63}

func sdLens() []int {
	var ls []int
	for n := 0; n <= 130; n++ {
		ls = append(ls, n)
	}
	return append(ls, sdLongs...)
}

func sdOffs(n int) []int {
	if n > 130 {
		return sdLongOffs
	}
	offs := make([]int, 64)
	for i := range offs {
		offs[i] = i
	}
	return offs
}

// sdPos reports whether position p of 0..n is tried: every one up to 130,
// then every 7th, the last two, and the three on each side of every 32-byte
// boundary (where a block of four vectors ends at either width).
func sdPos(n, p int) bool { return n <= 130 || p%7 == 0 || p >= n-2 || p%32 >= 29 || p%32 <= 2 }

func sdMix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// sdH folds v into h (FNV-1a's prime over a word); v >= -2.
func sdH(h uint64, v int) uint64 { return (h ^ uint64(v+2)) * 0x100000001b3 }

func sdBool(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sdCommon is the Mk side of sdMix, sdH and the length and offset lists.
const sdCommon = `
const G: int = 64
const SPAN: int = 4099 + 192
const NLENS: int = 141

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

H(h: u64, v: int): u64 {
    return (h ^ Cast[u64](v + 2)) *% 0x100000001b3
}

B(x: bool): int {
    if x {
        return 1
    }
    return 0
}

Len(k: int): int {
    if k <= 130 {
        return k
    }
    longs := [191, 255, 256, 257, 300, 511, 512, 513, 1000, 4099]
    return longs[k - 131]
}

NOffs(n: int): int {
    if n > 130 {
        return 5
    }
    return 64
}

Off(n: int, j: int): int {
    if n > 130 {
        offs := [0, 1, 15, 33, 63]
        return offs[j]
    }
    return j
}

Pos(n: int, p: int): bool {
    return n <= 130 || p % 7 == 0 || p >= n - 2 || p % 32 >= 29 || p % 32 <= 2
}
`

// ---- IndexByte, Count, IndexAny, Equal, Compare ----

const sdBytesProgram = `import std.simd
` + sdCommon + `
InSet(set: []u8, x: u8): bool {
    for y in set {
        if y == x {
            return true
        }
    }
    return false
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    buf := try sys.arena.Slice[u8](SPAN)
    other := try sys.arena.Slice[u8](SPAN)
    // IndexByte: no needle in the background; the needle at p and at n - 1,
    // and planted just before and just after the slice.
    needles := [0, 0x61, 0x80, 0xFF]
    for k in 0..NLENS {
        n := Len(k)
        var h: u64 = 0
        for nd in needles {
            b := Trunc[u8](nd)
            for i in 0..SPAN {
                var x := Trunc[u8](Mix(Cast[u64](i)))
                if x == b {
                    x = x ^ 1
                }
                buf[i] = x
            }
            for j in 0..NOffs(n) {
                st := G + Off(n, j)
                buf[st - 1] = b
                buf[st + n] = b
                for p in 0..n + 1 {
                    if !Pos(n, p) {
                        continue
                    }
                    if p < n {
                        buf[st + p] = b
                    }
                    if n > 0 {
                        buf[st + n - 1] = b
                    }
                    at := simd.IndexByte(buf[st..st + n], b) else { -1 }
                    h = H(h, at)
                    if n > 0 {
                        buf[st + n - 1] = Trunc[u8](Mix(Cast[u64](st + n - 1)))
                        if buf[st + n - 1] == b {
                            buf[st + n - 1] = b ^ 1
                        }
                    }
                    if p < n {
                        buf[st + p] = Trunc[u8](Mix(Cast[u64](st + p)))
                        if buf[st + p] == b {
                            buf[st + p] = b ^ 1
                        }
                    }
                }
                buf[st - 1] = b ^ 1
                buf[st + n] = b ^ 1
            }
        }
        try Print(mut out, "IB {n} {h}\n", (n, h))
    }
    // Count over the alphabet 0 to 3, the counted byte planted outside.
    for i in 0..SPAN {
        buf[i] = Trunc[u8](Mix(Cast[u64](i) +% 77) & 3)
    }
    for k in 0..NLENS {
        n := Len(k)
        var h: u64 = 0
        for j in 0..NOffs(n) {
            st := G + Off(n, j)
            for c in 0..5 {
                b := Trunc[u8](c)
                ob := buf[st - 1]
                oa := buf[st + n]
                buf[st - 1] = b
                buf[st + n] = b
                h = H(h, simd.Count(buf[st..st + n], b))
                buf[st - 1] = ob
                buf[st + n] = oa
            }
        }
        try Print(mut out, "CT {n} {h}\n", (n, h))
    }
    // Count past the byte counters' fold: 70,000 equal bytes.
    big := try sys.arena.Slice[u8](70004)
    for i in 0..70004 {
        big[i] = 7
    }
    for j in 0..4 {
        c7 := simd.Count(big[j..j + 70000], 7)
        c8 := simd.Count(big[j..j + 70000], 8)
        try Print(mut out, "CB {j} {c7} {c8}\n", (j, c7, c8))
    }
    // Count over up to 70,000 bytes of four values, at the counter folds
    // of both widths (255 steps of four vectors: 16,320 and 32,640 bytes)
    // and of the word form (255 words), review finding SIMD-2.
    // Values 0x00, 0x80, 0x7F and 0xFF: two bytes that differ only in
    // bit 7 (a SWAR zero test that missed bit 7 would count both).
    cvals := [0x00, 0x80, 0x7F, 0xFF, 0x01]
    for i in 0..70004 {
        big[i] = Trunc[u8](cvals[Cast[int](Mix(Cast[u64](i) +% 3) & 3)])
    }
    cls := [2039, 2040, 2041, 16319, 16320, 16321, 32639, 32640, 32641, 65280, 70000]
    for n in cls {
        var h: u64 = 0
        for j in 0..4 {
            for c in cvals {
                h = H(h, simd.Count(big[j..j + n], Trunc[u8](c)))
            }
        }
        try Print(mut out, "CL {n} {h}\n", (n, h))
    }
    // IndexAny over sets of 0, 1, 2, 3, 4, 8 and 16 bytes.
    var sets: [7][16]u8 = [[0; 16]; 7]
    sizes := [0, 1, 2, 3, 4, 8, 16]
    sets[1][0] = 0x41
    sets[2][0] = 0x22
    sets[2][1] = 0x5C
    sets[3][0] = 0x0A
    sets[3][1] = 0x0D
    sets[3][2] = 0x22
    sets[4][0] = 0x00
    sets[4][1] = 0x80
    sets[4][2] = 0xFF
    sets[4][3] = 0x7F
    big8 := [0x20, 0x09, 0x0A, 0x0D, 0x3C, 0x3E, 0xC3, 0xE2]
    for q in 0..8 {
        sets[5][q] = Trunc[u8](big8[q])
    }
    for q in 0..16 {
        sets[6][q] = Trunc[u8](q * 17)
    }
    for si in 0..7 {
        set := sets[si][0..sizes[si]]
        sz := sizes[si]
        for i in 0..SPAN {
            var x := Trunc[u8](Mix(Cast[u64](i) +% 5))
            for InSet(set, x) {
                x = x +% 37
            }
            other[i] = x
        }
        for k in 0..NLENS {
            n := Len(k)
            var h: u64 = 0
            for j in 0..NOffs(n) {
                st := G + Off(n, j)
                for i in 0..SPAN {
                    buf[i] = other[i]
                }
                if sz > 0 {
                    buf[st - 1] = set[0]
                    buf[st + n] = set[0]
                }
                for p in 0..n + 1 {
                    if !Pos(n, p) {
                        continue
                    }
                    if sz > 0 && p < n {
                        buf[st + p] = set[p % sz]
                    }
                    if sz > 0 && n > 0 {
                        buf[st + n - 1] = set[(p + 1) % sz]
                    }
                    at := simd.IndexAny(buf[st..st + n], set) else { -1 }
                    h = H(h, at)
                    if n > 0 {
                        buf[st + n - 1] = other[st + n - 1]
                    }
                    if p < n {
                        buf[st + p] = other[st + p]
                    }
                }
            }
            try Print(mut out, "IA {sz} {n} {h}\n", (sz, n, h))
        }
    }
    // Equal and Compare: one content at two offsets, then one byte changed
    // at every position, both ways; and the prefix cases.
    deltas := [1, 0x80, 0xFF]
    for i in 0..SPAN {
        buf[i] = Trunc[u8](Mix(Cast[u64](i) +% 9))
        other[i] = Trunc[u8](Mix(Cast[u64](i) +% 11))
    }
    for k in 0..NLENS {
        n := Len(k)
        var h: u64 = 0
        for j in 0..NOffs(n) {
            sa := G + Off(n, j)
            sb := G + (Off(n, j) * 5 + 3) % 64
            for q in 0..n + 1 {
                c := Trunc[u8](Mix(Cast[u64](q) ^ 0x5555))
                buf[sa + q] = c
                other[sb + q] = c
            }
            h = H(h, B(simd.Equal(buf[sa..sa + n], other[sb..sb + n])))
            h = H(h, simd.Compare(buf[sa..sa + n], other[sb..sb + n]))
            h = H(h, simd.Compare(buf[sa..sa + n], other[sb..sb + n + 1]))
            h = H(h, simd.Compare(buf[sa..sa + n + 1], other[sb..sb + n]))
            h = H(h, B(simd.Equal(buf[sa..sa + n + 1], other[sb..sb + n])))
            for p in 0..n {
                if !Pos(n, p) {
                    continue
                }
                c := other[sb + p]
                other[sb + p] = c ^ Trunc[u8](deltas[p % 3])
                h = H(h, B(simd.Equal(buf[sa..sa + n], other[sb..sb + n])))
                h = H(h, simd.Compare(buf[sa..sa + n], other[sb..sb + n]))
                h = H(h, simd.Compare(other[sb..sb + n], buf[sa..sa + n]))
                other[sb + p] = c
            }
        }
        try Print(mut out, "EQ {n} {h}\n", (n, h))
    }
}
`

func sdBytesModel() string {
	const span = 4099 + 192
	const g = 64
	var w strings.Builder
	buf := make([]byte, span)
	other := make([]byte, span)
	rnd := func(i int, add uint64) byte { return byte(sdMix(uint64(i) + add)) }
	for _, n := range sdLens() {
		var h uint64
		for _, nd := range []int{0, 0x61, 0x80, 0xFF} {
			b := byte(nd)
			for i := range buf {
				x := rnd(i, 0)
				if x == b {
					x ^= 1
				}
				buf[i] = x
			}
			for _, off := range sdOffs(n) {
				st := g + off
				buf[st-1], buf[st+n] = b, b
				for p := 0; p <= n; p++ {
					if !sdPos(n, p) {
						continue
					}
					if p < n {
						buf[st+p] = b
					}
					if n > 0 {
						buf[st+n-1] = b
					}
					h = sdH(h, bytes.IndexByte(buf[st:st+n], b))
					fix := func(k int) {
						buf[k] = rnd(k, 0)
						if buf[k] == b {
							buf[k] = b ^ 1
						}
					}
					if n > 0 {
						fix(st + n - 1)
					}
					if p < n {
						fix(st + p)
					}
				}
				buf[st-1], buf[st+n] = b^1, b^1
			}
		}
		fmt.Fprintf(&w, "IB %d %d\n", n, h)
	}
	for i := range buf {
		buf[i] = byte(sdMix(uint64(i)+77) & 3)
	}
	for _, n := range sdLens() {
		var h uint64
		for _, off := range sdOffs(n) {
			st := g + off
			for c := 0; c < 5; c++ {
				h = sdH(h, bytes.Count(buf[st:st+n], []byte{byte(c)}))
			}
		}
		fmt.Fprintf(&w, "CT %d %d\n", n, h)
	}
	for j := 0; j < 4; j++ {
		fmt.Fprintf(&w, "CB %d %d %d\n", j, 70000, 0)
	}
	big := make([]byte, 70004)
	for i := range big {
		big[i] = []byte{0x00, 0x80, 0x7F, 0xFF}[sdMix(uint64(i)+3)&3]
	}
	for _, n := range []int{2039, 2040, 2041, 16319, 16320, 16321, 32639, 32640, 32641, 65280, 70000} {
		var h uint64
		for j := 0; j < 4; j++ {
			for _, c := range []byte{0x00, 0x80, 0x7F, 0xFF, 0x01} {
				h = sdH(h, bytes.Count(big[j:j+n], []byte{c}))
			}
		}
		fmt.Fprintf(&w, "CL %d %d\n", n, h)
	}
	sets := [][]byte{{}, {0x41}, {0x22, 0x5C}, {0x0A, 0x0D, 0x22}, {0x00, 0x80, 0xFF, 0x7F},
		{0x20, 0x09, 0x0A, 0x0D, 0x3C, 0x3E, 0xC3, 0xE2}, {}}
	for q := 0; q < 16; q++ {
		sets[6] = append(sets[6], byte(q*17))
	}
	for _, set := range sets {
		sz := len(set)
		for i := range other {
			x := rnd(i, 5)
			for bytes.IndexByte(set, x) >= 0 {
				x += 37
			}
			other[i] = x
		}
		for _, n := range sdLens() {
			var h uint64
			for _, off := range sdOffs(n) {
				st := g + off
				copy(buf, other)
				if sz > 0 {
					buf[st-1], buf[st+n] = set[0], set[0]
				}
				for p := 0; p <= n; p++ {
					if !sdPos(n, p) {
						continue
					}
					if sz > 0 && p < n {
						buf[st+p] = set[p%sz]
					}
					if sz > 0 && n > 0 {
						buf[st+n-1] = set[(p+1)%sz]
					}
					h = sdH(h, sdIndexAny(buf[st:st+n], set))
					if n > 0 {
						buf[st+n-1] = other[st+n-1]
					}
					if p < n {
						buf[st+p] = other[st+p]
					}
				}
			}
			fmt.Fprintf(&w, "IA %d %d %d\n", sz, n, h)
		}
	}
	for i := range buf {
		buf[i] = rnd(i, 9)
		other[i] = rnd(i, 11)
	}
	deltas := []byte{1, 0x80, 0xFF}
	for _, n := range sdLens() {
		var h uint64
		for _, off := range sdOffs(n) {
			sa, sb := g+off, g+(off*5+3)%64
			for q := 0; q <= n; q++ {
				c := byte(sdMix(uint64(q) ^ 0x5555))
				buf[sa+q], other[sb+q] = c, c
			}
			h = sdH(h, sdBool(bytes.Equal(buf[sa:sa+n], other[sb:sb+n])))
			h = sdH(h, bytes.Compare(buf[sa:sa+n], other[sb:sb+n]))
			h = sdH(h, bytes.Compare(buf[sa:sa+n], other[sb:sb+n+1]))
			h = sdH(h, bytes.Compare(buf[sa:sa+n+1], other[sb:sb+n]))
			h = sdH(h, sdBool(bytes.Equal(buf[sa:sa+n+1], other[sb:sb+n])))
			for p := 0; p < n; p++ {
				if !sdPos(n, p) {
					continue
				}
				c := other[sb+p]
				other[sb+p] = c ^ deltas[p%3]
				h = sdH(h, sdBool(bytes.Equal(buf[sa:sa+n], other[sb:sb+n])))
				h = sdH(h, bytes.Compare(buf[sa:sa+n], other[sb:sb+n]))
				h = sdH(h, bytes.Compare(other[sb:sb+n], buf[sa:sa+n]))
				other[sb+p] = c
			}
		}
		fmt.Fprintf(&w, "EQ %d %d\n", n, h)
	}
	return w.String()
}

// sdIndexAny is the first byte of s in set (bytes.IndexAny reads its set as
// UTF-8 text, so bytes above 0x7F would not mean themselves there).
func sdIndexAny(s, set []byte) int {
	for i, c := range s {
		if bytes.IndexByte(set, c) >= 0 {
			return i
		}
	}
	return -1
}

func TestSTDSIMD_BytesDiff(t *testing.T) {
	t.Parallel()
	want := sdBytesModel()
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, sdRun(t, sdBytesProgram, flags...), want)
		})
	}
}

// ---- ValidUtf8 ----

// sdSeqs are well-formed sequences at the edges of table 3-7's rows.
var sdSeqs = [][]byte{{'a'}, {0x7F}, {0xC2, 0x80}, {0xDF, 0xBF}, {0xE0, 0xA0, 0x80}, {0xED, 0x9F, 0xBF},
	{0xEE, 0x80, 0x80}, {0xEF, 0xBF, 0xBF}, {0xF0, 0x90, 0x80, 0x80}, {0xF4, 0x8F, 0xBF, 0xBF},
	{0xC3, 0xA9}, {0xE2, 0x82, 0xAC}, {0xF0, 0x9F, 0x98, 0x80}}

// sdBad are the bytes written at each position in turn.
var sdBad = []byte{0x00, 0x7F, 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xC1, 0xC2, 0xDF, 0xE0, 0xED,
	0xEF, 0xF0, 0xF4, 0xF5, 0xFF}

// sdText writes n bytes of text into s (len(s) >= n + 4): mode 0 ASCII,
// mode 1 sequences from sdSeqs, mode 2 mostly ASCII; the last sequence may
// be cut by n.
func sdText(s []byte, n, mode int, seed uint64) {
	k, st := 0, seed
	for k < n {
		st += 0x9E3779B97F4A7C15
		r := sdMix(st)
		var seq []byte
		switch {
		case mode == 0:
			seq = []byte{byte('a' + r%26)}
		case mode == 1 || r%4 == 0:
			seq = sdSeqs[(r>>8)%uint64(len(sdSeqs))]
		default:
			seq = []byte{byte('A' + r%26)}
		}
		for _, c := range seq {
			if k < n {
				s[k] = c
				k++
			}
		}
	}
}

const sdUtf8Program = `import std.simd
` + sdCommon + `
Seq(k: int, q: int): int {
    seqs := [0x61, 0x7F, 0xC280, 0xDFBF, 0xE0A080, 0xED9FBF, 0xEE8080, 0xEFBFBF, 0xF0908080, 0xF48FBFBF, 0xC3A9, 0xE282AC, 0xF09F9880]
    lens := [1, 1, 2, 2, 3, 3, 3, 3, 4, 4, 2, 3, 4]
    if q < 0 {
        return lens[k]
    }
    return (seqs[k] >> ((lens[k] - 1 - q) * 8)) & 0xFF
}

Text(s: mut []u8, n: int, mode: int, seed: u64) {
    var k := 0
    var st := seed
    for k < n {
        st = st +% 0x9E3779B97F4A7C15
        r := Mix(st)
        if mode == 0 {
            s[k] = Trunc[u8](0x61 + r % 26)
            k += 1
        } else if mode == 1 || r % 4 == 0 {
            sk := Cast[int]((r >> 8) % 13)
            for q in 0..Seq(sk, -1) {
                if k < n {
                    s[k] = Trunc[u8](Seq(sk, q))
                    k += 1
                }
            }
        } else {
            s[k] = Trunc[u8](0x41 + r % 26)
            k += 1
        }
    }
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    buf := try sys.arena.Slice[u8](SPAN)
    bad := [0x00, 0x7F, 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xC1, 0xC2, 0xDF, 0xE0, 0xED, 0xEF, 0xF0, 0xF4, 0xF5, 0xFF]
    for k in 0..NLENS {
        n := Len(k)
        var h: u64 = 0
        for j in 0..NOffs(n) {
            a := Off(n, j)
            st := G + a
            // Leads before the slice and continuations after it: a read
            // outside would make a cut or stray sequence look whole.
            buf[st - 3] = 0xF0
            buf[st - 2] = 0xF0
            buf[st - 1] = 0xF0
            Text(mut buf[st..st + n], n, (n + a) % 3, Cast[u64](n * 64 + a))
            buf[st + n] = 0x80
            buf[st + n + 1] = 0x80
            buf[st + n + 2] = 0x80
            h = H(h, B(simd.ValidUtf8(buf[st..st + n])))
            for p in 0..n {
                if !Pos(n, p) {
                    continue
                }
                c := buf[st + p]
                buf[st + p] = Trunc[u8](bad[(p + n * 3 + a) % 19])
                h = H(h, B(simd.ValidUtf8(buf[st..st + n])))
                buf[st + p] = c
            }
        }
        try Print(mut out, "U8 {n} {h}\n", (n, h))
    }
}
`

func sdUtf8Model() string {
	var w strings.Builder
	buf := make([]byte, 4099+192)
	for _, n := range sdLens() {
		var h uint64
		for _, a := range sdOffs(n) {
			st := 64 + a
			s := buf[st : st+n]
			sdText(buf[st:], n, (n+a)%3, uint64(n*64+a))
			h = sdH(h, sdBool(utf8.Valid(s)))
			for p := 0; p < n; p++ {
				if !sdPos(n, p) {
					continue
				}
				c := s[p]
				s[p] = sdBad[(p+n*3+a)%19]
				h = sdH(h, sdBool(utf8.Valid(s)))
				s[p] = c
			}
		}
		fmt.Fprintf(&w, "U8 %d %d\n", n, h)
	}
	return w.String()
}

func TestSTDSIMD_ValidUtf8Diff(t *testing.T) {
	t.Parallel()
	want := sdUtf8Model()
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, sdRun(t, sdUtf8Program, flags...), want)
		})
	}
}

// ---- Dot and Sum ----

const sdFloatProgram = `import std.simd
` + sdCommon + `
B32(x: f32): u64 {
    return Cast[u64](simd.Vec[f32, 2].Splat(x).Reinterpret[u32, 2]().Lane(0))
}

B64(x: f64): u64 {
    return simd.Vec[f64, 2].Splat(x).Reinterpret[u64, 2]().Lane(0)
}

HB(h: u64, b: u64): u64 {
    return (h ^ b) *% 0x100000001b3
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    x32 := try sys.arena.Slice[f32](4099 + 32)
    y32 := try sys.arena.Slice[f32](4099 + 32)
    x64 := try sys.arena.Slice[f64](4099 + 32)
    y64 := try sys.arena.Slice[f64](4099 + 32)
    for i in 0..4099 + 32 {
        u := Cast[u64](i)
        x32[i] = Cast[f32]((u *% 0x9e3779b97f4a7c15 +% 1) >> 48) * 0.0000152587890625 - 0.5
        y32[i] = Cast[f32]((u *% 0xbf58476d1ce4e5b9 +% 1) >> 48) * 0.0000152587890625 - 0.5
        x64[i] = Cast[f64]((u *% 0x9e3779b97f4a7c15 +% 3) >> 40) * 0.000000059604644775390625 - 0.5
        y64[i] = Cast[f64]((u *% 0xbf58476d1ce4e5b9 +% 3) >> 40) * 0.000000059604644775390625 - 0.5
    }
    for k in 0..NLENS {
        n := Len(k)
        var h: u64 = 0
        for o in 0..16 {
            h = HB(h, B32(simd.Dot(x32[o..o + n], y32[o..o + n])))
            h = HB(h, B32(simd.Sum(x32[o..o + n])))
            h = HB(h, B64(simd.Dot(x64[o..o + n], y64[o..o + n])))
            h = HB(h, B64(simd.Sum(y64[o..o + n])))
        }
        try Print(mut out, "FL {n} {h}\n", (n, h))
    }
}
`

func sdDot32(x, y []float32) float32 {
	var s [16]float32
	i := 0
	for ; i+16 <= len(x); i += 16 {
		for j := 0; j < 16; j++ {
			s[j] = float32(s[j] + float32(x[i+j]*y[i+j]))
		}
	}
	for w := 8; w >= 1; w /= 2 {
		for j := 0; j < w; j++ {
			s[j] = float32(s[j] + s[j+w])
		}
	}
	r := s[0]
	for ; i < len(x); i++ {
		r = float32(r + float32(x[i]*y[i]))
	}
	return r
}

func sdDot64(x, y []float64) float64 {
	var s [16]float64
	i := 0
	for ; i+16 <= len(x); i += 16 {
		for j := 0; j < 16; j++ {
			s[j] = float64(s[j] + float64(x[i+j]*y[i+j]))
		}
	}
	for w := 8; w >= 1; w /= 2 {
		for j := 0; j < w; j++ {
			s[j] = float64(s[j] + s[j+w])
		}
	}
	r := s[0]
	for ; i < len(x); i++ {
		r = float64(r + float64(x[i]*y[i]))
	}
	return r
}

func sdSum32(x []float32) float32 {
	var s [16]float32
	i := 0
	for ; i+16 <= len(x); i += 16 {
		for j := 0; j < 16; j++ {
			s[j] = float32(s[j] + x[i+j])
		}
	}
	for w := 8; w >= 1; w /= 2 {
		for j := 0; j < w; j++ {
			s[j] = float32(s[j] + s[j+w])
		}
	}
	r := s[0]
	for ; i < len(x); i++ {
		r = float32(r + x[i])
	}
	return r
}

func sdSum64(x []float64) float64 {
	var s [16]float64
	i := 0
	for ; i+16 <= len(x); i += 16 {
		for j := 0; j < 16; j++ {
			s[j] = float64(s[j] + x[i+j])
		}
	}
	for w := 8; w >= 1; w /= 2 {
		for j := 0; j < w; j++ {
			s[j] = float64(s[j] + s[j+w])
		}
	}
	r := s[0]
	for ; i < len(x); i++ {
		r = float64(r + x[i])
	}
	return r
}

func sdFloatModel() string {
	const m = 4099 + 32
	x32, y32 := make([]float32, m), make([]float32, m)
	x64, y64 := make([]float64, m), make([]float64, m)
	for i := 0; i < m; i++ {
		u := uint64(i)
		x32[i] = float32(float32((u*0x9e3779b97f4a7c15+1)>>48)*0x1p-16) - 0.5
		y32[i] = float32(float32((u*0xbf58476d1ce4e5b9+1)>>48)*0x1p-16) - 0.5
		x64[i] = float64(float64((u*0x9e3779b97f4a7c15+3)>>40)*0x1p-24) - 0.5
		y64[i] = float64(float64((u*0xbf58476d1ce4e5b9+3)>>40)*0x1p-24) - 0.5
	}
	hb := func(h, b uint64) uint64 { return (h ^ b) * 0x100000001b3 }
	var w strings.Builder
	for _, n := range sdLens() {
		var h uint64
		for o := 0; o < 16; o++ {
			h = hb(h, uint64(math.Float32bits(sdDot32(x32[o:o+n], y32[o:o+n]))))
			h = hb(h, uint64(math.Float32bits(sdSum32(x32[o:o+n]))))
			h = hb(h, math.Float64bits(sdDot64(x64[o:o+n], y64[o:o+n])))
			h = hb(h, math.Float64bits(sdSum64(y64[o:o+n])))
		}
		fmt.Fprintf(&w, "FL %d %d\n", n, h)
	}
	return w.String()
}

func TestSTDSIMD_DotSumDiff(t *testing.T) {
	t.Parallel()
	want := sdFloatModel()
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, sdRun(t, sdFloatProgram, flags...), want)
		})
	}
}

// sdRun builds src over a copy of today's std with flags, runs it, and
// returns its standard output; a nonzero exit fails the test.
func sdRun(t *testing.T, src string, flags ...string) string {
	t.Helper()
	r := runNative(t, buildNative(t, nil, src, flags...), "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%.4000s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return strings.ReplaceAll(r.stdout, "\r\n", "\n")
}

// TestSTDSIMD_Dump writes the three programs and their models' outputs to
// the directory MK_SDDUMP names, so a planted bug in a copy of std can be
// shown to fail them (mutation checks); it skips
// otherwise.
func TestSTDSIMD_Dump(t *testing.T) {
	dir := os.Getenv("MK_SDDUMP")
	if dir == "" {
		t.Skip("MK_SDDUMP not set")
	}
	for name, pair := range map[string][2]string{
		"bytes": {sdBytesProgram, sdBytesModel()},
		"utf8":  {sdUtf8Program, sdUtf8Model()},
		"float": {sdFloatProgram, sdFloatModel()},
		"edge":  {sdEdgeProgram, sdEdgeModel()},
	} {
		if err := os.WriteFile(filepath.Join(dir, name+".mk"), []byte(pair[0]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".want"), []byte(pair[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
