// Why: Go's standard library has no XXH3, XXH64, BLAKE3 or SipHash, so the
// differential tests of std.hash need models written here.
// What: the four Go models and TestSTDHASHMOD_ModelsMatchPublishedVectors.
// How: each model is checked against published vectors before it judges Mk.

// The Go models of std.hash's tests: XXH3-64, XXH64, BLAKE3 and SipHash-1-3,
// which Go's standard
// library does not have, each checked against published vectors before the
// differential tests (std-hashmod_mk_test.go) compare std.hash's Mk code
// with them. CRC-32, SHA-256 and SHA-512 use Go's hash/crc32, crypto/sha256
// and crypto/sha512 as their models.

package std

import (
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"strings"
	"testing"
)

func hmLe64(b []byte, i int) uint64 { return binary.LittleEndian.Uint64(b[i:]) }
func hmLe32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i:]) }

// ---- XXH64 and XXH3-64 (xxHash specification, doc/xxhash_spec.md) ----

const (
	hmXxP32_1 = 0x9E3779B1
	hmXxP32_2 = 0x85EBCA77
	hmXxP32_3 = 0xC2B2AE3D
	hmXxP64_1 = 0x9E3779B185EBCA87
	hmXxP64_2 = 0xC2B2AE3D27D4EB4F
	hmXxP64_3 = 0x165667B19E3779F9
	hmXxP64_4 = 0x85EBCA77C2B2AE63
	hmXxP64_5 = 0x27D4EB2F165667C5
)

func hmXx64Round(acc, lane uint64) uint64 {
	acc += lane * hmXxP64_2
	acc = bits.RotateLeft64(acc, 31)
	return acc * hmXxP64_1
}

func hmXx64Merge(acc, v uint64) uint64 {
	acc ^= hmXx64Round(0, v)
	return acc*hmXxP64_1 + hmXxP64_4
}

func hmXx64Avalanche(h uint64) uint64 {
	h ^= h >> 33
	h *= hmXxP64_2
	h ^= h >> 29
	h *= hmXxP64_3
	h ^= h >> 32
	return h
}

// hmXxh64 is XXH64(b, seed).
func hmXxh64(b []byte, seed uint64) uint64 {
	n := len(b)
	i := 0
	var acc uint64
	if n >= 32 {
		v1 := seed + hmXxP64_1 + hmXxP64_2
		v2 := seed + hmXxP64_2
		v3 := seed
		v4 := seed - hmXxP64_1
		for ; n-i >= 32; i += 32 {
			v1 = hmXx64Round(v1, hmLe64(b, i))
			v2 = hmXx64Round(v2, hmLe64(b, i+8))
			v3 = hmXx64Round(v3, hmLe64(b, i+16))
			v4 = hmXx64Round(v4, hmLe64(b, i+24))
		}
		acc = bits.RotateLeft64(v1, 1) + bits.RotateLeft64(v2, 7) + bits.RotateLeft64(v3, 12) + bits.RotateLeft64(v4, 18)
		acc = hmXx64Merge(acc, v1)
		acc = hmXx64Merge(acc, v2)
		acc = hmXx64Merge(acc, v3)
		acc = hmXx64Merge(acc, v4)
	} else {
		acc = seed + hmXxP64_5
	}
	acc += uint64(n)
	for ; n-i >= 8; i += 8 {
		acc ^= hmXx64Round(0, hmLe64(b, i))
		acc = bits.RotateLeft64(acc, 27)*hmXxP64_1 + hmXxP64_4
	}
	if n-i >= 4 {
		acc ^= uint64(hmLe32(b, i)) * hmXxP64_1
		acc = bits.RotateLeft64(acc, 23)*hmXxP64_2 + hmXxP64_3
		i += 4
	}
	for ; i < n; i++ {
		acc ^= uint64(b[i]) * hmXxP64_5
		acc = bits.RotateLeft64(acc, 11) * hmXxP64_1
	}
	return hmXx64Avalanche(acc)
}

// hmXx3Secret is XXH3's default secret (kSecret, 192 bytes).
var hmXx3Secret = []byte{
	0xb8, 0xfe, 0x6c, 0x39, 0x23, 0xa4, 0x4b, 0xbe, 0x7c, 0x01, 0x81, 0x2c, 0xf7, 0x21, 0xad, 0x1c,
	0xde, 0xd4, 0x6d, 0xe9, 0x83, 0x90, 0x97, 0xdb, 0x72, 0x40, 0xa4, 0xa4, 0xb7, 0xb3, 0x67, 0x1f,
	0xcb, 0x79, 0xe6, 0x4e, 0xcc, 0xc0, 0xe5, 0x78, 0x82, 0x5a, 0xd0, 0x7d, 0xcc, 0xff, 0x72, 0x21,
	0xb8, 0x08, 0x46, 0x74, 0xf7, 0x43, 0x24, 0x8e, 0xe0, 0x35, 0x90, 0xe6, 0x81, 0x3a, 0x26, 0x4c,
	0x3c, 0x28, 0x52, 0xbb, 0x91, 0xc3, 0x00, 0xcb, 0x88, 0xd0, 0x65, 0x8b, 0x1b, 0x53, 0x2e, 0xa3,
	0x71, 0x64, 0x48, 0x97, 0xa2, 0x0d, 0xf9, 0x4e, 0x38, 0x19, 0xef, 0x46, 0xa9, 0xde, 0xac, 0xd8,
	0xa8, 0xfa, 0x76, 0x3f, 0xe3, 0x9c, 0x34, 0x3f, 0xf9, 0xdc, 0xbb, 0xc7, 0xc7, 0x0b, 0x4f, 0x1d,
	0x8a, 0x51, 0xe0, 0x4b, 0xcd, 0xb4, 0x59, 0x31, 0xc8, 0x9f, 0x7e, 0xc9, 0xd9, 0x78, 0x73, 0x64,
	0xea, 0xc5, 0xac, 0x83, 0x34, 0xd3, 0xeb, 0xc3, 0xc5, 0x81, 0xa0, 0xff, 0xfa, 0x13, 0x63, 0xeb,
	0x17, 0x0d, 0xdd, 0x51, 0xb7, 0xf0, 0xda, 0x49, 0xd3, 0x16, 0x55, 0x26, 0x29, 0xd4, 0x68, 0x9e,
	0x2b, 0x16, 0xbe, 0x58, 0x7d, 0x47, 0xa1, 0xfc, 0x8f, 0xf8, 0xb8, 0xd1, 0x7a, 0xd0, 0x31, 0xce,
	0x45, 0xcb, 0x3a, 0x8f, 0x95, 0x16, 0x04, 0x28, 0xaf, 0xd7, 0xfb, 0xca, 0xbb, 0x4b, 0x40, 0x7e,
}

func hmXx3Fold(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

func hmXx3Avalanche(h uint64) uint64 {
	h ^= h >> 37
	h *= 0x165667919E3779F9
	return h ^ h>>32
}

func hmXx3Rrmxmx(h uint64, n int) uint64 {
	h ^= bits.RotateLeft64(h, 49) ^ bits.RotateLeft64(h, 24)
	h *= 0x9FB21C651E98DF25
	h ^= (h >> 35) + uint64(n)
	h *= 0x9FB21C651E98DF25
	return h ^ h>>28
}

func hmXx3Mix16(b []byte, i int, s int) uint64 {
	return hmXx3Fold(hmLe64(b, i)^hmLe64(hmXx3Secret, s), hmLe64(b, i+8)^hmLe64(hmXx3Secret, s+8))
}

func hmXx3Acc512(acc *[8]uint64, b []byte, i int, s int) {
	for k := 0; k < 8; k++ {
		v := hmLe64(b, i+8*k)
		key := v ^ hmLe64(hmXx3Secret, s+8*k)
		acc[k^1] += v
		acc[k] += uint64(uint32(key)) * (key >> 32)
	}
}

// hmXxh3 is XXH3_64bits(b), seed 0, the default secret.
func hmXxh3(b []byte) uint64 {
	n := len(b)
	s := hmXx3Secret
	switch {
	case n == 0:
		return hmXx64Avalanche(hmLe64(s, 56) ^ hmLe64(s, 64))
	case n <= 3:
		c1, c2, c3 := uint32(b[0]), uint32(b[n>>1]), uint32(b[n-1])
		combined := c1<<16 | c2<<24 | c3 | uint32(n)<<8
		bitflip := uint64(hmLe32(s, 0) ^ hmLe32(s, 4))
		return hmXx64Avalanche(uint64(combined) ^ bitflip)
	case n <= 8:
		in1, in2 := hmLe32(b, 0), hmLe32(b, n-4)
		bitflip := hmLe64(s, 8) ^ hmLe64(s, 16)
		input64 := uint64(in2) + uint64(in1)<<32
		return hmXx3Rrmxmx(input64^bitflip, n)
	case n <= 16:
		bitflip1 := hmLe64(s, 24) ^ hmLe64(s, 32)
		bitflip2 := hmLe64(s, 40) ^ hmLe64(s, 48)
		lo := hmLe64(b, 0) ^ bitflip1
		hi := hmLe64(b, n-8) ^ bitflip2
		acc := uint64(n) + bits.ReverseBytes64(lo) + hi + hmXx3Fold(lo, hi)
		return hmXx3Avalanche(acc)
	case n <= 128:
		acc := uint64(n) * hmXxP64_1
		if n > 32 {
			if n > 64 {
				if n > 96 {
					acc += hmXx3Mix16(b, 48, 96)
					acc += hmXx3Mix16(b, n-64, 112)
				}
				acc += hmXx3Mix16(b, 32, 64)
				acc += hmXx3Mix16(b, n-48, 80)
			}
			acc += hmXx3Mix16(b, 16, 32)
			acc += hmXx3Mix16(b, n-32, 48)
		}
		acc += hmXx3Mix16(b, 0, 0)
		acc += hmXx3Mix16(b, n-16, 16)
		return hmXx3Avalanche(acc)
	case n <= 240:
		acc := uint64(n) * hmXxP64_1
		rounds := n / 16
		for i := 0; i < 8; i++ {
			acc += hmXx3Mix16(b, 16*i, 16*i)
		}
		acc = hmXx3Avalanche(acc)
		for i := 8; i < rounds; i++ {
			acc += hmXx3Mix16(b, 16*i, 16*(i-8)+3)
		}
		acc += hmXx3Mix16(b, n-16, 136-17)
		return hmXx3Avalanche(acc)
	}
	acc := [8]uint64{hmXxP32_3, hmXxP64_1, hmXxP64_2, hmXxP64_3, hmXxP64_4, hmXxP32_2, hmXxP64_5, hmXxP32_1}
	const stripes = (192 - 64) / 8
	const block = 64 * stripes
	blocks := (n - 1) / block
	for k := 0; k < blocks; k++ {
		for j := 0; j < stripes; j++ {
			hmXx3Acc512(&acc, b, k*block+64*j, 8*j)
		}
		for i := 0; i < 8; i++ {
			key := hmLe64(s, 192-64+8*i)
			a := acc[i]
			a ^= a >> 47
			a ^= key
			acc[i] = a * hmXxP32_1
		}
	}
	last := ((n - 1) - block*blocks) / 64
	for j := 0; j < last; j++ {
		hmXx3Acc512(&acc, b, blocks*block+64*j, 8*j)
	}
	hmXx3Acc512(&acc, b, n-64, 192-64-7)
	result := uint64(n) * hmXxP64_1
	for i := 0; i < 4; i++ {
		result += hmXx3Fold(acc[2*i]^hmLe64(s, 11+16*i), acc[2*i+1]^hmLe64(s, 11+16*i+8))
	}
	return hmXx3Avalanche(result)
}

// ---- SipHash-1-3 (Aumasson and Bernstein 2012, c = 1, d = 3) ----

// hmSip13Keyed is SipHash-1-3 of b under (k0, k1), the standard function (no
// suffix byte; std.core's str hash appends 0xFF itself).
func hmSip13Keyed(b []byte, k0, k1 uint64) uint64 {
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
	for ; n-i >= 8; i += 8 {
		m := hmLe64(b, i)
		v3 ^= m
		round()
		v0 ^= m
	}
	m := uint64(n&0xFF) << 56
	for k := 0; i+k < n; k++ {
		m |= uint64(b[i+k]) << (8 * k)
	}
	v3 ^= m
	round()
	v0 ^= m
	v2 ^= 0xFF
	round()
	round()
	round()
	return v0 ^ v1 ^ v2 ^ v3
}

// ---- BLAKE3 (the specification's reference_impl.rs, hash mode, 32 bytes) ----

var hmB3IV = [8]uint32{0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A, 0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19}

var hmB3Perm = [16]int{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}

const (
	hmB3ChunkStart = 1
	hmB3ChunkEnd   = 2
	hmB3Parent     = 4
	hmB3Root       = 8
)

func hmB3G(s *[16]uint32, a, b, c, d int, x, y uint32) {
	s[a] = s[a] + s[b] + x
	s[d] = bits.RotateLeft32(s[d]^s[a], -16)
	s[c] = s[c] + s[d]
	s[b] = bits.RotateLeft32(s[b]^s[c], -12)
	s[a] = s[a] + s[b] + y
	s[d] = bits.RotateLeft32(s[d]^s[a], -8)
	s[c] = s[c] + s[d]
	s[b] = bits.RotateLeft32(s[b]^s[c], -7)
}

func hmB3Compress(cv [8]uint32, m [16]uint32, counter uint64, blen uint32, flags uint32) [16]uint32 {
	s := [16]uint32{cv[0], cv[1], cv[2], cv[3], cv[4], cv[5], cv[6], cv[7],
		hmB3IV[0], hmB3IV[1], hmB3IV[2], hmB3IV[3], uint32(counter), uint32(counter >> 32), blen, flags}
	for r := 0; r < 7; r++ {
		hmB3G(&s, 0, 4, 8, 12, m[0], m[1])
		hmB3G(&s, 1, 5, 9, 13, m[2], m[3])
		hmB3G(&s, 2, 6, 10, 14, m[4], m[5])
		hmB3G(&s, 3, 7, 11, 15, m[6], m[7])
		hmB3G(&s, 0, 5, 10, 15, m[8], m[9])
		hmB3G(&s, 1, 6, 11, 12, m[10], m[11])
		hmB3G(&s, 2, 7, 8, 13, m[12], m[13])
		hmB3G(&s, 3, 4, 9, 14, m[14], m[15])
		if r < 6 {
			var p [16]uint32
			for i := range p {
				p[i] = m[hmB3Perm[i]]
			}
			m = p
		}
	}
	for i := 0; i < 8; i++ {
		s[i] ^= s[i+8]
		s[i+8] ^= cv[i]
	}
	return s
}

func hmB3Words(block []byte) [16]uint32 {
	var buf [64]byte
	copy(buf[:], block)
	var m [16]uint32
	for i := range m {
		m[i] = hmLe32(buf[:], 4*i)
	}
	return m
}

func hmB3First8(s [16]uint32) (cv [8]uint32) {
	copy(cv[:], s[:8])
	return
}

// hmBlake3 is BLAKE3(b), 32 bytes, written as the reference's incremental
// hasher fed in one piece (chunk state, CV stack merged by the chunk count).
func hmBlake3(b []byte) [32]byte {
	var stack [][8]uint32
	cv := hmB3IV
	var chunk uint64
	var block []byte
	blocks := 0
	feed := func(p []byte) {
		for len(p) > 0 {
			if len(block) == 64 {
				flags := uint32(0)
				if blocks == 0 {
					flags |= hmB3ChunkStart
				}
				cv = hmB3First8(hmB3Compress(cv, hmB3Words(block), chunk, 64, flags))
				blocks++
				block = block[:0]
			}
			if blocks*64+len(block) == 1024 {
				panic("unreachable")
			}
			take := min(64-len(block), len(p))
			block = append(block, p[:take]...)
			p = p[take:]
		}
	}
	chunkFlags := func() uint32 {
		f := uint32(hmB3ChunkEnd)
		if blocks == 0 {
			f |= hmB3ChunkStart
		}
		return f
	}
	for len(b) > 0 {
		if blocks*64+len(block) == 1024 {
			out := hmB3First8(hmB3Compress(cv, hmB3Words(block), chunk, uint32(len(block)), chunkFlags()))
			total := chunk + 1
			for total&1 == 0 {
				left := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				var m [16]uint32
				copy(m[:8], left[:])
				copy(m[8:], out[:])
				out = hmB3First8(hmB3Compress(hmB3IV, m, 0, 64, hmB3Parent))
				total >>= 1
			}
			stack = append(stack, out)
			chunk++
			cv = hmB3IV
			block = block[:0]
			blocks = 0
		}
		take := min(1024-(blocks*64+len(block)), len(b))
		feed(b[:take])
		b = b[take:]
	}
	// The output node: the chunk's last block, then parents down the stack.
	ocv, om, ocount, olen, oflags := cv, hmB3Words(block), chunk, uint32(len(block)), chunkFlags()
	for k := len(stack) - 1; k >= 0; k-- {
		child := hmB3First8(hmB3Compress(ocv, om, ocount, olen, oflags))
		var m [16]uint32
		copy(m[:8], stack[k][:])
		copy(m[8:], child[:])
		ocv, om, ocount, olen, oflags = hmB3IV, m, 0, 64, hmB3Parent
	}
	s := hmB3Compress(ocv, om, 0, olen, oflags|hmB3Root)
	var out [32]byte
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(out[4*i:], s[i])
	}
	return out
}

// ---- published vectors ----

// hmXxVectors are xxHash's sanity strings with their XXH3-64 (seed 0) and
// XXH64 (seed 0) values, as Zig 0.17.0's std/hash/xxhash.zig tests carry
// them (from xxHash's own sanity checks). A zero means no published value.
var hmXxVectors = []struct {
	in       string
	xx3, x64 uint64
}{
	{"", 0x2d06800538d394c2, 0xef46db3751d8e999},
	{"a", 0xe6c632b61e964e1f, 0xd24ec4f1a98c6e5b},
	{"abc", 0x78af5f94892f3950, 0x44bc2cf5ad770999},
	{"message", 0x0b1ca9b8977554fa, 0},
	{"message digest", 0x160d8e9329be94f9, 0x066ed728fceeb3be},
	{"abcdefghijklmnopqrstuvwxyz", 0x810f9ca067fbb90c, 0xcfe1f278fa89835c},
	{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", 0x643542bb51639cb2, 0xaaa46907d3047814},
	{strings.Repeat("1234567890", 8), 0x7f58aa2520c681f9, 0xe04a477f19ee145d},
	{strings.Repeat("1234567890", 21) + "12345678", 0xb66ea795b5edc38c, 0},
	{strings.Repeat("1234567890", 32), 0x8845e0b1b57330de, 0},
	{strings.Repeat("1234567890", 32) + "123123", 0xf031f373d63c5653, 0},
	{strings.Repeat("1234567890", 320), 0xf1bf601f9d868dce, 0},
}

// hmB3Vectors are BLAKE3's test_vectors.json, hash mode, the first 32 bytes:
// input byte i is i % 251 (as Zig 0.17.0's std/crypto/hmBlake3.zig carries it).
var hmB3Vectors = []struct {
	n   int
	hex string
}{
	{0, "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262"},
	{1, "2d3adedff11b61f14c886e35afa036736dcd87a74d27b5c1510225d0f592e213"},
	{1023, "10108970eeda3eb932baac1428c7a2163b0e924c9a9e25b35bba72b28f70bd11"},
	{1024, "42214739f095a406f3fc83deb889744ac00df831c10daa55189b5d121c855af7"},
	{1025, "d00278ae47eb27b34faecf67b4fe263f82d5412916c1ffd97c8cb7fb814b8444"},
	{2048, "e776b6028c7cd22a4d0ba182a8bf62205d2ef576467e838ed6f2529b85fba24a"},
	{2049, "5f4d72f40d7a5f82b15ca2b2e44b1de3c2ef86c426c95c1af0b6879522563030"},
	{3072, "b98cb0ff3623be03326b373de6b9095218513e64f1ee2edd2525c7ad1e5cffd2"},
	{3073, "7124b49501012f81cc7f11ca069ec9226cecb8a2c850cfe644e327d22d3e1cd3"},
	{4096, "015094013f57a5277b59d8475c0501042c0b642e531b0a1c8f58d2163229e969"},
	{4097, "9b4052b38f1c5fc8b1f9ff7ac7b27cd242487b3d890d15c96a1c25b8aa0fb995"},
	{5120, "9cadc15fed8b5d854562b26a9536d9707cadeda9b143978f319ab34230535833"},
	{5121, "628bd2cb2004694adaab7bbd778a25df25c47b9d4155a55f8fbd79f2fe154cff"},
	{6144, "3e2e5b74e048f3add6d21faab3f83aa44d3b2278afb83b80b3c35164ebeca205"},
	{6145, "f1323a8631446cc50536a9f705ee5cb619424d46887f3c376c695b70e0f0507f"},
	{7168, "61da957ec2499a95d6b8023e2b0e604ec7f6b50e80a9678b89d2628e99ada77a"},
	{7169, "a003fc7a51754a9b3c7fae0367ab3d782dccf28855a03d435f8cfe74605e7817"},
	{8192, "aae792484c8efe4f19e2ca7d371d8c467ffb10748d8a5a1ae579948f718a2a63"},
	{8193, "bab6c09cb8ce8cf459261398d2e7aef35700bf488116ceb94a36d0f5f1b7bc3b"},
	{16384, "f875d6646de28985646f34ee13be9a576fd515f76b5b0a26bb324735041ddde4"},
	{31744, "62b6960e1a44bcc1eb1a611a8d6235b6b4b78f32e7abc4fb4c6cdcce94895c47"},
	{102400, "bc3e3d41a1146b069abffad3c0d44860cf664390afce4d9661f7902e7943e085"},
}

// hmSip13Vectors are SipHash-1-3 of bytes 0..i-1 under the key 00..0f, as
// Rust's coretests/tests/hash/sip.rs carries them (little-endian words).
var hmSip13Vectors = [64]uint64{
	0xabac0158050fc4dc, 0xc9f49bf37d57ca93, 0x82cb9b024dc7d44d, 0x8bf80ab8e7ddf7fb,
	0xcf75576088d38328, 0xdef9d52f49533b67, 0xc50d2b50c59f22a7, 0xd3927d989bb11140,
	0x369095118d299a8e, 0x25a48eb36c063de4, 0x79de85ee92ff097f, 0x70c118c1f94dc352,
	0x78a384b157b4d9a2, 0x306f760c1229ffa7, 0x605aa111c0f95d34, 0xd320d86d2a519956,
	0xcc4fdd1a7d908b66, 0x9cf2689063dbd80c, 0x8ffc389cb473e63e, 0xf21f9de58d297d1c,
	0xc0dc2f46a6cce040, 0xb992abfe2b45f844, 0x7ffe7b9ba320872e, 0x525a0e7fdae6c123,
	0xf464aeb267349c8c, 0x45cd5928705b0979, 0x3a3e35e3ca9913a5, 0xa91dc74e4ade3b35,
	0xfb0bed02ef6cd00d, 0x88d93cb44ab1e1f4, 0x540f11d643c5e663, 0x2370dd1f8c21d1bc,
	0x81157b6c16a7b60d, 0x4d54b9e57a8ff9bf, 0x759f12781f2a753e, 0xcea1a3bebf186b91,
	0x2cf508d3ada26206, 0xb6101c2da3c33057, 0xb3f47496ae3a36a1, 0x626b57547b108392,
	0xc1d2363299e41531, 0x667cc1923f1ad944, 0x65704ffec8138825, 0x24f280d1c28949a6,
	0xc2ca1cedfaf8876b, 0xc2164bfc9f042196, 0xa16e9c9368b1d623, 0x49fb169c8b5114fd,
	0x9f3143f8df074c46, 0xc6fdaf2412cc86b3, 0x7eaf49d10a52098f, 0x1cf313559d292f9a,
	0xc44a30dda2f41f12, 0x36fae98943a71ed0, 0x318fb34c73f0bce6, 0xa27abf3670a7e980,
	0xb4bcc0db243c6d75, 0x23f8d852fdb71513, 0x8f035f4da67d8a08, 0xd89cd0e5b7e8f148,
	0xf6f4e6bcf7a644ee, 0xaec59ad80f1837f2, 0xc3b2f6154b6694e0, 0x9d199062b7bbb3a8,
}

func hmB3Input(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// TestSTDHASHMOD_ModelsMatchPublishedVectors checks the Go models before any
// Mk code is compared with them.
func TestSTDHASHMOD_ModelsMatchPublishedVectors(t *testing.T) {
	for _, v := range hmXxVectors {
		if got := hmXxh3([]byte(v.in)); got != v.xx3 {
			t.Errorf("hmXxh3(len %d) = %#x, want %#x", len(v.in), got, v.xx3)
		}
		if v.x64 != 0 {
			if got := hmXxh64([]byte(v.in), 0); got != v.x64 {
				t.Errorf("hmXxh64(len %d) = %#x, want %#x", len(v.in), got, v.x64)
			}
		}
	}
	for _, v := range hmB3Vectors {
		got := hmBlake3(hmB3Input(v.n))
		if hex.EncodeToString(got[:]) != v.hex {
			t.Errorf("hmBlake3(len %d) = %x, want %s", v.n, got, v.hex)
		}
	}
	msg := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		if got := hmSip13Keyed(msg, 0x0706050403020100, 0x0f0e0d0c0b0a0908); got != hmSip13Vectors[i] {
			t.Errorf("sip13(len %d) = %#x, want %#x", i, got, hmSip13Vectors[i])
		}
		msg = append(msg, byte(i))
	}
	// std.core's table hash is SipHash-1-3 of the same function: the
	// existing oracle (hash_test.go) must agree with the standard model.
	for n := 0; n < 40; n++ {
		b := hmB3Input(n)
		if sip13(b) != hmSip13Keyed(b, 0x0706050403020100, 0x0f0e0d0c0b0a0908) {
			t.Fatalf("hash_test.go's sip13 and the standard model differ at len %d", n)
		}
	}
}
