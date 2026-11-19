// Why: some text functions were covered only by tests that never ran them.
// What: native tests of str.FromC, the f64 and f32 Format impls, a user float
// %, str.Trim against Go, FindFrom from every start, and the empty str.
// How: each runs natively at dev and release against Go's result.

package std

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestSTDTEXT_FormatImplsMatchGo: x.Format(w) on f64 and f32, called
// directly (not through a Print hole, which FmtValue routes itself), gives
// the shortest round-trip layout (core_impls.mk calls FmtF64 and FmtF32).
// Natively, dev and
// release, over the edge values and 20,000 seeded bit patterns of each.
func TestSTDTEXT_FormatImplsMatchGo(t *testing.T) {
	t.Parallel()
	const n = 20000
	var edges []string
	for _, x := range edgeFloats() {
		edges = append(edges, fmt.Sprintf("0x%x", math.Float64bits(x)))
	}
	src := fmt.Sprintf(`import std.zzbits

const Edges: [%d]u64 = [%s]

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var s: u64 = 7
    for b in Edges {
        x := zzbits.F64From(b)
        try x.Format(mut out)
        try out.Str("\n")
        y := zzbits.F32From(Trunc[u32](b >> 32))
        try y.Format(mut out)
        try out.Str("\n")
    }
    for _ in 0..%d {
        s = s +%% 0x9E3779B97F4A7C15
        b := zzbits.Mix(s)
        x := zzbits.F64From(b)
        try x.Format(mut out)
        try out.Str(" ")
        y := zzbits.F32From(Trunc[u32](b))
        try y.Format(mut out)
        try out.Str("\n")
    }
    try out.Flush()
}
`, len(edges), strings.Join(edges, ", "), n)
	var want strings.Builder
	for _, x := range edgeFloats() {
		y := math.Float32frombits(uint32(math.Float64bits(x) >> 32))
		fmt.Fprintf(&want, "%s\n%s\n", goShortest(x, 64), goShortest(float64(y), 32))
	}
	s := uint64(7)
	for i := 0; i < n; i++ {
		b := splitmix64(&s)
		fmt.Fprintf(&want, "%s %s\n", goShortest(math.Float64frombits(b), 64), goShortest(float64(math.Float32frombits(uint32(b))), 32))
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildWithModule(t, "zzbits/bits.mk", bitsModule, src, flags...), "")
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		t.Logf("%v: %v", flags, res.took)
		compareLines(t, res.stdout, want.String())
	}
}

// TestSTDTEXT_UserFloatRemMatchesGo: a float % in a user program (the
// driver roots Rem64 for it), f64 and f32, with literal edge operands and 100,000 seeded pairs, against Go's
// math.Mod bits (NaN canonical), dev and release.
func TestSTDTEXT_UserFloatRemMatchesGo(t *testing.T) {
	t.Parallel()
	const n = 100000
	src := fmt.Sprintf(`import std.zzbits

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := 7.5
    b := 2.0
    c := -7.5
    z := 0.0
    r1 := a %% b
    r2 := c %% b
    r3 := a %% z
    w1 := zzbits.F64Bits(r1)
    w2 := zzbits.F64Bits(r2)
    w3 := zzbits.F64Bits(r3)
    try Print(mut out, "{w1:x} {w2:x} {w3:x}\n", (w1, w2, w3))
    p: f32 = 7.5
    q: f32 = -2.0
    r4 := p %% q
    try Print(mut out, "{r4}\n", (r4))
    var s: u64 = 11
    for i in 0..%d {
        s = s +%% 0x9E3779B97F4A7C15
        u := zzbits.Mix(s)
        s = s +%% 0x9E3779B97F4A7C15
        var v := zzbits.Mix(s)
        if i %% 2 == 1 {
            // exponents at most 63 apart, so the loop runs
            v = (v & 0x800FFFFFFFFFFFFF) | ((((u >> 52) & 0x7FF) -%% ((u >> 7) & 63)) & 0x7FF) << 52
        }
        x := zzbits.F64From(u)
        y := zzbits.F64From(v)
        m := zzbits.F64Bits(x %% y)
        f := zzbits.F32From(Trunc[u32](u))
        g := zzbits.F32From(Trunc[u32](v))
        h := f %% g
        try Print(mut out, "{m:x} {h}\n", (m, h))
    }
    try out.Flush()
}
`, n)
	canon := func(f float64) uint64 {
		if math.IsNaN(f) {
			return 0x7ff8000000000000
		}
		return math.Float64bits(f)
	}
	var want strings.Builder
	fmt.Fprintf(&want, "%x %x %x\n", canon(math.Mod(7.5, 2)), canon(math.Mod(-7.5, 2)), canon(math.Mod(7.5, 0)))
	fmt.Fprintf(&want, "%s\n", goShortest(float64(float32(math.Mod(7.5, -2))), 32))
	s := uint64(11)
	for i := 0; i < n; i++ {
		u := splitmix64(&s)
		v := splitmix64(&s)
		if i%2 == 1 {
			v = (v & 0x800FFFFFFFFFFFFF) | ((((u>>52)&0x7FF)-((u>>7)&63))&0x7FF)<<52
		}
		m := canon(math.Mod(math.Float64frombits(u), math.Float64frombits(v)))
		f, g := math.Float32frombits(uint32(u)), math.Float32frombits(uint32(v))
		h := float32(math.Mod(float64(f), float64(g))) // exact: the result is representable
		fmt.Fprintf(&want, "%x %s\n", m, goShortest(float64(h), 32))
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildWithModule(t, "zzbits/bits.mk", bitsModule, src, flags...), "")
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		t.Logf("%v: %v", flags, res.took)
		compareLines(t, res.stdout, want.String())
	}
}

// trimRunes is the alphabet of the Trim and FromC cases: every White_Space
// scalar, its neighbours, and scalars of each encoded length.
func trimRunes() []rune {
	rs := []rune{'a', 'Z', '0', 0x7F, 0x80, 0xE9, 0x7FF, 0x800, 0x200B, 0x2060, 0xFEFF, 0xFFFD, 0x10000, 0x1F600, 0x10FFFF}
	for r := rune(0); r < 0x3001; r++ {
		if unicode.Is(unicode.White_Space, r) {
			rs = append(rs, r, r+1)
			if r > 0 {
				rs = append(rs, r-1)
			}
		}
	}
	var out []rune
	for _, r := range rs {
		if r != '\n' && r != '\r' && r != 0 && utf8.ValidRune(r) {
			out = append(out, r)
		}
	}
	return out
}

// TestSTDTEXT_TrimMatchesGo: str.Trim natively, dev and release, against
// strings.TrimFunc(s, unicode.IsSpace) (Go's IsSpace is White_Space) on
// 50,000 seeded strings over trimRunes, so SpaceBefore meets every encoded
// length at the end.
func TestSTDTEXT_TrimMatchesGo(t *testing.T) {
	t.Parallel()
	rs := trimRunes()
	var spaces []rune
	for _, r := range rs {
		if unicode.IsSpace(r) {
			spaces = append(spaces, r)
		}
	}
	rnd := rand.New(rand.NewSource(61))
	var in, want strings.Builder
	for i := 0; i < 50000; i++ {
		var b strings.Builder
		for k := rnd.Intn(4); k > 0; k-- {
			b.WriteRune(spaces[rnd.Intn(len(spaces))])
		}
		for k := rnd.Intn(6); k > 0; k-- {
			b.WriteRune(rs[rnd.Intn(len(rs))])
		}
		for k := rnd.Intn(4); k > 0; k-- {
			b.WriteRune(spaces[rnd.Intn(len(spaces))])
		}
		s := strings.ReplaceAll(b.String(), "\v", " ") // keep one line per case
		in.WriteString(s + "\n")
		fmt.Fprintf(&want, "[%s]\n", strings.TrimFunc(s, unicode.IsSpace))
	}
	src := `export Main(sys: &System): ReadError|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    for {
        l := try inp.Line(sys.arena)
        s := l else {
            break
        }
        t := s.Trim()
        try Print(mut out, "[{t}]\n", (t))
    }
    try out.Flush()
}
`
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildNative(t, nil, src, flags...), in.String())
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		t.Logf("%v: %v", flags, res.took)
		compareLines(t, res.stdout, want.String())
	}
}

// fromCFixture is a std.core module that calls str.FromC on a NUL-ended
// copy of b at offset k of a buffer whose other bytes are 0xBF.
const fromCFixture = `import std.raw

export ZzFromC(b: []u8, k: int): int {
    var buf: [96]u8 = [0xBF; 96]
    for i in 0..b.Len() {
        buf[k + i] = b[i]
    }
    buf[k + b.Len()] = 0
    unsafe "fixture: buf holds a NUL at k + b.Len()" {
        s := str.FromC(raw.Offset(raw.Data(buf[..]), k))
        if s.Len() != b.Len() || !BytesEqual(s.AsBytes(), 0, b) {
            return -1
        }
        return s.Len()
    }
}
`

// TestSTDTEXT_FromC: str.FromC natively (check's E1101 test never runs
// it): valid NUL-ended text at offsets 0 to 7 gives
// the same bytes and length, and invalid UTF-8 before the NUL panics with
// code 8 in its own process, dev and release.
func TestSTDTEXT_FromC(t *testing.T) {
	t.Parallel()
	texts := []string{"", "a", "hello", "héllo", "€\U0001F600", strings.Repeat("x", 40), strings.Repeat("é", 20) + "z"}
	var lits []string
	var want strings.Builder
	for _, s := range texts {
		var bs []string
		for _, c := range []byte(s) {
			bs = append(bs, strconv.Itoa(int(c)))
		}
		bs = append(bs, "0") // one spare byte, so no array is empty
		lits = append(lits, fmt.Sprintf("    b%d: [%d]u8 = [%s]", len(lits), len(s)+1, strings.Join(bs, ", ")))
		for k := 0; k < 8; k++ {
			fmt.Fprintf(&want, "%d\n", len(s))
		}
	}
	var calls strings.Builder
	for i := range texts {
		fmt.Fprintf(&calls, "    for k in 0..8 {\n        n := zzfromc.FromC(b%d[0..%d], k)\n        try Print(mut out, \"{n}\\n\", (n))\n    }\n", i, len(texts[i]))
	}
	src := "import std.zzfromc\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n" + strings.Join(lits, "\n") + "\n" + calls.String() + "    try out.Flush()\n}\n"
	// std.core is internal to std: a program reaches the fixture through a
	// std module of its own (as moduleCompanions does).
	fx := map[string]string{
		"core/zz_fromc.mk": fromCFixture,
		"zzfromc/fromc.mk": "import std.core\n\nexport FromC(b: []u8, k: int): int {\n    return core.ZzFromC(b, k)\n}\n",
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildNative(t, fx, src, flags...), "")
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		compareLines(t, res.stdout, want.String())
	}
	for _, bad := range [][]byte{{0xC0, 0x80}, {0x61, 0xED, 0xA0, 0x80}, {0xE2, 0x82}, {0xF4, 0x90, 0x80, 0x80}, {0x80}} {
		var bs []string
		for _, c := range bad {
			bs = append(bs, strconv.Itoa(int(c)))
		}
		src := fmt.Sprintf("import std.zzfromc\n\nexport Main(sys: &System) {\n    b: [%d]u8 = [%s]\n    _ = zzfromc.FromC(b[..], sys.args.Len() %% 8)\n}\n", len(bad), strings.Join(bs, ", "))
		for _, flags := range [][]string{nil, {"--release"}} {
			res := runNative(t, buildNative(t, fx, src, flags...), "")
			if res.code != 101 || !strings.Contains(res.stderr, "str.FromC: invalid UTF-8") {
				t.Errorf("%v % x: exit %d, want 101 with the FromC panic\nstderr:\n%s", flags, bad, res.code, res.stderr)
			}
		}
	}
}

// TestSTDTEXT_FindFromEveryStart: FindFrom at compile time from every
// start 0..=n of 300 seeded haystacks (Two-Way traps and a small alphabet),
// including from == n with a non-empty needle, needles longer than the
// rest and needles equal to the haystack, against strings.Index(s[from:]).
func TestSTDTEXT_FindFromEveryStart(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewSource(71))
	var hs, ps, from, at []string
	add := func(h, p string, f int) {
		w := -1
		if i := strings.Index(h[f:], p); i >= 0 {
			w = f + i
		}
		hs = append(hs, mkString(h))
		ps = append(ps, mkString(p))
		from = append(from, strconv.Itoa(f))
		at = append(at, strconv.Itoa(w))
	}
	for _, c := range [][2]string{{"aaaa", "aaaa"}, {"abc", "abcd"}, {"abab", "ab"}, {"", "a"}, {"a", "a"}, {"aabaa", "aa"}, {"xyz", ""}} {
		for f := 0; f <= len(c[0]); f++ {
			add(c[0], c[1], f)
		}
	}
	for len(hs) < 3000 {
		alpha := "ab"
		if r.Intn(3) == 0 {
			alpha = "abcé"
		}
		mk := func(n int) string {
			var b strings.Builder
			for b.Len() < n {
				b.WriteByte(alpha[r.Intn(len(alpha))])
			}
			return strings.ToValidUTF8(b.String(), "a")
		}
		h := mk(r.Intn(14))
		var p string
		switch r.Intn(4) {
		case 0:
			p = h
		case 1:
			if len(h) > 0 {
				i := r.Intn(len(h))
				p = h[i:]
				if !utf8.ValidString(p) {
					p = "ab"
				}
			}
		default:
			p = mk(1 + r.Intn(5))
		}
		for f := 0; f <= len(h); f++ {
			if utf8.ValidString(h[f:]) {
				add(h, p, f)
			}
		}
	}
	n := len(hs)
	fixture := fmt.Sprintf(`const FfH: [%[1]d]str = [%[2]s]
const FfP: [%[1]d]str = [%[3]s]
const FfFrom: [%[1]d]int = [%[4]s]
const FfAt: [%[1]d]int = [%[5]s]

FfCheck(): int {
    hs := &FfH
    ps := &FfP
    fs := &FfFrom
    ws := &FfAt
    var bad := 0
    for k in 0..%[1]d {
        var got := -1
        if i := FindFrom(hs[k], ps[k], fs[k]) {
            got = i
        }
        if got != ws[k] {
            bad += 1
        }
    }
    return bad
}

const FfOk = [0; 1][FfCheck()]
`, n, strings.Join(hs, ", "), strings.Join(ps, ", "), strings.Join(from, ", "), strings.Join(at, ", "))
	expectClean(t, "stdtext_m5_findfrom", fixture)
}

// emptyStrProgram is a release failure reduced: a Words loop over
// "" and then "".Scalars().Next() hung at --release while check gave the ""
// literal a null data pointer under codegen's !nonnull. It is in TestSTDTEXT_LinuxNative too, so the [0]u8 const
// the literal now points at is seen to link and run on both targets.
func emptyStrProgram() textProgram {
	src := `WordCount(s: str): int {
    var it := s.Words()
    var n := 0
    for {
        _ = it.Next() else {
            return n
        }
        n += 1
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    b := WordCount("")
    var sc := "".Scalars()
    c := sc.Next().IsNone()
    d := "hello".StartsWith("")
    try Print(mut out, "{b} {c} {d}\n", (b, c, d))
    try out.Flush()
}
`
	return textProgram{"empty", "", "", src, "", "0 true true\n"}
}

// TestSTDTEXT_EmptyStrLiteral runs emptyStrProgram at dev and release.
func TestSTDTEXT_EmptyStrLiteral(t *testing.T) {
	t.Parallel()
	p := emptyStrProgram()
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildNative(t, nil, p.src, flags...), "")
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		compareLines(t, res.stdout, p.want)
	}
}
