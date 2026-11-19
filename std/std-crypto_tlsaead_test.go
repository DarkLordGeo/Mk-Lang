// Why: AES-GCM in std.crypto must give Go's crypto/cipher answers on every
// path (dev and release, AES instructions and the portable code).
// What: differential tests of AesGcm Seal and Open against Go, on the four
// driver variants.
// How: plaintext lengths 0 to 160 and around the block strides, aad lengths
// across the padding, both key sizes, and a flipped bit at every place.

package std

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"strings"
	"testing"
)

// cr2Rand is a splitmix64 stream for the tests' inputs.
func cr2Rand(seed uint64, n int) []byte {
	r := cryptoSplitmix{s: seed}
	b := make([]byte, n)
	r.fill(b)
	return b
}

func cr2Seal(key, nonce, aad, plain []byte) []byte {
	c, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(c)
	return g.Seal(nil, nonce, plain, aad)
}

// TestSTDCRYPTO_AesGcmMatchesGo: Seal and Open against Go.
func TestSTDCRYPTO_AesGcmMatchesGo(t *testing.T) {
	t.Parallel()
	var lines []string
	lens := []int{}
	for n := 0; n <= 160; n++ {
		lens = append(lens, n)
	}
	lens = append(lens, 191, 192, 193, 255, 256, 257, 511, 512, 513, 1023, 1024, 1025, 4096+7, 16384)
	for i, n := range lens {
		for _, kl := range []int{16, 32} {
			seed := uint64(i*131 + kl)
			key := cr2Rand(seed, kl)
			nonce := cr2Rand(seed+1, 12)
			aad := cr2Rand(seed+2, (i*7)%41)
			plain := cr2Rand(seed+3, n)
			lines = append(lines, fmt.Sprintf("G %s %s %s %s", cr2X(key), cr2X(nonce), cr2X(aad), cr2X(plain)))
			sealed := cr2Seal(key, nonce, aad, plain)
			lines = append(lines, fmt.Sprintf("O %s %s %s %s", cr2X(key), cr2X(nonce), cr2X(aad), cr2X(sealed)))
			if n <= 40 || n%256 == 1 {
				for bit := 0; bit < 8*len(sealed); bit += 1 + n/8 {
					bad := append([]byte(nil), sealed...)
					bad[bit/8] ^= 1 << (bit % 8)
					lines = append(lines, fmt.Sprintf("O %s %s %s %s", cr2X(key), cr2X(nonce), cr2X(aad), cr2X(bad)))
				}
				if len(aad) > 0 {
					bad := append([]byte(nil), aad...)
					bad[len(bad)-1] ^= 0x80
					lines = append(lines, fmt.Sprintf("O %s %s %s %s", cr2X(key), cr2X(nonce), cr2X(bad), cr2X(sealed)))
				}
				badNonce := append([]byte(nil), nonce...)
				badNonce[11] ^= 1
				lines = append(lines, fmt.Sprintf("O %s %s %s %s", cr2X(key), cr2X(badNonce), cr2X(aad), cr2X(sealed)))
			}
		}
	}
	// An out one byte short: Seal and Open fail with BadLength, writing
	// nothing.
	for _, n := range []int{1, 15, 16, 17, 64, 100} {
		for _, kl := range []int{16, 32} {
			key, nonce, aad, plain := cr2Rand(uint64(n), kl), cr2Rand(uint64(n)+1, 12), cr2Rand(uint64(n)+2, 5), cr2Rand(uint64(n)+3, n)
			lines = append(lines, fmt.Sprintf("S %s %s %s %s %s", cr2X(key), cr2X(nonce), cr2X(aad), cr2X(plain), cr2X(cr2Seal(key, nonce, aad, plain))))
		}
	}
	// Refused keys and short sealed messages.
	for _, kl := range []int{0, 1, 15, 17, 24, 31, 33, 64} {
		lines = append(lines, fmt.Sprintf("G %s %s  00", cr2X(cr2Rand(uint64(kl), kl)), cr2X(make([]byte, 12))))
	}
	key := cr2Rand(99, 16)
	for n := 0; n < 16; n++ {
		lines = append(lines, fmt.Sprintf("O %s %s  %s", cr2X(key), cr2X(make([]byte, 12)), cr2X(cr2Rand(uint64(n), n))))
	}
	cr2Check(t, "AesGcm", lines)
}

// TestSTDCRYPTO_AesGcmNistVectors: NIST's GCM test cases 1 to 4 and 13
// to 16 (McGrew and Viega, "The Galois/Counter Mode of Operation",
// appendix B; the 96-bit-IV cases with 128- and 256-bit keys), each
// sealed and opened on every variant, with the bytes written here, not
// computed by Go.
func TestSTDCRYPTO_AesGcmNistVectors(t *testing.T) {
	t.Parallel()
	type vec struct{ key, iv, aad, pt, ct, tag string }
	p4 := "d9313225f88406e5a55909c5aff5269a86a7a9531534f7da2e4c303d8a318a721c3c0c95956809532fcf0e2449a6b525b16aedf5aa0de657ba637b39"
	a4 := "feedfacedeadbeeffeedfacedeadbeefabaddad2"
	vs := []vec{
		{"00000000000000000000000000000000", "000000000000000000000000", "", "", "", "58e2fccefa7e3061367f1d57a4e7455a"},
		{"00000000000000000000000000000000", "000000000000000000000000", "", "00000000000000000000000000000000", "0388dace60b6a392f328c2b971b2fe78", "ab6e47d42cec13bdf53a67b21257bddf"},
		{"feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", "", p4 + "1aafd255", "42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091473f5985", "4d5c2af327cd64a62cf35abd2ba6fab4"},
		{"feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", a4, p4, "42831ec2217774244b7221b784d0d49ce3aa212f2c02a4e035c17e2329aca12e21d514b25466931c7d8f6a5aac84aa051ba30b396a0aac973d58e091", "5bc94fbc3221a5db94fae95ae7121a47"},
		{strings.Repeat("00", 32), "000000000000000000000000", "", "", "", "530f8afbc74536b9a963b4f1c4cb738b"},
		{strings.Repeat("00", 32), "000000000000000000000000", "", "00000000000000000000000000000000", "cea7403d4d606b6e074ec5d3baf39d18", "d0d1c8a799996bf0265b98b5d48ab919"},
		{"feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", "", p4 + "1aafd255", "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662898015ad", "b094dac5d93471bdec1a502270e3cc6c"},
		{"feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308", "cafebabefacedbaddecaf888", a4, p4, "522dc1f099567d07f47f37a32a84427d643a8cdcbfe5c0c97598a2bd2555d1aa8cb08e48590dbb3da7b08b1056828838c5f61e6393ba7a0abcc9f662", "76fc6ece0f4e1768cddf8853bb2d551b"},
	}
	var lines, want []string
	for _, v := range vs {
		lines = append(lines, fmt.Sprintf("G %s %s %s %s", v.key, v.iv, v.aad, v.pt))
		want = append(want, "G "+v.ct+v.tag+" true")
		lines = append(lines, fmt.Sprintf("O %s %s %s %s", v.key, v.iv, v.aad, v.ct+v.tag))
		want = append(want, "O ok "+v.pt+" true")
	}
	for _, v := range cr2Variants {
		got := cr2Ask(t, v, lines)
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s: %s\n got  %s\n want %s", v.name, lines[i], got[i], want[i])
			}
		}
	}
}
