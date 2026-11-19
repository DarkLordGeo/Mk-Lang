// Why: FindPre, the DFA scan's literal prefilter, has three paths (IndexByte,
// a vector pair filter, Two-Way past its budget) that must all find the same.
// What: TestSTDVECTOR_RegexFindPre and TestSTDVECTOR_RegexGenPrefilter (the
// RxPre that gen regex.Matcher emits), against core.FindFrom and Compile.
// How: seeded texts on 2 to 4 letters (periodic needles, the pair filter's
// worst case) and random bytes. TestSTDREGEX_* check against Go's regexp.

package std

import (
	"fmt"
	"strings"
	"testing"
)

const svRegexFixture = `// Test fixture (std/std-vector_regex_test.go): FindPre against
// core.FindFrom.
import std.core
` + svRngMk + `
export SvRegexRun(sys: &System, seed: u64): OutOfMemory|Utf8Error|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: seed)
    var tb := try sys.arena.Slice[u8](6000)
    var pb := try sys.arena.Slice[u8](64)
    var cases := 0
    var found := 0
    var bad := 0
    for c in 0..1500 {
        var n := SvR(mut rng, 400)
        if c % 50 == 0 {
            n = 3000 + SvR(mut rng, 3000)
        }
        mode := c % 4
        letters := 2 + SvR(mut rng, 3)
        for i in 0..n {
            if mode == 3 {
                tb[i] = Trunc[u8](0x20 + SvR(mut rng, 95))
            } else if mode == 2 && SvR(mut rng, 50) != 0 {
                tb[i] = 0x61
            } else {
                tb[i] = Trunc[u8](0x61 + SvR(mut rng, letters))
            }
        }
        m := 1 + SvR(mut rng, 40)
        if n >= m && SvR(mut rng, 3) != 0 {
            at := SvR(mut rng, n - m + 1)
            for k in 0..m {
                pb[k] = tb[at + k]
            }
        } else {
            for k in 0..m {
                pb[k] = Trunc[u8](0x61 + SvR(mut rng, letters))
            }
        }
        if mode == 2 {
            // aaa...ab style needles over a text of a's.
            for k in 0..m {
                pb[k] = 0x61
            }
            pb[m - 1] = 0x62
        }
        s := try str.From(tb[0..n])
        p := try str.From(pb[0..m])
        var step := 1
        if n > 1000 {
            step = 37
        }
        var from := 0
        for from <= n {
            cases += 1
            var x := -1
            var y := -1
            if k := FindPre(s, p, from) {
                x = k
            }
            if k := core.FindFrom(s, p, from) {
                y = k
            }
            if x != y {
                bad += 1
            }
            if y >= 0 {
                found += 1
            }
            from += step
        }
    }
    try Print(mut out, "findpre {cases} found {found} bad {bad}\n", (cases, found, bad))
    try out.Flush()
}
`

const svRegexMain = `import std.regex

export Main(sys: &System): OutOfMemory|Utf8Error|WriteError!void {
    try regex.SvRegexRun(sys, 0xA0761D6478BD642F)
}
`

// TestSTDVECTOR_RegexFindPre is FindPre against core.FindFrom, dev and
// release.
func TestSTDVECTOR_RegexFindPre(t *testing.T) {
	t.Parallel()
	fx := map[string]string{"regex/zz_vectest.mk": svRegexFixture}
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, fx, svRegexMain, flags...), "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			var cases, found, bad int
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if _, err := fmt.Sscanf(out, "findpre %d found %d bad %d\n", &cases, &found, &bad); err != nil {
				t.Fatalf("output: %v\n%s", err, out)
			}
			t.Logf("findpre %d cases, %d found", cases, found)
			if bad != 0 || cases < 100000 || found < cases/10 || found > cases-cases/10 {
				t.Fatalf("findpre: %d cases, %d found, %d differ", cases, found, bad)
			}
		})
	}
}

// svGenPats are the gen regex.Matcher patterns of
// TestSTDVECTOR_RegexGenPrefilter, each with a literal prefix of 1 to 8
// bytes (or none, the last two), so the emitted RxPre takes every path:
// simd.IndexByte, the pair filter, its budget and Two-Way.
var svGenPats = []string{"a", "ab", "aab", "abab", "aaaaaaab", "needle[0-9]+", "ba", "a[bc]d", "dcba+", "b+a", "[ab]c"}

// TestSTDVECTOR_RegexGenPrefilter: the code gen regex.Matcher emits, whose
// prefilter is RxPre (dfa.mk's FindPre written out), against
// regex.Compile's Find and IsMatch (which TestSTDREGEX_* hold against
// Go's regexp) on 1,200 seeded texts of 0 to 9,000 bytes over 2 to 5
// letters with needles planted, from the first 12 starts of each, release.
func TestSTDVECTOR_RegexGenPrefilter(t *testing.T) {
	// Not parallel: rxGenBuild sets MK_CACHE.
	var src, calls strings.Builder
	src.WriteString("import std.regex\n" + svRngMk + "\n")
	for i, p := range svGenPats {
		fmt.Fprintf(&src, "struct M%d {}\n\ngen regex.Matcher(M%d, %s) fuel %d\n\n", i, i, rxQuote(p), rxGenFuel)
		fmt.Fprintf(&src, `Check%d(ar: &Arena, s: str, k: mut [3]int): regex.Error!void {
    re := try regex.Compile(ar, %s)
    n := s.Len()
    for j in 0..12 {
        if j > n {
            break
        }
        t := s[j..n]
        k[0] += 1
        if M%d.IsMatch(t) != re.IsMatch(t) {
            k[2] += 1
        }
        a := M%d.Find(t)
        if Same(a, re.Find(t)) {
            if !a.IsNone() {
                k[1] += 1
            }
        } else {
            k[2] += 1
        }
    }
}

`, i, rxQuote(p), i, i)
		fmt.Fprintf(&calls, "        try Check%d(&ar, s, mut k)\n", i)
	}
	fmt.Fprintf(&src, `Same(a: ?regex.Match, b: ?regex.Match): bool {
    if x := a {
        if y := b {
            return x.lo == y.lo && x.hi == y.hi
        }
        return false
    }
    return b.IsNone()
}

const Needles: [6]str = ["needle7", "aaaaaaab", "abab", "dcbaa", "aab", "acd"]

export Main(sys: &System): OutOfMemory|regex.Error|Utf8Error|WriteError!void {
    var out := sys.Stdout()
    var rng := SvRng(x: 0x5DEECE66D)
    var buf := try sys.arena.Slice[u8](9100)
    var k: [3]int = [0; 3]
    for c in 0..1200 {
        var n := SvR(mut rng, 300)
        if c %% 40 == 0 {
            n = 4000 + SvR(mut rng, 5000)
        }
        letters := 2 + SvR(mut rng, 4)
        mode := c %% 3
        for i in 0..n {
            if mode == 0 {
                buf[i] = 0x61
            } else {
                buf[i] = Trunc[u8](0x61 + SvR(mut rng, letters))
            }
        }
        if n > 10 && SvR(mut rng, 2) == 0 {
            nd := Needles[SvR(mut rng, 6)].AsBytes()
            at := SvR(mut rng, n - nd.Len())
            for q in 0..nd.Len() {
                buf[at + q] = nd[q]
            }
        }
        s := try str.From(buf[0..n])
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
%s    }
    cases := k[0]
    found := k[1]
    bad := k[2]
    try Print(mut out, "cases {cases} found {found} bad {bad}\n", (cases, found, bad))
    try out.Flush()
}
`, calls.String())
	exe, _ := rxGenBuild(t, src.String(), false, "-release")
	out := strings.ReplaceAll(regexExec(t, exe), "\r\n", "\n")
	var cases, found, bad int
	if _, err := fmt.Sscanf(out, "cases %d found %d bad %d\n", &cases, &found, &bad); err != nil {
		t.Fatalf("output: %v\n%s", err, out)
	}
	t.Logf("%d cases, %d matches", cases, found)
	if bad != 0 {
		t.Fatalf("%d differences in %d cases", bad, cases)
	}
	if cases < 100000 || found < 30000 {
		t.Fatalf("cases not reached: %s", out)
	}
}
