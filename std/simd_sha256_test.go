// Why: the SHA-256 kernel built on CPU intrinsics must give crypto/sha256's
// digests, natively, in the compile-time evaluator, and masked off.
// What: tests of std.raw's Sha256Rounds4 and Sha256Schedule4 and of
// std.simd.kernel's Sha256Blocks (TestSimd_Sha*).
// How: messages padded in Mk, digests folded into one hash and compared with
// Go; the CPU tests are forced true or false in a copy of std.

package std

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/bits"
	"strings"
	"testing"
)

// The tests:
//
//   - The kernel natively at dev and release over FIPS 180-4's examples
//     ("abc", the 448-bit two-block message) and generated messages of
//     every length to 300 and several longer, padded in Mk, against
//     crypto/sha256; in the evaluator with its CPU test forced true; and
//     masked (forced false), where it reports false and writes nothing.
//   - Each intrinsic, evaluator against native code against the Go
//     definitions, over random inputs; the Go definitions themselves give
//     crypto/sha256's digests.

const s8ShaShim = `import std.simd.kernel

export Simd8Sha256Blocks(h: mut [8]u32, data: []u8): bool {
    return kernel.Sha256Blocks(h, data)
}
`

var (
	s8ShaOn  = []sdForce{{"simd/kernel/sha256.mk", `simd.Cpu().Has("sha")`, "true", 1}}
	s8ShaOff = []sdForce{{"simd/kernel/sha256.mk", `simd.Cpu().Has("sha")`, "false", 1}}
)

// s8ShaProgram hashes the two FIPS 180-4 examples and generated messages
// of each length of lens (from s8Bytes(n, n)) through the kernel, padding
// in Mk, and folds every digest, and whether the kernel ran, into one
// hash; a const does the same over evalLens in the evaluator.
func s8ShaProgram(lens, evalLens []int) string {
	list := func(ls []int) string {
		var sb strings.Builder
		for i, l := range ls {
			if i > 0 {
				sb.WriteString(", ")
			}
			fmt.Fprintf(&sb, "%d", l)
		}
		return sb.String()
	}
	return fmt.Sprintf(`import std.hash
import std.simd

const Lens: [%d]int = [%s]
const EvalLens: [%d]int = [%s]
const Abc: str = "abc"
const Two: str = "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"

Fill(buf: mut []u8, seed: u64) {
    var x := seed
    for i in 0..buf.Len() {
        x = x *%% 6364136223846793005 +%% 1442695040888963407
        buf[i] = Cast[u8](x >> 56)
    }
}

Mix(h: u64, x: u64): u64 {
    return (h ^ x) *%% 0x100000001b3
}

// Digest pads msg[0..n] (FIPS 180-4 section 5.1.1) in buf and compresses
// it through the kernel from the initial value, folding the words and
// whether the kernel ran into h.
Digest(h: u64, buf: mut []u8, n: int): u64 {
    var m := n + 1
    for m %% 64 != 56 {
        m += 1
    }
    buf[n] = 0x80
    for i in n + 1..m {
        buf[i] = 0
    }
    bit_len := Cast[u64](n) * 8
    for i in 0..8 {
        buf[m + i] = Cast[u8]((bit_len >> (56 - 8 * i)) & 0xFF)
    }
    var st: [8]u32 = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]
    ok := hash.Simd8Sha256Blocks(mut st, buf[0..m + 8])
    var z := h
    if ok {
        z = Mix(z, 1)
    }
    for i in 0..8 {
        z = Mix(z, Cast[u64](st[i]))
    }
    return z
}

Run(lens: []int): u64 {
    var buf: [1200]u8 = [0; 1200]
    var h: u64 = 0xcbf29ce484222325
    for i in 0..3 {
        buf[i] = Abc.AsBytes()[i]
    }
    h = Digest(h, mut buf[..], 3)
    for i in 0..56 {
        buf[i] = Two.AsBytes()[i]
    }
    h = Digest(h, mut buf[..], 56)
    for li in 0..lens.Len() {
        n := lens[li]
        Fill(mut buf[0..n], Cast[u64](n))
        h = Digest(h, mut buf[..], n)
    }
    return h
}

const CE: u64 = Run(EvalLens[..])

export Main(sys: &System): WriteError!void {
    try sys.Print("eval {e}\n", (e: CE))
    if !simd.Cpu().Has("sha") {
        try sys.Print("skip\n", ())
        return
    }
    try sys.Print("run {r}\n", (r: Run(Lens[..])))
}
`, len(lens), list(lens), len(evalLens), list(evalLens))
}

// s8ShaModel is Run's hash by crypto/sha256 (on), or with the kernel
// reporting false and the state left at the initial value (off).
func s8ShaModel(lens []int, on bool) uint64 {
	mix := func(h, x uint64) uint64 { return (h ^ x) * 0x100000001b3 }
	digest := func(h uint64, msg []byte) uint64 {
		st := []uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}
		if on {
			h = mix(h, 1)
			d := sha256.Sum256(msg)
			for i := range st {
				st[i] = binary.BigEndian.Uint32(d[4*i:])
			}
		}
		for _, x := range st {
			h = mix(h, uint64(x))
		}
		return h
	}
	h := digest(0xcbf29ce484222325, []byte("abc"))
	h = digest(h, []byte("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"))
	for _, n := range lens {
		h = digest(h, s8Bytes(uint64(n), n))
	}
	return h
}

var (
	s8ShaLens     = append(s8Range(0, 300), 447, 448, 511, 512, 513, 1000, 1119)
	s8ShaEvalLens = []int{0, 55, 56, 64, 119, 200}
)

func s8Range(a, b int) []int {
	var out []int
	for i := a; i <= b; i++ {
		out = append(out, i)
	}
	return out
}

func s8ShaNeedsCPU(t *testing.T, got, evalLine string) {
	t.Helper()
	if strings.HasSuffix(got, "skip\n") {
		if got != evalLine+"skip\n" {
			t.Fatalf("got %q, want %q then skip", got, evalLine)
		}
		t.Skip("this CPU lacks sha; the evaluator half passed")
	}
}

// TestSimd_ShaKernelNative: the kernel natively at dev and release against
// crypto/sha256; the evaluator's kernel reports false.
func TestSimd_ShaKernelNative(t *testing.T) {
	t.Parallel()
	fx := s8Fixtures(t, s8ShaShim, nil)
	src := s8ShaProgram(s8ShaLens, s8ShaEvalLens)
	want := fmt.Sprintf("eval %d\nrun %d\n", s8ShaModel(s8ShaEvalLens, false), s8ShaModel(s8ShaLens, true))
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			got := s8Run(t, fx, src, false, flags...)
			s8ShaNeedsCPU(t, got, strings.SplitAfter(want, "\n")[0])
			diffLines(t, got, want)
		})
	}
}

// TestSimd_ShaKernelEvaluator: with the CPU test forced true the
// evaluator runs the kernel through the intrinsics' semantics.
func TestSimd_ShaKernelEvaluator(t *testing.T) {
	t.Parallel()
	want := fmt.Sprintf("eval %d\nrun %d\n", s8ShaModel(s8ShaEvalLens, true), s8ShaModel(s8ShaLens, true))
	got := s8Run(t, s8Fixtures(t, s8ShaShim, s8ShaOn), s8ShaProgram(s8ShaLens, s8ShaEvalLens), false)
	s8ShaNeedsCPU(t, got, strings.SplitAfter(want, "\n")[0])
	diffLines(t, got, want)
}

// TestSimd_ShaKernelMasked: forced false, the kernel reports false and
// leaves the state, on any machine.
func TestSimd_ShaKernelMasked(t *testing.T) {
	t.Parallel()
	src := strings.Replace(s8ShaProgram(s8ShaLens, s8ShaEvalLens), `!simd.Cpu().Has("sha")`, `!simd.Cpu().Has("sse2")`, 1)
	want := fmt.Sprintf("eval %d\nrun %d\n", s8ShaModel(s8ShaEvalLens, false), s8ShaModel(s8ShaLens, false))
	diffLines(t, s8Run(t, s8Fixtures(t, s8ShaShim, s8ShaOff), src, false, "--release"), want)
}

// s8ShaRounds4 and s8ShaSchedule4 are the intrinsics' definitions (FIPS
// 180-4 section 6.2.2), s as (f, e, b, a, h, g, d, c).
func s8ShaRounds4(s [8]uint32, wk [4]uint32) [8]uint32 {
	f, e, b, a, h, g, d, c := s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7]
	for _, x := range wk {
		t1 := h + (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11) ^ bits.RotateLeft32(e, -25)) + (e&f ^ ^e&g) + x
		t2 := (bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)) + (a&b ^ a&c ^ b&c)
		h, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	return [8]uint32{f, e, b, a, h, g, d, c}
}

func s8ShaSchedule4(w [16]uint32) [4]uint32 {
	var out [4]uint32
	ext := append(w[:0:0], w[:]...)
	for t := 16; t < 20; t++ {
		x, y := ext[t-15], ext[t-2]
		ext = append(ext, ext[t-16]+(bits.RotateLeft32(x, -7)^bits.RotateLeft32(x, -18)^x>>3)+ext[t-7]+(bits.RotateLeft32(y, -17)^bits.RotateLeft32(y, -19)^y>>10))
	}
	copy(out[:], ext[16:])
	return out
}

// TestSimd_ShaDefinitionsChecked: the Go definitions, composed into
// FIPS 180-4's compression, give crypto/sha256's digests.
func TestSimd_ShaDefinitionsChecked(t *testing.T) {
	k := s8ShaK()
	for n := 0; n < 200; n += 3 {
		msg := s8Bytes(uint64(n), n)
		p := append(append([]byte{}, msg...), 0x80)
		for len(p)%64 != 56 {
			p = append(p, 0)
		}
		p = binary.BigEndian.AppendUint64(p, uint64(n)*8)
		h := [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}
		for i := 0; i < len(p); i += 64 {
			var w [64]uint32
			for j := 0; j < 16; j++ {
				w[j] = binary.BigEndian.Uint32(p[i+4*j:])
			}
			for j := 16; j < 64; j += 4 {
				var in [16]uint32
				copy(in[:], w[j-16:j])
				r := s8ShaSchedule4(in)
				copy(w[j:], r[:])
			}
			s := [8]uint32{h[5], h[4], h[1], h[0], h[7], h[6], h[3], h[2]}
			for j := 0; j < 64; j += 4 {
				s = s8ShaRounds4(s, [4]uint32{w[j] + k[j], w[j+1] + k[j+1], w[j+2] + k[j+2], w[j+3] + k[j+3]})
			}
			for j, idx := range []int{3, 2, 7, 6, 1, 0, 5, 4} {
				h[j] += s[idx]
			}
		}
		d := sha256.Sum256(msg)
		for j := range h {
			if h[j] != binary.BigEndian.Uint32(d[4*j:]) {
				t.Fatalf("len %d: word %d %#x, want %#x", n, j, h[j], binary.BigEndian.Uint32(d[4*j:]))
			}
		}
	}
}

func s8ShaK() [64]uint32 {
	return [64]uint32{
		0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
		0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
		0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
		0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
		0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
		0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
		0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
		0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
	}
}

// s8ShaIntrinsicProgram runs both intrinsics over n random inputs in sha
// functions and folds the results into one hash, in the evaluator (a
// const) and natively.
const s8ShaIntrinsicProgram = `import std.raw
import std.simd

Rounds(s: [8]u32, wk: [4]u32): [8]u32 cpu "sha" {
    return raw.Sha256Rounds4(s, wk)
}

Schedule(a: [4]u32, b: [4]u32, c: [4]u32, d: [4]u32): [4]u32 cpu "sha" {
    return raw.Sha256Schedule4(a, b, c, d)
}

// Base and Wide call the intrinsics from functions without sha (inside
// unsafe), through the private helpers: from a baseline function and from
// one whose [8]u32 values may sit in 256-bit registers.
Base(s: [8]u32, wk: [4]u32): [8]u32 {
    unsafe "called where Cpu() has sha, or by the evaluator" {
        return raw.Sha256Rounds4(s, wk)
    }
}

Wide(s: [8]u32, w: [4]u32, wk: [4]u32): [8]u32 cpu "avx2" {
    unsafe "called where Cpu() has sha and avx2, or by the evaluator" {
        q := raw.Sha256Schedule4(w, wk, w, wk)
        return raw.Sha256Rounds4(s, q)
    }
}

Next(x: u64): u64 {
    return x *% 6364136223846793005 +% 1442695040888963407
}

Mix(h: u64, x: u64): u64 {
    return (h ^ x) *% 0x100000001b3
}

Run(n: int, z: u64): u64 {
    var h: u64 = 0xcbf29ce484222325
    var x: u64 = 11 + z
    var w: [16]u32 = [0; 16]
    var s: [8]u32 = [0; 8]
    for _ in 0..n {
        for j in 0..16 {
            x = Next(x)
            w[j] = Cast[u32](x >> 32)
        }
        for j in 0..8 {
            x = Next(x)
            s[j] = Cast[u32](x >> 32)
        }
        var r: [8]u32 = [0; 8]
        var q: [4]u32 = [0; 4]
        unsafe "Run is called by the evaluator or where Cpu() has sha" {
            r = Rounds(s, [w[0], w[1], w[2], w[3]])
            q = Schedule([w[0], w[1], w[2], w[3]], [w[4], w[5], w[6], w[7]], [w[8], w[9], w[10], w[11]], [w[12], w[13], w[14], w[15]])
        }
        for j in 0..8 {
            h = Mix(h, Cast[u64](r[j]))
        }
        for j in 0..4 {
            h = Mix(h, Cast[u64](q[j]))
        }
        b1 := Base(s, [w[4], w[5], w[6], w[7]])
        unsafe "Wide runs where Cpu() has sha and avx2, or in the evaluator" {
            w1 := Wide(s, [w[0], w[1], w[2], w[3]], [w[8], w[9], w[10], w[11]])
            for j in 0..8 {
                h = Mix(Mix(h, Cast[u64](b1[j])), Cast[u64](w1[j]))
            }
        }
    }
    return h
}

const CE: u64 = Run(300, 0)

export Main(sys: &System): WriteError!void {
    z := Cast[u64]((sys.Env("MK_SIMD8_ZERO") else { "" }).Len())
    if !simd.Cpu().Has("sha") || !simd.Cpu().Has("avx2") {
        try sys.Print("eval {e}\nskip\n", (e: CE))
        return
    }
    try sys.Print("eval {e}\nrun {r}\n", (e: CE, r: Run(300, z)))
}
`

func s8ShaIntrinsicModel(n int) uint64 {
	next := func(x uint64) uint64 { return x*6364136223846793005 + 1442695040888963407 }
	mix := func(h, x uint64) uint64 { return (h ^ x) * 0x100000001b3 }
	h, x := uint64(0xcbf29ce484222325), uint64(11)
	for i := 0; i < n; i++ {
		var w [16]uint32
		var s [8]uint32
		for j := range w {
			x = next(x)
			w[j] = uint32(x >> 32)
		}
		for j := range s {
			x = next(x)
			s[j] = uint32(x >> 32)
		}
		r := s8ShaRounds4(s, [4]uint32{w[0], w[1], w[2], w[3]})
		q := s8ShaSchedule4(w)
		for _, v := range r {
			h = mix(h, uint64(v))
		}
		for _, v := range q {
			h = mix(h, uint64(v))
		}
		b1 := s8ShaRounds4(s, [4]uint32{w[4], w[5], w[6], w[7]})
		w0, wk := [4]uint32{w[0], w[1], w[2], w[3]}, [4]uint32{w[8], w[9], w[10], w[11]}
		var in [16]uint32
		for j := 0; j < 4; j++ {
			in[j], in[4+j], in[8+j], in[12+j] = w0[j], wk[j], w0[j], wk[j]
		}
		w1 := s8ShaRounds4(s, s8ShaSchedule4(in))
		for j := 0; j < 8; j++ {
			h = mix(mix(h, uint64(b1[j])), uint64(w1[j]))
		}
	}
	return h
}

// TestSimd_ShaIntrinsicsEvalNative: both intrinsics in the evaluator and
// natively (dev and release) against the definitions, 300 random inputs.
func TestSimd_ShaIntrinsicsEvalNative(t *testing.T) {
	t.Parallel()
	m := s8ShaIntrinsicModel(300)
	want := fmt.Sprintf("eval %d\nrun %d\n", m, m)
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			got := s8Run(t, nil, s8ShaIntrinsicProgram, true, flags...)
			s8ShaNeedsCPU(t, got, fmt.Sprintf("eval %d\n", m))
			diffLines(t, got, want)
		})
	}
}

// TestSimd_ShaKernelCheckEveryTarget: the SHA-256 kernel checks clean on
// the hosted targets, on aarch64 (checked only) and on
// x86_64-freestanding, where it reports false and holds no vector.
func TestSimd_ShaKernelCheckEveryTarget(t *testing.T) {
	t.Parallel()
	s8CheckTargets(t, s8ShaShim, `    var h: [8]u32 = [0; 8]
    var b: [64]u8 = [0; 64]
    _ = hash.Simd8Sha256Blocks(mut h, b[..])
`)
}

// TestSimd_ShaIntrinsicsNeedTheirFeature: a call outside unsafe from a
// function without sha is E1101 cpu-call.
func TestSimd_ShaIntrinsicsNeedTheirFeature(t *testing.T) {
	t.Parallel()
	s8WantCpuCall(t, `import std.raw

F(w: [4]u32): [4]u32 cpu "avx2" {
    return raw.Sha256Schedule4(w, w, w, w)
}

export Main(sys: &System): WriteError!void {
    unsafe "test" {
        x := F([1; 4])
        try sys.Print("{a}\n", (a: x[0]))
    }
}
`, "sha")
}

// TestSimd_ShaGeneralRegsOnlyNeedsSse2: on x86_64-freestanding, whose
// bins are general-regs-only, a call of a SHA-256 intrinsic from a context
// without sse2 is E1106 needs sse2: from a `cpu "sha"` function (once an
// LLVM backend crash) and inside unsafe from MkEntry with no clause (once a
// helper with SSE code in the bin), also
// when named as a fn value there.
func TestSimd_ShaGeneralRegsOnlyNeedsSse2(t *testing.T) {
	t.Parallel()
	s8GroWantNeeds(t, `import std.raw

F(s: [8]u32, w: [4]u32): [8]u32 cpu "sha" {
    return raw.Sha256Rounds4(s, w)
}

export extern MkEntry(): u64 {
    var s: [8]u32 = [1; 8]
    var w: [4]u32 = [2; 4]
    unsafe "probe" {
        s = F(s, w)
        s = raw.Sha256Rounds4(s, w)
        w = raw.Sha256Schedule4(w, w, w, w)
        g := raw.Sha256Schedule4
        w = g(w, w, w, w)
    }
    return Cast[u64](s[0] ^ w[0])
}
`, 4)
}

// TestSimd_ShaGeneralRegsOnlyBuild: in a general-regs-only bin the
// SHA-256 intrinsics build, at dev and release, inline in a
// `cpu "sse2, sha"` function and through the helper from a `cpu "sse2"`
// function inside unsafe, and no other function holds an SSE register
// (once a helper put SSE code in the bin).
func TestSimd_ShaGeneralRegsOnlyBuild(t *testing.T) {
	t.Parallel()
	src := `import std.raw

F(s: [8]u32, w: [4]u32): [8]u32 cpu "sse2, sha" {
    return raw.Sha256Rounds4(s, w)
}

H(w: [4]u32): [4]u32 cpu "sse2" {
    unsafe "probe: the caller's Cpu() test" {
        return raw.Sha256Schedule4(w, w, w, w)
    }
}

export extern MkEntry(): u64 {
    var s: [8]u32 = [1; 8]
    var w: [4]u32 = [2; 4]
    unsafe "probe" {
        s = F(s, w)
        w = H(w)
    }
    return Cast[u64](s[0] ^ w[0])
}
`
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			fns := s8GroXmm(t, src, flags...)
			s8GroOnlyIn(t, fns, "sha256rnds2", ".F", ".H")
			s8GroOnlyIn(t, fns, "sha256msg1", ".F", ".H")
		})
	}
}
