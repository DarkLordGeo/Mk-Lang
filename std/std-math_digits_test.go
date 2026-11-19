// Why: float-to-decimal conversion is easy to get wrong in the last digit,
// on ties and on subnormals; Go's strconv is a trusted reference.
// What: TestStd_DigitsEdges and TestStd_DigitsRandom, math.ShortestDigits
// and math.ExactDigits (all modes) against strconv.FormatFloat.
// How: Mk and Go generate the same values and fold every result into an FNV-1a
// digest per block; a block whose digests differ is dumped value by value.

package std

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	std10Blocks     = 160
	std10BlockSize  = 65536
	std10Procs      = 4
	std10RunTimeout = 20 * time.Minute
)

func std10Mix(z0 uint64) uint64 {
	z := z0 + 0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// std10Value is the i-th value's bits: random bits, or for i % 4 == 3 a
// dyadic 1.m * 2^e with e in [-8, 32) and m of 0 to 23 bits.
func std10Value(i uint64) uint64 {
	h := std10Mix(i)
	if i%4 == 3 {
		e := (h % 40)
		m := (h >> 8) % 24
		mant := (h >> 12) & 0xFFFFFFFFFFFFF
		mant &^= (uint64(1) << (52 - m)) - 1
		return (h & 0x8000000000000000) | ((1015 + e) << 52) | mant
	}
	return h
}

// std10Ns is the i-th value's n for the significant modes and for the
// fraction modes.
func std10Ns(i uint64) (int, int) {
	h := std10Mix(i ^ 0xABCDEF)
	n1 := 1 + int(h%20)
	if h%97 == 0 {
		n1 = 17 + int((h>>8)%800)
	}
	n2 := int((h >> 16) % 25)
	if (h>>40)%53 == 0 {
		n2 = int((h >> 24) % 1200)
	}
	return n1, n2
}

type std10Dig struct {
	s   string
	exp int
	neg bool
}

func (d std10Dig) line(k string) string {
	return fmt.Sprintf("%s %t %d %s", k, d.neg, d.exp, d.s)
}

// std10FromE reads FormatFloat's 'e' output of a magnitude.
func std10FromE(s string, neg bool) std10Dig {
	k := strings.IndexByte(s, 'e')
	mant := strings.Replace(s[:k], ".", "", 1)
	e, _ := strconv.Atoi(s[k+1:])
	mant = strings.TrimRight(mant, "0")
	if mant == "" {
		return std10Dig{neg: neg}
	}
	return std10Dig{mant, e, neg}
}

// std10FromF reads FormatFloat's 'f' output of a magnitude.
func std10FromF(s string, neg bool) std10Dig {
	ip, fp, _ := strings.Cut(s, ".")
	d := ip + fp
	dp := len(ip)
	for len(d) > 0 && d[0] == '0' {
		d = d[1:]
		dp--
	}
	d = strings.TrimRight(d, "0")
	if d == "" {
		return std10Dig{neg: neg}
	}
	return std10Dig{d, dp - 1, neg}
}

func std10Special(x float64) bool { return math.IsNaN(x) || math.IsInf(x, 0) }

func std10Shortest(x float64) std10Dig {
	neg := math.Signbit(x)
	if std10Special(x) {
		return std10Dig{neg: neg}
	}
	return std10FromE(strconv.FormatFloat(math.Abs(x), 'e', -1, 64), neg)
}

func std10Sig(x float64, n int) std10Dig {
	neg := math.Signbit(x)
	if std10Special(x) {
		return std10Dig{neg: neg}
	}
	return std10FromE(strconv.FormatFloat(math.Abs(x), 'e', n-1, 64), neg)
}

func std10Frac(x float64, n int) std10Dig {
	neg := math.Signbit(x)
	if std10Special(x) {
		return std10Dig{neg: neg}
	}
	return std10FromF(strconv.FormatFloat(math.Abs(x), 'f', n, 64), neg)
}

// std10RoundUp keeps k digits of the exact expansion d (exponent e),
// rounding half up; k <= 0 keeps none (a 1 at the next power when k == 0
// and the first digit is 5 or more).
func std10RoundUp(d string, e, k int, neg bool) std10Dig {
	if k >= len(d) {
		return std10Dig{d, e, neg}
	}
	if k < 0 {
		return std10Dig{neg: neg}
	}
	if d[k] < '5' {
		r := strings.TrimRight(d[:k], "0")
		if r == "" {
			return std10Dig{neg: neg}
		}
		return std10Dig{r, e, neg}
	}
	b := []byte(d[:k])
	i := k - 1
	for i >= 0 && b[i] == '9' {
		i--
	}
	if i < 0 {
		return std10Dig{"1", e + 1, neg}
	}
	b[i]++
	return std10Dig{string(b[:i+1]), e, neg}
}

func std10Exact(x float64) std10Dig {
	return std10FromE(strconv.FormatFloat(math.Abs(x), 'e', 800, 64), math.Signbit(x))
}

func std10SigUp(x float64, n int) std10Dig {
	if std10Special(x) {
		return std10Dig{neg: math.Signbit(x)}
	}
	d := std10Exact(x)
	if d.s == "" {
		return d
	}
	return std10RoundUp(d.s, d.exp, n, d.neg)
}

func std10FracUp(x float64, n int) std10Dig {
	if std10Special(x) {
		return std10Dig{neg: math.Signbit(x)}
	}
	d := std10Exact(x)
	if d.s == "" {
		return d
	}
	return std10RoundUp(d.s, d.exp, d.exp+1+n, d.neg)
}

// std10Results is the i-th value's results in the program's order.
func std10Results(i uint64) []std10Dig {
	x := math.Float64frombits(std10Value(i))
	n1, n2 := std10Ns(i)
	r := []std10Dig{std10Shortest(x), std10Sig(x, n1)}
	if i%16 == 0 {
		r = append(r, std10Frac(x, n2), std10SigUp(x, n1), std10FracUp(x, n2))
	}
	return r
}

func std10Fold(h, w uint64) uint64 { return (h ^ w) * 0x100000001B3 }

func std10FoldDig(h uint64, d std10Dig) uint64 {
	h = std10Fold(h, uint64(len(d.s)))
	h = std10Fold(h, uint64(d.exp+2000))
	if d.neg {
		h = std10Fold(h, 1)
	} else {
		h = std10Fold(h, 0)
	}
	for i := 0; i < len(d.s); i++ {
		h = std10Fold(h, uint64(d.s[i]))
	}
	return h
}

func std10GoBlock(b int) uint64 {
	h := uint64(0xCBF29CE484222325)
	for j := 0; j < std10BlockSize; j++ {
		for _, d := range std10Results(uint64(b*std10BlockSize + j)) {
			h = std10FoldDig(h, d)
		}
	}
	return h
}

// The edge set.
func std10EdgeBits() []uint64 {
	var v []uint64
	for _, x := range []float64{0, 1, 0.5, 1.5, 2.5, 3.5, 0.125, 0.375, 9.995, 0.1, 0.2, 0.3, 1.0 / 3, 2.0 / 3,
		123.456, 1e21, 1e22, 1e23, 9.999999999999999e22, 1e-7, 1e-6, 5e-324, 1e-323, 2.2250738585072009e-308,
		2.2250738585072014e-308, math.MaxFloat64, 1 << 53, 1<<53 + 2, 1<<53 - 1, 4503599627370496.5,
		0.000001, 1234567890123456789, 5.0e-1, 0.05, 0.005, 0.0005, 1.005, 1.255, 2.675, 8.5, 1e15 + 0.5,
		1e16, 4.35, 1.45, 0.045, 25, 250, 1e100, 1e-100, math.Pi, math.E, math.Sqrt2, 18014398509481984.0} {
		v = append(v, math.Float64bits(x), math.Float64bits(-x))
	}
	for e := -1074; e <= 1023; e += 1 {
		v = append(v, math.Float64bits(math.Ldexp(1, e)))
	}
	for e := -323; e <= 308; e++ {
		x, _ := strconv.ParseFloat("1e"+strconv.Itoa(e), 64)
		v = append(v, math.Float64bits(x))
	}
	v = append(v, 0x0000000000000001, 0x000FFFFFFFFFFFFF, 0x0010000000000000, 0x7FEFFFFFFFFFFFFF,
		0x7FF0000000000000, 0xFFF0000000000000, 0x7FF8000000000000, 0xFFF8000000000001, 0x7FF0000000000001)
	return v
}

var std10EdgeNs = []int{0, 1, 2, 3, 5, 8, 15, 16, 17, 18, 19, 20, 21, 25, 50, 100, 101, 400, 751, 766, 767, 768, 800, 1074, 1075, 1100, 1500}

func std10EdgeLines(bits []uint64) string {
	var b strings.Builder
	for k, xb := range bits {
		x := math.Float64frombits(xb)
		fmt.Fprintf(&b, "%d %s\n", k, std10Shortest(x).line("s"))
		for _, n := range std10EdgeNs {
			if n >= 1 {
				fmt.Fprintf(&b, "%d %d %s\n", k, n, std10Sig(x, n).line("e"))
				fmt.Fprintf(&b, "%d %d %s\n", k, n, std10SigUp(x, n).line("E"))
			}
			fmt.Fprintf(&b, "%d %d %s\n", k, n, std10Frac(x, n).line("f"))
			fmt.Fprintf(&b, "%d %d %s\n", k, n, std10FracUp(x, n).line("F"))
		}
	}
	return b.String()
}

func std10DigitsProgram(edges []uint64) string {
	var b strings.Builder
	b.WriteString(`import std.math

Mix(z0: u64): u64 {
    var z := z0 +% 0x9E3779B97F4A7C15
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

Value(i: u64): u64 {
    h := Mix(i)
    if i % 4 == 3 {
        e := h % 40
        m := (h >> 8) % 24
        var mant := (h >> 12) & 0xFFFFFFFFFFFFF
        mant = mant & ~((Trunc[u64](1) << (52 - m)) - 1)
        return (h & 0x8000000000000000) | ((1015 + e) << 52) | mant
    }
    return h
}

Fold(h: u64, w: u64): u64 {
    return (h ^ w) *% 0x100000001B3
}

FoldDig(h0: u64, d: &math.Digits): u64 {
    var h := Fold(h0, Cast[u64](d.len))
    h = Fold(h, Cast[u64](d.exp + 2000))
    h = Fold(h, if d.neg { 1 } else { 0 })
    for i in 0..d.len {
        h = Fold(h, u64(d.digits[i]))
    }
    return h
}

Line(w: mut Writer, pre: str, k: str, d: &math.Digits): WriteError!void {
    try w.Str(pre)
    try w.Str(k)
    if d.neg {
        try w.Str(" true ")
    } else {
        try w.Str(" false ")
    }
    Print(w, "{e} ", (e: d.exp)) else {}
    try w.Bytes(d.digits[0..d.len])
    try w.Str("\n")
}

// Value i's results, folded into h, or written out when dump.
One(w: mut Writer, i: u64, h0: u64, dump: bool): u64 {
    x := f64.FromBits(Value(i))
    hn := Mix(i ^ 0xABCDEF)
    var n1 := 1 + Trunc[int](hn % 20)
    if hn % 97 == 0 {
        n1 = 17 + Trunc[int]((hn >> 8) % 800)
    }
    var n2 := Trunc[int]((hn >> 16) % 25)
    if (hn >> 40) % 53 == 0 {
        n2 = Trunc[int]((hn >> 24) % 1200)
    }
    s := math.ShortestDigits(x)
    e := math.ExactDigits(x, n1, math.DigitMode.Significant)
    var h := FoldDig(FoldDig(h0, &s), &e)
    if dump {
        Line(w, "", "s", &s) else {}
        Line(w, "", "e", &e) else {}
    }
    if i % 16 == 0 {
        f := math.ExactDigits(x, n2, math.DigitMode.Fraction)
        su := math.ExactDigits(x, n1, math.DigitMode.SignificantHalfUp)
        fu := math.ExactDigits(x, n2, math.DigitMode.FractionHalfUp)
        h = FoldDig(FoldDig(FoldDig(h, &f), &su), &fu)
        if dump {
            Line(w, "", "f", &f) else {}
            Line(w, "", "E", &su) else {}
            Line(w, "", "F", &fu) else {}
        }
    }
    return h
}

`)
	fmt.Fprintf(&b, "const BlockSize: u64 = %d\n\n", std10BlockSize)
	fmt.Fprintf(&b, "const Edges: [%d]u64 = [", len(edges))
	for k, e := range edges {
		if k > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "0x%x", e)
	}
	b.WriteString("]\n\n")
	fmt.Fprintf(&b, "const Ns: [%d]int = [", len(std10EdgeNs))
	for k, n := range std10EdgeNs {
		if k > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%d", n)
	}
	b.WriteString("]\n\n")
	b.WriteString(`Edge(w: mut Writer) {
    for k in 0..Edges.Len() {
        x := f64.FromBits(Edges[k])
        s := math.ShortestDigits(x)
        Print(w, "{k} ", (k)) else {}
        Line(w, "", "s", &s) else {}
        for n in Ns {
            if n >= 1 {
                e := math.ExactDigits(x, n, math.DigitMode.Significant)
                Print(w, "{k} {n} ", (k, n)) else {}
                Line(w, "", "e", &e) else {}
                su := math.ExactDigits(x, n, math.DigitMode.SignificantHalfUp)
                Print(w, "{k} {n} ", (k, n)) else {}
                Line(w, "", "E", &su) else {}
            }
            f := math.ExactDigits(x, n, math.DigitMode.Fraction)
            Print(w, "{k} {n} ", (k, n)) else {}
            Line(w, "", "f", &f) else {}
            fu := math.ExactDigits(x, n, math.DigitMode.FractionHalfUp)
            Print(w, "{k} {n} ", (k, n)) else {}
            Line(w, "", "F", &fu) else {}
        }
    }
}

export Main(sys: &System) {
    var out := sys.Stdout()
    if sys.args[1] == "edges" {
        Edge(mut out)
        out.Flush() else {}
        return
    }
    a := sys.args[2].ParseInt() else {
        return
    }
    b := sys.args[3].ParseInt() else {
        return
    }
    dump := sys.args[1] == "dump"
    for blk in int(a)..int(b) {
        var h: u64 = 0xCBF29CE484222325
        for j in 0..Trunc[int](BlockSize) {
            h = One(mut out, Cast[u64](blk) * BlockSize + Cast[u64](j), h, dump)
        }
        if !dump {
            Print(mut out, "{blk} {h}\n", (blk, h)) else {}
        }
    }
    out.Flush() else {}
}
`)
	return b.String()
}

func std10Run(exe string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), std10RunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %v\n%s", args, err, errb.String())
	}
	return out.String(), nil
}

// TestStd_DigitsEdges: the edge set, every n and mode, dev and release.
func TestStd_DigitsEdges(t *testing.T) {
	edges := std10EdgeBits()
	want := strings.Split(strings.TrimSpace(std10EdgeLines(edges)), "\n")
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			var flags []string
			if prof == "release" {
				flags = append(flags, "--release")
			}
			exe := buildNative(t, nil, std10DigitsProgram(edges), flags...)
			out, err := std10Run(exe, "edges")
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSpace(out), "\n")
			bad := 0
			for i := 0; i < len(want) || i < len(got); i++ {
				var g, w string
				if i < len(got) {
					g = got[i]
				}
				if i < len(want) {
					w = want[i]
				}
				if g != w {
					bad++
					if bad <= 20 {
						t.Errorf("line %d: got %q, want %q", i, g, w)
					}
				}
			}
			if bad > 0 {
				t.Fatalf("%d of %d lines differ", bad, len(want))
			}
			t.Logf("%s: %d edge values, %d lines equal to strconv", prof, len(edges), len(want))
		})
	}
}

// TestStd_DigitsRandom: the 10,485,760 values, release.
//
//   - ShortestDigits(x) against FormatFloat(x, 'e', -1, 64), and
//     ExactDigits(x, n, Significant) against FormatFloat(x, 'e', n-1, 64),
//     over 10,485,760 values (160 blocks of 65,536; three in four random
//     bit patterns, NaN and the infinities among them, one in four a short
//     dyadic m * 2^e, whose exact expansions end in a 5 and so make ties),
//     n from 1 to 20 and, for one value in 97, up to 816. Each block folds
//     every result (len, exp, neg, digits) into an FNV-1a digest in Mk and
//     in Go; a block whose digests differ is dumped value by value, and the
//     first differences are reported.
//   - One value in 16 also runs Fraction (FormatFloat 'f' with n), and the
//     two HalfUp modes, whose oracle is Go's exact expansion
//     (FormatFloat 'e' with 800 digits, every f64's whole expansion) rounded
//     half up by hand: n from 0 to 24 and, for one in 53, up to 1,199.
//   - The edge set (std10EdgeBits: zeros, the extreme subnormals and
//     normals, every power of two, the powers of ten, ties like 0.5, 2.5,
//     0.125 and 9.995, 2^53 +- 1, NaN, the infinities) at every n in
//     std10EdgeNs, every mode, line by line, dev and release.
//
// The values are the same on both sides: SplitMix64 of the index, written
// twice (std10Mix and the program's Mix).
func TestStd_DigitsRandom(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: the 10,485,760-value oracle takes minutes")
	}
	exe := buildNative(t, nil, std10DigitsProgram(std10EdgeBits()), "--release")
	mk := make([]uint64, std10Blocks)
	gold := make([]uint64, std10Blocks)
	errs := make([]error, std10Procs)
	var wg sync.WaitGroup
	per := std10Blocks / std10Procs
	for p := 0; p < std10Procs; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			out, err := std10Run(exe, "blocks", strconv.Itoa(p*per), strconv.Itoa((p+1)*per))
			if err != nil {
				errs[p] = err
				return
			}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				fs := strings.Fields(line)
				bk, _ := strconv.Atoi(fs[0])
				h, _ := strconv.ParseUint(fs[1], 10, 64)
				mk[bk] = h
			}
		}(p)
	}
	next := make(chan int, std10Blocks)
	for b := 0; b < std10Blocks; b++ {
		next <- b
	}
	close(next)
	for w := 0; w < std10Procs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range next {
				gold[b] = std10GoBlock(b)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	shown := 0
	badBlocks := 0
	for b := 0; b < std10Blocks; b++ {
		if mk[b] == gold[b] {
			continue
		}
		badBlocks++
		if shown >= 20 {
			continue
		}
		out, err := std10Run(exe, "dump", strconv.Itoa(b), strconv.Itoa(b+1))
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimSpace(out), "\n")
		var want []string
		var idx []uint64
		for j := 0; j < std10BlockSize; j++ {
			i := uint64(b*std10BlockSize + j)
			for k, d := range std10Results(i) {
				want = append(want, d.line(string("sefEF"[k])))
				idx = append(idx, i)
			}
		}
		for k := 0; k < len(want) && shown < 20; k++ {
			if k >= len(got) || got[k] != want[k] {
				g := ""
				if k < len(got) {
					g = got[k]
				}
				n1, n2 := std10Ns(idx[k])
				t.Errorf("value %d (bits %016x, n %d, %d): got %q, want %q", idx[k], std10Value(idx[k]), n1, n2, g, want[k])
				shown++
			}
		}
	}
	if badBlocks > 0 {
		t.Fatalf("%d of %d blocks differ", badBlocks, std10Blocks)
	}
	t.Logf("%d values (%d blocks): every digest equal to strconv's", std10Blocks*std10BlockSize, std10Blocks)
}
