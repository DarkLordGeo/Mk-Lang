// Why: std's Hasher and the Swiss table's group masks also run in the
// compile-time evaluator, which must agree with an outside oracle.
// What: TestHasherMatchesSip13 and TestGroupMasks, with a
// SipHash-1-3 (sip13) and group-mask oracle written here in Go.
// How: std's Mk code runs in the evaluator through mk check.

package std

import (
	"fmt"
	"math/bits"
	"strings"
	"testing"
)

// sip13 is SipHash-1-3 with std's fixed key k0 = 0x0706050403020100, k1 =
// 0x0f0e0d0c0b0a0908 (4.8): one compression round, three finalization
// rounds, the length's low byte on top of the last word.
func sip13(b []byte) uint64 {
	k0, k1 := uint64(0x0706050403020100), uint64(0x0f0e0d0c0b0a0908)
	v0 := 0x736f6d6570736575 ^ k0
	v1 := 0x646f72616e646f6d ^ k1
	v2 := 0x6c7967656e657261 ^ k0
	v3 := 0x7465646279746573 ^ k1
	round := func() {
		v0 += v1
		v1 = bits.RotateLeft64(v1, 13)
		v1 ^= v0
		v0 = bits.RotateLeft64(v0, 32)
		v2 += v3
		v3 = bits.RotateLeft64(v3, 16)
		v3 ^= v2
		v0 += v3
		v3 = bits.RotateLeft64(v3, 21)
		v3 ^= v0
		v2 += v1
		v1 = bits.RotateLeft64(v1, 17)
		v1 ^= v2
		v2 = bits.RotateLeft64(v2, 32)
	}
	n := len(b)
	i := 0
	for ; i+8 <= n; i += 8 {
		var m uint64
		for j := 7; j >= 0; j-- {
			m = m<<8 | uint64(b[i+j])
		}
		v3 ^= m
		round()
		v0 ^= m
	}
	last := uint64(n&0xff) << 56
	for j := 0; i+j < n; j++ {
		last |= uint64(b[i+j]) << (8 * j)
	}
	v3 ^= last
	round()
	v0 ^= last
	v2 ^= 0xff
	round()
	round()
	round()
	return v0 ^ v1 ^ v2 ^ v3
}

// TestHasherMatchesSip13 runs Hasher.Bytes, Hasher.U64 and
// HashFinish at compile time: the hash of the bytes 0..n-1 for n from 0 to
// 63 against sip13, every split of the first 24 inputs into two Bytes calls,
// and U64 against the 8 bytes it stands for, after 0 to 7 bytes of tail.
// NewHasher is the fold hash, so the fixture streams
// through NewHasherKeyed under SipHash's reference key, the SipHash path
// NewKeyed still uses; TestStdHash_EvalAndNativeMatchModel is the
// fold twin.
func TestHasherMatchesSip13(t *testing.T) {
	in := make([]byte, 64)
	for i := range in {
		in[i] = byte(i)
	}
	var want []string
	for n := 0; n < 64; n++ {
		want = append(want, fmt.Sprintf("%#x", sip13(in[:n])))
	}
	// U64 after k tail bytes: bytes 0..k-1, then the 8 bytes of
	// 0x1716151413121110, then nothing.
	var u64want []string
	for k := 0; k < 8; k++ {
		b := append(append([]byte{}, in[:k]...), 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17)
		u64want = append(u64want, fmt.Sprintf("%#x", sip13(b)))
	}
	fixture := `const SipWant: [64]u64 = [` + strings.Join(want, ", ") + `]
const SipU64Want: [8]u64 = [` + strings.Join(u64want, ", ") + `]

SipInput(): [64]u8 {
    var b: [64]u8 = [0; 64]
    for i in 0..64 {
        b[i] = Trunc[u8](i)
    }
    return b
}

SipWhole(b: []u8): u64 {
    var h := NewHasherKeyed(FixedK0, FixedK1)
    h.Bytes(b)
    return HashFinish(mut h)
}

SipSplit(b: []u8, k: int): u64 {
    var h := NewHasherKeyed(FixedK0, FixedK1)
    h.Bytes(b[0..k])
    h.Bytes(b[k..b.Len()])
    return HashFinish(mut h)
}

SipU64(b: []u8, k: int): u64 {
    var h := NewHasherKeyed(FixedK0, FixedK1)
    h.Bytes(b[0..k])
    h.U64(0x1716151413121110)
    return HashFinish(mut h)
}

SipCheck(): int {
    b := SipInput()
    var bad := 0
    for n in 0..64 {
        if SipWhole(b[0..n]) != SipWant[n] {
            bad += 1
        }
    }
    for n in 0..24 {
        for k in 0..n + 1 {
            if SipSplit(b[0..n], k) != SipWant[n] {
                bad += 1
            }
        }
    }
    for k in 0..8 {
        if SipU64(b[..], k) != SipU64Want[k] {
            bad += 1
        }
    }
    return bad
}

const SipOk = [0; 1][SipCheck()]
`
	expectClean(t, "s_g179", fixture)
}

// TestGroupMasks checks the Swiss table's masks (4.8 rule 2) at
// compile time on groups built from every control byte value: EmptyMask
// sets bit 7 of exactly the 0xFF bytes, FreeMask of the bytes with bit 7,
// and MatchMask of every byte equal to h2 (false positives allowed, a byte
// equal to h2 never missed), for each control byte value (0xFF, 0x80,
// 0x00 to 0x7F) at each of the 8 positions among neighbours 0xFF, 0x80 and
// 0x00.
func TestGroupMasks(t *testing.T) {
	fixture := `MaskByte(m: u64, j: int): bool {
    return (m >> (8 * j + 7)) & 1 == 1
}

MaskGroup(b: u8, j: int, fill: u8): u64 {
    var g: u64 = 0
    for k in 0..8 {
        var c := fill
        if k == j {
            c = b
        }
        g = g | (u64(c) << (8 * k))
    }
    return g
}

MaskCheck(): int {
    var bad := 0
    var fills: [3]u8 = [0xFF, 0x80, 0x00]
    for v in 0..256 {
        b := Trunc[u8](v)
        if b > 0x80 && b < 0xFF {
            continue // not a control byte
        }
        for j in 0..8 {
            for f in 0..3 {
                fill := fills[f]
                g := MaskGroup(b, j, fill)
                e := EmptyMask(g)
                fr := FreeMask(g)
                if MaskByte(e, j) != (b == 0xFF) || MaskByte(fr, j) != (b >= 0x80) {
                    bad += 1
                }
                if MaskByte(e, (j + 1) % 8) != (fill == 0xFF) {
                    bad += 1
                }
                if b < 0x80 && !MaskByte(MatchMask(g, b), j) {
                    bad += 1
                }
            }
        }
    }
    return bad
}

const MaskOk = [0; 1][MaskCheck()]
`
	expectClean(t, "s_g179_masks", fixture)
}
