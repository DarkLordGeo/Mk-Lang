// Why: std's table hash (core_hash.mk) needs a model to test the Mk code
// against and to score for quality.
// What: foldHash, foldHashInt, foldHashStr and foldHashPair, the Go model.
// How: foldhash's "quality" form; std-hash_quality_test.go scores it and
// std-hash_mk_test.go compares std's Mk code with it.

package std

import (
	"encoding/binary"
	"math/bits"
)

// The fixed constants: the first five 64-bit words of pi's fraction
// (0x243f6a88... is pi's fraction), F0 to F3 as foldhash's ARBITRARY0 to
// ARBITRARY3.
const (
	foldF0 uint64 = 0x243f6a8885a308d3
	foldF1 uint64 = 0x13198a2e03707344
	foldF2 uint64 = 0xa4093822299f31d0
	foldF3 uint64 = 0x082efa98ec4e6c89
	foldF4 uint64 = 0x452821e638d01377 // the fifth word: lane 1's seed
)

// fold is the folded multiply: the 128-bit product's halves, xored.
func fold(x, y uint64) uint64 {
	hi, lo := bits.Mul64(x, y)
	return hi ^ lo
}

// foldHash is the fast hash of the byte stream b (the model, word by word).
//
// The model is std's fast fixed table hash ("fold hash", after foldhash by
// O. Peters, the default hasher of Rust's hashbrown since 0.15, in its
// "quality" form: one more folded multiply at the end).
//
// The hash is a function of the byte stream a Hasher absorbs: its 8-byte
// little-endian words w0..w(m-1), its tail t (the last n % 8 bytes as a
// little-endian word, upper bytes zero) and its length n:
//
//	acc0 = F0, acc1 = F4
//	pair j (words 2j, 2j+1) goes to lane j % 2:
//	    accL = fold(w(2j) ^ accL, w(2j+1) ^ F1)
//	(a, b) = (w(m-1), t) when m is odd, else (t, 0)
//	h = fold(fold(a ^ acc0, b ^ F2 ^ n ^ acc1 ^ F4), F3)
//
// where fold(x, y) is the 128-bit product's high word xor its low word.
// One folded multiply per 16 bytes, two more to finish: an integer key (8
// bytes) costs two multiplies, a two-field key three. The two lanes are
// independent chains, so a long key runs two multiplies in flight,
// as foldhash's medium and long paths do; a stream of fewer than 32 bytes
// has at most one pair, leaves acc1 at F4, and so hashes exactly as with
// one lane (acc1 ^ F4 = 0). The last fold is the
// quality finish: without it an integer key's hash fails avalanche (a flip
// of input bit i never reaches some output bits) and keys that differ only
// in their top 16 bits collide 1.7 times as often as a random function on
// the table's index bits.
//
// Other tests that modelled New's tables with sip13 call foldHashInt and
// foldHashStr from here instead.
func foldHash(b []byte) uint64 {
	n := len(b)
	m := n / 8
	acc := [2]uint64{foldF0, foldF4}
	j := 0
	for ; j+2 <= m; j += 2 {
		l := (j / 2) % 2
		acc[l] = fold(binary.LittleEndian.Uint64(b[8*j:])^acc[l], binary.LittleEndian.Uint64(b[8*j+8:])^foldF1)
	}
	var t uint64
	for k := 8 * m; k < n; k++ {
		t |= uint64(b[k]) << (8 * (k - 8*m))
	}
	a, c := t, uint64(0)
	if m%2 == 1 {
		a, c = binary.LittleEndian.Uint64(b[8*(m-1):]), t
	}
	return fold(fold(a^acc[0], c^foldF2^uint64(n)^acc[1]^foldF4), foldF3)
}

// foldHashInt is the hash New's tables give an integer key: its Hash impl
// absorbs U64 of the value (sign-extended for signed types), 8 bytes.
func foldHashInt(k int) uint64 {
	return foldHashU64(uint64(int64(k)))
}

func foldHashU64(x uint64) uint64 {
	return fold(fold(x^foldF0, foldF2^8), foldF3)
}

// foldHashStr is the hash New's tables give a str key: its bytes, then one
// 0xFF byte (str's Hash impl).
func foldHashStr(s string) uint64 {
	return foldHash(append([]byte(s), 0xFF))
}

// foldHashPair is the hash of a two-field integer key (two U64 calls).
func foldHashPair(x, y uint64) uint64 {
	return fold(fold(fold(x^foldF0, y^foldF1), foldF2^16), foldF3)
}
