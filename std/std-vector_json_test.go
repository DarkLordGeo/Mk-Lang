// Why: NextSpecial, the 16-lane scan behind ParseStr, PutStr and WriteStr,
// must find the same byte as a plain loop from any start.
// What: TestSTDVECTOR_JsonScanDiff, NextSpecial against a byte loop.
// How: every start of seeded random and adversarial buffers ('"', '\', bytes
// up to 0x20, 0x7F to 0xFF, runs longer than a vector and shorter than a
// word). TestSTDJSON_* check the parser against Go's encoding/json.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// svRngMk is a seeded xorshift generator in Mk, for fixtures.
const svRngMk = `
struct SvRng {
    x: u64
}

SvNext(r: mut SvRng): u64 {
    r.x = r.x ^ (r.x << 13)
    r.x = r.x ^ (r.x >> 7)
    r.x = r.x ^ (r.x << 17)
    return r.x
}

// SvR is a number in 0..k.
SvR(r: mut SvRng, k: int): int {
    return Cast[int](SvNext(r) % Cast[u64](k))
}
`

const svJsonFixture = `// Test fixture (std/std-vector_json_test.go): NextSpecial against the
// byte loop.
` + svRngMk + `
SvSpecialScalar(b: []u8, i0: int): int {
    var i := i0
    for i < b.Len() {
        c := b[i]
        if c == 0x22 || c == 0x5C || c < 0x20 {
            return i
        }
        i += 1
    }
    return b.Len()
}

const SvAlpha: [12]u8 = [0x22, 0x5C, 0x00, 0x1F, 0x20, 0x21, 0x5B, 0x5D, 0x7F, 0x80, 0xC3, 0xFF]

export SvJsonRun(sys: &System, seed: u64): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var buf := try sys.arena.Slice[u8](5000)
    var cases := 0
    var bad := 0
    for c in 0..3000 {
        var n := SvR(mut rng, 200)
        if c % 100 == 0 {
            n = 1000 + SvR(mut rng, 4000)
        }
        // Mode 0: special bytes dense; 1: one in 40; 2: one at a random
        // position in printable text, or none.
        mode := c % 3
        at := SvR(mut rng, n + 1)
        var i := 0
        for i < n {
            x := SvNext(mut rng)
            if mode == 0 || (mode == 1 && x % 40 == 0) || (mode == 2 && i == at) {
                ch := SvAlpha[Cast[int]((x >> 8) % 12)]
                if ch < 0x80 {
                    buf[i] = ch
                    i += 1
                } else if i + 3 <= n {
                    // A byte from 0x80 up appears as UTF-8: U+00E9, U+07FF
                    // or U+FFFF, so its lanes are above 0x7F.
                    k := Cast[int]((x >> 12) % 3)
                    if k == 0 {
                        buf[i] = 0xC3
                        buf[i + 1] = 0xA9
                        i += 2
                    } else if k == 1 {
                        buf[i] = 0xDF
                        buf[i + 1] = 0xBF
                        i += 2
                    } else {
                        buf[i] = 0xEF
                        buf[i + 1] = 0xBF
                        buf[i + 2] = 0xBF
                        i += 3
                    }
                } else {
                    buf[i] = 0x7E
                    i += 1
                }
            } else {
                buf[i] = Trunc[u8](0x20 + (x >> 16) % 95)
                if buf[i] == 0x22 || buf[i] == 0x5C {
                    buf[i] = 0x61
                }
                i += 1
            }
        }
        s := str.From(buf[0..n]).Must()
        for i0 in 0..n + 1 {
            cases += 1
            if NextSpecial(s, i0) != SvSpecialScalar(s.AsBytes(), i0) {
                bad += 1
            }
        }
    }
    try Print(mut out, "nextspecial {cases} bad {bad}\n", (cases, bad))
    try out.Flush()
}
`

const svJsonMain = `import std.json

export Main(sys: &System): OutOfMemory|WriteError!void {
    try json.SvJsonRun(sys, 0xD1B54A32D192ED03)
}
`

// TestSTDVECTOR_JsonScanDiff is NextSpecial against the byte loop, dev and
// release.
func TestSTDVECTOR_JsonScanDiff(t *testing.T) {
	t.Parallel()
	fx := map[string]string{"json/zz_vectest.mk": svJsonFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svJsonMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			var cases, bad int
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if _, err := fmt.Sscanf(out, "nextspecial %d bad %d\n", &cases, &bad); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			t.Logf("nextspecial %d cases", cases)
			if bad != 0 || cases < 300000 {
				t.Fatalf("nextspecial: %d cases, %d differ", cases, bad)
			}
		})
	}
}
