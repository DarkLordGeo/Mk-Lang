// Why: AES and GHASH kernels built on CPU intrinsics must give the same bits
// as the reference algorithms, natively and in the compile-time evaluator.
// What: tests of std.raw's AesRound, AesLastRound and Clmul64 and of
// std.simd.kernel's AesEncryptBlocks, AesCtr and GhashBlocks.
// How: crypto/aes, a GCM inc32 counter mode and SP 800-38D's bitwise GHASH are
// the references; the CPU tests are forced true or false in a copy of std.

package std

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// The tests:
//
//   - The kernels, natively at dev and release, against the scalar
//     references: crypto/aes (FIPS 197's Cipher), a GCM inc32 counter mode
//     over it, and SP 800-38D's bitwise GHASH (algorithm 1), which is itself
//     checked against crypto/cipher's GCM tags and the GCM specification's
//     test cases 2 to 4; every key size, lengths around each loop's step,
//     and a counter that wraps.
//   - The kernels in the evaluator (the CPU test forced true in a copy of
//     std, so the intrinsics' evaluator semantics run them) against the same
//     references, bit for bit.
//   - The CPU-feature mask: with the tests forced false every kernel
//     reports false and writes nothing, on any machine.
//   - Each intrinsic, evaluator against native code, over FIPS 197's
//     vectors and random inputs.

const s8AesShim = `import std.simd.kernel

export Simd8AesBlocks(rk: []u8, src: []u8, dst: mut []u8): bool {
    return kernel.AesEncryptBlocks(rk, src, dst)
}

export Simd8AesCtr(rk: []u8, counter: mut [16]u8, src: []u8, dst: mut []u8): bool {
    return kernel.AesCtr(rk, counter, src, dst)
}

export Simd8GhashKey(h: &[16]u8, powers: mut [64]u8): bool {
    return kernel.GhashKey(h, powers)
}

export Simd8Ghash(powers: &[64]u8, y: mut [16]u8, data: []u8): bool {
    return kernel.GhashBlocks(powers, y, data)
}
`

// s8AesOn forces the kernels' CPU tests true (the evaluator's run), and
// s8AesOff false (the mask).
var (
	s8AesOn = []sdForce{
		{"simd/kernel/aes.mk", `simd.Cpu().Has("aes")`, "true", 2},
		{"simd/kernel/ghash.mk", `simd.Cpu().Has("aes")`, "true", 2},
		{"simd/kernel/ghash.mk", `simd.Cpu().Has("pclmul")`, "true", 2},
	}
	s8AesOff = []sdForce{
		{"simd/kernel/aes.mk", `simd.Cpu().Has("aes")`, "false", 2},
		{"simd/kernel/ghash.mk", `simd.Cpu().Has("aes")`, "false", 2},
		{"simd/kernel/ghash.mk", `simd.Cpu().Has("pclmul")`, "false", 2},
	}
)

// s8Expand is FIPS 197 section 5.2's KeyExpansion, as bytes.
func s8Expand(key []byte) []byte {
	sbox := s8Sbox()
	nk := len(key) / 4
	nr := nk + 6
	w := make([]byte, 16*(nr+1))
	copy(w, key)
	rcon := byte(1)
	for i := nk; i < 4*(nr+1); i++ {
		t := [4]byte{w[4*i-4], w[4*i-3], w[4*i-2], w[4*i-1]}
		if i%nk == 0 {
			t = [4]byte{sbox[t[1]] ^ rcon, sbox[t[2]], sbox[t[3]], sbox[t[0]]}
			rcon = s8GfMul(rcon, 2)
		} else if nk > 6 && i%nk == 4 {
			t = [4]byte{sbox[t[0]], sbox[t[1]], sbox[t[2]], sbox[t[3]]}
		}
		for j := 0; j < 4; j++ {
			w[4*i+j] = w[4*(i-nk)+j] ^ t[j]
		}
	}
	return w
}

func s8GfMul(a, b byte) byte {
	var p byte
	for ; b != 0; b >>= 1 {
		if b&1 != 0 {
			p ^= a
		}
		a = a<<1 ^ (a>>7)*0x1b
	}
	return p
}

func s8Sbox() (s [256]byte) {
	for x := 0; x < 256; x++ {
		inv := byte(0)
		for y := 1; y < 256 && x != 0; y++ {
			if s8GfMul(byte(x), byte(y)) == 1 {
				inv = byte(y)
				break
			}
		}
		r := func(n uint) byte { return inv<<n | inv>>(8-n) }
		s[x] = inv ^ r(1) ^ r(2) ^ r(3) ^ r(4) ^ 0x63
	}
	return
}

// s8Round is FIPS 197's Cipher round (mix) or last round.
func s8Round(s, k [16]byte, mix bool) [16]byte {
	sbox := s8Sbox()
	var t [16]byte
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			t[r+4*c] = sbox[s[r+4*((c+r)%4)]]
		}
	}
	if mix {
		for c := 0; c < 4; c++ {
			a := [4]byte{t[4*c], t[4*c+1], t[4*c+2], t[4*c+3]}
			for r := 0; r < 4; r++ {
				t[4*c+r] = s8GfMul(a[r], 2) ^ s8GfMul(a[(r+1)%4], 3) ^ a[(r+2)%4] ^ a[(r+3)%4]
			}
		}
	}
	for i := range t {
		t[i] ^= k[i]
	}
	return t
}

// s8Ctr is GCM's counter mode (inc32) over crypto/aes; it returns the
// output and the next counter block.
func s8Ctr(key []byte, ctr [16]byte, src []byte) ([]byte, [16]byte) {
	c, _ := aes.NewCipher(key)
	out := make([]byte, len(src))
	var ks [16]byte
	for i := 0; i < len(src); i += 16 {
		c.Encrypt(ks[:], ctr[:])
		for j := i; j < len(src) && j < i+16; j++ {
			out[j] = src[j] ^ ks[j-i]
		}
		x := uint32(ctr[12])<<24 | uint32(ctr[13])<<16 | uint32(ctr[14])<<8 | uint32(ctr[15])
		x++
		ctr[12], ctr[13], ctr[14], ctr[15] = byte(x>>24), byte(x>>16), byte(x>>8), byte(x)
	}
	return out, ctr
}

// s8GfMul128 is SP 800-38D section 6.3's algorithm 1, X * Y.
func s8GfMul128(x, y [16]byte) [16]byte {
	var z [16]byte
	v := y
	for i := 0; i < 128; i++ {
		if x[i/8]>>(7-i%8)&1 != 0 {
			for j := range z {
				z[j] ^= v[j]
			}
		}
		lsb := v[15] & 1
		for j := 15; j > 0; j-- {
			v[j] = v[j]>>1 | v[j-1]<<7
		}
		v[0] >>= 1
		if lsb != 0 {
			v[0] ^= 0xe1
		}
	}
	return z
}

// s8Ghash is GHASH over the whole blocks of data from y.
func s8Ghash(h, y [16]byte, data []byte) [16]byte {
	for i := 0; i+16 <= len(data); i += 16 {
		for j := 0; j < 16; j++ {
			y[j] ^= data[i+j]
		}
		y = s8GfMul128(y, h)
	}
	return y
}

// s8GcmTag is GCM's tag from the references: E(K, J0) ^ GHASH(H, A, C).
func s8GcmTag(key, iv, aad, ct []byte) []byte {
	c, _ := aes.NewCipher(key)
	var h, j0, s [16]byte
	c.Encrypt(h[:], h[:])
	copy(j0[:], iv)
	j0[15] = 1
	pad := func(b []byte) []byte { return append(append([]byte{}, b...), make([]byte, (16-len(b)%16)%16)...) }
	data := append(pad(aad), pad(ct)...)
	var l [16]byte
	for i := 0; i < 8; i++ {
		l[7-i] = byte(uint64(8*len(aad)) >> (8 * i))
		l[15-i] = byte(uint64(8*len(ct)) >> (8 * i))
	}
	g := s8Ghash(h, [16]byte{}, append(data, l[:]...))
	c.Encrypt(s[:], j0[:])
	for i := range s {
		s[i] ^= g[i]
	}
	return s[:]
}

// TestSimd_GhashReferenceChecked: the GHASH reference gives the GCM
// specification's test cases 2 to 4 (McGrew and Viega, 2005) and
// crypto/cipher's tags for random messages.
func TestSimd_GhashReferenceChecked(t *testing.T) {
	dec := func(s string) []byte { b, _ := hex.DecodeString(s); return b }
	k3 := dec("feffe9928665731c6d6a8f9467308308")
	iv3 := dec("cafebabefacedbaddecaf888")
	p4 := dec("d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39")
	cases := []struct{ key, iv, aad, pt []byte }{
		{make([]byte, 16), make([]byte, 12), nil, make([]byte, 16)},
		{k3, iv3, nil, dec("d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b391aafd255")},
		{k3, iv3, dec("feedfacedeadbeeffeedfacedeadbeefabaddad2"), p4},
	}
	tags := []string{"ab6e47d42cec13bdf53a67b21257bddf", "4d5c2af327cd64a62cf35abd2ba6fab4", "5bc94fbc3221a5db94fae95ae7121a47"}
	for i, c := range cases {
		b, _ := aes.NewCipher(c.key)
		g, _ := cipher.NewGCM(b)
		sealed := g.Seal(nil, c.iv, c.pt, c.aad)
		ct, tag := sealed[:len(c.pt)], sealed[len(c.pt):]
		if hex.EncodeToString(tag) != tags[i] {
			t.Fatalf("case %d: crypto/cipher tag %x, want %s", i+2, tag, tags[i])
		}
		if got := s8GcmTag(c.key, c.iv, c.aad, ct); hex.EncodeToString(got) != tags[i] {
			t.Fatalf("case %d: reference tag %x, want %s", i+2, got, tags[i])
		}
	}
	for n := 0; n < 200; n += 7 {
		key, iv, aad, pt := s8Bytes(uint64(n), 32), s8Bytes(uint64(n)+1, 12), s8Bytes(uint64(n)+2, n%37), s8Bytes(uint64(n)+3, n)
		b, _ := aes.NewCipher(key)
		g, _ := cipher.NewGCM(b)
		sealed := g.Seal(nil, iv, pt, aad)
		if got := s8GcmTag(key, iv, aad, sealed[:n]); !strings.EqualFold(hex.EncodeToString(got), hex.EncodeToString(sealed[n:])) {
			t.Fatalf("n %d: reference tag %x, crypto/cipher %x", n, got, sealed[n:])
		}
	}
}

func s8Fnv(h uint64, b []byte) uint64 {
	for _, c := range b {
		h = (h ^ uint64(c)) * 0x100000001b3
	}
	return h
}

// s8MkBytes is b as an Mk array literal.
func s8MkBytes(b []byte) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i, c := range b {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d", c)
	}
	sb.WriteString("]")
	return sb.String()
}

// s8AesKeys are the kernel tests' keys: FIPS 197 C.1 to C.3's and three
// generated, one per size.
func s8AesKeys() [][]byte {
	k := func(s string) []byte { b, _ := hex.DecodeString(s); return b }
	return [][]byte{
		k("000102030405060708090a0b0c0d0e0f"),
		k("000102030405060708090a0b0c0d0e0f1011121314151617"),
		k("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"),
		s8Bytes(11, 16), s8Bytes(12, 24), s8Bytes(13, 32),
	}
}

// s8AesCounters are the counter blocks, one per key; the last two wrap
// within the eight-block step.
func s8AesCounters() [][16]byte {
	var out [][16]byte
	for i := 0; i < 6; i++ {
		var c [16]byte
		copy(c[:], s8Bytes(uint64(20+i), 16))
		if i >= 4 {
			c[12], c[13], c[14], c[15] = 0xff, 0xff, 0xff, byte(0xfb+i)
		}
		out = append(out, c)
	}
	return out
}

// s8AesProgram runs the kernels over the keys and every length of lens; it
// prints, per key and length, ECB's and CTR's results and the counter
// after, and per length GHASH's result under the first two keys' H. When
// eval is set, a const runs the same through the evaluator first (lengths
// evalLens) and Main prints its lines before the run's.
func s8AesProgram(lens, evalLens []int) string {
	var sb strings.Builder
	sb.WriteString("import std.hash\nimport std.simd\n\n")
	keys, ctrs := s8AesKeys(), s8AesCounters()
	for i, k := range keys {
		fmt.Fprintf(&sb, "const RK%d: [%d]u8 = %s\n", i, len(s8Expand(k)), s8MkBytes(s8Expand(k)))
		fmt.Fprintf(&sb, "const CT%d: [16]u8 = %s\n", i, s8MkBytes(ctrs[i][:]))
	}
	for i := 0; i < 2; i++ {
		fmt.Fprintf(&sb, "const H%d: [16]u8 = %s\n", i, s8MkBytes(s8AesH(keys[i])))
	}
	lensMk := func(name string, ls []int) {
		fmt.Fprintf(&sb, "const %s: [%d]int = [", name, len(ls))
		for i, l := range ls {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%d", l)
		}
		sb.WriteString("]\n")
	}
	lensMk("Lens", lens)
	lensMk("EvalLens", evalLens)
	sb.WriteString(`
Fill(buf: mut []u8, seed: u64) {
    var x := seed
    for i in 0..buf.Len() {
        x = x *% 6364136223846793005 +% 1442695040888963407
        buf[i] = Cast[u8](x >> 56)
    }
}

Fnv(h: u64, s: []u8): u64 {
    var x := h
    for i in 0..s.Len() {
        x = (x ^ Cast[u64](s[i])) *% 0x100000001b3
    }
    return x
}

// Line folds one case's results into h.
Line(h: u64, kind: u64, k: int, n: int, ok: bool, x: u64, y: u64): u64 {
    var z := (h ^ kind) *% 0x100000001b3
    z = (z ^ Cast[u64](k)) *% 0x100000001b3
    z = (z ^ Cast[u64](n)) *% 0x100000001b3
    if ok {
        z = (z ^ 1) *% 0x100000001b3
    }
    z = (z ^ x) *% 0x100000001b3
    return (z ^ y) *% 0x100000001b3
}

// Cases runs the kernels over src with round keys rk and counter c (key
// k), folding ECB's and CTR's lines into h.
Cases(h: u64, k: int, rk: []u8, c0: [16]u8, src: []u8): u64 {
    n := src.Len()
    var dst: [600]u8 = [0; 600]
    ok := hash.Simd8AesBlocks(rk, src, mut dst[0..n])
    var z := Line(h, 1, k, n, ok, Fnv(0xcbf29ce484222325, dst[0..n]), 0)
    var dst2: [600]u8 = [0; 600]
    var c := c0
    ok2 := hash.Simd8AesCtr(rk, mut c, src, mut dst2[0..n])
    return Line(z, 2, k, n, ok2, Fnv(0xcbf29ce484222325, dst2[0..n]), Fnv(0xcbf29ce484222325, c[..]))
}

// Ghash runs GhashKey on hk, then GhashBlocks over src from y0 with its
// table (case k); ok is both reports.
Ghash(h: u64, k: int, hk: [16]u8, y0: [16]u8, src: []u8): u64 {
    var y := y0
    var powers: [64]u8 = [0; 64]
    ok1 := hash.Simd8GhashKey(&hk, mut powers)
    ok := hash.Simd8Ghash(&powers, mut y, src)
    return Line(h, 3, k, src.Len(), ok1 && ok, Fnv(0xcbf29ce484222325, y[..]), 0)
}

Run(lens: []int): u64 {
    var src: [600]u8 = [0; 600]
    var h: u64 = 0xcbf29ce484222325
    for li in 0..lens.Len() {
        n := lens[li]
        Fill(mut src[0..n], Cast[u64](n))
        h = Cases(h, 0, RK0[..], CT0, src[0..n])
        h = Cases(h, 1, RK1[..], CT1, src[0..n])
        h = Cases(h, 2, RK2[..], CT2, src[0..n])
        h = Cases(h, 3, RK3[..], CT3, src[0..n])
        h = Cases(h, 4, RK4[..], CT4, src[0..n])
        h = Cases(h, 5, RK5[..], CT5, src[0..n])
        h = Ghash(h, 0, H0, CT2, src[0..n])
        h = Ghash(h, 1, H1, CT3, src[0..n])
    }
    return h
}

const CE: u64 = Run(EvalLens[..])

export Main(sys: &System): WriteError!void {
    try sys.Print("eval {e}\n", (e: CE))
    if !simd.Cpu().Has("aes") || !simd.Cpu().Has("pclmul") {
        try sys.Print("skip\n", ())
        return
    }
    try sys.Print("run {r}\n", (r: Run(Lens[..])))
}
`)
	return sb.String()
}

// s8AesH is H = E(K, 0^128).
func s8AesH(key []byte) []byte {
	c, _ := aes.NewCipher(key)
	var h [16]byte
	c.Encrypt(h[:], h[:])
	return h[:]
}

// s8AesModel is Run's hash over lens by the references; on is false for
// the mask, where every kernel reports false and writes nothing.
func s8AesModel(lens []int, on bool) uint64 {
	keys, ctrs := s8AesKeys(), s8AesCounters()
	const off = 0xcbf29ce484222325
	line := func(h, kind uint64, k, n int, ok bool, x, y uint64) uint64 {
		z := (h ^ kind) * 0x100000001b3
		z = (z ^ uint64(k)) * 0x100000001b3
		z = (z ^ uint64(n)) * 0x100000001b3
		if ok {
			z = (z ^ 1) * 0x100000001b3
		}
		z = (z ^ x) * 0x100000001b3
		return (z ^ y) * 0x100000001b3
	}
	h := uint64(off)
	for _, n := range lens {
		src := s8Bytes(uint64(n), n)
		for k, key := range keys {
			ecb := make([]byte, n)
			if on {
				c, _ := aes.NewCipher(key)
				for i := 0; i+16 <= n; i += 16 {
					c.Encrypt(ecb[i:i+16], src[i:i+16])
				}
			}
			h = line(h, 1, k, n, on, s8Fnv(off, ecb), 0)
			ctr, next := make([]byte, n), ctrs[k]
			if on {
				ctr, next = s8Ctr(key, ctrs[k], src)
			}
			h = line(h, 2, k, n, on, s8Fnv(off, ctr), s8Fnv(off, next[:]))
		}
		for k := 0; k < 2; k++ {
			y := ctrs[k+2]
			if on {
				var hk [16]byte
				copy(hk[:], s8AesH(keys[k]))
				y = s8Ghash(hk, y, src)
			}
			h = line(h, 3, k, n, on, s8Fnv(off, y[:]), 0)
		}
	}
	return h
}

var (
	s8AesLens     = []int{0, 1, 15, 16, 17, 31, 32, 48, 63, 64, 65, 100, 127, 128, 129, 143, 144, 145, 255, 256, 257, 300, 383, 384, 511, 512, 513, 600}
	s8AesEvalLens = []int{0, 17, 64, 145}
)

// s8AesWant is the program's output when the evaluator's kernels report
// evalOn and the run's runOn.
func s8AesWant(evalOn, runOn bool) string {
	return fmt.Sprintf("eval %d\nrun %d\n", s8AesModel(s8AesEvalLens, evalOn), s8AesModel(s8AesLens, runOn))
}

// s8AesNeedsCPU skips when the machine lacks aes or pclmul (the program
// printed skip after its evaluator line, which got is checked against).
func s8AesNeedsCPU(t *testing.T, got, evalLine string) {
	t.Helper()
	if strings.HasSuffix(got, "skip\n") {
		if got != evalLine+"skip\n" {
			t.Fatalf("got %q, want %q then skip", got, evalLine)
		}
		t.Skip("this CPU lacks aes or pclmul; the evaluator half passed")
	}
}

// TestSimd_AesKernelsNative: the kernels natively at dev and release
// against the references; the evaluator's kernels report false (Cpu() is
// the baseline there).
func TestSimd_AesKernelsNative(t *testing.T) {
	t.Parallel()
	fx := s8Fixtures(t, s8AesShim, nil)
	src := s8AesProgram(s8AesLens, s8AesEvalLens)
	want := s8AesWant(false, true)
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			got := s8Run(t, fx, src, false, flags...)
			s8AesNeedsCPU(t, got, strings.SplitAfter(want, "\n")[0])
			diffLines(t, got, want)
		})
	}
}

// TestSimd_AesKernelsEvaluator: with the CPU tests forced true, the
// evaluator runs the kernels through the intrinsics' semantics and agrees
// with the references, as does the run.
func TestSimd_AesKernelsEvaluator(t *testing.T) {
	t.Parallel()
	want := s8AesWant(true, true)
	got := s8Run(t, s8Fixtures(t, s8AesShim, s8AesOn), s8AesProgram(s8AesLens, s8AesEvalLens), false)
	s8AesNeedsCPU(t, got, strings.SplitAfter(want, "\n")[0])
	diffLines(t, got, want)
}

// TestSimd_AesKernelsMasked: with the CPU tests forced false every kernel
// reports false and writes nothing, so the callers' portable paths run on
// any machine.
func TestSimd_AesKernelsMasked(t *testing.T) {
	t.Parallel()
	src := strings.Replace(s8AesProgram(s8AesLens, s8AesEvalLens), `!simd.Cpu().Has("aes") || !simd.Cpu().Has("pclmul")`, `!simd.Cpu().Has("sse2")`, 1)
	diffLines(t, s8Run(t, s8Fixtures(t, s8AesShim, s8AesOff), src, false, "--release"), s8AesWant(false, false))
}

// s8IntrinsicProgram runs AesRound, AesLastRound and Clmul64 over FIPS
// 197 C.1's first round and n random inputs, in cpu functions, and folds
// the results into one hash: a const runs it in the evaluator, Main at
// run time.
const s8AesIntrinsicProgram = `import std.raw
import std.simd

Round(s: [16]u8, k: [16]u8, last: bool): [16]u8 cpu "aes" {
    if last {
        return raw.AesLastRound(s, k)
    }
    return raw.AesRound(s, k)
}

Mul(a: u64, b: u64): (hi: u64, lo: u64) cpu "pclmul, aes" {
    return raw.Clmul64(a, b)
}

// Base and Wide call the intrinsics from functions without their features
// (inside unsafe): the calls go through the private helpers, from a
// baseline function and from one with 256-bit registers.
Base(s: [16]u8, k: [16]u8, a: u64, b: u64): (r: [16]u8, x: u64) {
    unsafe "called where Cpu() has aes and pclmul, or by the evaluator" {
        p := raw.Clmul64(a, b)
        return (r: raw.AesRound(s, k), x: p.hi ^ p.lo)
    }
}

Wide(s: [16]u8, k: [16]u8, a: u64, b: u64): (r: [16]u8, x: u64) cpu "avx2" {
    unsafe "called where Cpu() has aes, pclmul and avx2, or by the evaluator" {
        p := raw.Clmul64(a, b)
        return (r: raw.AesLastRound(s, k), x: p.hi ^ p.lo)
    }
}

Next(x: u64): u64 {
    return x *% 6364136223846793005 +% 1442695040888963407
}

Mix(h: u64, x: u64): u64 {
    return (h ^ x) *% 0x100000001b3
}

Run(n: int, z: u64): u64 {
    var s: [16]u8 = [0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0]
    var k: [16]u8 = [0xd6, 0xaa, 0x74, 0xfd, 0xd2, 0xaf, 0x72, 0xfa, 0xda, 0xa6, 0x78, 0xf1, 0xd6, 0xab, 0x76, 0xfe]
    var h: u64 = 0xcbf29ce484222325
    var x: u64 = 7 + z
    for _ in 0..n {
        var r: [16]u8 = [0; 16]
        var r2: [16]u8 = [0; 16]
        var p: (hi: u64, lo: u64) = (hi: 0, lo: 0)
        unsafe "Run is called by the evaluator or where Cpu() has aes and pclmul" {
            r = Round(s, k, false)
            r2 = Round(s, k, true)
            p = Mul(x, Next(x))
        }
        for j in 0..16 {
            h = Mix(h, Cast[u64](r[j]))
            h = Mix(h, Cast[u64](r2[j]))
        }
        h = Mix(Mix(h, p.hi), p.lo)
        b1 := Base(s, k, x, Next(x))
        unsafe "Wide runs where Cpu() has avx2, or in the evaluator" {
            w1 := Wide(s, k, x, Next(x))
            for j in 0..16 {
                h = Mix(h, Cast[u64](b1.r[j]))
                h = Mix(h, Cast[u64](w1.r[j]))
            }
            h = Mix(Mix(h, b1.x), w1.x)
        }
        for j in 0..16 {
            x = Next(x)
            s[j] = Cast[u8](x >> 56)
            x = Next(x)
            k[j] = Cast[u8](x >> 56)
        }
        x = Next(x)
    }
    return h
}

const CE: u64 = Run(300, 0)

export Main(sys: &System): WriteError!void {
    z := Cast[u64]((sys.Env("MK_SIMD8_ZERO") else { "" }).Len())
    if !simd.Cpu().Has("aes") || !simd.Cpu().Has("pclmul") || !simd.Cpu().Has("avx2") {
        try sys.Print("eval {e}\nskip\n", (e: CE))
        return
    }
    try sys.Print("eval {e}\nrun {r}\n", (e: CE, r: Run(300, z)))
}
`

// s8AesIntrinsicModel is Run(n)'s hash by the references.
func s8AesIntrinsicModel(n int) uint64 {
	s := [16]byte{0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0}
	k := [16]byte{0xd6, 0xaa, 0x74, 0xfd, 0xd2, 0xaf, 0x72, 0xfa, 0xda, 0xa6, 0x78, 0xf1, 0xd6, 0xab, 0x76, 0xfe}
	if r := s8Round(s, k, true); hex.EncodeToString(r[:]) != "89d810e8855ace682d1843d8cb128fe4" {
		panic("s8Round disagrees with FIPS 197 C.1")
	}
	next := func(x uint64) uint64 { return x*6364136223846793005 + 1442695040888963407 }
	mix := func(h, x uint64) uint64 { return (h ^ x) * 0x100000001b3 }
	h, x := uint64(0xcbf29ce484222325), uint64(7)
	for i := 0; i < n; i++ {
		// Round(s, k, false) is AesRound, the round with MixColumns.
		r, r2 := s8Round(s, k, true), s8Round(s, k, false)
		hi, lo := s8Clmul(x, next(x))
		for j := 0; j < 16; j++ {
			h = mix(h, uint64(r[j]))
			h = mix(h, uint64(r2[j]))
		}
		h = mix(mix(h, hi), lo)
		for j := 0; j < 16; j++ {
			h = mix(h, uint64(r[j]))
			h = mix(h, uint64(r2[j]))
		}
		h = mix(mix(h, hi^lo), hi^lo)
		for j := 0; j < 16; j++ {
			x = next(x)
			s[j] = byte(x >> 56)
			x = next(x)
			k[j] = byte(x >> 56)
		}
		x = next(x)
	}
	return h
}

// s8Clmul is the carry-less product by the definition.
func s8Clmul(a, b uint64) (hi, lo uint64) {
	for i := 0; i < 64; i++ {
		if b>>i&1 != 0 {
			lo ^= a << i
			if i > 0 {
				hi ^= a >> (64 - i)
			}
		}
	}
	return
}

// TestSimd_AesIntrinsicsEvalNative: each intrinsic in the evaluator and
// natively (dev and release) against the references, over FIPS 197 C.1's
// round and 300 random inputs.
func TestSimd_AesIntrinsicsEvalNative(t *testing.T) {
	t.Parallel()
	m := s8AesIntrinsicModel(300)
	want := fmt.Sprintf("eval %d\nrun %d\n", m, m)
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			got := s8Run(t, nil, s8AesIntrinsicProgram, true, flags...)
			s8AesNeedsCPU(t, got, fmt.Sprintf("eval %d\n", m))
			diffLines(t, got, want)
		})
	}
}

// TestSimd_AesKernelsCheckEveryTarget: the AES and GHASH kernels check
// clean on the hosted targets, on aarch64 (checked only) and on
// x86_64-freestanding, where they report false and hold no vector.
func TestSimd_AesKernelsCheckEveryTarget(t *testing.T) {
	t.Parallel()
	s8CheckTargets(t, s8AesShim, `    var b: [64]u8 = [0; 64]
    var c: [64]u8 = [0; 64]
    var y: [16]u8 = [0; 16]
    var k: [16]u8 = [0; 16]
    var rk: [176]u8 = [0; 176]
    var pw: [64]u8 = [0; 64]
    _ = hash.Simd8AesBlocks(rk[..], b[..], mut c[..])
    _ = hash.Simd8AesCtr(rk[..], mut y, b[..], mut c[..])
    _ = hash.Simd8GhashKey(&k, mut pw)
    _ = hash.Simd8Ghash(&pw, mut y, b[..])
`)
}

// TestSimd_AesIntrinsicsNeedTheirFeature: a call outside unsafe from a
// function without aes (or pclmul) is E1101 cpu-call.
func TestSimd_AesIntrinsicsNeedTheirFeature(t *testing.T) {
	t.Parallel()
	s8WantCpuCall(t, `import std.raw

F(s: [16]u8): [16]u8 {
    return raw.AesRound(s, s)
}

export Main(sys: &System): WriteError!void {
    x := F([0; 16])
    try sys.Print("{a}\n", (a: x[0]))
}
`, "aes")
	s8WantCpuCall(t, `import std.raw

F(a: u64): u64 cpu "aes" {
    return raw.Clmul64(a, a).lo
}

export Main(sys: &System): WriteError!void {
    unsafe "test" {
        try sys.Print("{a}\n", (a: F(3)))
    }
}
`, "pclmul")
}

// s8GcmCase is one GCM vector: key, 96-bit IV, aad, plaintext.
type s8GcmCase struct{ key, iv, aad, pt []byte }

// s8GcmCases are the GCM specification's test cases 2 to 4 (McGrew and
// Viega, 2005), whose tags TestSimd_GhashReferenceChecked pins, and test
// case 16's AES-256 shape (case 4's data under a 256-bit key).
func s8GcmCases() []s8GcmCase {
	dec := func(s string) []byte { b, _ := hex.DecodeString(s); return b }
	k3, iv3 := dec("feffe9928665731c6d6a8f9467308308"), dec("cafebabefacedbaddecaf888")
	p3 := dec("d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b391aafd255")
	aad := dec("feedfacedeadbeeffeedfacedeadbeefabaddad2")
	return []s8GcmCase{
		{make([]byte, 16), make([]byte, 12), nil, make([]byte, 16)},
		{k3, iv3, nil, p3},
		{k3, iv3, aad, p3[:60]},
		{append(append([]byte{}, k3...), k3...), iv3, aad, p3[:60]},
	}
}

// s8GcmProgram seals each case with the kernels alone, into one fixed
// buffer: H = E(K, 0), the GHASH table by GhashKey, the ciphertext by
// AesCtr from inc32(J0), GHASH over the padded aad, the padded ciphertext
// and the lengths, the tag E(K, J0) ^ GHASH; then FIPS 197 C.1 to C.3's
// plaintext block under their keys, and a last byte 1 when every kernel
// reported true. A const runs All in the evaluator (the NIST GCM vectors
// through the evaluator) and Main
// runs it natively when the CPU has aes and pclmul; each result prints as
// hex lines, the const's first.
func s8GcmProgram() string {
	var sb strings.Builder
	sb.WriteString("import std.hash\nimport std.simd\n\n")
	cases := s8GcmCases()
	off := 0
	var seals []string
	for i, c := range cases {
		fmt.Fprintf(&sb, "const RK%d: [%d]u8 = %s\n", i, len(s8Expand(c.key)), s8MkBytes(s8Expand(c.key)))
		j0 := append(append([]byte{}, c.iv...), 0, 0, 0, 1)
		fmt.Fprintf(&sb, "const J%d: [16]u8 = %s\n", i, s8MkBytes(j0))
		fmt.Fprintf(&sb, "const A%d: [%d]u8 = %s\n", i, len(c.aad), s8MkBytes(c.aad))
		fmt.Fprintf(&sb, "const P%d: [%d]u8 = %s\n", i, len(c.pt), s8MkBytes(c.pt))
		n := len(c.pt) + 16
		seals = append(seals, fmt.Sprintf("    ok = Seal(RK%d[..], J%d, A%d[..], P%d[..], mut out[%d..%d]) && ok\n", i, i, i, i, off, off+n))
		off += n
	}
	var lines []int
	for _, c := range cases {
		lines = append(lines, len(c.pt), 16)
	}
	for i, k := range s8AesKeys()[:3] {
		fmt.Fprintf(&sb, "const F%d: [%d]u8 = %s\n", i, len(s8Expand(k)), s8MkBytes(s8Expand(k)))
		seals = append(seals, fmt.Sprintf("    ok = Fips(F%d[..], mut out[%d..%d]) && ok\n", i, off, off+16))
		off += 16
		lines = append(lines, 16)
	}
	fmt.Fprintf(&sb, "const Size: int = %d\n", off+1)
	fmt.Fprintf(&sb, "const Lines: [%d]int = [", len(lines))
	for i, l := range lines {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%d", l)
	}
	sb.WriteString("]\n")
	sb.WriteString(`const Block: [16]u8 = [0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff]
const Digits: str = "0123456789abcdef"

// Seal writes one case's ciphertext and then its tag to out (pt.Len() +
// 16 bytes) and reports whether every kernel reported true.
Seal(rk: []u8, j0: [16]u8, aad: []u8, pt: []u8, out: mut []u8): bool {
    var h: [16]u8 = [0; 16]
    var zero: [16]u8 = [0; 16]
    var ok := hash.Simd8AesBlocks(rk, zero[..], mut h[..])
    var powers: [64]u8 = [0; 64]
    ok = hash.Simd8GhashKey(&h, mut powers) && ok
    var ctr := j0
    ctr[15] = 2
    n := pt.Len()
    ok = hash.Simd8AesCtr(rk, mut ctr, pt, mut out[0..n]) && ok
    var buf: [160]u8 = [0; 160]
    for i in 0..aad.Len() {
        buf[i] = aad[i]
    }
    a := (aad.Len() + 15) / 16 * 16
    for i in 0..n {
        buf[a + i] = out[i]
    }
    c := a + (n + 15) / 16 * 16
    abits := Cast[u64](aad.Len()) * 8
    cbits := Cast[u64](n) * 8
    for i in 0..8 {
        buf[c + 7 - i] = Cast[u8]((abits >> (8 * i)) & 0xFF)
        buf[c + 15 - i] = Cast[u8]((cbits >> (8 * i)) & 0xFF)
    }
    var y: [16]u8 = [0; 16]
    ok = hash.Simd8Ghash(&powers, mut y, buf[0..c + 16]) && ok
    var s: [16]u8 = [0; 16]
    ok = hash.Simd8AesBlocks(rk, j0[..], mut s[..]) && ok
    for i in 0..16 {
        out[n + i] = s[i] ^ y[i]
    }
    return ok
}

// Fips writes Block encrypted under rk to out.
Fips(rk: []u8, out: mut []u8): bool {
    return hash.Simd8AesBlocks(rk, Block[..], out)
}

All(): [Size]u8 {
    var out: [Size]u8 = [0; Size]
    var ok := true
`)
	for _, s := range seals {
		sb.WriteString(s)
	}
	sb.WriteString(`    if ok {
        out[Size - 1] = 1
    }
    return out
}

const CE: [Size]u8 = All()

Hex(sys: &System, b: []u8): WriteError!void {
    var line: [256]u8 = [0; 256]
    for i in 0..b.Len() {
        line[2 * i] = Digits.AsBytes()[Cast[int](b[i] >> 4)]
        line[2 * i + 1] = Digits.AsBytes()[Cast[int](b[i] & 15)]
    }
    line[2 * b.Len()] = 10
    var out := sys.Stdout()
    try out.Bytes(line[0..2 * b.Len() + 1])
    try out.Flush()
}

Show(sys: &System, b: []u8): WriteError!void {
    var o := 0
    for i in 0..Lines.Len() {
        try Hex(sys, b[o..o + Lines[i]])
        o += Lines[i]
    }
    try sys.Print("ok {k}\n", (k: b[Size - 1]))
}

export Main(sys: &System): WriteError!void {
    try sys.Print("eval\n", ())
    try Show(sys, CE[..])
    if !simd.Cpu().Has("aes") || !simd.Cpu().Has("pclmul") {
        try sys.Print("skip\n", ())
        return
    }
    try sys.Print("run\n", ())
    r := All()
    try Show(sys, r[..])
}
`)
	return sb.String()
}

// TestSimd_AesKernelsGcmAndFipsVectors: the GCM cases sealed by the
// kernels alone give crypto/cipher's ciphertexts and tags (cases 2 to 4's
// tags are the specification's), and FIPS 197 C.1 to C.3's blocks their
// ciphertexts, with every kernel reporting true: in the evaluator (a const,
// the CPU tests forced true, so it runs AesRound, AesLastRound and Clmul64's
// semantics) and natively at dev and release.
// The native half skips on a CPU without aes or pclmul; the evaluator half
// is checked first.
func TestSimd_AesKernelsGcmAndFipsVectors(t *testing.T) {
	t.Parallel()
	var want strings.Builder
	for _, c := range s8GcmCases() {
		b, _ := aes.NewCipher(c.key)
		g, _ := cipher.NewGCM(b)
		sealed := g.Seal(nil, c.iv, c.pt, c.aad)
		fmt.Fprintf(&want, "%x\n%x\n", sealed[:len(c.pt)], sealed[len(c.pt):])
	}
	want.WriteString("69c4e0d86a7b0430d8cdb78070b4c55a\ndda97ca4864cdfe06eaf70a0ec0d7191\n8ea2b7ca516745bfeafc49904b496089\nok 1\n")
	if !strings.Contains(want.String(), "ab6e47d42cec13bdf53a67b21257bddf\n") {
		t.Fatalf("case 2's tag is not the specification's:\n%s", want.String())
	}
	evalPart := "eval\n" + want.String()
	fx := s8Fixtures(t, s8AesShim, s8AesOn)
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			got := s8Run(t, fx, s8GcmProgram(), false, flags...)
			if strings.HasSuffix(got, "skip\n") {
				diffLines(t, got, evalPart+"skip\n")
				t.Skip("this CPU lacks aes or pclmul; the evaluator half passed")
			}
			diffLines(t, got, evalPart+"run\n"+want.String())
		})
	}
}

// TestSimd_AesGeneralRegsOnlyNeedsSse2: on x86_64-freestanding, whose
// bins are general-regs-only, a call of an AES or carry-less multiply
// intrinsic from a context without sse2 is E1106 needs sse2, whether its
// function's clause names the feature without sse2 (once an LLVM backend
// crash) or it is called inside unsafe from a function with no clause (once
// a helper with SSE code in the bin), also when
// named as a fn value there.
func TestSimd_AesGeneralRegsOnlyNeedsSse2(t *testing.T) {
	t.Parallel()
	t.Run("clause", func(t *testing.T) {
		t.Parallel()
		s8GroWantNeeds(t, `import std.raw

F(s: [16]u8, k: [16]u8): [16]u8 cpu "aes" {
    return raw.AesRound(s, k)
}

G(a: u64, b: u64): u64 cpu "pclmul, aes" {
    r := raw.Clmul64(a, b)
    return r.hi ^ r.lo
}

export extern MkEntry(): u64 {
    var s: [16]u8 = [1; 16]
    var x: u64 = 0
    unsafe "probe" {
        s = F(s, s)
        x = G(3, 5)
    }
    return Cast[u64](s[0]) ^ x
}
`, 2)
	})
	t.Run("unsafe", func(t *testing.T) {
		t.Parallel()
		s8GroWantNeeds(t, `import std.raw

export extern MkEntry(): u64 {
    var s: [16]u8 = [1; 16]
    var x: u64 = 0
    unsafe "probe" {
        s = raw.AesRound(s, s)
        s = raw.AesLastRound(s, s)
        x = raw.Clmul64(3, 5).lo
        f := raw.AesRound
        s = f(s, s)
    }
    return Cast[u64](s[0]) ^ x
}
`, 4)
	})
}

// TestSimd_AesGeneralRegsOnlyBuild: in a general-regs-only bin the
// intrinsics build, at dev and release, from functions whose clause holds
// sse2 (inline) and through the private helper from a `cpu "sse2"`
// function inside unsafe, and only those functions and the helpers hold an
// SSE register; MkEntry, with no clause, holds none.
func TestSimd_AesGeneralRegsOnlyBuild(t *testing.T) {
	t.Parallel()
	src := `import std.raw

F(s: [16]u8, k: [16]u8): [16]u8 cpu "sse2, aes" {
    return raw.AesRound(s, k)
}

G(a: u64, b: u64): u64 cpu "sse2, pclmul, aes" {
    r := raw.Clmul64(a, b)
    return r.hi ^ r.lo
}

H(s: [16]u8, k: [16]u8): [16]u8 cpu "sse2" {
    unsafe "probe: the caller's Cpu() test" {
        return raw.AesLastRound(s, k)
    }
}

export extern MkEntry(): u64 {
    var s: [16]u8 = [1; 16]
    var x: u64 = 0
    unsafe "probe" {
        s = F(s, s)
        s = H(s, s)
        x = G(3, 5)
    }
    return Cast[u64](s[0]) ^ x
}
`
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			fns := s8GroXmm(t, src, flags...)
			s8GroOnlyIn(t, fns, "aesenc", ".F", ".G", ".H")
			s8GroOnlyIn(t, fns, "aesenclast", ".F", ".G", ".H")
			s8GroOnlyIn(t, fns, "pclmulqdq", ".F", ".G", ".H")
		})
	}
}

// s8GhashFnsProgram calls GhashKey once and GhashBlocks twice.
const s8GhashFnsProgram = `import std.hash

export Main(sys: &System): WriteError!void {
    var h: [16]u8 = [7; 16]
    var pw: [64]u8 = [0; 64]
    var y: [16]u8 = [0; 16]
    var b: [256]u8 = [3; 256]
    k := hash.Simd8GhashKey(&h, mut pw)
    n := (sys.Env("MK_SIMD8_N") else { "" }).Len() % 3 + 1
    g := hash.Simd8Ghash(&pw, mut y, b[0..n * 64])
    g2 := hash.Simd8Ghash(&pw, mut y, b[0..n * 80])
    try sys.Print("{k} {g} {g2} {y}\n", (k: k, g: g, g2: g2, y: y[0]))
}
`

// TestSimd_GhashBlocksNoSetup: GhashBlocks takes the key's table
// (GhashKey's H .. H^4) instead of computing H^2 .. H^4 on every call
// (which would be nine carry-less products and three reductions of setup
// per call). Counted statically in the release disassembly
// (x86_64-linux, built, not run): the functions that hold GhashBlocks'
// work have at most the four-block step's 12 pclmulqdq and the single
// block's 3, and the setup's 9 are in GhashKey's functions. This counts
// instructions in the code, not instructions executed per call.
func TestSimd_GhashBlocksNoSetup(t *testing.T) {
	t.Parallel()
	objdump := cryptoObjdump()
	if objdump == "" {
		t.Skip("needs llvm-objdump (LLVM 21)")
	}
	exe, out, status := s8Try(t, s8Fixtures(t, s8AesShim, nil), s8GhashFnsProgram, false, "--release", "--debug", "-target", "x86_64-linux")
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	dis, err := exec.Command(objdump, "-d", "--no-show-raw-insn", exe).Output()
	if err != nil {
		t.Fatalf("%s -d: %v", objdump, err)
	}
	per := map[string]int{}
	for _, x := range ctParse(string(dis)) {
		if x.op == "pclmulqdq" {
			per[x.fn]++
		}
	}
	blocks, key := 0, 0
	for fn, n := range per {
		switch {
		case strings.Contains(fn, "GhashKey") || strings.Contains(fn, "KeyNi"):
			key += n
		case strings.Contains(fn, "Ghash"):
			blocks += n
		default:
			t.Errorf("pclmulqdq in %s (%d), outside the GHASH functions: count it", fn, n)
		}
	}
	t.Logf("pclmulqdq per function: %v", per)
	if blocks == 0 || blocks > 15 || key == 0 {
		t.Fatalf("pclmulqdq per function %v: GhashBlocks' %d, want 1 to 15 (12 + 3, no setup); GhashKey's %d, want some", per, blocks, key)
	}
}
