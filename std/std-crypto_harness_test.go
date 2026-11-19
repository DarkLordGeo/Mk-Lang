// Why: crypto tests need std's private functions and many Mk programs, built
// and run the same way.
// What: cryptoFixture, cryptoStdDir, cryptoRun, cryptoCompare, cryptoPrelude
// and the splitmix64 generator shared by the std.crypto tests.
// How: builds over a copy of std with the fixtures; MK_CRYPTO_BASE names a std
// snapshot to build over, with std/crypto taken from this tree.

package std

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cryptoFixture is written to std/crypto in the test's copy of std. It
// exports the private functions the vector and model tests reach; nothing
// here is part of std.
const cryptoFixture = `// Fixture of the std-crypto tests: private functions, exported.

export FxPolyMac(key: []u8, msg: []u8): [16]u8 {
    return PolyMac(key, msg)
}

export FxChaBlock(key: &[32]u8, nonce: &[12]u8, ctr: u32): [64]u8 {
    k := ChaKeyOf(key, nonce)
    w := ChaBlock(&k, ctr)
    var out: [64]u8 = [0; 64]
    for i in 0..16 {
        PutLe32(mut out[..], 4 * i, w[i])
    }
    return out
}

export FxChaXor(key: &[32]u8, nonce: &[12]u8, ctr: u32, src: []u8, dst: mut []u8) {
    k := ChaKeyOf(key, nonce)
    ChaXor(&k, ctr, src, mut dst[..])
}

// FxAeadLenOk is Seal's and Open's length limit (CRY-10).
export FxAeadLenOk(n: int): bool {
    return AeadLenOk(n)
}

FxPut(out: mut []u8, at: int, b: [32]u8) {
    for i in 0..32 {
        out[at + i] = b[i]
    }
}

// FxFeOps: add, sub, mul, square, neg, invert, a * 121665 and a^(2^252 - 3)
// of the two elements, each canonical, 32 bytes apiece.
export FxFeOps(a: &[32]u8, b: &[32]u8): [256]u8 {
    x := FeFromBytes(a)
    y := FeFromBytes(b)
    var out: [256]u8 = [0; 256]
    FxPut(mut out[..], 0, FeBytes(FeAdd(x, y)))
    FxPut(mut out[..], 32, FeBytes(FeSub(x, y)))
    FxPut(mut out[..], 64, FeBytes(FeMul(x, y)))
    FxPut(mut out[..], 96, FeBytes(FeSquare(x)))
    FxPut(mut out[..], 128, FeBytes(FeNeg(x)))
    FxPut(mut out[..], 160, FeBytes(FeInvert(x)))
    FxPut(mut out[..], 192, FeBytes(FeMulSmall(x, 121665)))
    FxPut(mut out[..], 224, FeBytes(FePow22523(x)))
    return out
}

// FxFeFlags: IsNegative(a), IsZero(a) & 1, Eq(a, b) & 1 as bits 0, 1, 2.
export FxFeFlags(a: &[32]u8, b: &[32]u8): u64 {
    x := FeFromBytes(a)
    y := FeFromBytes(b)
    return FeIsNegative(x) | ((FeIsZero(x) & 1) << 1) | ((FeEq(x, y) & 1) << 2)
}

// FxFeChain runs n rounds of operations on lightly reduced elements, with
// no canonical step in between, so the limb bounds are what the ladder
// sees.
export FxFeChain(a: &[32]u8, b: &[32]u8, n: int): [32]u8 {
    var x := FeFromBytes(a)
    var y := FeFromBytes(b)
    for _ in 0..n {
        x = FeMul(x, y)
        y = FeAdd(x, y)
        x = FeSub(x, FeSquare(y))
        y = FeMulSmall(FeNeg(y), 121665)
        x = FeAdd(FeAdd(x, x), y)
    }
    return FeBytes(FeMul(x, y))
}

export FxScReduce64(h: &[64]u8): [32]u8 {
    return ScReduce64(h)
}

export FxScMulAdd(a: &[32]u8, b: &[32]u8, c: &[32]u8): [32]u8 {
    return ScMulAdd(a, b, c)
}

export FxScCanonical(s: &[32]u8): bool {
    return ScCanonical(s)
}

export FxBaseMult(s: &[32]u8): [32]u8 {
    p := PtBaseMult(s)
    return PtBytes(&p)
}

export FxDecode(b: &[32]u8): ?[32]u8 {
    p := PtFromBytes(b) else {
        return none
    }
    return some(PtBytes(&p))
}

export FxDoubleMult(a: &[32]u8, pa: &[32]u8, b: &[32]u8): ?[32]u8 {
    p := PtFromBytes(pa) else {
        return none
    }
    r := PtDoubleMult(a, &p, b)
    return some(PtBytes(&r))
}
`

// cryptoStdDir builds the std directory a test's mk uses: the live tree
// (copyStd), or MK_CRYPTO_BASE with this tree's std/crypto, plus the
// fixtures.
func cryptoStdDir(t *testing.T, root string, fixtures map[string]string) string {
	t.Helper()
	all := map[string]string{"crypto/tests_cryptofx.mk": cryptoFixture}
	for k, v := range fixtures {
		all[k] = v
	}
	base := os.Getenv("MK_CRYPTO_BASE")
	if base == "" {
		return copyStd(t, root, all)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "crypto")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "crypto"), os.DirFS("crypto")); err != nil {
		t.Fatal(err)
	}
	for p, text := range all {
		if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}

// cryptoTry runs mk build of src over the crypto std and returns the
// executable, mk's output and its exit status.
func cryptoTry(t *testing.T, src string, flags ...string) (string, string, int) {
	t.Helper()
	root := t.TempDir()
	stdDir := cryptoStdDir(t, root, nil)
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

// cryptoRun builds src (dev, or with flags) and returns its standard
// output; a failed build or a non-zero exit fails the test, a missing
// toolchain skips it.
func cryptoRun(t *testing.T, src string, flags ...string) string {
	t.Helper()
	exe, out, status := cryptoTry(t, src, flags...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build %v: exit %d\n%s", flags, status, out)
	}
	r := runNative(t, exe, "")
	if r.code != 0 {
		t.Fatalf("program exit %d\nstderr: %s\nstdout (tail): %s", r.code, r.stderr, cryptoTail(r.stdout, 2000))
	}
	return strings.ReplaceAll(r.stdout, "\r\n", "\n")
}

// cryptoModes are the builds every differential runs: dev and release.
var cryptoModes = [][]string{nil, {"--release"}}

func cryptoTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// cryptoCompare reports the first differing lines of got and want.
func cryptoCompare(t *testing.T, what, got, want string) {
	t.Helper()
	g := strings.Split(strings.TrimRight(got, "\n"), "\n")
	w := strings.Split(strings.TrimRight(want, "\n"), "\n")
	bad := 0
	for i := 0; i < max(len(g), len(w)); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Errorf("%s line %d:\n got  %s\n want %s", what, i+1, gl, wl)
			bad++
			if bad == 5 {
				t.Fatalf("%s: stopping after 5 differences (%d lines got, %d want)", what, len(g), len(w))
			}
		}
	}
}

// cryptoUnhex decodes a test vector, failing the test on a typo.
func cryptoUnhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// cryptoPrelude is the Mk side's splitmix64, hex printer and hex reader.
const cryptoPrelude = `import std.crypto

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

Nib(c: u8): u8 {
    if c >= 97 {
        return c - 87
    }
    return c - 48
}

// Unhex decodes s (lower-case hex) into out, which has half its length.
Unhex(s: str, out: mut []u8) {
    b := s.AsBytes()
    for i in 0..out.Len() {
        out[i] = (Nib(b[2 * i]) << 4) | Nib(b[2 * i + 1])
    }
}

Arr32(b: []u8): [32]u8 {
    var k: [32]u8 = [0; 32]
    for i in 0..32 {
        k[i] = b[i]
    }
    return k
}

Arr12(b: []u8): [12]u8 {
    var k: [12]u8 = [0; 12]
    for i in 0..12 {
        k[i] = b[i]
    }
    return k
}
`

// cryptoSplitmix is the Go side of the prelude's Rng.
type cryptoSplitmix struct{ s uint64 }

func (r *cryptoSplitmix) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func (r *cryptoSplitmix) fill(b []byte) {
	for i := 0; i < len(b); {
		x := r.next()
		for k := 0; k < 8 && i < len(b); k++ {
			b[i] = byte(x >> (8 * k))
			i++
		}
	}
}
