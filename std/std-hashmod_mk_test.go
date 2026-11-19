// Why: std.hash's Mk code must give the same digests natively, in release and
// at compile time as independent implementations do.
// What: TestSTDHASHMOD_DifferentialDev, DifferentialRelease,
// PublishedVectorsInMk and CompileTimeEqualsRunTime.
// How: one program prints digests of splitmix64 bytes of every length class;
// Go models and Go's hash packages compute the expected lines.

// The differential tests of std.hash: its Mk code, built natively (dev
// and release) and run at compile time, against the Go models of
// std-hashmod_model_test.go and Go's hash/crc32, crypto/sha256 and
// crypto/sha512, on splitmix64 bytes of every length class, the published
// BLAKE3 and SipHash-1-3 vectors, and seeded random splits of the
// streaming states.

package std

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// hashModCore is a fixture written to std/core in the test's copy of std:
// a HashKey with chosen words, which no public name makes (HashKey.New(io)
// is random). Nothing here is part of std.
const hashModCore = `// Fixture of the std-hashmod tests: a HashKey with chosen words.

export HashModKeyOf(k0: u64, k1: u64): HashKey {
    return HashKey(k0: k0, k1: k1)
}
`

// hashModSip is written to std/hash in the copy: TestKey, so the programs
// can run the public hash.Sip13 under the published vectors' key.
const hashModSip = `// Fixture of the std-hashmod tests: a key with chosen words for the
// published vectors.

import std.core

export TestKey(k0: u64, k1: u64): HashKey {
    return core.HashModKeyOf(k0, k1)
}
`

// hashModBuild builds src over a copy of std with the fixtures and returns
// the executable.
func hashModBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	exe, out, status := hashModTry(t, src, flags...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// hashModTry runs mk build of src over a copy of std with the fixtures and
// returns the executable path, mk's output and its exit status.
func hashModTry(t *testing.T, src string, flags ...string) (string, string, int) {
	t.Helper()
	root := t.TempDir()
	fixtures := map[string]string{
		"core/tests_hashmod.mk": hashModCore,
		"hash/tests_sip13.mk":   hashModSip,
	}
	var stdDir string
	if base := os.Getenv("MK_HASHMOD_BASE"); base != "" {
		// std from base (a snapshot of HEAD's std), std/hash from this
		// tree.
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(stdDir, "hash")); err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(filepath.Join(stdDir, "hash"), os.DirFS("hash")); err != nil {
			t.Fatal(err)
		}
		for p, text := range fixtures {
			if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		stdDir = copyStd(t, root, fixtures)
	}
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

// hashModPrelude is the Mk side's splitmix64, hex printer and hash line.
const hashModPrelude = `import std.hash

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

// Line prints every one-shot hash of b.
Line(out: mut Writer, tag: str, b: []u8): WriteError!void {
    n := b.Len()
    x := hash.Xx(b)
    y := hash.Xx64(b, Trunc[u64](n) *% 0x9E3779B97F4A7C15)
    c := hash.Crc32(b)
    s := hash.Sip13(b, hash.TestKey(0x0706050403020100, 0x0f0e0d0c0b0a0908))
    s2 := hash.Sip13(b, hash.TestKey(Trunc[u64](n), 0xFFFFFFFFFFFFFFFF))
    try Print(out, "{tag} {n} {x:x} {y:x} {c:x} {s:x} {s2:x} ", (tag: tag, n: n, x: x, y: y, c: c, s: s, s2: s2))
    a := hash.Sha256(b)
    try Hex(out, a[..])
    try Print(out, " ")
    d := hash.Sha512(b)
    try Hex(out, d[..])
    try Print(out, " ")
    e := hash.Blake3(b)
    try Hex(out, e[..])
    try Print(out, "\n")
}
`

const hashModMain = hashModPrelude + `
const Lengths: [20]int = [511, 512, 513, 1023, 1024, 1025, 2047, 2048, 2049, 3072, 3073, 4097, 5121, 8192, 8193, 16384, 16385, 31744, 65543, 102400]

// Pieces are the split sizes the streaming states take, chosen by r.
const Pieces: [16]int = [0, 1, 2, 3, 7, 55, 56, 63, 64, 65, 111, 127, 128, 129, 1024, 1025]

Stream(out: mut Writer, r: mut Rng, b: []u8): WriteError!void {
    var s2 := hash.Sha256State.New()
    var s5 := hash.Sha512State.New()
    var s3 := hash.Blake3State.New()
    var i := 0
    for i < b.Len() {
        var k := Pieces[Trunc[int](r.Next() % 16)]
        if k > b.Len() - i {
            k = b.Len() - i
        }
        s2.Bytes(b[i..i + k])
        s5.Bytes(b[i..i + k])
        s3.Bytes(b[i..i + k])
        i += k
    }
    n := b.Len()
    try Print(out, "S {n} ", (n: n))
    a := s2.Sum()
    try Hex(out, a[..])
    try Print(out, " ")
    d := s5.Sum()
    try Hex(out, d[..])
    try Print(out, " ")
    e := s3.Sum()
    try Hex(out, e[..])
    try Print(out, "\n")
}

// Fork hashes b's first half into a state, copies it, and finishes the
// copy with the second half and the original with nothing.
Fork(out: mut Writer, b: []u8): WriteError!void {
    h := b.Len() / 2
    var s := hash.Blake3State.New()
    s.Bytes(b[0..h])
    var t := s
    t.Bytes(b[h..b.Len()])
    x := t.Sum()
    y := s.Sum()
    var p := hash.Sha256State.New()
    p.Bytes(b[0..h])
    var q := p
    q.Bytes(b[h..b.Len()])
    u := q.Sum()
    v := p.Sum()
    try Print(out, "F ")
    try Hex(out, x[..])
    try Print(out, " ")
    try Hex(out, y[..])
    try Print(out, " ")
    try Hex(out, u[..])
    try Print(out, " ")
    try Hex(out, v[..])
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
    for k in 0..20 {
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
    vec := try sys.arena.Slice[u8](102400)
    for i in 0..102400 {
        vec[i] = Trunc[u8](i % 251)
    }
    for n in 0..64 {
        try Line(mut out, "V", vec[0..n])
    }
    for k in 0..20 {
        try Line(mut out, "V", vec[0..Lengths[k]])
    }
    var sr := Rng(s: 7)
    for n in 0..300 {
        try Stream(mut out, mut sr, buf[0..n])
    }
    for k in 0..20 {
        try Stream(mut out, mut sr, buf[0..Lengths[k]])
        try Stream(mut out, mut sr, buf[0..Lengths[k]])
    }
    try Fork(mut out, buf[0..5000])
    try Fork(mut out, buf[0..3])
}
`

// hmSplitmix is the Go side of the Mk program's Rng.
type hmSplitmix struct{ s uint64 }

func (r *hmSplitmix) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func (r *hmSplitmix) fill(b []byte) {
	for i := 0; i < len(b); {
		x := r.next()
		for k := 0; k < 8 && i < len(b); k++ {
			b[i] = byte(x >> (8 * k))
			i++
		}
	}
}

func hashModLine(tag string, b []byte) string {
	n := len(b)
	s2 := sha256.Sum256(b)
	s5 := sha512.Sum512(b)
	b3 := hmBlake3(b)
	return fmt.Sprintf("%s %d %x %x %x %x %x %s %s %s", tag, n, hmXxh3(b), hmXxh64(b, uint64(n)*0x9E3779B97F4A7C15),
		crc32.ChecksumIEEE(b), hmSip13Keyed(b, 0x0706050403020100, 0x0f0e0d0c0b0a0908), hmSip13Keyed(b, uint64(n), ^uint64(0)),
		hex.EncodeToString(s2[:]), hex.EncodeToString(s5[:]), hex.EncodeToString(b3[:]))
}

var hashModLengths = []int{511, 512, 513, 1023, 1024, 1025, 2047, 2048, 2049, 3072, 3073, 4097, 5121, 8192, 8193, 16384, 16385, 31744, 65543, 102400}

var hashModPieces = []int{0, 1, 2, 3, 7, 55, 56, 63, 64, 65, 111, 127, 128, 129, 1024, 1025}

func hashModStream(r *hmSplitmix, b []byte) string {
	// The pieces only decide the splits; the digest is the one-shot's. The
	// Go side draws the same numbers so the two runs stay in step.
	for i := 0; i < len(b); {
		k := hashModPieces[r.next()%16]
		k = min(k, len(b)-i)
		i += k
	}
	s2 := sha256.Sum256(b)
	s5 := sha512.Sum512(b)
	b3 := hmBlake3(b)
	return fmt.Sprintf("S %d %s %s %s", len(b), hex.EncodeToString(s2[:]), hex.EncodeToString(s5[:]), hex.EncodeToString(b3[:]))
}

func hashModFork(b []byte) string {
	h := len(b) / 2
	x, y := hmBlake3(b), hmBlake3(b[:h])
	u, v := sha256.Sum256(b), sha256.Sum256(b[:h])
	return fmt.Sprintf("F %x %x %x %x", x, y, u, v)
}

// hashModWant is the Go models' output for hashModMain.
func hashModWant() string {
	var lines []string
	r := hmSplitmix{1}
	buf := make([]byte, 102400+16)
	r.fill(buf)
	for n := 0; n <= 300; n++ {
		off := n % 8
		lines = append(lines, hashModLine("L", buf[off:off+n]))
	}
	for _, n := range hashModLengths {
		off := n % 8
		lines = append(lines, hashModLine("L", buf[off:off+n]))
	}
	lines = append(lines, hashModLine("Z", make([]byte, 1000)))
	ones := make([]byte, 1000)
	for i := range ones {
		ones[i] = 0xFF
	}
	lines = append(lines, hashModLine("O", ones))
	vec := hmB3Input(102400)
	for n := 0; n < 64; n++ {
		lines = append(lines, hashModLine("V", vec[:n]))
	}
	for _, n := range hashModLengths {
		lines = append(lines, hashModLine("V", vec[:n]))
	}
	sr := hmSplitmix{7}
	for n := 0; n < 300; n++ {
		lines = append(lines, hashModStream(&sr, buf[:n]))
	}
	for _, n := range hashModLengths {
		lines = append(lines, hashModStream(&sr, buf[:n]))
		lines = append(lines, hashModStream(&sr, buf[:n]))
	}
	lines = append(lines, hashModFork(buf[:5000]), hashModFork(buf[:3]))
	return strings.Join(lines, "\n") + "\n"
}

// hashModCompare reports the first differing lines of got and want.
func hashModCompare(t *testing.T, got, want string) {
	t.Helper()
	g := strings.Split(strings.ReplaceAll(got, "\r\n", "\n"), "\n")
	w := strings.Split(want, "\n")
	bad := 0
	for i := 0; i < max(len(g), len(w)); i++ {
		var a, b string
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if a != b {
			t.Errorf("line %d:\n got  %s\n want %s", i+1, a, b)
			if bad++; bad == 8 {
				t.Fatal("stopping after 8 differences")
			}
		}
	}
}

func hashModRun(t *testing.T, flags ...string) {
	exe := hashModBuild(t, hashModMain, flags...)
	got := hashModExec(t, exe)
	want := hashModWant()
	hashModCompare(t, got, want)
}

func hashModExec(t *testing.T, exe string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b, err := exec.CommandContext(ctx, exe).CombinedOutput()
	out := string(b)
	if err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, out)
	}
	return out
}

// TestSTDHASHMOD_DifferentialDev runs every std.hash function, one-shot and
// streaming, in a dev build against the Go models.
func TestSTDHASHMOD_DifferentialDev(t *testing.T) {
	hashModRun(t)
}

// TestSTDHASHMOD_DifferentialRelease is the same in a release build.
func TestSTDHASHMOD_DifferentialRelease(t *testing.T) {
	hashModRun(t, "-release")
}

// TestSTDHASHMOD_PublishedVectorsInMk checks the Mk code's lines for the
// published vector inputs against the published values themselves (BLAKE3's
// test_vectors.json, SipHash-1-3's 64 vectors), independently of the models.
func TestSTDHASHMOD_PublishedVectorsInMk(t *testing.T) {
	exe := hashModBuild(t, hashModMain)
	got := hashModExec(t, exe)
	b3 := map[int]string{}
	for _, v := range hmB3Vectors {
		b3[v.n] = v.hex
	}
	seen := 0
	for _, line := range strings.Split(strings.ReplaceAll(got, "\r\n", "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) != 10 || f[0] != "V" {
			continue
		}
		var n int
		fmt.Sscan(f[1], &n)
		if want, ok := b3[n]; ok {
			seen++
			if f[9] != want {
				t.Errorf("Blake3(len %d) = %s, want %s", n, f[9], want)
			}
		}
		if n < 64 {
			if want := fmt.Sprintf("%x", hmSip13Vectors[n]); f[5] != want {
				t.Errorf("Sip13(len %d) = %s, want %s", n, f[5], want)
			}
		}
	}
	if seen < 5 {
		t.Fatalf("only %d BLAKE3 vector lines in the output", seen)
	}
}

// hashModConst hashes a 230-byte string at compile time (const) and at run
// time, and prints whether each pair agrees, then the compile-time Xx.
const hashModConst = `import std.hash

const Text = "The quick brown fox jumps over the lazy dog, then the quick brown fox jumps over the lazy dog again and again, until two hundred and fifty bytes or more have gone by: abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ!"
const CX: u64 = hash.Xx(Text.AsBytes())
const CX64: u64 = hash.Xx64(Text.AsBytes(), 42)
const CC: u32 = hash.Crc32(Text.AsBytes())
const CS: [32]u8 = hash.Sha256(Text.AsBytes())
const CS5: [64]u8 = hash.Sha512(Text.AsBytes())
const CB: [32]u8 = hash.Blake3(Text.AsBytes())

Same(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    b := Text.AsBytes()
    x := hash.Xx(b) == CX
    y := hash.Xx64(b, 42) == CX64
    c := hash.Crc32(b) == CC
    s := Same(hash.Sha256(b)[..], CS[..])
    s5 := Same(hash.Sha512(b)[..], CS5[..])
    e := Same(hash.Blake3(b)[..], CB[..])
    n := b.Len()
    try Print(mut out, "{n} {x} {y} {c} {s} {s5} {e} {cx:x}\n", (n: n, x: x, y: y, c: c, s: s, s5: s5, e: e, cx: CX))
}
`

// TestSTDHASHMOD_CompileTimeEqualsRunTime runs every one-shot hash in the
// evaluator (a const) and natively, and checks both against the model.
func TestSTDHASHMOD_CompileTimeEqualsRunTime(t *testing.T) {
	exe := hashModBuild(t, hashModConst)
	got := strings.TrimSpace(hashModExec(t, exe))
	text := "The quick brown fox jumps over the lazy dog, then the quick brown fox jumps over the lazy dog again and again, until two hundred and fifty bytes or more have gone by: abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ!"
	want := fmt.Sprintf("%d true true true true true true %x", len(text), hmXxh3([]byte(text)))
	if got != want {
		t.Fatalf("got  %s, want %s", got, want)
	}
}
