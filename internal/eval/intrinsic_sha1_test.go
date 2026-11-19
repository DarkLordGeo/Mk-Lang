// Why: the evaluator's SHA-1 intrinsics are defined by FIPS 180-4, and a
// fault in one would hide in compile-time hashes.
// What: tests of the round, next-e and schedule functions as Go functions.
// How: drives them through FIPS 180-4's compression in the shape
// std.simd.kernel's Sha1Ni uses, against crypto/sha1 and the "abc" example,
// and tests each function alone against FIPS 180-4 section 6.1.2.

package eval

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"math/rand"
	"testing"
)

// s1Group is four big-endian words of p in x86_64's SHA-1 lane order.
func s1Group(p []byte) [4]uint32 {
	return [4]uint32{binary.BigEndian.Uint32(p[12:]), binary.BigEndian.Uint32(p[8:]), binary.BigEndian.Uint32(p[4:]), binary.BigEndian.Uint32(p)}
}

// s1Sha1 is SHA-1 of msg through the intrinsics' functions.
func s1Sha1(msg []byte) [20]byte {
	p := append([]byte{}, msg...)
	p = append(p, 0x80)
	for len(p)%64 != 56 {
		p = append(p, 0)
	}
	p = binary.BigEndian.AppendUint64(p, uint64(len(msg))*8)
	h := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	for i := 0; i < len(p); i += 64 {
		abcd := [4]uint32{h[3], h[2], h[1], h[0]}
		w := [4][4]uint32{s1Group(p[i:]), s1Group(p[i+16:]), s1Group(p[i+32:]), s1Group(p[i+48:])}
		last := [4]uint32{0, 0, 0, h[4]}
		we := w[0]
		we[3] += h[4]
		for q := 0; q < 20; q++ {
			next := sha1NextE(abcd, w[1])
			abcd = sha1Rounds4(abcd, we, q/5)
			w4 := last
			if q < 16 {
				w4 = sha1Schedule4(w[0], w[1], w[2], w[3])
			}
			w = [4][4]uint32{w[1], w[2], w[3], w4}
			we = next
		}
		h[0] += abcd[3]
		h[1] += abcd[2]
		h[2] += abcd[1]
		h[3] += abcd[0]
		h[4] = we[3] // last's h[4] plus the e the last group leaves
	}
	var out [20]byte
	for i, x := range h {
		binary.BigEndian.PutUint32(out[4*i:], x)
	}
	return out
}

func TestSHA1_EvalMatchesCryptoSha1(t *testing.T) {
	if got := s1Sha1([]byte("abc")); hex.EncodeToString(got[:]) != "a9993e364706816aba3e25717850c26c9cd0d89d" {
		t.Fatalf("SHA-1(abc) = %x", got)
	}
	rng := rand.New(rand.NewSource(161))
	for n := 0; n < 300; n++ {
		msg := make([]byte, n)
		rng.Read(msg)
		if got, want := s1Sha1(msg), sha1.Sum(msg); got != want {
			t.Fatalf("len %d: %x, want %x", n, got, want)
		}
	}
}

// TestSHA1_EvalFunctionsAlone: each function on random inputs against FIPS
// 180-4 section 6.1.2 read directly (one round at a time, the schedule word by
// word), so a fault that the compression's composition might hide shows.
func TestSHA1_EvalFunctionsAlone(t *testing.T) {
	rng := rand.New(rand.NewSource(1802))
	r4 := func() [4]uint32 { return [4]uint32{rng.Uint32(), rng.Uint32(), rng.Uint32(), rng.Uint32()} }
	for k := 0; k < 2000; k++ {
		abcd, we, g := r4(), r4(), k%4
		a, b, c, d, e := abcd[3], abcd[2], abcd[1], abcd[0], uint32(0)
		for i := 0; i < 4; i++ {
			var f uint32
			switch g {
			case 0:
				f = (b & c) ^ (^b & d)
			case 2:
				f = (b & c) ^ (b & d) ^ (c & d)
			default:
				f = b ^ c ^ d
			}
			x := bits.RotateLeft32(a, 5) + f + e + sha1K[g] + we[3-i]
			e, d, c, b, a = d, c, bits.RotateLeft32(b, 30), a, x
		}
		if got, want := sha1Rounds4(abcd, we, g), [4]uint32{d, c, b, a}; got != want {
			t.Fatalf("Sha1Rounds4(%x, %x, %d) = %x, want %x", abcd, we, g, got, want)
		}
		// The e four rounds leave is ROTL30 of the first a.
		if e != bits.RotateLeft32(abcd[3], 30) {
			t.Fatalf("e after four rounds %x, want ROTL30(a) %x", e, bits.RotateLeft32(abcd[3], 30))
		}
		w := r4()
		got := sha1NextE(abcd, w)
		if got[0] != w[0] || got[1] != w[1] || got[2] != w[2] || got[3] != w[3]+bits.RotateLeft32(abcd[3], 30) {
			t.Fatalf("Sha1NextE(%x, %x) = %x", abcd, w, got)
		}
		var ws [20]uint32
		for i := range 16 {
			ws[i] = rng.Uint32()
		}
		for i := 16; i < 20; i++ {
			ws[i] = bits.RotateLeft32(ws[i-3]^ws[i-8]^ws[i-14]^ws[i-16], 1)
		}
		grp := func(i int) [4]uint32 { return [4]uint32{ws[i+3], ws[i+2], ws[i+1], ws[i]} }
		if got, want := sha1Schedule4(grp(0), grp(4), grp(8), grp(12)), grp(16); got != want {
			t.Fatalf("Sha1Schedule4 = %x, want %x", got, want)
		}
	}
}
