// Why: str.Find, Has, StartsWith and EndsWith run over the Two-Way FindFrom
// and the word-at-a-time BytesEqual, which must match the naive results.
// What: tests against Go's strings package, and a timed adversarial input.
// How: at compile time and natively; the timed input is the one on which a
// naive scan is quadratic.

package std

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// findCases is seeded haystacks and needles over small alphabets (so
// matches, near-matches and periodic needles are common), the classic
// Two-Way traps, and needles of every length near the 8-byte word.
func findCases(r *rand.Rand, n int) [][2]string {
	out := [][2]string{
		{"", ""}, {"a", ""}, {"", "a"}, {"aaaaaaaaab", "aab"}, {"abababababc", "ababc"}, {"aaaa", "aaaaa"},
		{"banana", "nana"}, {"abcabcabcabd", "abcabd"}, {"xyz", "z"}, {"héllo €", "€"},
		{"éééè", "éè"}, {"zzzzzzzzzzzzzzzzzzzzzzzzzza", "a"}, {"abcdefghabcdefgh", "habc"},
		{strings.Repeat("a", 100) + "b", strings.Repeat("a", 30) + "b"},
		{strings.Repeat("ab", 50) + "abb", "ababb"}, {strings.Repeat("abc", 40), "cab"},
	}
	for len(out) < n {
		al := "ab"
		if r.Intn(3) == 0 {
			al = "abc"
		}
		mk := func(k int) string {
			b := make([]byte, k)
			for i := range b {
				b[i] = al[r.Intn(len(al))]
			}
			return string(b)
		}
		h := mk(r.Intn(40))
		var p string
		if len(h) > 0 && r.Intn(2) == 0 {
			i := r.Intn(len(h))
			p = h[i:min(len(h), i+1+r.Intn(12))]
		} else {
			p = mk(1 + r.Intn(10))
		}
		out = append(out, [2]string{h, p})
	}
	return out
}

// TestFindMatchesGo checks Find, Has, StartsWith and EndsWith on
// 400 cases at compile time against strings.Index, Contains, HasPrefix and
// HasSuffix.
func TestFindMatchesGo(t *testing.T) {
	t.Parallel()
	cases := findCases(rand.New(rand.NewSource(22)), 400)
	var hs, ps, idx, pre, suf []string
	for _, c := range cases {
		hs = append(hs, mkString(c[0]))
		ps = append(ps, mkString(c[1]))
		idx = append(idx, fmt.Sprint(strings.Index(c[0], c[1])))
		pre = append(pre, fmt.Sprint(strings.HasPrefix(c[0], c[1])))
		suf = append(suf, fmt.Sprint(strings.HasSuffix(c[0], c[1])))
	}
	n := len(cases)
	fixture := fmt.Sprintf(`
const FindH: [%[1]d]str = [%[2]s]
const FindP: [%[1]d]str = [%[3]s]
const FindAt: [%[1]d]int = [%[4]s]
const FindPre: [%[1]d]bool = [%[5]s]
const FindSuf: [%[1]d]bool = [%[6]s]

FindOne(k: int): int {
    var bad := 0
    h := FindH[k]
    p := FindP[k]
    var got := -1
    if at := h.Find(p) {
        got = at
    }
    if got != FindAt[k] || h.Has(p) != (FindAt[k] >= 0) {
        bad += 1
    }
    if h.StartsWith(p) != FindPre[k] || h.EndsWith(p) != FindSuf[k] {
        bad += 1
    }
    return bad
}

FindCheck(from: int, to: int): int {
    var bad := 0
    for k in from..to {
        bad += FindOne(k)
    }
    return bad
}
`, n, strings.Join(hs, ", "), strings.Join(ps, ", "), strings.Join(idx, ", "), strings.Join(pre, ", "), strings.Join(suf, ", "))
	var parts []string
	for k := 0; k < n; k += 100 {
		fixture += fmt.Sprintf("const Find%d: int = FindCheck(%d, %d)\n", k, k, min(k+100, n))
		parts = append(parts, fmt.Sprintf("Find%d", k))
	}
	fixture += "\nconst FindOk = [0; 1][" + strings.Join(parts, " + ") + "]\n"
	expectClean(t, "lib024", fixture)
}

// TestFindNativeAndWorstCase runs, natively at release, Find on
// 100,000 seeded cases from stdin (against strings.Index), then the input
// that makes a naive scan quadratic: a 4 MiB haystack of 'a' and the
// needle "a" * 4096 + "b", which naive matching compares about 1.7e10
// times. Two-Way is linear, so it must finish in well under a second;
// the time is logged as the benchmark number.
func TestFindNativeAndWorstCase(t *testing.T) {
	t.Parallel()
	cases := findCases(rand.New(rand.NewSource(23)), 100000)
	var in, want strings.Builder
	for _, c := range cases {
		if strings.ContainsAny(c[0]+c[1], "\n\r|") {
			continue
		}
		in.WriteString(c[0] + "|" + c[1] + "\n")
		fmt.Fprintf(&want, "%d\n", strings.Index(c[0], c[1]))
	}
	src := `export Main(sys: &System): ReadError|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    for {
        l := try inp.Line(sys.arena)
        s := l else {
            break
        }
        bar := s.Find("|") else {
            break
        }
        h := s[0..bar]
        p := s[bar + 1..]
        var at := -1
        if i := h.Find(p) {
            at = i
        }
        try Print(mut out, "{at}\n", (at))
    }
    try out.Flush()
}
`
	r := runNative(t, buildNative(t, nil, src, "--release"), in.String())
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	compareLines(t, r.stdout, want.String())

	worst := `export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var h := Text.New(sys.arena)
    for _ in 0..4194304 / 64 {
        try h.Push("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    }
    var p := Text.New(sys.arena)
    for _ in 0..4096 / 64 {
        try p.Push("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    }
    try p.Push("b")
    var found := 0
    for _ in 0..10 {
        if h.Str().Find(p.Str()).IsSome() {
            found += 1
        }
    }
    try Print(mut out, "{found}\n", (found))
    try out.Flush()
}
`
	start := time.Now()
	w := runNative(t, buildNative(t, nil, worst, "--release"), "")
	if w.code != 0 || w.stdout != "0\n" {
		t.Fatalf("worst case: exit %d, stdout %q\n%s", w.code, w.stdout, w.stderr)
	}
	t.Logf("worst case, 10 searches of a 4,097-byte needle in 4 MiB: %v (wall %v)", w.took, time.Since(start))
	if !timingRun() {
		t.Logf("%s: the 5 s bound is not asserted", timingOff)
	} else if w.took > 5*time.Second {
		t.Errorf("worst case took %v: not linear", w.took)
	}
}
