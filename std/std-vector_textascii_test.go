// Why: Width, FoldCase and Normalize's Span take 16 ASCII bytes per vector
// step, and must match the scalar code and build without vector registers.
// What: TestSTDVECTOR_TextAsciiDiff against the scalar versions (svOldAscii),
// and TestSTDVECTOR_TextGeneralRegsOnly (no E1108 in a freestanding bin).
// How: mostly-ASCII seeded text with non-ASCII scalars planted (marks, ZWJ,
// emoji, Hangul, CJK, multi-scalar folds), compared from many starts.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const svTextAsciiFixture = `// Test fixture (std/std-vector_textascii_test.go): Width, FoldCase and
// Span against the versions they replaced.
` + svRngMk + `
// The versions before std-vector, verbatim but for their names.
SVOLDASCII

// Picks are the non-ASCII scalars planted in the text.
const Picks: [20]u32 = [0x301, 0x903, 0xE9, 0xC4, 0xDF, 0x4E00, 0x1F600, 0xFE0F, 0xFE0E, 0x2764, 0x1100, 0x1161, 0xAC00, 0x130, 0x200D, 0x1F1FA, 0x587, 0x390, 0x1E9E, 0x308]

// Bytes are the special ASCII bytes drawn a third of the time.
const Bytes: [24]u8 = [0x41, 0x5A, 0x61, 0x7A, 0x4D, 0x6D, 0x40, 0x5B, 0x60, 0x7B, 0x30, 0x20, 0x0D, 0x0A, 0x09, 0x7F, 0x00, 0x1F, 0x7E, 0x21, 0x51, 0x71, 0x0D, 0x0A]

// SvAscii writes at most n bytes of mostly-ASCII UTF-8 into b and returns
// the length: runs of up to run - 1 ASCII bytes, each followed by one to
// three planted scalars.
SvAscii(r: mut SvRng, b: mut []u8, n: int, run: int): int {
    var i := 0
    for i < n {
        k := SvR(r, run)
        for _ in 0..k {
            if i >= n {
                return i
            }
            if SvR(r, 3) == 0 {
                b[i] = Bytes[SvR(r, 24)]
            } else {
                b[i] = Trunc[u8](0x20 + SvR(r, 95))
            }
            i += 1
        }
        for _ in 0..1 + SvR(r, 3) {
            c := Picks[SvR(r, 20)]
            if i + Utf8Len(c) > n {
                return i
            }
            i = Encode(b, i, c)
        }
    }
    return i
}

export SvAsciiRun(sys: &System, seed: u64): OutOfMemory|Utf8Error|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var b := try sys.arena.Slice[u8](5000)
    var cases := 0
    var vec := 0
    var bad := 0
    masks := [QcMask(Form.Nfc), QcMask(Form.Nfd), QcMask(Form.Nfkc), QcMask(Form.Nfkd)]
    runs := [2, 9, 40, 81, 400]
    for c in 0..3000 {
        var n := SvR(mut rng, 301)
        if c % 150 == 0 {
            n = 2000 + SvR(mut rng, 3000)
        }
        m := SvAscii(mut rng, b, n, runs[c % 5])
        s := try str.From(b[0..m])
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        var starts := 0
        for j in 0..m + 1 {
            if j < m && b[j] & 0xC0 == 0x80 {
                continue
            }
            starts += 1
            if starts > 24 && j + 40 < m {
                continue
            }
            t := s[j..m]
            cases += 1
            if j + 16 <= m {
                vec += 1
            }
            if Width(t) != SvWidthOld(t) {
                bad += 1
            }
            x := try FoldCase(&ar, t)
            y := try SvFoldCaseOld(&ar, t)
            if x != y {
                bad += 1
            }
            for mk in masks {
                p := Span(b[0..m], j, mk)
                q := SvSpanOld(b[0..m], j, mk)
                if p.stop != q.stop || p.boundary != q.boundary {
                    bad += 1
                }
            }
        }
    }
    try Print(mut out, "cases {cases} vector {vec} bad {bad}\n", (cases, vec, bad))
    try out.Flush()
}
`

const svTextAsciiMain = `import std.text

export Main(sys: &System): OutOfMemory|Utf8Error|WriteError!void {
    try text.SvAsciiRun(sys, 0x9E3779B97F4A7C15)
}
`

// svTextGuards forces the text files' freestanding guards: "true" compiles
// the vector steps on every target (the control), "false" on none.
func svTextGuards(t *testing.T, to string) map[string]string {
	return sdFixtures(t, []sdForce{
		{"text/utf.mk", "Target.os != Os.Freestanding", to, 4},
		{"text/grapheme.mk", "Target.os != Os.Freestanding", to, 1},
		{"text/fold.mk", "Target.os != Os.Freestanding", to, 2},
		{"text/normalize.mk", "Target.os != Os.Freestanding", to, 1},
	})
}

// TestSTDVECTOR_TextAsciiDiff is Width, FoldCase and Span against their
// scalar versions, dev and release, and in release once more
// with the vector steps off (the freestanding form, on this target).
func TestSTDVECTOR_TextAsciiDiff(t *testing.T) {
	t.Parallel()
	old := strings.Replace(svTextAsciiFixture, "SVOLDASCII", svOldAscii, 1)
	type variant struct {
		name  string
		flags []string
		words bool
	}
	vs := []variant{{"dev", nil, false}, {"release", []string{"--release"}, false}, {"release-words", []string{"--release"}, true}}
	for _, v := range vs {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			fx := map[string]string{}
			if v.words {
				fx = svTextGuards(t, "false")
			}
			fx["text/zz_vectest.mk"] = old
			r := runNative(t, buildNative(t, fx, svTextAsciiMain, v.flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			var cases, vec, bad int
			if _, err := fmt.Sscanf(out, "cases %d vector %d bad %d\n", &cases, &vec, &bad); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			t.Logf("%d cases, %d of them 16 bytes or longer", cases, vec)
			if bad != 0 {
				t.Fatalf("%d differences in %d cases", bad, cases)
			}
			if cases < 50000 || vec < 40000 {
				t.Fatalf("cases not reached: %s", out)
			}
		})
	}
}

// svTextFreeProgram is a freestanding bin, general-regs-only by default,
// that reaches every std.text function with a vector step.
const svTextFreeProgram = `import std.text
import std.free

Use(a: &Arena): int {
    u := text.ToUtf16(a, "hello, world, sixteen and more bytes") else { return 0 }
    s := text.FromUtf16(a, u) else { return 0 }
    f := text.FoldCase(a, s) else { return 0 }
    m := text.Normalize(a, f, text.Form.Nfc) else { return 0 }
    var k := 0
    var g := text.Graphemes(m)
    for {
        if c := g.Next() {
            k += c.Len()
        } else {
            break
        }
    }
    return k + text.Width(m)
}

export extern MkEntry() {
    a := Arena.New(free.Heap()) else { return }
    _ = Use(&a)
    a.Free()
}
`

// TestSTDVECTOR_TextGeneralRegsOnly: mk check --target x86_64-freestanding
// of svTextFreeProgram is clean. The control compiles the vector steps on
// every target and must report E1108 in each std/text file that has one.
func TestSTDVECTOR_TextGeneralRegsOnly(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, fx map[string]string) (string, int) {
		root := t.TempDir()
		stdDir := copyStd(t, root, fx)
		if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(svTextFreeProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		return mkCmd(t, root, stdDir, "check", "--target", "x86_64-freestanding", "main.mk")
	}
	t.Run("guarded", func(t *testing.T) {
		t.Parallel()
		if out, status := check(t, nil); status != 0 || strings.Contains(out, "E1108") {
			t.Errorf("mk check --target x86_64-freestanding: exit %d, want a clean check\n%s", status, out)
		}
	})
	t.Run("control", func(t *testing.T) {
		t.Parallel()
		out, status := check(t, svTextGuards(t, "true"))
		for _, f := range []string{"utf.mk", "grapheme.mk", "fold.mk", "normalize.mk"} {
			if status == 0 || !strings.Contains(out, "std/text/"+f) {
				t.Errorf("control: exit %d, want E1108 in std/text/%s\n%s", status, f, out)
			}
		}
	})
}

// svOldAscii is std/text's scalar Width, FoldCase and Span, the reference.
const svOldAscii = `SvWidthOld(s: str): int {
    b := s.AsBytes()
    n := b.Len()
    var w := 0
    var i := 0
    for i < n {
        c := b[i]
        if c < 0x80 && c != 0x0D && (i + 1 == n || b[i + 1] < 0x80) {
            if c >= 0x20 && c < 0x7F {
                w += 1
            }
            i += 1
            continue
        }
        end := ClusterEnd(b, i)
        w += ClusterWidth(b, i, end)
        i = end
    }
    return w
}

SvFoldCaseOld(a: &Arena, s: str): OutOfMemory!str from a {
    b := s.AsBytes()
    n := b.Len()
    var size := 0
    var changed := false
    var i := 0
    for i < n {
        if i + 8 <= n {
            w := Word(b, i)
            if w & HighBits == 0 {
                if Upper8(w) != 0 {
                    changed = true
                }
                size += 8
                i += 8
                continue
            }
        }
        c := b[i]
        if c < 0x80 {
            if c >= 0x41 && c <= 0x5A {
                changed = true
            }
            size += 1
            i += 1
            continue
        }
        r := Decode(b, i)
        f := FoldOf(r.c)
        if f == 0 {
            size += r.n
        } else {
            changed = true
            size += FoldBytes(f)
        }
        i += r.n
    }
    if !changed {
        return try a.Dup(s)
    }
    out := try a.Slice[u8](size)
    pool := &FoldPool
    var o := 0
    i = 0
    for i < n {
        if i + 8 <= n {
            w := Word(b, i)
            if w & HighBits == 0 {
                // Bit 5 of each upper-case byte set: its lower case.
                x := w | (Upper8(w) >> 2)
                var d := mut out[o..o + 8]
                for k in 0..8 {
                    d[k] = Trunc[u8](x >> (8 * k))
                }
                o += 8
                i += 8
                continue
            }
        }
        c := b[i]
        if c < 0x80 {
            if c >= 0x41 && c <= 0x5A {
                out[o] = c + 0x20
            } else {
                out[o] = c
            }
            o += 1
            i += 1
            continue
        }
        r := Decode(b, i)
        f := FoldOf(r.c)
        if f == 0 {
            for k in 0..r.n {
                out[o + k] = b[i + k]
            }
            o += r.n
        } else {
            off := f >> 2
            for k in 0..f & 3 {
                o = Encode(out, o, pool[off + k])
            }
        }
        i += r.n
    }
    return str.From(out[..]).Must()
}

SvSpanOld(b: []u8, start: int, mask: u32): (stop: int, boundary: int) {
    n := b.Len()
    meta := &NormMeta
    var i := start
    var bnd := start
    var last: u32 = 0
    for i < n {
        if i + 8 <= n && Word(b, i) & HighBits == 0 {
            // Eight ASCII bytes: each a boundary.
            bnd = i + 7
            last = 0
            i += 8
            continue
        }
        c := b[i]
        if c < 0x80 {
            bnd = i
            last = 0
            i += 1
            continue
        }
        r := Decode(b, i)
        id := NormId(r.c)
        if id == 0 {
            bnd = i
            last = 0
            i += r.n
            continue
        }
        m := meta[id]
        ccc := m & 0xFF
        if (m >> 8) & mask != 0 || (ccc != 0 && ccc < last) {
            return (stop: i, boundary: bnd)
        }
        if ccc == 0 {
            bnd = i
        }
        last = ccc
        i += r.n
    }
    return (stop: n, boundary: n)
}
`
