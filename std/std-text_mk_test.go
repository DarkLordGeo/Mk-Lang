// Why: std.text's segmentation, width, normalization, folding and UTF-16
// code must agree with the Unicode test files and with independent models.
// What: the UCD conformance tests, the differential test against the Go
// models of std-text_model_test.go, and UTF-16 against Go's unicode/utf16.
// How: one program (text2Prog) built dev and release reads cases from stdin,
// one a line, as hex scalars or hex UTF-16 units, and prints the results.

package std

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

// text2Prog is the one test program. Its first argument picks the mode:
//
//	g  cluster lengths in scalars of each line
//	w  the width of each line
//	n  NFC;NFD;NFKC;NFKD of each line, as hex scalars
//	f  the case folding of each line
//	u  the UTF-16 units of each line, then the round trip back ("ok" or "bad")
//	s  FromUtf16 of each line of hex units: the scalars, or "err"
//	x  reads scalars, one a line, until "end"; then prints every other
//	   scalar that some form changes, and "done N" with the number checked
const text2Prog = `import std.text

Digit(x: u8): u32 {
    if x >= 0x30 && x <= 0x39 {
        return u32(x - 0x30)
    }
    if x >= 0x41 && x <= 0x46 {
        return u32(x - 0x41) + 10
    }
    return u32(x - 0x61) + 10
}

// Values is the hex numbers of line l, separated by spaces.
Values(a: &Arena, l: str): OutOfMemory!List[u32] from a {
    var vs := List[u32].New(a)
    var v: u32 = 0
    var any := false
    for x in l.AsBytes() {
        if x == 0x20 {
            if any {
                try vs.Push(v)
            }
            v = 0
            any = false
        } else {
            v = v * 16 + Digit(x)
            any = true
        }
    }
    if any {
        try vs.Push(v)
    }
    return vs
}

// Build is the text the hex scalars of line l spell.
Build(a: &Arena, l: str): OutOfMemory!str from a {
    vs := try Values(a, l)
    var t := Text.New(a)
    for v in vs {
        try t.PushChar(Cast[char](v))
    }
    return try a.Dup(t.Str())
}

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

Forms(out: mut Writer, a: &Arena, s: str): OutOfMemory|WriteError!void {
    try Hex(out, try text.Normalize(a, s, text.Form.Nfc))
    try out.Str(";")
    try Hex(out, try text.Normalize(a, s, text.Form.Nfd))
    try out.Str(";")
    try Hex(out, try text.Normalize(a, s, text.Form.Nfkc))
    try out.Str(";")
    try Hex(out, try text.Normalize(a, s, text.Form.Nfkd))
}

// Same reports whether every form leaves s as it is.
Same(a: &Arena, s: str): OutOfMemory!bool {
    return try text.Normalize(a, s, text.Form.Nfc) == s && try text.Normalize(a, s, text.Form.Nfd) == s && try text.Normalize(a, s, text.Form.Nfkc) == s && try text.Normalize(a, s, text.Form.Nfkd) == s
}

// Unchanged reports whether every form leaves the scalar c as it is.
Unchanged(a: &Arena, c: u32): OutOfMemory!bool {
    var t := Text.New(a)
    try t.PushChar(Cast[char](c))
    return try Same(a, t.Str())
}

export Main(sys: &System): Utf8Error|ReadError|OutOfMemory|WriteError!void {
    var input := sys.Stdin()
    var out := sys.Stdout()
    mode := sys.args[1]
    a := sys.arena
    if mode == "x" {
        listed := try a.Slice[bool](0x110000)
        for {
            l := try input.Line(a) else {
                break
            }
            if l == "end" {
                break
            }
            vs := try Values(a, l)
            listed[vs[0]] = true
        }
        var checked := 0
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        for c in 0..0x110000 {
            if c >= 0xD800 && c <= 0xDFFF || listed[c] {
                continue
            }
            if !try Unchanged(&ar, Cast[u32](c)) {
                try Print(mut out, "{c:x}\n", (c))
            }
            checked += 1
            if checked % 4096 == 0 {
                ar.Reset()
            }
        }
        try Print(mut out, "done {checked}\n", (checked))
        return
    }
    for {
        l := try input.Line(a) else {
            break
        }
        if mode == "s" {
            vs := try Values(a, l)
            units := try a.Slice[u16](vs.Len())
            for i in 0..vs.Len() {
                units[i] = Trunc[u16](vs[i])
            }
            s := text.FromUtf16(a, units[..]) else e {
                match e {
                    Utf8Error {
                        try out.Str("err\n")
                        continue
                    }
                    OutOfMemory {
                        fail OutOfMemory
                    }
                }
            }
            try Hex(mut out, s)
            try out.Str("\n")
            continue
        }
        s := try Build(a, l)
        if mode == "g" {
            var g := text.Graphemes(s)
            var first := true
            for c := g.Next() {
                if !first {
                    try out.Str(" ")
                }
                first = false
                var it := c.Scalars()
                var k := 0
                for it.Next().IsSome() {
                    k += 1
                }
                try Print(mut out, "{k}", (k))
            }
        } else if mode == "w" {
            w := text.Width(s)
            try Print(mut out, "{w}", (w))
        } else if mode == "n" {
            try Forms(mut out, a, s)
        } else if mode == "f" {
            try Hex(mut out, try text.FoldCase(a, s))
        } else if mode == "u" {
            u := try text.ToUtf16(a, s)
            for i in 0..u.Len() {
                if i > 0 {
                    try out.Str(" ")
                }
                x := u[i]
                try Print(mut out, "{x:x}", (x))
            }
            back := try text.FromUtf16(a, u)
            if back == s {
                try out.Str(";ok")
            } else {
                try out.Str(";bad")
            }
        }
        try out.Str("\n")
    }
}
`

// text2Exe builds text2Prog once per test binary and build kind, and
// returns the executable.
func text2Exe(t *testing.T, release bool) string {
	t.Helper()
	key := "dev"
	var flags []string
	if release {
		key = "release"
		flags = append(flags, "--release")
	}
	text2Builds.mu.Lock()
	defer text2Builds.mu.Unlock()
	if exe, ok := text2Builds.exe[key]; ok {
		return exe
	}
	exe := text2BuildSrc(t, text2Prog, flags...)
	text2Builds.exe[key] = exe
	return exe
}

// text2BuildSrc builds src with flags over a copy of std and returns the
// executable. MK_TEXT2_BASE names a snapshot of std (for instance git
// archive HEAD std) to build against while the live std is mid-edit;
// std/text then comes from this tree.
func text2BuildSrc(t *testing.T, src string, flags ...string) string {
	t.Helper()
	root, err := packageTempDir("mktext2")
	if err != nil {
		t.Fatal(err)
	}
	var stdDir string
	if base := os.Getenv("MK_TEXT2_BASE"); base != "" {
		stdDir = filepath.Join(root, "std")
		if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(stdDir, "text")); err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(filepath.Join(stdDir, "text"), os.DirFS("text")); err != nil {
			t.Fatal(err)
		}
	} else {
		stdDir = copyStd(t, root, nil)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

var text2Builds = struct {
	mu  sync.Mutex
	exe map[string]string
}{exe: map[string]string{}}

// text2Run runs the program in mode over the input lines and returns its
// output lines, one per input line (fails the test on a non-zero exit).
func text2Run(t *testing.T, release bool, mode string, lines []string) []string {
	t.Helper()
	exe := text2Exe(t, release)
	r := runNative(t, exe, strings.Join(lines, "\n")+"\n", mode)
	if r.code != 0 {
		t.Fatalf("mode %s: exit %d\nstderr:\n%s", mode, r.code, r.stderr)
	}
	out := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if mode != "x" && len(out) != len(lines) {
		t.Fatalf("mode %s: %d output lines for %d inputs", mode, len(out), len(lines))
	}
	return out
}

func hexLine(s []rune) string {
	var b strings.Builder
	for i, c := range s {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%x", c)
	}
	return b.String()
}

func text2Builds2(t *testing.T, f func(t *testing.T, release bool)) {
	for _, release := range []bool{false, true} {
		name := "dev"
		if release {
			name = "release"
		}
		t.Run(name, func(t *testing.T) { f(t, release) })
	}
}

// TestSTDTEXT_GraphemeBreakTest runs every line of the UCD's
// GraphemeBreakTest.txt through text.Graphemes.
func TestSTDTEXT_GraphemeBreakTest(t *testing.T) {
	needUCD(t)
	cases := readGraphemeTest(t, ucdDir())
	var lines, want []string
	for _, c := range cases {
		lines = append(lines, hexLine(c.s))
		var w []string
		for _, n := range c.lens {
			w = append(w, strconv.Itoa(n))
		}
		want = append(want, strings.Join(w, " "))
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		got := text2Run(t, release, "g", lines)
		bad := 0
		for i := range got {
			if got[i] != want[i] && bad < 20 {
				bad++
				t.Errorf("%s: got %q, want %q", lines[i], got[i], want[i])
			}
		}
	})
}

// TestSTDTEXT_NormalizationTest runs c1 to c5 of every line of the UCD's
// NormalizationTest.txt through the four forms and checks the file's
// conformance table; then every scalar part 1 does not list must come back
// unchanged from every form.
func TestSTDTEXT_NormalizationTest(t *testing.T) {
	needUCD(t)
	cases := readNormTest(t, ucdDir())
	var lines []string
	var part1 []string
	for _, c := range cases {
		for k := 0; k < 5; k++ {
			lines = append(lines, hexLine(c.c[k]))
		}
		if c.part == 1 {
			part1 = append(part1, hexLine(c.c[0]))
		}
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		got := text2Run(t, release, "n", lines)
		bad := 0
		for i, c := range cases {
			for k := 0; k < 5; k++ {
				forms := strings.Split(got[5*i+k], ";")
				for form := 0; form < 4; form++ {
					want := hexLine(c.c[normWant[form][k]])
					if forms[form] != want && bad < 20 {
						bad++
						t.Errorf("form %d of c%d %s: got %s, want %s", form, k+1, lines[5*i+k], forms[form], want)
					}
				}
			}
		}
		x := text2Run(t, release, "x", append(part1, "end"))
		wantDone := fmt.Sprintf("done %d", 0x110000-0x800-len(part1))
		if len(x) != 1 || x[0] != wantDone {
			t.Errorf("scalars outside part 1 that a form changes, or a wrong count (want %q):\n%s", wantDone, strings.Join(x[:min(len(x), 20)], "\n"))
		}
	})
}

// text2Classes are scalars of every class the algorithms treat apart:
// ASCII, controls, marks of many classes, Hangul jamo and syllables,
// emoji and their joiners, regional indicators, Indic conjuncts,
// singletons, compatibility forms, case-folding expansions.
var text2Classes = []rune{
	'a', 'A', 'z', 'Z', 'e', 'E', 's', 'K', 'k', '0', ' ', '\r', '\n', '\t', 0x7F, 0x85, 0xAD,
	0xC5, 0xE5, 0xDF, 0x130, 0x131, 0x149, 0x17F, 0x1E9E, 0x3A3, 0x3C2, 0x390, 0x1F80, 0xFB03,
	0x300, 0x301, 0x302, 0x308, 0x327, 0x328, 0x323, 0x31B, 0x345, 0x344, 0x340, 0x334, 0x5B0, 0x5C2, 0x6DD, 0x600,
	0x1100, 0x1161, 0x11A8, 0x11A7, 0x1175, 0xAC00, 0xAC01, 0xD7A3, 0x3131,
	0x200D, 0x200C, 0xFE0F, 0xFE0E, 0x1F600, 0x1F468, 0x1F469, 0x1F3FB, 0x2764, 0xA9, 0x1F1E6, 0x1F1FA, 0x1F1F8,
	0x915, 0x93C, 0x94D, 0x937, 0x930, 0x9CD, 0x995, 0x903, 0x93F, 0xD4D, 0xD15,
	0x212B, 0x2126, 0x212A, 0x1E0A, 0x1E0C, 0x1E9B, 0xF73, 0xF71, 0xF72, 0xF80, 0xFDFA, 0x3300, 0xFF21, 0x2460, 0x1D15E, 0x1109A, 0x110BA,
	0x4E2D, 0xFF5E, 0x3000, 0x1F1E6, 0xE0001, 0xE0020, 0x10FFFD, 0x20000, 0x2E3A,
}

// text2Random is n seeded strings of up to 12 scalars from text2Classes,
// with one scalar in eight drawn from all of Unicode.
func text2Random(seed uint64, n int) [][]rune {
	return text2RandomMix(seed, n, 12, 0)
}

// text2RandomMix is n seeded strings of up to maxLen scalars; ascii in 100
// of them are ASCII letters of either case or spaces, so runs of eight
// ASCII bytes or more (the word-at-a-time paths) start and end at every
// offset; the rest are as text2Random's.
func text2RandomMix(seed uint64, n, maxLen, ascii int) [][]rune {
	r := rand.New(rand.NewPCG(seed, 0x5EED))
	out := make([][]rune, n)
	for i := range out {
		s := make([]rune, r.IntN(maxLen+1))
		for k := range s {
			if r.IntN(100) < ascii {
				s[k] = rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ "[r.IntN(53)])
			} else if r.IntN(8) == 0 {
				for {
					c := rune(r.IntN(0x110000))
					if c < 0xD800 || c > 0xDFFF {
						s[k] = c
						break
					}
				}
			} else {
				s[k] = text2Classes[r.IntN(len(text2Classes))]
			}
		}
		out[i] = s
	}
	return out
}

// TestSTDTEXT_DifferentialAgainstModels compares graphemes, width, the
// four forms and folding on 25,000 seeded strings with the Go models:
// 20,000 short ones over the classes, 5,000 of up to 48 scalars that are
// mostly ASCII.
func TestSTDTEXT_DifferentialAgainstModels(t *testing.T) {
	u := needUCD(t)
	strs := append(text2Random(1, 20000), text2RandomMix(4, 5000, 48, 85)...)
	var lines []string
	for _, s := range strs {
		lines = append(lines, hexLine(s))
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		g := text2Run(t, release, "g", lines)
		w := text2Run(t, release, "w", lines)
		nf := text2Run(t, release, "n", lines)
		f := text2Run(t, release, "f", lines)
		bad := 0
		report := func(format string, args ...any) {
			if bad < 30 {
				t.Errorf(format, args...)
			}
			bad++
		}
		for i, s := range strs {
			var lens []string
			for _, c := range u.mGraphemes(s) {
				lens = append(lens, strconv.Itoa(len(c)))
			}
			if want := strings.Join(lens, " "); g[i] != want {
				report("graphemes %s: got %q, want %q", lines[i], g[i], want)
			}
			if want := strconv.Itoa(u.mWidth(s)); w[i] != want {
				report("width %s: got %s, want %s", lines[i], w[i], want)
			}
			var forms []string
			for form := 0; form < 4; form++ {
				forms = append(forms, hexLine(u.mNormalize(s, form)))
			}
			if want := strings.Join(forms, ";"); nf[i] != want {
				report("normalize %s: got %s, want %s", lines[i], nf[i], want)
			}
			if want := hexLine(u.mFold(s)); f[i] != want {
				report("fold %s: got %s, want %s", lines[i], f[i], want)
			}
		}
		if bad > 0 {
			t.Errorf("%d mismatches", bad)
		}
	})
}

// TestSTDTEXT_Utf16AgainstGo checks ToUtf16 and FromUtf16 against Go's
// unicode/utf16 on seeded strings, and FromUtf16 on unit sequences with
// unpaired surrogates in every position.
func TestSTDTEXT_Utf16AgainstGo(t *testing.T) {
	strs := append(text2Random(2, 5000), text2RandomMix(5, 2000, 48, 85)...)
	strs = append(strs, nil, []rune{0x10000}, []rune{0x10FFFF, 'a'}, []rune{0xFFFF, 0xE000, 0xD7FF})
	var lines []string
	for _, s := range strs {
		lines = append(lines, hexLine(s))
	}
	r := rand.New(rand.NewPCG(3, 3))
	var units [][]uint16
	for i := 0; i < 5000; i++ {
		u := utf16.Encode(strs[i%len(strs)])
		if i%2 == 0 && len(u) > 0 {
			// Put a lone surrogate somewhere, or cut a pair.
			k := r.IntN(len(u) + 1)
			switch r.IntN(3) {
			case 0:
				u = slices.Insert(u, k, uint16(0xD800+r.IntN(0x400)))
			case 1:
				u = slices.Insert(u, k, uint16(0xDC00+r.IntN(0x400)))
			case 2:
				u = u[:k]
			}
		}
		units = append(units, u)
	}
	units = append(units, []uint16{0xD800}, []uint16{0xDC00}, []uint16{0xDBFF, 0xDFFF}, []uint16{0xDC00, 0xD800}, []uint16{'a', 0xD800, 'b'}, nil)
	var ulines []string
	for _, u := range units {
		var b strings.Builder
		for i, x := range u {
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%x", x)
		}
		ulines = append(ulines, b.String())
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		got := text2Run(t, release, "u", lines)
		for i, s := range strs {
			var b strings.Builder
			for k, x := range utf16.Encode(s) {
				if k > 0 {
					b.WriteByte(' ')
				}
				fmt.Fprintf(&b, "%x", x)
			}
			if want := b.String() + ";ok"; got[i] != want {
				t.Errorf("ToUtf16 %s: got %s, want %s", lines[i], got[i], want)
			}
		}
		sg := text2Run(t, release, "s", ulines)
		for i, u := range units {
			want := "err"
			if valid16(u) {
				want = hexLine(utf16.Decode(u))
			}
			if sg[i] != want {
				t.Errorf("FromUtf16 %s: got %s, want %s", ulines[i], sg[i], want)
			}
		}
	})
}

// valid16 reports whether u has no unpaired surrogate.
func valid16(u []uint16) bool {
	for i := 0; i < len(u); i++ {
		switch {
		case u[i] >= 0xD800 && u[i] <= 0xDBFF:
			if i+1 >= len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				return false
			}
			i++
		case u[i] >= 0xDC00 && u[i] <= 0xDFFF:
			return false
		}
	}
	return true
}
