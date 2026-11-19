// Why: std.hash's SHA-1 and SHA-384 are Mk code and need an outside oracle.
// What: oracle tests of hash.Sha1, Sha1State, hash.Sha384 and Sha384State,
// the zero-fill rejection, and mk check on every target.
// How: native (dev, release) and compile-time runs against crypto/sha1 and
// crypto/sha512 on splitmix64 bytes, FIPS 180-4 messages and random splits.

package std

import (
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// h2Try builds src as main.mk over a copy of std with fixtures and returns
// the executable path, mk's output and its exit status.
func h2Try(t *testing.T, fixtures map[string]string, src string, flags ...string) (string, string, int) {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, fixtures)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	return exe, out, status
}

// h2Run builds src with h2Try and runs it; a failed build or run fails the
// test, a missing toolchain skips it. It returns standard output with LF
// line ends.
func h2Run(t *testing.T, fixtures map[string]string, src string, flags ...string) string {
	t.Helper()
	exe, out, status := h2Try(t, fixtures, src, flags...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %v: exit %d\n%s", flags, status, out)
	}
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%.4000s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return strings.ReplaceAll(r.stdout, "\r\n", "\n")
}

// h2Prelude is the Mk side's splitmix64 (hmSplitmix's), filler and hex
// printer.
const h2Prelude = `import std.hash

struct Rng {
    s: u64
}

Rng.Next(mut self): u64 {
    self.s = self.s +% 0x9E3779B97F4A7C15
    var z := self.s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

Fill(r: mut Rng, b: mut []u8) {
    var i := 0
    for i < b.Len() {
        x := r.Next()
        var k := 0
        for k < 8 && i < b.Len() {
            b[i] = Trunc[u8](x >> (8 * k))
            k += 1
            i += 1
        }
    }
}

Hex(out: mut Writer, b: []u8): WriteError!void {
    for i in 0..b.Len() {
        x := b[i]
        if x < 16 {
            try Print(out, "0")
        }
        try Print(out, "{x:x}", (x: x))
    }
}
`

// h2Lengths are the long lengths past 300: block edges of both block
// sizes (64 and 128) and longer messages.
var h2Lengths = []int{447, 448, 511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097, 65543, 102400}

// h2Pieces are the split sizes the streaming states take, chosen by the
// generator: around both block sizes and the padding thresholds (55, 56,
// 111, 112).
var h2Pieces = []int{0, 1, 2, 3, 7, 55, 56, 63, 64, 65, 111, 112, 127, 128, 129, 1025}

// h2Fips are FIPS 180-4's example messages (the one-block and the
// multi-block messages of both families) and the empty message.
var h2Fips = []string{
	"",
	"abc",
	"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
	"abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu",
}

// h2FipsWant are the digests FIPS 180-4's examples state (and the empty
// message's), as published; TestSTDHASH_FipsVectorsAgreeWithGo checks
// them against Go first, so a mistyped vector fails there.
var h2FipsWant = map[string][2]string{
	"":    {"da39a3ee5e6b4b0d3255bfef95601890afd80709", "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b"},
	"abc": {"a9993e364706816aba3e25717850c26c9cd0d89d", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7"},
	"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq":                                                         {"84983e441c3bd26ebaae4aa1f95129e5e54670f1", "3391fdddfc8dc7393707a65b1b4709397cf8b1d162af05abfe8f450de5f36bc6b0455a8520bc4e6f5fe95b1fe3c8452b"},
	"abcdefghbcdefghicdefghijdefghijkefghijklfghijklmghijklmnhijklmnoijklmnopjklmnopqklmnopqrlmnopqrsmnopqrstnopqrstu": {"a49b2446a02c645bf419f995b67091253a04a259", "09330c33f71147e83d192fc782cd1b4753111b173b3b05d22fa08086e3b0f712fcc7c71a557e2db966c3e9fa91746039"},
	// A million 'a's.
	"M": {"34aa973cd4c4daa4f61eeb2bdbad27316534016f", "9d0e1809716474cb086e834e310a4a1ced149e9c00f248527972cec5704c2a5b07b8b3dc38ecc4ebae97ddd87f3d8985"},
}

// h2Program prints, per input, the one-shot Sha1 and Sha384 digests in hex;
// then the streaming states over seeded random pieces; then a fork of each
// state; then a million 'a's fed in 1,000-byte pieces.
func h2Program() string {
	var b strings.Builder
	b.WriteString(h2Prelude)
	fmt.Fprintf(&b, "\nconst Lengths: [%d]int = %s\n", len(h2Lengths), mkIntArray(h2Lengths))
	fmt.Fprintf(&b, "const Pieces: [%d]int = %s\n", len(h2Pieces), mkIntArray(h2Pieces))
	b.WriteString(`
Line(out: mut Writer, tag: str, b: []u8): WriteError!void {
    n := b.Len()
    try Print(out, "{tag} {n} ", (tag: tag, n: n))
    a := hash.Sha1(b)
    try Hex(out, a[..])
    try Print(out, " ")
    c := hash.Sha384(b)
    try Hex(out, c[..])
    try Print(out, "\n")
}

Stream(out: mut Writer, r: mut Rng, b: []u8): WriteError!void {
    var s1 := hash.Sha1State.New()
    var s3 := hash.Sha384State.New()
    var i := 0
    for i < b.Len() {
        var k := Pieces[Trunc[int](r.Next() % 16)]
        if k > b.Len() - i {
            k = b.Len() - i
        }
        s1.Bytes(b[i..i + k])
        s3.Bytes(b[i..i + k])
        i += k
    }
    n := b.Len()
    try Print(out, "S {n} ", (n: n))
    a := s1.Sum()
    try Hex(out, a[..])
    try Print(out, " ")
    c := s3.Sum()
    try Hex(out, c[..])
    try Print(out, "\n")
}

// Fork hashes b's first half into each state, copies it, finishes the copy
// with the second half and the original with nothing, then feeds the
// original the second half after its Sum (Sum leaves the state as it was).
Fork(out: mut Writer, b: []u8): WriteError!void {
    h := b.Len() / 2
    var p := hash.Sha1State.New()
    p.Bytes(b[0..h])
    var q := p
    q.Bytes(b[h..b.Len()])
    x := q.Sum()
    y := p.Sum()
    p.Bytes(b[h..b.Len()])
    z := p.Sum()
    var u := hash.Sha384State.New()
    u.Bytes(b[0..h])
    var v := u
    v.Bytes(b[h..b.Len()])
    x3 := v.Sum()
    y3 := u.Sum()
    u.Bytes(b[h..b.Len()])
    z3 := u.Sum()
    try Print(out, "F ")
    try Hex(out, x[..])
    try Print(out, " ")
    try Hex(out, y[..])
    try Print(out, " ")
    try Hex(out, z[..])
    try Print(out, " ")
    try Hex(out, x3[..])
    try Print(out, " ")
    try Hex(out, y3[..])
    try Print(out, " ")
    try Hex(out, z3[..])
    try Print(out, "\n")
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var r := Rng(s: 1)
    buf := try sys.arena.Slice[u8](102400 + 16)
    Fill(mut r, buf)
    for n in 0..301 {
        off := n % 8
        try Line(mut out, "L", buf[off..off + n])
    }
    for k in 0..Lengths.Len() {
        n := Lengths[k]
        off := n % 8
        try Line(mut out, "L", buf[off..off + n])
    }
    zeros := try sys.arena.Slice[u8](1000)
    try Line(mut out, "Z", zeros[..])
    ones := try sys.arena.Slice[u8](1000)
    for i in 0..1000 {
        ones[i] = 0xFF
    }
    try Line(mut out, "O", ones[..])
`)
	for _, m := range h2Fips {
		fmt.Fprintf(&b, "    try Line(mut out, \"N\", %q.AsBytes())\n", m)
	}
	b.WriteString(`    var sr := Rng(s: 7)
    for n in 0..300 {
        try Stream(mut out, mut sr, buf[0..n])
    }
    for k in 0..Lengths.Len() {
        try Stream(mut out, mut sr, buf[0..Lengths[k]])
        try Stream(mut out, mut sr, buf[0..Lengths[k]])
    }
    try Fork(mut out, buf[0..5000])
    try Fork(mut out, buf[0..3])
    try Fork(mut out, buf[0..256])
    aa := try sys.arena.Slice[u8](1000)
    for i in 0..1000 {
        aa[i] = 0x61
    }
    var m1 := hash.Sha1State.New()
    var m3 := hash.Sha384State.New()
    for _ in 0..1000 {
        m1.Bytes(aa[..])
        m3.Bytes(aa[..])
    }
    try Print(mut out, "M ")
    d1 := m1.Sum()
    try Hex(mut out, d1[..])
    try Print(mut out, " ")
    d3 := m3.Sum()
    try Hex(mut out, d3[..])
    try Print(mut out, "\n")
}
`)
	return b.String()
}

// h2Line is the oracle's line for one input.
func h2Line(tag string, b []byte) string {
	a := sha1.Sum(b)
	c := sha512.Sum384(b)
	return fmt.Sprintf("%s %d %x %x", tag, len(b), a, c)
}

// h2Want is the oracle's output for h2Program.
func h2Want() string {
	var lines []string
	r := hmSplitmix{1}
	buf := make([]byte, 102400+16)
	r.fill(buf)
	for n := 0; n <= 300; n++ {
		off := n % 8
		lines = append(lines, h2Line("L", buf[off:off+n]))
	}
	for _, n := range h2Lengths {
		off := n % 8
		lines = append(lines, h2Line("L", buf[off:off+n]))
	}
	lines = append(lines, h2Line("Z", make([]byte, 1000)))
	ones := make([]byte, 1000)
	for i := range ones {
		ones[i] = 0xFF
	}
	lines = append(lines, h2Line("O", ones))
	for _, m := range h2Fips {
		lines = append(lines, h2Line("N", []byte(m)))
	}
	sr := hmSplitmix{7}
	stream := func(b []byte) string {
		// The pieces only decide the splits; the digest is the one-shot's.
		for i := 0; i < len(b); {
			i += min(h2Pieces[sr.next()%16], len(b)-i)
		}
		return "S" + h2Line("", b)
	}
	for n := 0; n < 300; n++ {
		lines = append(lines, stream(buf[:n]))
	}
	for _, n := range h2Lengths {
		lines = append(lines, stream(buf[:n]), stream(buf[:n]))
	}
	fork := func(b []byte) string {
		h := len(b) / 2
		return fmt.Sprintf("F %x %x %x %x %x %x", sha1.Sum(b), sha1.Sum(b[:h]), sha1.Sum(b), sha512.Sum384(b), sha512.Sum384(b[:h]), sha512.Sum384(b))
	}
	lines = append(lines, fork(buf[:5000]), fork(buf[:3]), fork(buf[:256]))
	million := []byte(strings.Repeat("a", 1000000))
	lines = append(lines, fmt.Sprintf("M %x %x", sha1.Sum(million), sha512.Sum384(million)))
	return strings.Join(lines, "\n") + "\n"
}

// TestSTDHASH_FipsVectorsAgreeWithGo checks the published digests the
// Mk tests compare against with Go's crypto/sha1 and crypto/sha512, so a
// mistyped vector fails here and not as a false failure of std.hash.
func TestSTDHASH_FipsVectorsAgreeWithGo(t *testing.T) {
	for m, want := range h2FipsWant {
		in := []byte(m)
		if m == "M" {
			in = []byte(strings.Repeat("a", 1000000))
		}
		if want[0] != "" {
			if got := fmt.Sprintf("%x", sha1.Sum(in)); got != want[0] {
				t.Errorf("SHA-1(%.20q): Go %s, vector %s", m, got, want[0])
			}
		}
		if want[1] != "" {
			if got := fmt.Sprintf("%x", sha512.Sum384(in)); got != want[1] {
				t.Errorf("SHA-384(%.20q): Go %s, vector %s", m, got, want[1])
			}
		}
	}
}

// h2CheckFips checks the program's N and M lines against the published
// digests themselves, independently of the Go oracle.
func h2CheckFips(t *testing.T, got string) {
	t.Helper()
	seen := 0
	for _, line := range strings.Split(got, "\n") {
		f := strings.Fields(line)
		var key string
		var d1, d3 string
		switch {
		case len(f) == 4 && f[0] == "N":
			var n int
			fmt.Sscan(f[1], &n)
			for _, m := range h2Fips {
				if len(m) == n {
					key = m
				}
			}
			d1, d3 = f[2], f[3]
		case len(f) == 3 && f[0] == "M":
			key, d1, d3 = "M", f[1], f[2]
		default:
			continue
		}
		want := h2FipsWant[key]
		seen++
		if want[0] != "" && d1 != want[0] {
			t.Errorf("Sha1(%.20q) = %s, published %s", key, d1, want[0])
		}
		if want[1] != "" && d3 != want[1] {
			t.Errorf("Sha384(%.20q) = %s, published %s", key, d3, want[1])
		}
	}
	if seen != len(h2Fips)+1 {
		t.Fatalf("found %d published-vector lines, want %d", seen, len(h2Fips)+1)
	}
}

func h2Differential(t *testing.T, flags ...string) {
	got := h2Run(t, nil, h2Program(), flags...)
	hashModCompare(t, got, h2Want())
	h2CheckFips(t, got)
}

// TestSTDHASH_ShaDifferentialDev runs Sha1, Sha384 and their states in a
// dev build against crypto/sha1 and crypto/sha512, and the FIPS 180-4
// examples against their published digests.
func TestSTDHASH_ShaDifferentialDev(t *testing.T) {
	t.Parallel()
	h2Differential(t)
}

// TestSTDHASH_ShaDifferentialRelease is the same in a release build.
func TestSTDHASH_ShaDifferentialRelease(t *testing.T) {
	t.Parallel()
	h2Differential(t, "--release")
}

// h2ConstText is longer than two SHA-384 blocks, so the evaluator runs the
// whole-block path of both families and a two-block padding for neither.
const h2ConstText = "The quick brown fox jumps over the lazy dog, then the quick brown fox jumps over the lazy dog again and again, until two hundred and fifty bytes or more have gone by: abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ!"

// TestSTDHASH_CompileTimeEqualsRunTime runs Sha1 and Sha384 (and their
// states) in the evaluator, as consts, and natively, and checks both
// against the oracle, for the long text, a 56-byte one (SHA-1's two-block
// padding) and a 112-byte one (SHA-384's).
func TestSTDHASH_CompileTimeEqualsRunTime(t *testing.T) {
	t.Parallel()
	texts := []string{h2ConstText, h2ConstText[:56], h2ConstText[:112]}
	var b strings.Builder
	b.WriteString(h2Prelude)
	for i, s := range texts {
		fmt.Fprintf(&b, "const T%d = %q\nconst A%d: [20]u8 = hash.Sha1(T%d.AsBytes())\nconst C%d: [48]u8 = hash.Sha384(T%d.AsBytes())\n", i, s, i, i, i, i)
	}
	b.WriteString(`
StateSha1(t: []u8): [20]u8 {
    var s := hash.Sha1State.New()
    s.Bytes(t[0..t.Len() / 3])
    s.Bytes(t[t.Len() / 3..t.Len()])
    return s.Sum()
}

StateSha384(t: []u8): [48]u8 {
    var s := hash.Sha384State.New()
    s.Bytes(t[0..t.Len() / 3])
    s.Bytes(t[t.Len() / 3..t.Len()])
    return s.Sum()
}

`)
	for i := range texts {
		fmt.Fprintf(&b, "const SA%d: [20]u8 = StateSha1(T%d.AsBytes())\nconst SC%d: [48]u8 = StateSha384(T%d.AsBytes())\n", i, i, i, i)
	}
	b.WriteString("\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	for i := range texts {
		fmt.Fprintf(&b, `    try Hex(mut out, A%[1]d[..])
    try Print(mut out, " ")
    try Hex(mut out, hash.Sha1(T%[1]d.AsBytes())[..])
    try Print(mut out, " ")
    try Hex(mut out, SA%[1]d[..])
    try Print(mut out, " ")
    try Hex(mut out, C%[1]d[..])
    try Print(mut out, " ")
    try Hex(mut out, hash.Sha384(T%[1]d.AsBytes())[..])
    try Print(mut out, " ")
    try Hex(mut out, SC%[1]d[..])
    try Print(mut out, "\n")
`, i)
	}
	b.WriteString("}\n")
	got := h2Run(t, nil, b.String())
	var want strings.Builder
	for _, s := range texts {
		a, c := sha1.Sum([]byte(s)), sha512.Sum384([]byte(s))
		ah, ch := hex.EncodeToString(a[:]), hex.EncodeToString(c[:])
		fmt.Fprintf(&want, "%s %s %s %s %s %s\n", ah, ah, ah, ch, ch, ch)
	}
	diffLines(t, got, want.String())
}

// TestSTDHASH_ZeroFilledStatesRejected: Sha1State carries an invariant and
// Sha384State holds a Sha512State, which carries one, so neither is
// zero-valid and Arena.Slice of one, which zero-fills (a state whose
// chaining value is not the IV), is E0701 at the call.
func TestSTDHASH_ZeroFilledStatesRejected(t *testing.T) {
	for _, st := range []string{"Sha1State", "Sha384State"} {
		t.Run(st, func(t *testing.T) {
			t.Parallel()
			src := `import std.hash

export Main(sys: &System): WriteError|OutOfMemory!void {
    s := try sys.arena.Slice[hash.` + st + `](1)
    var t := s[0]
    t.Bytes("abc".AsBytes())
    _ = t.Sum()
}
`
			_, out, status := h2Try(t, nil, src)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			if status == 0 || !strings.Contains(out, "E0701") || !strings.Contains(out, "not zero-valid") {
				t.Fatalf("Arena.Slice[hash.%s] built (exit %d), want E0701 not zero-valid:\n%s", st, status, out)
			}
		})
	}
}

// TestSTDHASH_CheckEveryTarget: a program calling every SHA function of
// std.hash (SHA-256 reaches std.simd.kernel) checks clean on both hosted
// targets, aarch64-linux (checked only) and x86_64-freestanding
// (std.hash is layer 0).
func TestSTDHASH_CheckEveryTarget(t *testing.T) {
	body := `    a := hash.Sha1("abc".AsBytes())
    b := hash.Sha384("abc".AsBytes())
    c := hash.Sha256("abc".AsBytes())
    var s := hash.Sha1State.New()
    s.Bytes(a[..])
    var u := hash.Sha384State.New()
    u.Bytes(b[..])
    var v := hash.Sha256State.New()
    v.Bytes(c[..])
    _ = s.Sum()
    _ = u.Sum()
    _ = v.Sum()
`
	hosted := "import std.hash\n\nexport Main(sys: &System): WriteError!void {\n" + body + "    var out := sys.Stdout()\n    try Print(mut out, \"done\\n\")\n}\n"
	free := "import std.hash\n\nexport extern MkEntry() {\n" + body + "}\n"
	for _, c := range []struct{ target, src string }{
		{"x86_64-windows", hosted}, {"x86_64-linux", hosted}, {"aarch64-linux", hosted}, {"x86_64-freestanding", free},
	} {
		t.Run(c.target, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			stdDir := copyStd(t, root, nil)
			if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, status := mkCmd(t, root, stdDir, "check", "--target", c.target, "main.mk"); status != 0 {
				t.Errorf("mk check --target %s: exit %d, want clean\n%s", c.target, status, out)
			}
		})
	}
}
