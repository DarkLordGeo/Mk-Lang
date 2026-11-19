// Why: ToUtf16 and FromUtf16 take 16 bytes or 8 to 16 units per vector step,
// and must give exactly what the scalar code gives.
// What: TestSTDVECTOR_TextUtf16Diff, against the scalar versions (svOldUtf16).
// How: seeded inputs: ASCII runs around every vector edge, 2- to 4-byte
// scalars, surrogate pairs, unpaired and reversed surrogates at every
// position, lengths 0 to 300 and some of 4,000 to 8,000.

package std

import (
	"fmt"
	"strings"
	"testing"
)

const svTextFixture = `// Test fixture (std/std-vector_text_test.go): ToUtf16 and FromUtf16
// against the scalar versions they replaced.
` + svRngMk + `
// The scalar versions, verbatim but for their names.
SVOLDUTF16

// SvScalars writes n bytes of UTF-8 into b: runs of ASCII and of 2-, 3- and
// 4-byte scalars, a run of up to 40 each, the kind and length drawn from r.
SvScalars(r: mut SvRng, b: mut []u8, n: int): int {
    var i := 0
    for i < n {
        kind := SvR(r, 5)
        run := SvR(r, 41)
        for _ in 0..run {
            if kind <= 1 {
                if i + 1 > n {
                    return i
                }
                b[i] = Trunc[u8](0x20 + SvR(r, 95))
                i += 1
            } else if kind == 2 {
                if i + 2 > n {
                    return i
                }
                c := 0x80 + SvR(r, 0x780)
                b[i] = Trunc[u8](0xC0 | (c >> 6))
                b[i + 1] = Trunc[u8](0x80 | (c & 0x3F))
                i += 2
            } else if kind == 3 {
                if i + 3 > n {
                    return i
                }
                var c := 0x800 + SvR(r, 0xF800)
                if c >= 0xD800 && c <= 0xDFFF {
                    c = 0xFFFD
                }
                b[i] = Trunc[u8](0xE0 | (c >> 12))
                b[i + 1] = Trunc[u8](0x80 | ((c >> 6) & 0x3F))
                b[i + 2] = Trunc[u8](0x80 | (c & 0x3F))
                i += 3
            } else {
                if i + 4 > n {
                    return i
                }
                c := 0x10000 + SvR(r, 0x100000)
                b[i] = Trunc[u8](0xF0 | (c >> 18))
                b[i + 1] = Trunc[u8](0x80 | ((c >> 12) & 0x3F))
                b[i + 2] = Trunc[u8](0x80 | ((c >> 6) & 0x3F))
                b[i + 3] = Trunc[u8](0x80 | (c & 0x3F))
                i += 4
            }
        }
    }
    return i
}

// SvUnits writes n UTF-16 units into u: runs of ASCII, of units below
// 0x800, of other BMP units and of surrogate pairs; with bad set, one
// unit at a drawn position becomes a lone high, a lone low, or the pair
// there is reversed.
SvUnits(r: mut SvRng, u: mut []u16, n: int, bad: bool) {
    var i := 0
    for i < n {
        kind := SvR(r, 5)
        run := SvR(r, 41)
        for _ in 0..run {
            if i >= n {
                break
            }
            if kind <= 1 {
                u[i] = Trunc[u16](SvR(r, 0x80))
            } else if kind == 2 {
                u[i] = Trunc[u16](0x80 + SvR(r, 0x780))
            } else if kind == 3 {
                var c := 0x800 + SvR(r, 0xF800)
                if c >= 0xD800 && c <= 0xDFFF {
                    c = 0xE000
                }
                u[i] = Trunc[u16](c)
            } else if i + 2 <= n {
                u[i] = Trunc[u16](0xD800 + SvR(r, 0x400))
                u[i + 1] = Trunc[u16](0xDC00 + SvR(r, 0x400))
                i += 1
            } else {
                u[i] = 0x41
            }
            i += 1
        }
    }
    if bad && n > 0 {
        at := SvR(r, n)
        k := SvR(r, 3)
        if k == 0 {
            u[at] = Trunc[u16](0xD800 + SvR(r, 0x400))
            if at + 1 < n && u[at + 1] >= 0xDC00 && u[at + 1] <= 0xDFFF {
                u[at + 1] = 0x41
            }
        } else if k == 1 {
            u[at] = Trunc[u16](0xDC00 + SvR(r, 0x400))
        } else if at + 1 < n {
            u[at] = Trunc[u16](0xDC00 + SvR(r, 0x400))
            u[at + 1] = Trunc[u16](0xD800 + SvR(r, 0x400))
        }
    }
}

export SvTextRun(sys: &System, seed: u64): OutOfMemory|Utf8Error|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var b := try sys.arena.Slice[u8](9000)
    var u := try sys.arena.Slice[u16](9000)
    var to_cases := 0
    var to_bad := 0
    var from_cases := 0
    var from_errs := 0
    var from_bad := 0
    for c in 0..4000 {
        var n := SvR(mut rng, 301)
        if c % 200 == 0 {
            n = 4000 + SvR(mut rng, 4000)
        }
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        // ToUtf16 from every start offset 0 to 3 of the text.
        m := SvScalars(mut rng, b, n)
        s := try str.From(b[0..m])
        for off in 0..4 {
            if off > m {
                break
            }
            t := s[0..m]
            if off > 0 && t.Len() > 0 {
                // Cut at the first scalar boundary at or after off.
                var j := off
                for j < m && b[j] & 0xC0 == 0x80 {
                    j += 1
                }
                x := try ToUtf16(&ar, s[j..m])
                y := try SvToUtf16Old(&ar, s[j..m])
                to_cases += 1
                if !SvEq16(x, y) {
                    to_bad += 1
                }
                continue
            }
            x := try ToUtf16(&ar, t)
            y := try SvToUtf16Old(&ar, t)
            to_cases += 1
            if !SvEq16(x, y) {
                to_bad += 1
            }
        }
        // FromUtf16 of well-formed and of broken units.
        for k in 0..2 {
            SvUnits(mut rng, u, n, k == 1)
            for off in 0..3 {
                if off > n {
                    break
                }
                us := u[off..n]
                from_cases += 1
                match FromUtf16(&ar, us) {
                    ok(x) {
                        match SvFromUtf16Old(&ar, us) {
                            ok(y) {
                                if x != y {
                                    from_bad += 1
                                }
                            }
                            err(_) {
                                from_bad += 1
                            }
                        }
                    }
                    err(_) {
                        from_errs += 1
                        match SvFromUtf16Old(&ar, us) {
                            ok(_) {
                                from_bad += 1
                            }
                            err(_) {}
                        }
                    }
                }
            }
        }
    }
    try Print(mut out, "toutf16 {to_cases} bad {to_bad}\n", (to_cases, to_bad))
    try Print(mut out, "fromutf16 {from_cases} errors {from_errs} bad {from_bad}\n", (from_cases, from_errs, from_bad))
    try out.Flush()
}

SvEq16(x: []u16, y: []u16): bool {
    if x.Len() != y.Len() {
        return false
    }
    for i in 0..x.Len() {
        if x[i] != y[i] {
            return false
        }
    }
    return true
}
`

const svTextMain = `import std.text

export Main(sys: &System): OutOfMemory|Utf8Error|WriteError!void {
    try text.SvTextRun(sys, 0x2545F4914F6CDD1D)
}
`

// TestSTDVECTOR_TextUtf16Diff is ToUtf16 and FromUtf16 against their
// scalar versions, dev and release.
func TestSTDVECTOR_TextUtf16Diff(t *testing.T) {
	t.Parallel()
	fx := map[string]string{"text/zz_vectest.mk": strings.Replace(svTextFixture, "SVOLDUTF16", svOldUtf16, 1)}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svTextMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			var tc, tb, fc, fe, fb int
			if _, err := fmt.Sscanf(out, "toutf16 %d bad %d\nfromutf16 %d errors %d bad %d\n", &tc, &tb, &fc, &fe, &fb); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			t.Logf("toutf16 %d cases, fromutf16 %d cases (%d errors)", tc, fc, fe)
			if tb != 0 || fb != 0 {
				t.Fatalf("differences: toutf16 %d, fromutf16 %d", tb, fb)
			}
			if tc < 12000 || fc < 20000 || fe < 3000 || fe > fc-8000 {
				t.Fatalf("cases not reached: %s", out)
			}
		})
	}
}

// svOldUtf16 is std/text/utf.mk's scalar ToUtf16 and FromUtf16, the
// reference.
const svOldUtf16 = `SvToUtf16Old(a: &Arena, s: str): OutOfMemory![]u16 from a {
    b := s.AsBytes()
    n := b.Len()
    // Units: every byte that is not a continuation (10xxxxxx), plus one
    // more for each four-byte lead (11110xxx), eight bytes a step.
    var units := n
    var i := 0
    for i + 8 <= n {
        w := Word(b, i)
        cont := w & ~(w << 1) & HighBits
        four := w & (w << 1) & (w << 2) & (w << 3) & HighBits
        units = units - Count8(cont) + Count8(four)
        i += 8
    }
    for x in b[i..] {
        if x & 0xC0 == 0x80 {
            units -= 1
        }
        if x >= 0xF0 {
            units += 1
        }
    }
    out := try a.Slice[u16](units)
    i = 0
    var o := 0
    for i < n {
        x := u32(b[i])
        if x < 0x80 {
            // The eight-byte probe runs only from an ASCII byte, so mixed
            // text pays it once per ASCII run, not once per scalar.
            if i + 8 <= n {
                w := Word(b, i)
                if w & HighBits == 0 {
                    // Eight ASCII bytes widen to eight units.
                    var d := mut out[o..o + 8]
                    for k in 0..8 {
                        d[k] = Trunc[u16](w >> (8 * k)) & 0xFF
                    }
                    o += 8
                    i += 8
                    continue
                }
            }
            out[o] = Trunc[u16](x)
            o += 1
            i += 1
            continue
        }
        if x < 0xE0 {
            out[o] = Trunc[u16](((x & 0x1F) << 6) | (u32(b[i + 1]) & 0x3F))
            o += 1
            i += 2
            continue
        }
        if x < 0xF0 {
            q := b[i..i + 3]
            out[o] = Trunc[u16](((x & 0x0F) << 12) | ((u32(q[1]) & 0x3F) << 6) | (u32(q[2]) & 0x3F))
            o += 1
            i += 3
            continue
        }
        q := b[i..i + 4]
        v := (((x & 0x07) << 18) | ((u32(q[1]) & 0x3F) << 12) | ((u32(q[2]) & 0x3F) << 6) | (u32(q[3]) & 0x3F)) - 0x10000
        var d := mut out[o..o + 2]
        d[0] = Trunc[u16](0xD800 + (v >> 10))
        d[1] = Trunc[u16](0xDC00 + (v & 0x3FF))
        o += 2
        i += 4
    }
    return out[..]
}

// The text the UTF-16 code units spell. Utf8Error on an unpaired
// surrogate, a high one (D800 to DBFF) not followed by a low one (DC00 to
// DFFF) or a low one not after a high one; that is found before anything
// is allocated. Then one allocation of exactly the bytes needed.
SvFromUtf16Old(a: &Arena, units: []u16): Utf8Error|OutOfMemory!str from a {
    n := units.Len()
    var bytes := 0
    var i := 0
    for i < n {
        u := u32(units[i])
        if u & 0xF800 != 0xD800 {
            // 1, 2 or 3 bytes, without a branch on which.
            bytes += 1 + Trunc[int](((0x7F -% u) >> 31) + ((0x7FF -% u) >> 31))
            i += 1
            continue
        }
        if u <= 0xDBFF && i + 1 < n && units[i + 1] >= 0xDC00 && units[i + 1] <= 0xDFFF {
            bytes += 4
            i += 2
            continue
        }
        fail Utf8Error
    }
    out := try a.Slice[u8](bytes)
    var o := 0
    i = 0
    for i < n {
        u := u32(units[i])
        if u < 0x80 {
            // The four-unit probe runs only from an ASCII unit.
            if i + 4 <= n {
                q := units[i..i + 4]
                if q[0] | q[1] | q[2] | q[3] < 0x80 {
                    var d := mut out[o..o + 4]
                    for k in 0..4 {
                        d[k] = Trunc[u8](q[k])
                    }
                    o += 4
                    i += 4
                    continue
                }
            }
            out[o] = Trunc[u8](u)
            o += 1
            i += 1
            continue
        }
        if u < 0x800 {
            var d := mut out[o..o + 2]
            d[0] = Trunc[u8](0xC0 | (u >> 6))
            d[1] = Trunc[u8](0x80 | (u & 0x3F))
            o += 2
            i += 1
            continue
        }
        if u & 0xF800 != 0xD800 {
            var d := mut out[o..o + 3]
            d[0] = Trunc[u8](0xE0 | (u >> 12))
            d[1] = Trunc[u8](0x80 | ((u >> 6) & 0x3F))
            d[2] = Trunc[u8](0x80 | (u & 0x3F))
            o += 3
            i += 1
            continue
        }
        // A high surrogate; pass 1 proved a low one follows.
        c := 0x10000 + ((u - 0xD800) << 10) + (u32(units[i + 1]) - 0xDC00)
        var d := mut out[o..o + 4]
        d[0] = Trunc[u8](0xF0 | (c >> 18))
        d[1] = Trunc[u8](0x80 | ((c >> 12) & 0x3F))
        d[2] = Trunc[u8](0x80 | ((c >> 6) & 0x3F))
        d[3] = Trunc[u8](0x80 | (c & 0x3F))
        o += 4
        i += 2
    }
    return try str.From(out[..])
}
`
