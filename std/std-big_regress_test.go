// Why: three fixed defects of std.big each keep the case that failed.
// What: TestSTDBIG_ModPowMemoryBoundedInX (ModPow, ModInverse and Gcd hold
// memory bounded in m, not in x), TestSTDBIG_SquareMatchesMathBig (Mul of
// a value by itself squares) and TestSTDBIG_GcdUnbalancedMatchesMathBig
// (Gcd of very different lengths reduces the longer one first).
// How: results against math/big, at the lengths that reach each path.

package std

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSTDBIG_SquareMatchesMathBig: x.Mul(a, &x) and x times an equal
// copy, against math/big's Mul(x, x), at every length from 1 to 420 words
// (each side of SqrBasicWords, KaraSqrWords and ToomSqrWords) and at 899
// to 905, 2,700 and 2,701 words, where Toom-3's squares are Toom-3 again.
// Patterns: random, all ones and a lone top bit (which stress the doubling
// pass's carries), sparse, and edge words; signs mixed.
func TestSTDBIG_SquareMatchesMathBig(t *testing.T) {
	g := newBigGen(51)
	var c bigCases
	sq := func(x *big.Int) {
		want := bigInt(new(big.Int).Mul(x, x))
		c.add("sqr", []string{g.operand(x)}, want, want)
	}
	var sizes []int
	for n := 1; n <= 420; n++ {
		sizes = append(sizes, n)
	}
	sizes = append(sizes, 899, 900, 901, 902, 903, 904, 905, 2700, 2701)
	for _, n := range sizes {
		x := g.int(n)
		sq(x)
		ones := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(64*n)), big.NewInt(1))
		top := new(big.Int).Lsh(big.NewInt(1), uint(64*n-1))
		if n <= 140 || n%7 == 0 || n > 420 {
			sq(ones)
			sq(new(big.Int).Neg(top))
			sq(new(big.Int).Add(top, big.NewInt(1)))
		}
	}
	sq(big.NewInt(0))
	bigRun(t, &c)
}

// TestSTDBIG_GcdUnbalancedMatchesMathBig: Gcd with x more than a word
// longer than y, so that GcdMag reduces x modulo y first (RemMag): y of 2
// to 59 words (Algorithm D blocks), 60, 61 and up to 300 words (recursive
// division blocks), x of 2n, 2n + 1 and up to 3,000 words, lengths that
// are a multiple of RemMag's block and lengths that are not, with a large
// common factor and with y dividing x; both argument orders. ModInverse
// and ModPow, which reduce x by the same RemMag, run on the same pairs.
func TestSTDBIG_GcdUnbalancedMatchesMathBig(t *testing.T) {
	g := newBigGen(52)
	var c bigCases
	gcd := func(x, y *big.Int) {
		want := bigInt(new(big.Int).GCD(nil, nil, new(big.Int).Abs(x), new(big.Int).Abs(y)))
		c.add("gcd", []string{bigInt(x), bigInt(y)}, want)
		c.add("gcd", []string{bigInt(y), bigInt(x)}, want)
	}
	inv := func(x, m *big.Int) {
		am := new(big.Int).Abs(m)
		want := "none"
		if am.Cmp(big.NewInt(1)) == 0 {
			want = "+"
		} else if new(big.Int).GCD(nil, nil, new(big.Int).Abs(x), am).Cmp(big.NewInt(1)) == 0 {
			want = bigInt(new(big.Int).ModInverse(new(big.Int).Mod(x, am), am))
		}
		c.add("modinv", []string{bigInt(x), bigInt(m)}, want)
	}
	pow := func(x, m *big.Int) {
		am := new(big.Int).Abs(m)
		e := g.nat(1 + g.r.IntN(3))
		want := new(big.Int).Exp(new(big.Int).Mod(x, am), e, am)
		c.add("modpow", []string{bigInt(x), bigInt(e), bigInt(m)}, bigInt(want))
	}
	for _, m := range []int{1, 2, 3, 7, 31, 59, 60, 61, 64, 100, 255, 256, 257, 300} {
		b := max(m, 256)
		for _, n := range []int{m + 2, 2 * m, 2*m + 1, m + b, m + b + 1, 2*b + 3, 3000} {
			if n <= m+1 {
				continue
			}
			x, y := g.int(n), g.int(m)
			gcd(x, y)
			inv(x, y)
			pow(x, y)
			// A common factor of about m / 2 words, and y dividing x.
			f := g.nat(1 + m/2)
			gcd(new(big.Int).Mul(x, f), new(big.Int).Mul(y, f))
			gcd(new(big.Int).Mul(y, g.nat(n-m)), y)
			// An even modulus, for ModPow's Chinese-remainder path.
			pow(x, new(big.Int).Lsh(y, 1))
		}
	}
	bigRun(t, &c)
}

// bigCapFixture is a heap that refuses any request that would hold more
// than cap bytes live at once and records the peak, and a driver that runs
// one std.big operation over an arena on it.
const bigCapFixture = `// Fixture of std.big's memory-bound test: a heap with a byte cap.

import std.core
import std.raw
import std.big

struct Cap {
    inner: *Alloc
    live: int
    peak: int
    cap: int
}

const CapTable = core.AllocTable(alloc: CapAlloc, resize: CapResize, free: CapFree)

CapAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is a live Cap whose inner allocator outlives it" {
        q := raw.PtrCast[u8, Cap](ctx)
        var c := raw.Read(q)
        var p := raw.Null[u8]()
        if c.live + size <= c.cap {
            p = core.AllocBytes(&*c.inner, size, align)
            if !raw.IsNull(p) {
                c.live += size
                if c.live > c.peak {
                    c.peak = c.live
                }
            }
        }
        raw.Write(q, c)
        return p
    }
}

CapResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    unsafe "ctx is a live Cap whose inner allocator outlives it" {
        q := raw.PtrCast[u8, Cap](ctx)
        var c := raw.Read(q)
        var ok := false
        if new <= old || c.live + new - old <= c.cap {
            ok = core.ResizeBytes(&*c.inner, p, old, new, align)
            if ok {
                c.live += new - old
                if c.live > c.peak {
                    c.peak = c.live
                }
            }
        }
        raw.Write(q, c)
        return ok
    }
}

CapFree(ctx: *u8, p: *u8, size: int, align: int) {
    unsafe "ctx is a live Cap whose inner allocator outlives it" {
        q := raw.PtrCast[u8, Cap](ctx)
        var c := raw.Read(q)
        core.FreeBytes(&*c.inner, p, size, align)
        c.live -= size
        raw.Write(q, c)
    }
}

Get(c: &Cap): Cap {
    unsafe "c is live" {
        return raw.Read(raw.Of(c))
    }
}

Fnv(h: u64, x: u64): u64 {
    return (h ^ x) *% 1099511628211
}

Digest(x: &big.Int): u64 {
    var h: u64 = 14695981039346656037
    if x.Sign() < 0 {
        h = Fnv(h, 1)
    }
    for w in x.Words() {
        h = Fnv(h, w)
    }
    return h
}

Op(ar: &Arena, op: int, x: &big.Int, e: &big.Int, m: &big.Int): big.Error!u64 {
    if op == 0 {
        return Digest(&(try x.ModPow(ar, e, m)))
    } else if op == 1 {
        m2 := try m.Shl(ar, 1)
        return Digest(&(try x.ModPow(ar, e, &m2)))
    } else if op == 2 {
        match try x.ModInverse(ar, m) {
            some(v) {
                return Digest(&v)
            }
            none {
                return 1
            }
        }
    } else if op == 3 {
        return Digest(&(try x.Gcd(ar, m)))
    } else if op == 4 {
        return Digest(&(try m.Gcd(ar, x)))
    }
    return 0
}

// Run runs op over an arena on a heap capped at cap bytes and prints "op
// result digest peak live".
export Run(w: mut Writer, a: &Alloc, cap: int, op: int, x: &big.Int, e: &big.Int, m: &big.Int): WriteError!void {
    var c := Cap(inner: raw.Of(a), live: 0, peak: 0, cap: cap)
    unsafe "c outlives the heap made over it, freed below" {
        h := core.MakeHeap(raw.PtrCast[Cap, u8](raw.Of(&c)), raw.Of(&CapTable))
        var ar := Arena.New(&h) else {
            try Print(w, "{op} new 0 0 0\n", (op))
            return
        }
        var res := "ok"
        var d: u64 = 0
        match Op(&ar, op, x, e, m) {
            ok(v) {
                d = v
            }
            err(er) {
                match er {
                    OutOfMemory {
                        res = "OutOfMemory"
                    }
                    _ {
                        res = "other"
                    }
                }
            }
        }
        ar.Free()
        s := Get(&c)
        pk := s.peak
        lv := s.live
        try Print(w, "{op} {res} {d} {pk} {lv}\n", (op, res, d, pk, lv))
    }
}
`

// bigCapProgram: x of bigCapXWords words, e = 65537, and m of 32 words,
// odd, made by splitmix64 as bigCapNat makes them in Go.
const bigCapProgram = `import std.big
import std.bigcap

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Rand(a: &Arena, n: int, seed: u64): OutOfMemory!big.Int from a {
    z := try a.Slice[u64](n)
    for i in 0..n {
        z[i] = Mix(seed +% Trunc[u64](i))
    }
    z[0] = z[0] | 1
    z[n - 1] = z[n - 1] | 1
    return big.FromWords(z[..], false)
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    x := try Rand(sys.arena, 200000, 1)
    e := try big.FromI64(sys.arena, 65537)
    m := try Rand(sys.arena, 32, 3)
    var out := sys.Stdout()
    for op in 0..5 {
        try bigcap.Run(mut out, sys.heap.Alloc(), 262144, op, &x, &e, &m)
    }
}
`

// bigCapNat is the program's Rand in Go.
func bigCapNat(n int, seed uint64) *big.Int {
	mix := func(x uint64) uint64 {
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		return z ^ (z >> 31)
	}
	ws := make([]big.Word, n)
	for i := range ws {
		ws[i] = big.Word(mix(seed + uint64(i)))
	}
	ws[0] |= 1
	ws[n-1] |= 1
	return new(big.Int).SetBits(ws)
}

// bigCapDigest is the fixture's Digest in Go.
func bigCapDigest(x *big.Int) uint64 {
	h := uint64(14695981039346656037)
	fnv := func(h, x uint64) uint64 { return (h ^ x) * 1099511628211 }
	if x.Sign() < 0 {
		h = fnv(h, 1)
	}
	for _, w := range x.Bits() {
		h = fnv(h, uint64(w))
	}
	return h
}

var bigCapLine = regexp.MustCompile(`^(\d+) (ok|OutOfMemory|other|new) (\d+) (\d+) (-?\d+)$`)

// TestSTDBIG_ModPowMemoryBoundedInX: ModPow (odd and even m),
// ModInverse and Gcd (both orders) with x of 200,000 words (1.6 MB) and m
// of 32 words, over an arena on a heap capped at 256 KiB. Each doc line
// bounds what it holds by m's length, not x's (about 100n words for the
// even ModPow, the largest), so each call must fit and match math/big. A
// reduction that kept x's quotient, 200,000 words, fails the cap with
// OutOfMemory.
func TestSTDBIG_ModPowMemoryBoundedInX(t *testing.T) {
	x := bigCapNat(200000, 1)
	m := bigCapNat(32, 3)
	e := big.NewInt(65537)
	m2 := new(big.Int).Lsh(m, 1)
	want := []uint64{
		bigCapDigest(new(big.Int).Exp(x, e, m)),
		bigCapDigest(new(big.Int).Exp(x, e, m2)),
		1,
		bigCapDigest(new(big.Int).GCD(nil, nil, x, m)),
		bigCapDigest(new(big.Int).GCD(nil, nil, m, x)),
	}
	if r := new(big.Int).ModInverse(x, m); r != nil {
		want[2] = bigCapDigest(r)
	}
	root := t.TempDir()
	stdDir := bigStdDir(t, root)
	manifest, err := os.ReadFile(filepath.Join(stdDir, "pkg.mk"))
	if err != nil {
		t.Fatal(err)
	}
	mf := string(manifest)
	if !strings.Contains(mf, `unsafe_modules: [`) {
		t.Fatalf("pkg.mk: no unsafe_modules list to extend:\n%s", mf)
	}
	mf = strings.Replace(mf, `unsafe_modules: [`, `unsafe_modules: ["bigcap", `, 1)
	for p, text := range map[string]string{"pkg.mk": mf, "bigcap/bigcap.mk": bigCapFixture} {
		dst := filepath.Join(stdDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(bigCapProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main.exe")
	out, status := mkCmd(t, root, stdDir, "build", "--release", "-o", exe, "main.mk")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("driver exit %d\n%s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(r.stdout, "\r\n", "\n")), "\n")
	if len(lines) != len(want) {
		t.Fatalf("%d lines, want %d:\n%s", len(lines), len(want), r.stdout)
	}
	names := []string{"ModPow odd m", "ModPow even m", "ModInverse", "Gcd(x, m)", "Gcd(m, x)"}
	for i, line := range lines {
		g := bigCapLine.FindStringSubmatch(line)
		if g == nil {
			t.Fatalf("bad line %q", line)
		}
		if g[2] != "ok" {
			t.Errorf("%s: %s under a 256 KiB cap (line %q): its memory grows with x", names[i], g[2], line)
			continue
		}
		if g[3] != fmt.Sprint(want[i]) {
			t.Errorf("%s: digest %s, want %d", names[i], g[3], want[i])
		}
		if g[5] != "0" {
			t.Errorf("%s: %s bytes live after Free", names[i], g[5])
		}
		t.Logf("%s: peak %s bytes", names[i], g[4])
	}
}
