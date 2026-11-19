// Why: std.text's line breaking (UAX 14) and bidi (UAX 9) Mk code is judged
// by the Unicode conformance files.
// What: every line of LineBreakTest.txt, BidiCharacterTest.txt and
// BidiTest.txt at 100 percent, and TestSTDTEXT_Lookup on every scalar.
// How: std.text built natively, dev and release, as an oracle program that
// answers one request a line.

package std

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// uaxProg answers one request a line, until end of input:
//
//	l HEX        the LineBreaks of the text: "at" or "at!" (mandatory), each
//	             followed by a space
//	b D HEX      BidiLevels in direction D (0 Ltr, 1 Rtl, 2 Auto):
//	             "base;" then At of each scalar, then ";" and the runs of
//	             Line over the whole text as "start,end,level", each
//	             followed by a space
//	p LO COUNT   FxUaxProps of the code points from LO
//
// HEX is the text's UTF-8 in hex; "utf8" answers a text that is not UTF-8.
const uaxProg = `import std.text

HexVal(c: u8): int {
    if c >= 0x61 {
        return int(c) - 0x61 + 10
    }
    if c >= 0x41 {
        return int(c) - 0x41 + 10
    }
    return int(c) - 0x30
}

Unhex(a: &Arena, h: []u8): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](h.Len() / 2)
    for i in 0..out.Len() {
        out[i] = Trunc[u8](HexVal(h[2 * i]) * 16 + HexVal(h[2 * i + 1]))
    }
    return out[..]
}

Num(s: []u8): int {
    var v := 0
    for c in s {
        v = v * 10 + int(c) - 0x30
    }
    return v
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var r := sys.Stdin()
    var out := sys.Stdout()
    var a := try Arena.New(sys.heap)
    defer a.Free()
    for {
        line := try r.Line(&a) else {
            break
        }
        lb := line.AsBytes()
        op := lb[0]
        if op == 0x70 {
            sp := line[2..].Find(" ") else { 0 }
            lo := Num(lb[2..2 + sp])
            count := Num(lb[3 + sp..])
            try out.Str(try text.FxUaxProps(&a, lo, count))
            try out.Str("\n")
            a.Reset()
            continue
        }
        hex := if op == 0x62 { lb[4..] } else { lb[2..] }
        s := str.From(try Unhex(&a, hex)) else {
            try out.Str("utf8\n")
            a.Reset()
            continue
        }
        if op == 0x6C {
            for br in text.LineBreaks(s) {
                if br.mandatory {
                    try Print(mut out, "{at}! ", (at: br.at))
                } else {
                    try Print(mut out, "{at} ", (at: br.at))
                }
            }
        } else {
            dir := if lb[2] == 0x30 { text.Direction.Ltr } else if lb[2] == 0x31 { text.Direction.Rtl } else { text.Direction.Auto }
            lv := try text.BidiLevels(&a, s, dir)
            try Print(mut out, "{b};", (b: lv.Base()))
            sb := s.AsBytes()
            for p in 0..sb.Len() {
                if sb[p] & 0xC0 != 0x80 {
                    try Print(mut out, "{l} ", (l: lv.At(p)))
                }
            }
            try out.Str(";")
            for run in try lv.Line(&a, 0, s.Len()) {
                try Print(mut out, "{s},{e},{l} ", (s: run.start, e: run.end, l: run.level))
            }
        }
        try out.Str("\n")
        a.Reset()
    }
    try out.Flush()
}
`

// uaxFixture is written to std/text in the oracle's copy of std: it
// exports the private lookups.
const uaxFixture = `// Test fixture of std/std-textlines_mk_test.go (not in std): the lookups of
// bidi.mk and linebreak.mk, for TestSTDTEXT_Lookup.

// FxUaxProps is, for each code point from lo, its BidiProp byte, its
// LineProp byte, its bracket key and its mirroring glyph (0 for none), as
// 2, 2, 6 and 6 hex digits.
export FxUaxProps(a: &Arena, lo: int, count: int): OutOfMemory!str from a {
    digits := "0123456789abcdef".AsBytes()
    out := try a.Slice[u8](16 * count)
    for k in 0..count {
        c := Cast[u32](lo + k)
        i := BidiFind(BidiMirrorCp[..], c)
        to := &BidiMirrorTo
        m := if i < 0 { u32(0) } else { to[i] }
        v := (u64(BidiPropOf(c)) << 56) | (u64(LinePropOf(c)) << 48) | (u64(BracketKey(c)) << 24) | u64(m)
        for j in 0..16 {
            out[16 * k + j] = digits[Trunc[int]((v >> Cast[u64](60 - 4 * j)) & 15)]
        }
    }
    return str.From(out[..]).Must()
}
`

// uaxBuilt holds one oracle executable per build.
var uaxBuilt struct {
	sync.Mutex
	exe map[bool]string
}

func uaxExe(t *testing.T, release bool) string {
	t.Helper()
	uaxBuilt.Lock()
	defer uaxBuilt.Unlock()
	if e, ok := uaxBuilt.exe[release]; ok {
		return e
	}
	// The executable outlives the test that builds it, so it goes in a
	// package temporary directory, not t.TempDir.
	root, err := packageTempDir("mktext4")
	if err != nil {
		t.Fatal(err)
	}
	stdDir := copyStd(t, root, map[string]string{"text/tests_fixture4.mk": uaxFixture})
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(uaxProg), 0o644); err != nil {
		t.Fatal(err)
	}
	e := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		e += ".exe"
	}
	args := []string{"build"}
	if release {
		args = append(args, "--release")
	}
	out, status := mkCmd(t, root, stdDir, append(args, "-o", e, "main.mk")...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	if uaxBuilt.exe == nil {
		uaxBuilt.exe = map[bool]string{}
	}
	uaxBuilt.exe[release] = e
	return e
}

// uaxAsk sends the requests to the oracle in batches and returns one
// answer per request.
func uaxAsk(t *testing.T, release bool, reqs []string, batch int) []string {
	t.Helper()
	exe := uaxExe(t, release)
	var answers []string
	for start := 0; start < len(reqs); start += batch {
		end := min(start+batch, len(reqs))
		var in strings.Builder
		for _, r := range reqs[start:end] {
			in.WriteString(r)
			in.WriteByte('\n')
		}
		r := runNative(t, exe, in.String())
		if r.code != 0 {
			t.Fatalf("oracle (release %v): exit %d\nstderr: %s", release, r.code, r.stderr)
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if len(got) != end-start {
			t.Fatalf("oracle (release %v): %d answers for %d requests\nstderr: %s", release, len(got), end-start, r.stderr)
		}
		answers = append(answers, got...)
	}
	return answers
}

// uaxHex is s's UTF-8 in hex.
func uaxHex(s []rune) string {
	return fmt.Sprintf("%x", string(s))
}

// uaxBuilds runs f for the dev and the release build.
func uaxBuilds(t *testing.T, f func(t *testing.T, release bool)) {
	for _, release := range []bool{false, true} {
		name := "dev"
		if release {
			name = "release"
		}
		t.Run(name, func(t *testing.T) { f(t, release) })
	}
}

// lbIsHard reports whether a break after c is mandatory (LB4, LB5), next
// being the scalar after it or -1.
func lbIsHard(d *uaxData, c, next rune) bool {
	switch d.lbRaw[c] {
	case "BK", "LF", "NL":
		return true
	case "CR":
		return next != 0x0A
	}
	return false
}

// TestSTDTEXT_LineBreakTest runs every line of the UCD's
// LineBreakTest.txt through text.LineBreaks.
//
// LineBreakTest.txt gives the break positions and each one's kind,
// mandatory exactly after BK, LF, NL and a CR not before LF.
func TestSTDTEXT_LineBreakTest(t *testing.T) {
	d := needUax(t)
	cases := readBreakTest(t, "LineBreakTest.txt")
	reqs := make([]string, len(cases))
	wants := make([]string, len(cases))
	for i, c := range cases {
		reqs[i] = "l " + uaxHex(c.s)
		off := make([]int, len(c.s)+1)
		for k, r := range c.s {
			off[k+1] = off[k] + utf8.RuneLen(r)
		}
		var w strings.Builder
		for _, k := range c.bounds {
			if k == 0 {
				continue
			}
			next := rune(-1)
			if k < len(c.s) {
				next = c.s[k]
			}
			fmt.Fprintf(&w, "%d", off[k])
			if lbIsHard(d, c.s[k-1], next) {
				w.WriteByte('!')
			}
			w.WriteByte(' ')
		}
		wants[i] = w.String()
	}
	uaxBuilds(t, func(t *testing.T, release bool) {
		got := uaxAsk(t, release, reqs, 5000)
		bad := 0
		for i := range cases {
			if got[i] != wants[i] {
				bad++
				if bad <= 10 {
					t.Errorf("%s: got %q, want %q", hexLine(cases[i].s), got[i], wants[i])
				}
			}
		}
		t.Logf("LineBreakTest.txt: %d of %d cases", len(cases)-bad, len(cases))
	})
}

// bidiCase is one paragraph of a bidi test: the text, the direction (0
// Ltr, 1 Rtl, 2 Auto), the paragraph level (-1 when the file gives none),
// the levels after L1 (-1 for x) and the visual order.
type bidiCase struct {
	line  int
	s     []rune
	dir   int
	base  int
	level []int
	order []int
}

// bidiParseLevels reads "1 x 0" as levels, x as -1.
func bidiParseLevels(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Fields(s) {
		if f == "x" {
			out = append(out, -1)
			continue
		}
		v, err := strconv.Atoi(f)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func bidiParseInts(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Fields(s) {
		v, err := strconv.Atoi(f)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// readBidiCharacterTest reads the UCD's BidiCharacterTest.txt.
func readBidiCharacterTest(t *testing.T) []bidiCase {
	t.Helper()
	f, err := os.Open(filepath.Join(ucdDir(), "BidiCharacterTest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []bidiCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fl := strings.Split(line, ";")
		if len(fl) != 5 {
			t.Fatalf("BidiCharacterTest.txt:%d: %d fields", n, len(fl))
		}
		s, err := ucdScalars(fl[0])
		if err != nil {
			t.Fatal(err)
		}
		c := bidiCase{line: n, s: s}
		if c.dir, err = strconv.Atoi(strings.TrimSpace(fl[1])); err != nil {
			t.Fatal(err)
		}
		if c.base, err = strconv.Atoi(strings.TrimSpace(fl[2])); err != nil {
			t.Fatal(err)
		}
		if c.level, err = bidiParseLevels(fl[3]); err != nil {
			t.Fatal(err)
		}
		if c.order, err = bidiParseInts(fl[4]); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

// bidiRep is the scalar BidiTest.txt's classes are written as: none is a
// paired bracket (the file assumes there are none).
var bidiRep = map[string]rune{
	"L": 'a', "R": 0x05D0, "AL": 0x0627, "EN": '1', "ES": '+', "ET": '#',
	"AN": 0x0660, "CS": ',', "NSM": 0x0300, "BN": 0x00AD, "B": 0x2029,
	"S": '\t', "WS": ' ', "ON": '!', "LRE": 0x202A, "LRO": 0x202D,
	"RLE": 0x202B, "RLO": 0x202E, "PDF": 0x202C, "LRI": 0x2066,
	"RLI": 0x2067, "FSI": 0x2068, "PDI": 0x2069,
}

// readBidiTest reads the UCD's BidiTest.txt, one case per data line and
// paragraph direction of its bitset.
func readBidiTest(t *testing.T, d *uaxData) []bidiCase {
	t.Helper()
	for name, c := range bidiRep {
		k, err := urlIndexOf(urlBidiNames, name)
		if err != nil {
			t.Fatal(err)
		}
		if d.bidi[c] != k {
			t.Fatalf("BidiTest's %s is written as U+%04X, whose BidiProp is %#x", name, c, d.bidi[c])
		}
	}
	f, err := os.Open(filepath.Join(ucdDir(), "BidiTest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []bidiCase
	var levels, order []int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line, _, _ := strings.Cut(sc.Text(), "#")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "@Levels:"); ok {
			if levels, err = bidiParseLevels(rest); err != nil {
				t.Fatalf("BidiTest.txt:%d: %v", n, err)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "@Reorder:"); ok {
			if order, err = bidiParseInts(rest); err != nil {
				t.Fatalf("BidiTest.txt:%d: %v", n, err)
			}
			continue
		}
		if strings.HasPrefix(line, "@") {
			continue
		}
		in, bits, ok := strings.Cut(line, ";")
		if !ok {
			t.Fatalf("BidiTest.txt:%d: no bitset", n)
		}
		var s []rune
		for _, w := range strings.Fields(in) {
			c, ok := bidiRep[w]
			if !ok {
				t.Fatalf("BidiTest.txt:%d: unknown class %q", n, w)
			}
			s = append(s, c)
		}
		set, err := strconv.ParseUint(strings.TrimSpace(bits), 16, 8)
		if err != nil {
			t.Fatalf("BidiTest.txt:%d: %v", n, err)
		}
		// Bit 1 is auto (2), bit 2 LTR (0), bit 4 RTL (1).
		for _, bd := range [][2]int{{1, 2}, {2, 0}, {4, 1}} {
			if set&uint64(bd[0]) != 0 {
				cases = append(cases, bidiCase{line: n, s: s, dir: bd[1], base: -1, level: levels, order: order})
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

// bidiJudge compares the oracle's answer for c with c's expectations and
// returns what differs, "" when nothing does.
func bidiJudge(c bidiCase, answer string) string {
	parts := strings.Split(answer, ";")
	if len(parts) != 3 {
		return "malformed answer " + strconv.Quote(answer)
	}
	base, err := strconv.Atoi(parts[0])
	if err != nil {
		return "malformed base " + strconv.Quote(answer)
	}
	if c.base >= 0 && base != c.base {
		return fmt.Sprintf("paragraph level %d, want %d", base, c.base)
	}
	at, err := bidiParseInts(parts[1])
	if err != nil || len(at) != len(c.s) {
		return "malformed levels " + strconv.Quote(answer)
	}
	// The byte offset of each scalar, and the scalar at each byte.
	off := make([]int, len(c.s)+1)
	for k, r := range c.s {
		off[k+1] = off[k] + utf8.RuneLen(r)
	}
	scalarAt := make([]int, off[len(c.s)])
	for k := range c.s {
		for b := off[k]; b < off[k+1]; b++ {
			scalarAt[b] = k
		}
	}
	got := make([]int, len(c.s))
	var order []int
	cover := 0
	for _, f := range strings.Fields(parts[2]) {
		var s, e, l int
		if _, err := fmt.Sscanf(f, "%d,%d,%d", &s, &e, &l); err != nil {
			return "malformed run " + strconv.Quote(f)
		}
		if s >= e || s < 0 || e > len(scalarAt) {
			return "bad run " + strconv.Quote(f)
		}
		cover += e - s
		var ks []int
		for b := s; b < e; b++ {
			if b == off[scalarAt[b]] {
				ks = append(ks, scalarAt[b])
			}
		}
		if l%2 == 1 {
			for i, j := 0, len(ks)-1; i < j; i, j = i+1, j-1 {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
		for _, k := range ks {
			got[k] = l
			if c.level[k] >= 0 {
				order = append(order, k)
			}
		}
	}
	if cover != len(scalarAt) {
		return fmt.Sprintf("runs cover %d of %d bytes", cover, len(scalarAt))
	}
	for k := range c.s {
		if c.level[k] >= 0 && got[k] != c.level[k] {
			return fmt.Sprintf("levels %v, want %v", got, c.level)
		}
	}
	if !sameInts(order, c.order) {
		return fmt.Sprintf("order %v, want %v", order, c.order)
	}
	return ""
}

func bidiRun(t *testing.T, name string, cases []bidiCase, batch int) {
	reqs := make([]string, len(cases))
	for i, c := range cases {
		reqs[i] = fmt.Sprintf("b %d %s", c.dir, uaxHex(c.s))
	}
	uaxBuilds(t, func(t *testing.T, release bool) {
		got := uaxAsk(t, release, reqs, batch)
		bad := 0
		for i, c := range cases {
			if why := bidiJudge(c, got[i]); why != "" {
				bad++
				if bad <= 10 {
					t.Errorf("%s:%d (direction %d) %s: %s", name, c.line, c.dir, hexLine(c.s), why)
				}
			}
		}
		t.Logf("%s: %d of %d cases", name, len(cases)-bad, len(cases))
	})
}

// TestSTDTEXT_BidiCharacterTest runs every line of the UCD's
// BidiCharacterTest.txt through text.BidiLevels and Levels.Line: the
// paragraph level, the levels and the order.
func TestSTDTEXT_BidiCharacterTest(t *testing.T) {
	needUax(t)
	bidiRun(t, "BidiCharacterTest.txt", readBidiCharacterTest(t), 10000)
}

// TestSTDTEXT_BidiTest runs every case of the UCD's BidiTest.txt through
// text.BidiLevels and Levels.Line: the levels and the order, each
// Bidi_Class written as one scalar of it.
func TestSTDTEXT_BidiTest(t *testing.T) {
	d := needUax(t)
	bidiRun(t, "BidiTest.txt", readBidiTest(t, d), 20000)
}

// TestSTDTEXT_Lookup compares the Mk lookups of every code point
// (BidiPropOf, LinePropOf, BracketKey and the mirroring table) with the
// generator's data.
func TestSTDTEXT_Lookup(t *testing.T) {
	d := needUax(t)
	const chunk = 0x8000
	var reqs []string
	for lo := 0; lo < 0x110000; lo += chunk {
		reqs = append(reqs, fmt.Sprintf("p %d %d", lo, chunk))
	}
	got := uaxAsk(t, true, reqs, 8)
	bad := 0
	for i, a := range got {
		if len(a) != 16*chunk {
			t.Fatalf("chunk %d: answer of %d bytes", i, len(a))
		}
		for k := 0; k < chunk; k++ {
			c := rune(i*chunk + k)
			want := fmt.Sprintf("%02x%02x%06x%06x", d.bidi[c], d.line[c], d.bracketKey[c], d.mirror[c])
			if g := a[16*k : 16*k+16]; g != want {
				bad++
				if bad <= 10 {
					t.Errorf("U+%04X: got %s, want %s", c, g, want)
				}
			}
		}
	}
}

// TestSTDTEXT_LinesTestBlocks runs the test blocks of std/text/tests_bidi.mk
// and tests_linebreak.mk through mk test --std=std.text, dev and release.
func TestSTDTEXT_LinesTestBlocks(t *testing.T) {
	var names []string
	for _, f := range []string{"tests_bidi.mk", "tests_linebreak.mk"} {
		src, err := os.ReadFile(filepath.Join("text", f))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, testNames(string(src))...)
	}
	if len(names) < 10 {
		t.Fatalf("%d blocks, want at least 10", len(names))
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		root := t.TempDir()
		stdDir := copyStd(t, root, nil)
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
