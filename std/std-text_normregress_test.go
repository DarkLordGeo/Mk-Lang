// Why: past bugs in Normalize's output sizing, scratch buffer and mark sort,
// and a Unicode version gap between two case tables, must stay fixed.
// What: std/text/tests_text.mk's blocks, full-size adversarial inputs and
// long mark runs against the Go models, and str.ToLower vs text.FoldCase.
// How: Mk programs print hashes or hex scalars that the Go side compares
// with the models of std-text_model_test.go, dev and release.

package std

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// text2StdDir is a copy of std under root with this tree's std/text, over
// MK_TEXT2_BASE when it is set (text2BuildSrc's rule).
func text2StdDir(t *testing.T, root string) string {
	t.Helper()
	base := os.Getenv("MK_TEXT2_BASE")
	if base == "" {
		return copyStd(t, root, nil)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "text")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "text"), os.DirFS("text")); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// TestSTDTEXT_TestBlocks: mk test --std=std.text passes every block of
// std/text/tests_text.mk, dev and release (the output bound of Korean and
// Latin-1 text after one mark is the result's length, and the Nfd and Nfkd
// bounds are exact; one scratch buffer is sized by the largest chunk; the
// counting sort gives a stable sort's order).
func TestSTDTEXT_TestBlocks(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("text", "tests_text.mk"))
	if err != nil {
		t.Fatal(err)
	}
	names := testNames(string(src))
	if len(names) < 6 {
		t.Fatalf("tests_text.mk has %d blocks, want at least 6", len(names))
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		root := t.TempDir()
		stdDir := text2StdDir(t, root)
		out, _ := mkCmd(t, root, stdDir, append([]string{"test", "--std=std.text"}, flags...)...)
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		res := parseStdTest(out, "std.text")
		for _, n := range names {
			if r, ok := res[n]; !ok || !r.pass {
				t.Errorf("%v: block %q: %+v\n%s", flags, n, r, out)
			}
		}
	}
}

// text2Review builds the regression inputs at full size and prints the
// hash (text2Adv's H) of each result. kor normalizes ten times into one
// arena without a reset, to catch an oversized output bound.
const text2Review = `import std.text

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

Line(out: mut Writer, label: str, v: u64): WriteError!void {
    try Print(out, "{label} {v}\n", (label, v))
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    a := sys.arena
    kor := try Rep(a, "\u{301}", "\u{AC01}", 1000000)
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var h: u64 = 0
    for _ in 0..10 {
        h = H(try text.Normalize(&ar, kor, text.Form.Nfc))
    }
    try Line(mut out, "kor.nfc", h)
    try Line(mut out, "kor.nfd", H(try text.Normalize(a, kor, text.Form.Nfd)))
    latin := try Rep(a, "\u{301}", "\u{E9}", 1000000)
    try Line(mut out, "latin.nfc", H(try text.Normalize(a, latin, text.Form.Nfc)))
    fdfa := try Rep(a, "", "x\u{FDFA}\u{FDFA}", 150000)
    try Line(mut out, "fdfa.nfkc", H(try text.Normalize(a, fdfa, text.Form.Nfkc)))
    try Line(mut out, "fdfa.nfkd", H(try text.Normalize(a, fdfa, text.Form.Nfkd)))
    cyc := try Cycle(a, 1048576)
    try Line(mut out, "cycle20.nfd", H(try text.Normalize(a, cyc, text.Form.Nfd)))
    try Line(mut out, "cycle20.nfc", H(try text.Normalize(a, cyc, text.Form.Nfc)))
}
`

// cycleNFC is the Nfc of "a" then 4q marks of classes 240, 230, 220, 202
// cycling: U+1EA1, then q U+0327, q-1 U+0323, q U+0301 and q U+0345.
func cycleNFC(q int) []rune {
	s := []rune{0x1EA1}
	for _, m := range []struct {
		c rune
		n int
	}{{0x327, q}, {0x323, q - 1}, {0x301, q}, {0x345, q}} {
		for range m.n {
			s = append(s, m.c)
		}
	}
	return s
}

// TestSTDTEXT_FullSizeInputs runs three inputs (a mark, then a
// million Korean syllables or Latin-1 letters; 150,000 copies of x U+FDFA
// U+FDFA under Nfkc and Nfkd, 37 scalars a chunk; a run of 2^20 marks of
// four classes) against the Go models, dev and release.
func TestSTDTEXT_FullSizeInputs(t *testing.T) {
	u := needUCD(t)
	cycle := make([]rune, 0, 1048577)
	cycle = append(cycle, 'a')
	for i := 0; i < 1048576; i++ {
		cycle = append(cycle, []rune{0x345, 0x301, 0x323, 0x327}[i%4])
	}
	kor := "́" + strings.Repeat("각", 1000000)
	latin := "́" + strings.Repeat("é", 1000000)
	fdfa := []rune(strings.Repeat("xﷺﷺ", 150000))
	h := func(rs []rune) string { return fmt.Sprint(text2Hash(string(rs))) }
	want := map[string]string{
		"kor.nfc":     fmt.Sprint(text2Hash(kor)),
		"kor.nfd":     h(u.mNormalize([]rune(kor), formNFD)),
		"latin.nfc":   fmt.Sprint(text2Hash(latin)),
		"fdfa.nfkc":   h(u.mNormalize(fdfa, formNFKC)),
		"fdfa.nfkd":   h(u.mNormalize(fdfa, formNFKD)),
		"cycle20.nfd": h(u.mNormalize(cycle, formNFD)),
		"cycle20.nfc": h(cycleNFC(1048576 / 4)),
	}
	// The model composes a long run in quadratic time (minutes at 2^20),
	// so the Nfc result is written out and checked against the model at
	// 4,000 marks: a with the first U+0323 is U+1EA1, nothing else joins.
	if small := u.mNormalize(cycle[:4001], formNFC); !slices.Equal(small, cycleNFC(1000)) {
		t.Fatalf("cycleNFC(1000) is not the model's Nfc of the 4,000-mark cycle")
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		var flags []string
		if release {
			flags = append(flags, "--release")
		}
		exe := text2BuildSrc(t, text2Review, flags...)
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
		t.Logf("took %s", r.took)
	})
}

// TestSTDTEXT_LongRunsAgainstModel puts 80 seeded runs of 9 to
// 10,000 non-starters (every scalar of the UCD with a nonzero class, some
// of which decompose), after a starter that composes with some of them or
// none, through the four forms against the models: runs above 32 take the
// counting sort, the rest insertion.
func TestSTDTEXT_LongRunsAgainstModel(t *testing.T) {
	u := needUCD(t)
	var marks []rune
	for c, k := range u.ccc {
		if k != 0 {
			marks = append(marks, rune(c))
		}
	}
	if len(marks) < 500 {
		t.Fatalf("%d scalars with a nonzero class", len(marks))
	}
	r := rand.New(rand.NewPCG(0x7E472, 3))
	starters := []rune{'a', 'e', 'o', 'u', 0x1100, 0x0915, 0x05D0}
	var lines []string
	var cases [][]rune
	for i := 0; i < 80; i++ {
		n := 9 + int(float64(10000-9)*r.Float64()*r.Float64()*r.Float64())
		if i < 4 {
			n = []int{9, 32, 33, 10000}[i]
		}
		var s []rune
		if r.IntN(4) != 0 {
			s = append(s, starters[r.IntN(len(starters))])
		}
		// Few classes in some runs (long stretches of equal keys, where
		// stability shows), all of them in the rest.
		pool := marks
		if r.IntN(2) == 0 {
			pool = nil
			for range 3 {
				pool = append(pool, marks[r.IntN(len(marks))])
			}
		}
		for range n {
			s = append(s, pool[r.IntN(len(pool))])
		}
		if r.IntN(3) == 0 {
			s = append(s, 'b', marks[r.IntN(len(marks))])
		}
		cases = append(cases, s)
		lines = append(lines, hexLine(s))
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		out := text2Run(t, release, "n", lines)
		bad := 0
		for i, s := range cases {
			var want []string
			for _, f := range []int{formNFC, formNFD, formNFKC, formNFKD} {
				want = append(want, hexLine(u.mNormalize(s, f)))
			}
			if w := strings.Join(want, ";"); out[i] != w {
				if bad++; bad <= 3 {
					t.Errorf("run %d (%d scalars): the forms differ from the model", i, len(s))
				}
			}
		}
	})
}

// text2CaseProg prints, for each line's decimal scalar, str.ToLower and
// text.FoldCase of it as hex scalars, separated by ';'.
const text2CaseProg = `import std.text

Hex(out: mut Writer, s: str): WriteError!void {
    var it := s.Scalars()
    var first := true
    for c := it.Next() {
        if !first {
            try out.Str(" ")
        }
        first = false
        try Print(out, "{c:x}", (c: u32(c)))
    }
}

export Main(sys: &System): ReadError|OutOfMemory|WriteError|ParseError!void {
    var input := sys.Stdin()
    var out := sys.Stdout()
    a := sys.arena
    for {
        l := try input.Line(a) else {
            break
        }
        v := try l.ParseInt()
        var t := Text.New(a)
        try t.PushChar(Cast[char](Cast[u32](v)))
        s := t.Str()
        try Hex(mut out, try s.ToLower(a))
        try out.Str(";")
        try Hex(mut out, try text.FoldCase(a, s))
        try out.Str("\n")
    }
}
`

var text2CaseVersionRE = regexp.MustCompile(`unicode\.Version: (\d+\.\d+\.\d+)`)

// TestSTDTEXT_CaseVersions checks the scalars whose simple
// lowercase (UnicodeData field 13) is also their CaseFolding status C
// folding, so str.ToLower and text.FoldCase must agree on them at one
// Unicode version. std.text's tables are the UCD's (18.0.0) and
// str.ToLower's CaseTable is Go's unicode package's (text_case.mk's
// header). While the two versions differ, the scalars where they disagree
// must be exactly those whose Go lowercase differs from the UCD's, the
// version gap and nothing else; once they are equal, there must be none.
func TestSTDTEXT_CaseVersions(t *testing.T) {
	u := needUCD(t)
	dir := ucdDir()
	lower := map[rune]rune{}
	if _, err := ucdLines(dir, "UnicodeData.txt", func(fl []string) error {
		if len(fl) < 14 || fl[13] == "" {
			return nil
		}
		c, err1 := strconv.ParseUint(fl[0], 16, 32)
		l, err2 := strconv.ParseUint(fl[13], 16, 32)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("bad scalar")
		}
		lower[rune(c)] = rune(l)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	status := map[rune]string{}
	if _, err := ucdLines(dir, "CaseFolding.txt", func(fl []string) error {
		c, err := strconv.ParseUint(fl[0], 16, 32)
		if err != nil {
			return err
		}
		if fl[1] == "C" {
			status[rune(c)] = fl[1]
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var cs []rune
	for c, l := range lower {
		if f := u.fold[c]; status[c] == "C" && len(f) == 1 && f[0] == l {
			cs = append(cs, c)
		}
	}
	slices.Sort(cs)
	if len(cs) < 1000 {
		t.Fatalf("%d scalars with a simple lowercase that is their C folding", len(cs))
	}
	var gap []rune
	for _, c := range cs {
		if unicode.ToLower(c) != lower[c] {
			gap = append(gap, c)
		}
	}
	src, err := os.ReadFile(filepath.Join("core", "text_case.mk"))
	if err != nil {
		t.Fatal(err)
	}
	coreVersion := ""
	if m := text2CaseVersionRE.FindSubmatch(src); m != nil {
		coreVersion = string(m[1])
	}
	var lines []string
	for _, c := range cs {
		lines = append(lines, strconv.Itoa(int(c)))
	}
	exe := text2BuildSrc(t, text2CaseProg)
	r := runNative(t, exe, strings.Join(lines, "\n")+"\n")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	out := strings.Split(strings.TrimSpace(strings.ReplaceAll(r.stdout, "\r\n", "\n")), "\n")
	if len(out) != len(cs) {
		t.Fatalf("%d lines for %d scalars", len(out), len(cs))
	}
	var differ []rune
	for i, c := range cs {
		lo, fo, _ := strings.Cut(out[i], ";")
		if fo != fmt.Sprintf("%x", lower[c]) {
			t.Errorf("FoldCase(U+%04X) = %s, want %x (UCD %s)", c, fo, lower[c], u.version)
		}
		if lo != fo {
			differ = append(differ, c)
		}
	}
	t.Logf("std.text at UCD %s, str.ToLower at Go unicode %s (text_case.mk: %q): %d scalars checked, %d disagree, %d in the version gap", u.version, unicode.Version, coreVersion, len(cs), len(differ), len(gap))
	if coreVersion == u.version {
		if len(differ) != 0 {
			t.Errorf("one Unicode version, yet str.ToLower and text.FoldCase disagree on %d scalars: %U", len(differ), differ)
		}
		return
	}
	if !slices.Equal(differ, gap) {
		t.Errorf("str.ToLower and text.FoldCase disagree on %U; the version gap (Go unicode %s against UCD %s) is %U", differ, unicode.Version, u.version, gap)
	}
}
