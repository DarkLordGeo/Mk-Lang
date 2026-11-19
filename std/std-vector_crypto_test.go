// Why: ChaXor runs four ChaCha20 blocks across vector lanes while 256 bytes
// remain (ChaXor4), and must give the same bytes as the one-block loop.
// What: TestSTDVECTOR_CryptoChaXorDiff, ChaXor against the one-block loop
// (kept below as the reference).
// How: every length 0 to 1,100 and longer ones, seeded keys, nonces and
// counters, and counters that wrap inside a four-block step (2^32 - 1 to
// 2^32 - 4). TestSTDCRYPTO_* check the RFC 8439 vectors.

package std

import (
	"fmt"
	"strings"
	"testing"
)

const svCryptoFixture = `// Test fixture (std/std-vector_crypto_test.go): ChaXor against the
// one-block loop.
` + svRngMk + `
// SvChaXorScalar is chacha20.mk's ChaXor before std-vector.
SvChaXorScalar(key: &ChaKey, ctr: u32, src: []u8, dst: mut []u8) {
    n := src.Len()
    var i := 0
    var c := ctr
    for n - i >= 64 {
        ks := ChaBlock(key, c)
        s := src[i..n][0..64]
        t := mut dst[i..n]
        d := mut t[0..64]
        for w in 0..16 {
            PutLe32(mut d[..], 4 * w, Le32(s, 4 * w) ^ ks[w])
        }
        c = c +% 1
        i += 64
    }
    if i < n {
        ks := ChaBlock(key, c)
        var b: [64]u8 = [0; 64]
        for w in 0..16 {
            PutLe32(mut b[..], 4 * w, ks[w])
        }
        for j in i..n {
            dst[j] = src[j] ^ b[j - i]
        }
    }
}

export SvCryptoRun(sys: &System, seed: u64): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var src := try sys.arena.Slice[u8](70000)
    var d1 := try sys.arena.Slice[u8](70000)
    var d2 := try sys.arena.Slice[u8](70000)
    for i in 0..70000 {
        src[i] = Trunc[u8](SvNext(mut rng))
    }
    var cases := 0
    var bad := 0
    for c in 0..1300 {
        var n := c
        if c > 1100 {
            n = 1101 + SvR(mut rng, 68000)
        }
        var k: [8]u32 = [0; 8]
        for w in 0..8 {
            k[w] = Trunc[u32](SvNext(mut rng))
        }
        key := ChaKey(k: k, n: [Trunc[u32](SvNext(mut rng)), Trunc[u32](SvNext(mut rng)), Trunc[u32](SvNext(mut rng))])
        var ctr := Trunc[u32](SvNext(mut rng))
        if c % 4 == 0 {
            ctr = 0xFFFFFFFF - Trunc[u32](SvR(mut rng, 4))
        } else if c % 4 == 1 {
            ctr = Trunc[u32](SvR(mut rng, 3))
        }
        off := SvR(mut rng, 16)
        s := src[off..off + n]
        ChaXor(&key, ctr, s, mut d1[0..n])
        SvChaXorScalar(&key, ctr, s, mut d2[0..n])
        cases += 1
        for i in 0..n {
            if d1[i] != d2[i] {
                bad += 1
                break
            }
        }
    }
    try Print(mut out, "chaxor {cases} bad {bad}\n", (cases, bad))
    try out.Flush()
}
`

const svCryptoMain = `import std.crypto

export Main(sys: &System): OutOfMemory|WriteError!void {
    try crypto.SvCryptoRun(sys, 0x853C49E6748FEA9B)
}
`

// TestSTDVECTOR_CryptoChaXorDiff is ChaXor against the one-block loop, dev
// and release.
func TestSTDVECTOR_CryptoChaXorDiff(t *testing.T) {
	t.Parallel()
	fx := map[string]string{"crypto/zz_vectest.mk": svCryptoFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svCryptoMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			var cases, bad int
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if _, err := fmt.Sscanf(out, "chaxor %d bad %d\n", &cases, &bad); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			if bad != 0 || cases != 1300 {
				t.Fatalf("chaxor: %d cases, %d differ", cases, bad)
			}
		})
	}
}
