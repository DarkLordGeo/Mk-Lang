// Why: std.text must stay correct and linear on the inputs that reach each
// function's worst case, a million scalars long.
// What: TestSTDTEXT_Adversarial and its program text2Adv.
// How: one Mk program builds every input (dev and release) and prints hashes;
// expected values come from arithmetic or from the Go models where fast.

package std

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// text2Adv builds each input, runs std.text over it and prints one line a
// result: a label and a value. H is a polynomial hash of the bytes
// (h = h * 31 + b, wrapping), which the Go side computes the same way.
const text2Adv = `import std.text

H(s: str): u64 {
    var h: u64 = 0
    for x in s.AsBytes() {
        h = h *% 31 +% u64(x)
    }
    return h
}

Rep(a: &Arena, head: str, unit: str, n: int): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push(head)
    for _ in 0..n {
        try t.Push(unit)
    }
    return try a.Dup(t.Str())
}

// Cycle is "a" then n marks taking classes 240, 230, 220, 202 in turn.
Cycle(a: &Arena, n: int): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push("a")
    for i in 0..n {
        k := i % 4
        if k == 0 {
            try t.Push("\u{345}")
        } else if k == 1 {
            try t.Push("\u{301}")
        } else if k == 2 {
            try t.Push("\u{323}")
        } else {
            try t.Push("\u{327}")
        }
    }
    return try a.Dup(t.Str())
}

Count(s: str): int {
    var g := text.Graphemes(s)
    var n := 0
    for g.Next().IsSome() {
        n += 1
    }
    return n
}

Line(out: mut Writer, label: str, v: u64): WriteError!void {
    try Print(out, "{label} {v}\n", (label, v))
}

LineN(out: mut Writer, label: str, n: int): WriteError!void {
    try Print(out, "{label} {n}\n", (label, n))
}

export Main(sys: &System): Utf8Error|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    a := sys.arena
    ri := try Rep(a, "", "\u{1F1E6}", 1000000)
    try LineN(mut out, "ri.clusters", Count(ri))
    try LineN(mut out, "ri.width", text.Width(ri))
    marks := try Rep(a, "a", "\u{301}", 1000000)
    try LineN(mut out, "marks.clusters", Count(marks))
    try LineN(mut out, "marks.width", text.Width(marks))
    try Line(mut out, "marks.nfc", H(try text.Normalize(a, marks, text.Form.Nfc)))
    try Line(mut out, "marks.nfd", H(try text.Normalize(a, marks, text.Form.Nfd)))
    lone := try Rep(a, "", "\u{301}", 1000000)
    try LineN(mut out, "lone.clusters", Count(lone))
    try Line(mut out, "lone.nfkc", H(try text.Normalize(a, lone, text.Form.Nfkc)))
    pairs := try Rep(a, "a", "\u{327}\u{301}", 500000)
    try Line(mut out, "pairs.nfc", H(try text.Normalize(a, pairs, text.Form.Nfc)))
    cyc := try Cycle(a, 200000)
    try Line(mut out, "cycle.nfd", H(try text.Normalize(a, cyc, text.Form.Nfd)))
    small := try Cycle(a, 8000)
    try Line(mut out, "small.nfc", H(try text.Normalize(a, small, text.Form.Nfc)))
    emoji := try Rep(a, "\u{1F468}", "\u{200D}\u{1F469}", 100000)
    try LineN(mut out, "emoji.clusters", Count(emoji))
    try LineN(mut out, "emoji.width", text.Width(emoji))
    crlf := try Rep(a, "", "\r\n", 100000)
    try LineN(mut out, "crlf.clusters", Count(crlf))
    try LineN(mut out, "crlf.width", text.Width(crlf))
    hangul := try Rep(a, "", "\u{AC01}\u{D7A3}\u{AC00}", 100000)
    d := try text.Normalize(a, hangul, text.Form.Nfd)
    try Line(mut out, "hangul.nfd", H(d))
    back := try text.Normalize(a, d, text.Form.Nfc)
    try Line(mut out, "hangul.back", H(back))
    try LineN(mut out, "hangul.clusters", Count(d))
    fold := try Rep(a, "", "\u{DF}\u{130}\u{FB03}A", 250000)
    try Line(mut out, "fold", H(try text.FoldCase(a, fold)))
    wide := try Rep(a, "", "\u{1F600}", 1000000)
    u := try text.ToUtf16(a, wide)
    try LineN(mut out, "utf16.units", u.Len())
    try Line(mut out, "utf16.back", H(try text.FromUtf16(a, u)))
    var e := text.Graphemes("")
    try LineN(mut out, "empty.next", Count(""))
    if e.Next().IsSome() {
        try LineN(mut out, "empty.bad", 1)
    }
    try LineN(mut out, "empty.width", text.Width(""))
    try LineN(mut out, "empty.nfc", (try text.Normalize(a, "", text.Form.Nfc)).Len())
    try LineN(mut out, "empty.fold", (try text.FoldCase(a, "")).Len())
    eu := try text.ToUtf16(a, "")
    try LineN(mut out, "empty.utf16", eu.Len())
    try LineN(mut out, "empty.from", (try text.FromUtf16(a, eu)).Len())
}
`

func text2Hash(s string) uint64 {
	var h uint64
	for i := 0; i < len(s); i++ {
		h = h*31 + uint64(s[i])
	}
	return h
}

// TestSTDTEXT_Adversarial runs the worst-case inputs: a million regional
// indicators, a million marks after one starter and with none, half a
// million mark pairs that reorder, 200,000 marks of four classes cycling
// (every run longer than the frame's buffer, sorted by SortStable), an
// emoji ZWJ chain of 200,001 scalars, CR LF runs, Hangul both ways, and
// the empty string through every function.
func TestSTDTEXT_Adversarial(t *testing.T) {
	u := needUCD(t)
	rep := func(head, unit string, n int) string { return head + strings.Repeat(unit, n) }
	cycle := func(n int) []rune {
		s := []rune{'a'}
		for i := 0; i < n; i++ {
			s = append(s, []rune{0x345, 0x301, 0x323, 0x327}[i%4])
		}
		return s
	}
	h := func(rs []rune) string { return fmt.Sprint(text2Hash(string(rs))) }
	marks := rep("a", "́", 1000000)
	hangul := rep("", "각힣가", 100000)
	hangulD := string(u.mNormalize([]rune(hangul), formNFD))
	want := map[string]string{
		"ri.clusters":    "500000",
		"ri.width":       "1000000",
		"marks.clusters": "1",
		"marks.width":    "1",
		"marks.nfc":      fmt.Sprint(text2Hash(rep("á", "́", 999999))),
		"marks.nfd":      fmt.Sprint(text2Hash(marks)),
		"lone.clusters":  "1",
		"lone.nfkc":      fmt.Sprint(text2Hash(rep("", "́", 1000000))),
		// a, 500,000 cedillas then 500,000 acutes; a composes with the
		// first acute across the cedillas (class 202 < 230).
		"pairs.nfc":       fmt.Sprint(text2Hash("á" + strings.Repeat("̧", 500000) + strings.Repeat("́", 499999))),
		"cycle.nfd":       h(u.mNormalize(cycle(200000), formNFD)),
		"small.nfc":       h(u.mNormalize(cycle(8000), formNFC)),
		"emoji.clusters":  "1",
		"emoji.width":     "2",
		"crlf.clusters":   "100000",
		"crlf.width":      "0",
		"hangul.nfd":      fmt.Sprint(text2Hash(hangulD)),
		"hangul.back":     fmt.Sprint(text2Hash(hangul)),
		"hangul.clusters": "300000",
		"fold":            fmt.Sprint(text2Hash(strings.Repeat("ssi̇ffia", 250000))),
		"utf16.units":     "2000000",
		"utf16.back":      fmt.Sprint(text2Hash(rep("", "\U0001F600", 1000000))),
		"empty.next":      "0",
		"empty.width":     "0",
		"empty.nfc":       "0",
		"empty.fold":      "0",
		"empty.utf16":     "0",
		"empty.from":      "0",
	}
	if utf8.RuneCountInString(hangulD) != 800000 {
		t.Fatalf("model: %d scalars in the Hangul NFD", utf8.RuneCountInString(hangulD))
	}
	for _, release := range []bool{false, true} {
		name := map[bool]string{false: "dev", true: "release"}[release]
		t.Run(name, func(t *testing.T) {
			flags := []string{}
			if release {
				flags = append(flags, "--release")
			}
			exe := text2BuildSrc(t, text2Adv, flags...)
			r := runNative(t, exe, "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			got := map[string]string{}
			for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(r.stdout, "\r\n", "\n")), "\n") {
				k, v, _ := strings.Cut(l, " ")
				got[k] = v
			}
			for k, w := range want {
				if got[k] != w {
					t.Errorf("%s: got %q, want %q", k, got[k], w)
				}
			}
			if _, bad := got["empty.bad"]; bad {
				t.Errorf("Graphemes(\"\").Next() gave a cluster")
			}
			t.Logf("%s: %s", name, r.took)
		})
	}
}
