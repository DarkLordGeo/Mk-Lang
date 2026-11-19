// Why: the Slice[T] functions (core_slice.mk) and Hasher.Bytes (core_hash.mk)
// take word-at-a-time paths that need checks at every offset and length.
// What: TestStdColl_SliceFunctionsMatchGo, TestStdColl_CopyFromLengthPanics
// and TestStdColl_HasherChunksMatchSip13.
// How: Index against bytes.IndexByte around the 8- and 16-byte steps; Fill and
// CopyFrom check the whole array; Hasher.Bytes against SipHash-1-3.

package std

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sliceBytes is the seeded byte data the Index cases slice: few distinct
// values, so every needle occurs, in runs and alone, near word boundaries.
func sliceBytes(n int) []byte {
	r := rand.New(rand.NewSource(11))
	alphabet := []byte{0x00, 0x01, 0x02, 0x80, 0xFF, 'a', 'a', 'a', 'a', 'a', 'a', 'a'}
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return b
}

func mkBytesArray(b []byte) string {
	var s strings.Builder
	s.WriteByte('[')
	for i, x := range b {
		if i > 0 {
			s.WriteString(", ")
		}
		fmt.Fprintf(&s, "0x%02X", x)
	}
	s.WriteByte(']')
	return s.String()
}

// TestStdColl_SliceFunctionsMatchGo: Index of 0x00, 0x01, 0x80, 0xFF and 0x07
// (absent) in data[o..o + n] for o in 0..9 and n in 0..136 equals
// bytes.IndexByte; Index and Has of int and str elements equal a Go scan;
// Fill and CopyFrom of every sub-slice [o, o + n) of 40-element int, u8 and
// struct arrays leave exactly the Go model's array.
func TestStdColl_SliceFunctionsMatchGo(t *testing.T) {
	t.Parallel()
	data := sliceBytes(160)
	needles := []byte{0x00, 0x01, 0x80, 0xFF, 0x07}
	ints := []int{5, 3, 9, 3, 0, 7, 7, 1, 8, 2, 6, 4, 9, 0, 5, 11, 3}
	strs := []string{"b", "", "abc", "ab", "abc", "é", "z"}
	var src strings.Builder
	fmt.Fprintf(&src, `struct P {
    x: int
    y: u8
}

const Data: [%d]u8 = %s
const Needles: [%d]u8 = %s
const Ints: [%d]int = %s
const Strs: [%d]str = [`, len(data), mkBytesArray(data), len(needles), mkBytesArray(needles), len(ints), mkIntArray(ints), len(strs))
	for i, s := range strs {
		if i > 0 {
			src.WriteString(", ")
		}
		src.WriteString(mkString(s))
	}
	src.WriteString(`]

Ix(i: ?int): int {
    if j := i {
        return j
    }
    return -1
}

SumInts(a: []int): u64 {
    var h: u64 = 14695981039346656037
    for x in a {
        h = (h ^ Trunc[u64](x)) *% 1099511628211
    }
    return h
}

SumBytes(a: []u8): u64 {
    var h: u64 = 14695981039346656037
    for x in a {
        h = (h ^ u64(x)) *% 1099511628211
    }
    return h
}

SumPs(a: []P): u64 {
    var h: u64 = 14695981039346656037
    for p in a {
        h = (h ^ Trunc[u64](p.x)) *% 1099511628211
        h = (h ^ u64(p.y)) *% 1099511628211
    }
    return h
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    d := Data
    for o in 0..9 {
        for n in 0..136 {
            try Print(mut out, "i", ())
            for c in Needles {
                x := Ix(d[o..o + n].Index(&c))
                try Print(mut out, " {x}", (x))
            }
            try Print(mut out, "\n", ())
        }
    }
    ints := Ints
    strs := Strs
    for n in 0..ints.Len() + 1 {
        try Print(mut out, "n", ())
        for v in 0..13 {
            x := Ix(ints[0..n].Index(&v))
            h := ints[0..n].Has(&v)
            try Print(mut out, " {x} {h}", (x, h))
        }
        try Print(mut out, "\n", ())
    }
    for n in 0..strs.Len() + 1 {
        try Print(mut out, "s", ())
        for v in Strs {
            x := Ix(strs[0..n].Index(&v))
            try Print(mut out, " {x}", (x))
        }
        y := Ix(strs[0..n].Index(&"q"))
        try Print(mut out, " {y}\n", (y))
    }
    for o in 0..6 {
        for n in 0..35 {
            var a: [40]int = [-1; 40]
            var b: [40]u8 = [0xEE; 40]
            var p: [40]P = [P(x: -1, y: 0xEE); 40]
            sa := mut a[o..o + n]
            sa.Fill(n)
            sb := mut b[o..o + n]
            sb.Fill(Trunc[u8](n))
            sp := mut p[o..o + n]
            sp.Fill(P(x: n, y: Trunc[u8](o)))
            fa := SumInts(a[..])
            fb := SumBytes(b[..])
            fp := SumPs(p[..])
            try Print(mut out, "f {fa} {fb} {fp}\n", (fa, fb, fp))
        }
    }
    for o in 0..6 {
        for n in 0..35 {
            var a: [40]int = [-1; 40]
            var b: [40]u8 = [0xEE; 40]
            var p: [40]P = [P(x: -1, y: 0xEE); 40]
            var sa: [40]int = [0; 40]
            var sp: [40]P = [P(x: 0, y: 0); 40]
            for i in 0..40 {
                sa[i] = i * 3 + 1
                sp[i] = P(x: i * 5, y: Trunc[u8](i + 1))
            }
            ta := mut a[o..o + n]
            ta.CopyFrom(sa[3..3 + n])
            tb := mut b[o..o + n]
            tb.CopyFrom(d[o + 1..o + 1 + n])
            tp := mut p[o..o + n]
            tp.CopyFrom(sp[2..2 + n])
            ca := SumInts(a[..])
            cb := SumBytes(b[..])
            cp := SumPs(p[..])
            try Print(mut out, "c {ca} {cb} {cp}\n", (ca, cb, cp))
        }
    }
}
`)
	fnv := func(xs []uint64) uint64 {
		h := uint64(14695981039346656037)
		for _, x := range xs {
			h = (h ^ x) * 1099511628211
		}
		return h
	}
	type pt struct {
		x int
		y byte
	}
	sumP := func(p []pt) uint64 {
		var xs []uint64
		for _, q := range p {
			xs = append(xs, uint64(q.x), uint64(q.y))
		}
		return fnv(xs)
	}
	sumI := func(a []int) uint64 {
		var xs []uint64
		for _, x := range a {
			xs = append(xs, uint64(x))
		}
		return fnv(xs)
	}
	sumB := func(a []byte) uint64 {
		var xs []uint64
		for _, x := range a {
			xs = append(xs, uint64(x))
		}
		return fnv(xs)
	}
	var want strings.Builder
	for o := 0; o < 9; o++ {
		for n := 0; n < 136; n++ {
			want.WriteString("i")
			for _, c := range needles {
				fmt.Fprintf(&want, " %d", bytes.IndexByte(data[o:o+n], c))
			}
			want.WriteString("\n")
		}
	}
	for n := 0; n <= len(ints); n++ {
		want.WriteString("n")
		for v := 0; v < 13; v++ {
			x := -1
			for i, e := range ints[:n] {
				if e == v {
					x = i
					break
				}
			}
			fmt.Fprintf(&want, " %d %v", x, x >= 0)
		}
		want.WriteString("\n")
	}
	for n := 0; n <= len(strs); n++ {
		want.WriteString("s")
		for _, v := range append(append([]string{}, strs...), "q") {
			x := -1
			for i, e := range strs[:n] {
				if e == v {
					x = i
					break
				}
			}
			fmt.Fprintf(&want, " %d", x)
		}
		want.WriteString("\n")
	}
	fresh := func() ([]int, []byte, []pt) {
		a := make([]int, 40)
		b := make([]byte, 40)
		p := make([]pt, 40)
		for i := range a {
			a[i], b[i], p[i] = -1, 0xEE, pt{-1, 0xEE}
		}
		return a, b, p
	}
	for o := 0; o < 6; o++ {
		for n := 0; n < 35; n++ {
			a, b, p := fresh()
			for i := o; i < o+n; i++ {
				a[i], b[i], p[i] = n, byte(n), pt{n, byte(o)}
			}
			fmt.Fprintf(&want, "f %d %d %d\n", sumI(a), sumB(b), sumP(p))
		}
	}
	for o := 0; o < 6; o++ {
		for n := 0; n < 35; n++ {
			a, b, p := fresh()
			for i := 0; i < n; i++ {
				a[o+i] = (3+i)*3 + 1
				b[o+i] = data[o+1+i]
				p[o+i] = pt{(2 + i) * 5, byte(2 + i + 1)}
			}
			fmt.Fprintf(&want, "c %d %d %d\n", sumI(a), sumB(b), sumP(p))
		}
	}
	diffLines(t, collRun(t, src.String()), want.String())
}

// TestStdColl_CopyFromLengthPanics: CopyFrom between slices of different
// lengths panics with code 2 before any store, so the program exits
// 101 with the message and its first print only.
func TestStdColl_CopyFromLengthPanics(t *testing.T) {
	t.Parallel()
	exe := collBuild(t, `export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var a: [4]int = [1, 2, 3, 4]
    b := [7, 8, 9]
    try Print(mut out, "before\n", ())
    try out.Flush()
    s := mut a[..]
    s.CopyFrom(b[..])
    x := a[0]
    try Print(mut out, "after {x}\n", (x))
}
`)
	ctx, cancel := context.WithTimeout(context.Background(), collTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = filepath.Dir(exe)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 101 || out.String() != "before\n" || !strings.Contains(errb.String(), "Slice.CopyFrom: lengths differ") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 101, \"before\\n\", the CopyFrom panic", code, out.String(), errb.String())
	}
}

// TestStdColl_HasherChunksMatchSip13: Hasher.Bytes fed in seeded random
// pieces (empty pieces, pieces shorter than the pending tail, pieces that
// complete it, long word runs), with U64 words between some pieces (the
// aligned path and the path through the tail), equals Go's SipHash-1-3 of
// the concatenated bytes and little-endian words.
func TestStdColl_HasherChunksMatchSip13(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewSource(5))
	type hcase struct {
		b     []byte
		cuts  []int
		words []uint64
	}
	var cases []hcase
	for k := 0; k < 120; k++ {
		n := r.Intn(90)
		if k < 40 {
			n = k
		}
		b := make([]byte, n)
		r.Read(b)
		var cuts []int
		for c := 0; c < r.Intn(6); c++ {
			cuts = append(cuts, r.Intn(n+1))
		}
		for i := 1; i < len(cuts); i++ {
			for j := i; j > 0 && cuts[j] < cuts[j-1]; j-- {
				cuts[j], cuts[j-1] = cuts[j-1], cuts[j]
			}
		}
		var words []uint64
		if k%3 != 0 {
			for range cuts {
				if r.Intn(2) == 0 {
					words = append(words, r.Uint64())
				} else {
					break
				}
			}
		}
		cases = append(cases, hcase{b, cuts, words})
	}
	var src strings.Builder
	src.WriteString("import std.collfix\n\n")
	for i, c := range cases {
		fmt.Fprintf(&src, "const B%d: [%d]u8 = %s\nconst C%d: [%d]int = %s\nconst W%d: [%d]u64 = [", i, len(c.b), mkBytesArray(c.b), i, len(c.cuts), mkIntArray(c.cuts), i, len(c.words))
		for j, w := range c.words {
			if j > 0 {
				src.WriteString(", ")
			}
			fmt.Fprintf(&src, "%d", w)
		}
		src.WriteString("]\n")
	}
	src.WriteString("\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	for i := range cases {
		fmt.Fprintf(&src, "    b%d := B%d\n    c%d := C%d\n    w%d := W%d\n    h%d := collfix.HasherChunks(b%d[..], c%d[..], w%d[..])\n    try Print(mut out, \"{h%d}\\n\", (h%d))\n", i, i, i, i, i, i, i, i, i, i, i, i)
	}
	src.WriteString("}\n")
	var want strings.Builder
	for _, c := range cases {
		var all []byte
		prev := 0
		for i, cut := range c.cuts {
			all = append(all, c.b[prev:cut]...)
			prev = cut
			if i < len(c.words) {
				all = binary.LittleEndian.AppendUint64(all, c.words[i])
			}
		}
		all = append(all, c.b[prev:]...)
		fmt.Fprintf(&want, "%d\n", foldHash(all))
	}
	diffLines(t, collRun(t, src.String()), want.String())
}
