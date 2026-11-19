// Why: std.raw's AES and carry-less multiply intrinsics (std/raw/aes.mk)
// compile to CPU instructions natively; a compile-time evaluation needs the
// same results without them, so crypto code can run in consts and tests.
//
// What: aesSbox, gfMul, aesRound, clmul64 and the intrinsics they back,
// registered in init.
//
// How: each runs its definition: FIPS 197 section 5.1's Cipher round
// (SubBytes, ShiftRows, MixColumns, AddRoundKey) and last round (without
// MixColumns), and the 128-bit carry-less product of two 64-bit polynomials
// over GF(2). The state is FIPS 197's byte order: byte r + 4c is row r of
// column c.

package eval

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// aesSbox is FIPS 197 section 5.1.1's S-box: the multiplicative inverse in
// GF(2^8) modulo x^8 + x^4 + x^3 + x + 1 (0 to 0), then the affine map.
var aesSbox = func() (s [256]byte) {
	for x := 0; x < 256; x++ {
		inv := byte(0)
		for y := 1; y < 256 && x != 0; y++ {
			if gfMul(byte(x), byte(y)) == 1 {
				inv = byte(y)
				break
			}
		}
		b := inv
		s[x] = b ^ rotl8(b, 1) ^ rotl8(b, 2) ^ rotl8(b, 3) ^ rotl8(b, 4) ^ 0x63
	}
	return
}()

func rotl8(b byte, n uint) byte { return b<<n | b>>(8-n) }

// gfMul is the product in GF(2^8) modulo x^8 + x^4 + x^3 + x + 1.
func gfMul(a, b byte) byte {
	var p byte
	for b != 0 {
		if b&1 != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1b
		}
		b >>= 1
	}
	return p
}

// aesRound is one Cipher round of state s with round key k (FIPS 197
// section 5.1), MixColumns included when mix.
func aesRound(s, k [16]byte, mix bool) [16]byte {
	var t [16]byte
	for c := 0; c < 4; c++ {
		for r := 0; r < 4; r++ {
			t[r+4*c] = aesSbox[s[r+4*((c+r)%4)]] // SubBytes after ShiftRows' gather
		}
	}
	if mix {
		for c := 0; c < 4; c++ {
			a0, a1, a2, a3 := t[4*c], t[4*c+1], t[4*c+2], t[4*c+3]
			t[4*c] = gfMul(a0, 2) ^ gfMul(a1, 3) ^ a2 ^ a3
			t[4*c+1] = a0 ^ gfMul(a1, 2) ^ gfMul(a2, 3) ^ a3
			t[4*c+2] = a0 ^ a1 ^ gfMul(a2, 2) ^ gfMul(a3, 3)
			t[4*c+3] = gfMul(a0, 3) ^ a1 ^ a2 ^ gfMul(a3, 2)
		}
	}
	for i := range t {
		t[i] ^= k[i]
	}
	return t
}

// clmul64 is the carry-less product of a and b, as (hi, lo).
func clmul64(a, b uint64) (hi, lo uint64) {
	for i := uint(0); i < 64; i++ {
		if b>>i&1 != 0 {
			lo ^= a << i
			if i > 0 {
				hi ^= a >> (64 - i)
			}
		}
	}
	return
}

// bytesArg reads argument i, a [16]u8.
func (m *Machine) bytesArg(f *frame, t *mir.Terminator, i int) [16]byte {
	var out [16]byte
	for j, x := range m.vecLanes(m.vargLoc(f, t, i), m.ti(types.U8)) {
		out[j] = byte(x)
	}
	return out
}

func init() {
	round := func(mix bool) intrinsicFunc {
		return func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
			m.vchargeIO(48)
			r := aesRound(m.bytesArg(f, t, 0), m.bytesArg(f, t, 1), mix)
			xs := make([]uint64, 16)
			for j, b := range r {
				xs[j] = uint64(b)
			}
			m.putLanes(m.dest(f, t), m.ti(types.U8), xs)
		}
	}
	intrinsics["std.raw.AesRound"] = round(true)
	intrinsics["std.raw.AesLastRound"] = round(false)
	intrinsics["std.raw.Clmul64"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		hi, lo := clmul64(m.argU(f, t, 0), m.argU(f, t, 1))
		dst := m.dest(f, t)
		for i, fd := range dst.ti.ty.Fields {
			v := lo
			if m.cfg.Interner.Str(fd.Name) == "hi" {
				v = hi
			}
			fl := dst.ti.layout.Fields[i]
			m.store(m.at(dst, fl.Offset, fl.Type), v)
		}
	}
}
