// Why: std.raw's SHA-256 intrinsics (std/raw/sha256.mk) map to CPU SHA
// instructions natively; a compile-time evaluation needs the same results.
//
// What: sha256Rounds4 and sha256Schedule4, registered in init, with the
// helpers wordsArg and putWords.
//
// How: each follows FIPS 180-4 section 6.2.2: four rounds of step 3 over the
// working variables held as (f, e, b, a, h, g, d, c), and four words of step
// 1's message schedule.

package eval

import (
	"math/bits"

	"mk/internal/mir"
	"mk/internal/types"
)

// sha256Rounds4 is four rounds over s = (f, e, b, a, h, g, d, c) with the
// W + K words wk.
func sha256Rounds4(s [8]uint32, wk [4]uint32) [8]uint32 {
	f, e, b, a, h, g, d, c := s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7]
	for _, x := range wk {
		t1 := h + (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11) ^ bits.RotateLeft32(e, -25)) + (e&f ^ ^e&g) + x
		t2 := (bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)) + (a&b ^ a&c ^ b&c)
		h, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	return [8]uint32{f, e, b, a, h, g, d, c}
}

// sha256Schedule4 is W[t .. t + 3] from W[t - 16 .. t - 1].
func sha256Schedule4(w [16]uint32) [4]uint32 {
	var ext [20]uint32
	copy(ext[:], w[:])
	for t := 16; t < 20; t++ {
		x, y := ext[t-15], ext[t-2]
		s0 := bits.RotateLeft32(x, -7) ^ bits.RotateLeft32(x, -18) ^ x>>3
		s1 := bits.RotateLeft32(y, -17) ^ bits.RotateLeft32(y, -19) ^ y>>10
		ext[t] = ext[t-16] + s0 + ext[t-7] + s1
	}
	return [4]uint32{ext[16], ext[17], ext[18], ext[19]}
}

// wordsArg reads argument i, an array of u32, into out.
func (m *Machine) wordsArg(f *frame, t *mir.Terminator, i int, out []uint32) {
	for j, x := range m.vecLanes(m.vargLoc(f, t, i), m.ti(types.U32)) {
		out[j] = uint32(x)
	}
}

// putWords writes xs as the u32 lanes of Dest.
func (m *Machine) putWords(f *frame, t *mir.Terminator, xs []uint32) {
	us := make([]uint64, len(xs))
	for j, x := range xs {
		us[j] = uint64(x)
	}
	m.putLanes(m.dest(f, t), m.ti(types.U32), us)
}

func init() {
	intrinsics["std.raw.Sha256Rounds4"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		m.vchargeIO(80)
		var s [8]uint32
		var wk [4]uint32
		m.wordsArg(f, t, 0, s[:])
		m.wordsArg(f, t, 1, wk[:])
		r := sha256Rounds4(s, wk)
		m.putWords(f, t, r[:])
	}
	intrinsics["std.raw.Sha256Schedule4"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		m.vchargeIO(80)
		var w [16]uint32
		for i := 0; i < 4; i++ {
			m.wordsArg(f, t, i, w[4*i:4*i+4])
		}
		r := sha256Schedule4(w)
		m.putWords(f, t, r[:])
	}
}
