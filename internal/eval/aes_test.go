// Why: the evaluator defines the AES round and carry-less multiply
// intrinsics itself, and they must match FIPS 197 and GHASH bit for bit.
// What: tests of the round functions, the S-box and Clmul64 as Go functions.
// How: drives the rounds through FIPS 197's Cipher and key expansion against
// crypto/aes and Appendix C.1; checks Clmul64 against the defining sum.

package eval

import (
	"crypto/aes"
	"encoding/hex"
	"math/rand"
	"testing"
)

// aesExpand is FIPS 197 section 5.2's KeyExpansion as round keys.
func aesExpand(key []byte) [][16]byte {
	nk := len(key) / 4
	nr := nk + 6
	w := make([]byte, 16*(nr+1))
	copy(w, key)
	rcon := byte(1)
	for i := nk; i < 4*(nr+1); i++ {
		var t [4]byte
		copy(t[:], w[4*(i-1):4*i])
		if i%nk == 0 {
			t = [4]byte{aesSbox[t[1]] ^ rcon, aesSbox[t[2]], aesSbox[t[3]], aesSbox[t[0]]}
			rcon = gfMul(rcon, 2)
		} else if nk > 6 && i%nk == 4 {
			t = [4]byte{aesSbox[t[0]], aesSbox[t[1]], aesSbox[t[2]], aesSbox[t[3]]}
		}
		for j := 0; j < 4; j++ {
			w[4*i+j] = w[4*(i-nk)+j] ^ t[j]
		}
	}
	rk := make([][16]byte, nr+1)
	for r := range rk {
		copy(rk[r][:], w[16*r:])
	}
	return rk
}

// aesEncrypt is FIPS 197's Cipher through the round functions.
func aesEncrypt(rk [][16]byte, in [16]byte) [16]byte {
	s := in
	for i := range s {
		s[i] ^= rk[0][i]
	}
	for r := 1; r < len(rk)-1; r++ {
		s = aesRound(s, rk[r], true)
	}
	return aesRound(s, rk[len(rk)-1], false)
}

func TestEvalAesSbox(t *testing.T) {
	for x, want := range map[byte]byte{0x00: 0x63, 0x01: 0x7c, 0x53: 0xed, 0xff: 0x16, 0x10: 0xca} {
		if aesSbox[x] != want {
			t.Errorf("S-box %#02x = %#02x, want %#02x", x, aesSbox[x], want)
		}
	}
}

func TestEvalAesMatchesCryptoAes(t *testing.T) {
	// FIPS 197 Appendix C.1.
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	var pt [16]byte
	p, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	copy(pt[:], p)
	if got := aesEncrypt(aesExpand(key), pt); hex.EncodeToString(got[:]) != "69c4e0d86a7b0430d8cdb78070b4c55a" {
		t.Fatalf("FIPS 197 C.1: %x", got)
	}
	rng := rand.New(rand.NewSource(197))
	for _, n := range []int{16, 24, 32} {
		for i := 0; i < 200; i++ {
			key := make([]byte, n)
			rng.Read(key)
			var in [16]byte
			rng.Read(in[:])
			c, err := aes.NewCipher(key)
			if err != nil {
				t.Fatal(err)
			}
			var want [16]byte
			c.Encrypt(want[:], in[:])
			if got := aesEncrypt(aesExpand(key), in); got != want {
				t.Fatalf("AES-%d key %x block %x: %x, want %x", 8*n, key, in, got, want)
			}
		}
	}
}

func TestEvalClmul64(t *testing.T) {
	rng := rand.New(rand.NewSource(64))
	for i := 0; i < 2000; i++ {
		a, b := rng.Uint64(), rng.Uint64()
		if i < 4 {
			a, b = []uint64{0, 1, ^uint64(0), 1 << 63}[i], []uint64{5, ^uint64(0), ^uint64(0), 1 << 63}[i]
		}
		var whi, wlo uint64
		for k := 0; k < 64; k++ {
			for j := 0; j < 64; j++ {
				if a>>j&1 != 0 && b>>k&1 != 0 {
					if p := j + k; p < 64 {
						wlo ^= 1 << p
					} else {
						whi ^= 1 << (p - 64)
					}
				}
			}
		}
		if hi, lo := clmul64(a, b); hi != whi || lo != wlo {
			t.Fatalf("clmul(%#x, %#x) = (%#x, %#x), want (%#x, %#x)", a, b, hi, lo, whi, wlo)
		}
	}
	// (x^63)^2 = x^126.
	if hi, lo := clmul64(1<<63, 1<<63); hi != 1<<62 || lo != 0 {
		t.Fatalf("x^126: (%#x, %#x)", hi, lo)
	}
}

// TestEvalAesRoundFips197: FIPS 197 Appendix C.1's first round, one
// AesRound from round[1].start with round[1].k_sch.
func TestEvalAesRoundFips197(t *testing.T) {
	var s, k [16]byte
	b, _ := hex.DecodeString("00102030405060708090a0b0c0d0e0f0")
	copy(s[:], b)
	b, _ = hex.DecodeString("d6aa74fdd2af72fadaa678f1d6ab76fe")
	copy(k[:], b)
	if got := aesRound(s, k, true); hex.EncodeToString(got[:]) != "89d810e8855ace682d1843d8cb128fe4" {
		t.Fatalf("round[2].start = %x", got)
	}
}
