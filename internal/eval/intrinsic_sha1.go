// Why: std.raw's SHA-1 intrinsics (std/raw/sha1.mk) map to CPU SHA
// instructions natively; a compile-time evaluation needs the same results.
//
// What: sha1K, sha1Rounds4, sha1NextE and sha1Schedule4, registered in init.
//
// How: each follows FIPS 180-4 section 6.1.2: four rounds of step 3 in one
// round group, the next group's e, and four words of step 1's message
// schedule. Every [4]u32 is in x86_64's SHA-1 lane order: a, and the oldest
// word, in lane 3.

package eval

import (
	"math/bits"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// sha1K are FIPS 180-4 section 4.2.1's constants, one per round group.
var sha1K = [4]uint32{0x5a827999, 0x6ed9eba1, 0x8f1bbcdc, 0xca62c1d6}

// sha1Rounds4 is four rounds of round group g over abcd = (d, c, b, a)
// with we = (W3, W2, W1, W0 + e).
func sha1Rounds4(abcd, we [4]uint32, g int) [4]uint32 {
	a, b, c, d := abcd[3], abcd[2], abcd[1], abcd[0]
	var e uint32
	for i := 3; i >= 0; i-- {
		var f uint32
		switch g {
		case 0:
			f = b&c | ^b&d
		case 2:
			f = b&c | b&d | c&d
		default:
			f = b ^ c ^ d
		}
		x := bits.RotateLeft32(a, 5) + f + e + sha1K[g] + we[i]
		e, d, c, b, a = d, c, bits.RotateLeft32(b, 30), a, x
	}
	return [4]uint32{d, c, b, a}
}

// sha1NextE is w with ROTL30 of abcd's a (lane 3) added to lane 3.
func sha1NextE(abcd, w [4]uint32) [4]uint32 {
	w[3] += bits.RotateLeft32(abcd[3], 30)
	return w
}

// sha1Schedule4 is W[t .. t + 3] from W[t - 16 .. t - 1], each group of
// four in lane order (the oldest in lane 3).
func sha1Schedule4(w0, w1, w2, w3 [4]uint32) [4]uint32 {
	var ext [20]uint32
	for i, g := range [4][4]uint32{w0, w1, w2, w3} {
		for j := 0; j < 4; j++ {
			ext[4*i+j] = g[3-j]
		}
	}
	for t := 16; t < 20; t++ {
		ext[t] = bits.RotateLeft32(ext[t-3]^ext[t-8]^ext[t-14]^ext[t-16], 1)
	}
	return [4]uint32{ext[19], ext[18], ext[17], ext[16]}
}

func init() {
	intrinsics["std.raw.Sha1Rounds4"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		m.vchargeIO(80)
		var abcd, we [4]uint32
		m.wordsArg(f, t, 0, abcd[:])
		m.wordsArg(f, t, 1, we[:])
		g := m.argU(f, t, 2)
		if g > 3 {
			// check's E0706 judged the requires clause at the call.
			source.ICEf(f.curSpan(), "eval: std.raw.Sha1Rounds4 with round group %d", g)
		}
		r := sha1Rounds4(abcd, we, int(g))
		m.putWords(f, t, r[:])
	}
	intrinsics["std.raw.Sha1NextE"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		m.vchargeIO(20)
		var abcd, w [4]uint32
		m.wordsArg(f, t, 0, abcd[:])
		m.wordsArg(f, t, 1, w[:])
		r := sha1NextE(abcd, w)
		m.putWords(f, t, r[:])
	}
	intrinsics["std.raw.Sha1Schedule4"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		m.vchargeIO(40)
		var w [4][4]uint32
		for i := range w {
			m.wordsArg(f, t, i, w[i][:])
		}
		r := sha1Schedule4(w[0], w[1], w[2], w[3])
		m.putWords(f, t, r[:])
	}
}
