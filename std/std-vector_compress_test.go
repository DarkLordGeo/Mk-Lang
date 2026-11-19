// Why: Common, deflate's match length, compares 8 bytes per step, and a
// word-at-a-time scan is easy to get wrong at the edges.
// What: TestSTDVECTOR_CompressCommonDiff, Common against a byte loop.
// How: every common-prefix length 0 to 300 at every alignment of a and b
// (the first difference planted at each position and byte distance) and runs
// up to 70,000 bytes (MatchLong's range). TestSTDCOMPRESS_* cover Deflate.

package std

import (
	"fmt"
	"strings"
	"testing"
)

const svCompressFixture = `// Test fixture (std/std-vector_compress_test.go): Common against a byte
// loop.
` + svRngMk + `
SvCommonScalar(data: []u8, a: int, b: int, max: int): int {
    var k := 0
    for k < max && data[a + k] == data[b + k] {
        k += 1
    }
    return k
}

export SvCompressRun(sys: &System, seed: u64): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var d := try sys.arena.Slice[u8](150000)
    var cases := 0
    var bad := 0
    for c in 0..6000 {
        var l := c % 301
        if c >= 5800 {
            l = 300 + SvR(mut rng, 70000)
        }
        a := SvR(mut rng, 32)
        b := a + 1 + SvR(mut rng, 40000)
        // data[b..b + l] copies data[a..a + l], then one byte differs.
        for i in 0..l + 1 {
            d[a + i] = Trunc[u8](SvNext(mut rng))
        }
        for i in 0..l {
            d[b + i] = d[a + i]
        }
        d[b + l] = d[a + l] ^ Trunc[u8](1 + SvR(mut rng, 255))
        if b < a + l + 1 {
            // Overlapping copy (distance below the length): fill as LZ77
            // does, byte by byte.
            for i in 0..l {
                d[b + i] = d[a + i]
            }
            d[b + l] = d[a + l] ^ 1
        }
        for max in [l, l + 1, l + 2, l / 2, 300] {
            if b + max <= d.Len() {
                cases += 1
                if Common(d[..], a, b, max) != SvCommonScalar(d[..], a, b, max) {
                    bad += 1
                }
            }
        }
    }
    try Print(mut out, "common {cases} bad {bad}\n", (cases, bad))
    try out.Flush()
}
`

const svCompressMain = `import std.compress

export Main(sys: &System): OutOfMemory|WriteError!void {
    try compress.SvCompressRun(sys, 0x6A09E667F3BCC909)
}
`

// TestSTDVECTOR_CompressCommonDiff is Common against a byte loop, dev and
// release.
func TestSTDVECTOR_CompressCommonDiff(t *testing.T) {
	t.Parallel()
	fx := map[string]string{"compress/zz_vectest.mk": svCompressFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svCompressMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			var cases, bad int
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if _, err := fmt.Sscanf(out, "common %d bad %d\n", &cases, &bad); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			if bad != 0 || cases < 25000 {
				t.Fatalf("common: %d cases, %d differ", cases, bad)
			}
		})
	}
}
