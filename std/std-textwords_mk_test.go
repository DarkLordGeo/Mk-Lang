// Why: text.WordBounds and text.SentenceBounds must split exactly as UAX 29
// says, on every UCD test line and on input no test file holds.
// What: TestSTDTEXT_WordBreakTest, SentenceBreakTest,
// DifferentialAgainstModels, Adversarial, TestBlocks and FuzzSTDTEXT_Bounds.
// How: one long-running oracle process per build answers one text a request.

// The tests of text.WordBounds and text.SentenceBounds' Mk
// code: std.text built natively, dev and release, as one long-running
// oracle process per build that answers one text a request; every line of
// the UCD's WordBreakTest.txt and SentenceBreakTest.txt (the oracle, at
// 100 percent), generated strings against the Go models of
// std-textwords_model_test.go, adversarial inputs that would make a scan with
// overlapping look-aheads quadratic, and a fuzz target seeded from the
// UCD lines:
//
//	go test ./std -run '^$' -fuzz FuzzSTDTEXT_Bounds -fuzztime 5m -parallel 2
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// text3Prog reads requests until end of input: a mode byte ('w' for
// WordBounds, 's' for SentenceBounds), the text's length as four bytes
// little-endian, and the text. It answers each with the boundaries, each
// followed by a space, then a newline; "utf8" when the text is not UTF-8.
// The loops are the for-in form the reference shows.
const text3Prog = `import std.text

export Main(sys: &System): WriteError|ReadError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    var hdr: [5]u8 = [0; 5]
    var a := try Arena.New(sys.heap)
    defer a.Free()
    for {
        got := try inp.ReadAll(mut hdr[..])
        if got < 5 {
            return
        }
        n := int(hdr[1]) | (int(hdr[2]) << 8) | (int(hdr[3]) << 16) | (int(hdr[4]) << 24)
        data := try a.Slice[u8](n)
        _ = try inp.ReadAll(data)
        s := str.From(data[..]) else {
            try out.Str("utf8\n")
            try out.Flush()
            a.Reset()
            continue
        }
        if hdr[0] == 0x73 {
            for b in text.SentenceBounds(s) {
                try Print(mut out, "{b} ", (b: b))
            }
        } else {
            for b in text.WordBounds(s) {
                try Print(mut out, "{b} ", (b: b))
            }
        }
        try out.Str("\n")
        try out.Flush()
        a.Reset()
    }
}
`

// text3Server is a running oracle with its pipes.
type text3Server struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	errb *bytes.Buffer
}

var text3Srv struct {
	sync.Mutex
	m map[bool]*text3Server
}

func text3Start(t *testing.T, release bool) *text3Server {
	t.Helper()
	text3Srv.Lock()
	defer text3Srv.Unlock()
	if s, ok := text3Srv.m[release]; ok {
		return s
	}
	var flags []string
	if release {
		flags = append(flags, "--release")
	}
	exe := text2BuildSrc(t, text3Prog, flags...)
	cmd := exec.Command(exe)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &text3Server{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<16), errb: &errb}
	if text3Srv.m == nil {
		text3Srv.m = map[bool]*text3Server{}
	}
	text3Srv.m[release] = s
	return s
}

// text3Ask returns the boundaries the oracle gives text in mode ('w' or
// 's'); a dead oracle fails the test with its stderr.
func text3Ask(t *testing.T, release bool, mode byte, text string) []int {
	t.Helper()
	s := text3Start(t, release)
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func(err error) {
		text3Srv.Lock()
		delete(text3Srv.m, release)
		text3Srv.Unlock()
		s.in.Close()
		s.cmd.Wait()
		t.Fatalf("oracle (release %v): %v\nstderr:\n%s", release, err, s.errb.String())
	}
	var hdr [5]byte
	hdr[0] = mode
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(text)))
	// The request goes in from a goroutine: a large text could fill the
	// pipe while the oracle's answer waits to be read.
	werr := make(chan error, 1)
	go func() {
		_, err := s.in.Write(append(hdr[:], text...))
		werr <- err
	}()
	line, err := s.out.ReadString('\n')
	if err != nil {
		fail(err)
	}
	if err := <-werr; err != nil {
		fail(err)
	}
	line = strings.TrimSpace(line)
	if line == "utf8" {
		t.Fatalf("oracle refused %q as not UTF-8", text)
	}
	var out []int
	for _, f := range strings.Fields(line) {
		v, err := strconv.Atoi(f)
		if err != nil {
			fail(fmt.Errorf("bad answer %q", line))
		}
		out = append(out, v)
	}
	return out
}

// byteBounds maps boundaries given as scalar indices of s to byte offsets
// of its UTF-8 encoding.
func byteBounds(s []rune, idx []int) []int {
	off := make([]int, len(s)+1)
	for i, c := range s {
		off[i+1] = off[i] + utf8.RuneLen(c)
	}
	out := make([]int, 0, len(idx))
	for _, k := range idx {
		out = append(out, off[k])
	}
	return out
}

func text3Builds(t *testing.T, f func(t *testing.T, release bool)) {
	for _, release := range []bool{false, true} {
		name := "dev"
		if release {
			name = "release"
		}
		t.Run(name, func(t *testing.T) { f(t, release) })
	}
}

// text3Check runs every case through the oracle in mode and reports the
// pass rate; want gives a case's boundaries as byte offsets.
func text3Check(t *testing.T, release bool, mode byte, name string, cases [][]rune, want func(i int) []int) {
	t.Helper()
	bad := 0
	for i, c := range cases {
		got := text3Ask(t, release, mode, string(c))
		if w := want(i); !sameInts(got, w) {
			bad++
			if bad <= 10 {
				t.Errorf("%s %c: %s: got %v, want %v", name, mode, hexLine(c), got, w)
			}
		}
	}
	t.Logf("%s %c: %d of %d cases", name, mode, len(cases)-bad, len(cases))
}

// TestSTDTEXT_WordBreakTest runs every line of the UCD's
// WordBreakTest.txt through text.WordBounds.
func TestSTDTEXT_WordBreakTest(t *testing.T) {
	needSeg(t)
	text3UCDFile(t, "WordBreakTest.txt", 'w')
}

// TestSTDTEXT_SentenceBreakTest runs every line of the UCD's
// SentenceBreakTest.txt through text.SentenceBounds.
func TestSTDTEXT_SentenceBreakTest(t *testing.T) {
	needSeg(t)
	text3UCDFile(t, "SentenceBreakTest.txt", 's')
}

func text3UCDFile(t *testing.T, file string, mode byte) {
	cases := readBreakTest(t, file)
	var ss [][]rune
	for _, c := range cases {
		ss = append(ss, c.s)
	}
	text3Builds(t, func(t *testing.T, release bool) {
		text3Check(t, release, mode, file, ss, func(i int) []int {
			return byteBounds(cases[i].s, cases[i].bounds)
		})
	})
}

// text3Pool is a few scalars of every Word_Break and Sentence_Break value
// and of Extended_Pictographic (the first, middle and last of each, in
// scalar order), for generated strings.
func text3Pool(d *segData) []rune {
	type key struct{ wb, sb uint8 }
	byKey := map[key][]rune{}
	for c := rune(0); c < 0x110000; c++ {
		if c >= 0xD800 && c < 0xE000 {
			continue
		}
		k := key{d.wb[c], d.sb[c]}
		byKey[k] = append(byKey[k], c)
	}
	var pool []rune
	for _, cs := range byKey {
		pool = append(pool, cs[0], cs[len(cs)/2], cs[len(cs)-1])
	}
	// Map order is random: sort for one sequence per seed.
	slices.Sort(pool)
	return pool
}

var text3PoolCache struct {
	once sync.Once
	pool []rune
}

// text3PoolOnce is text3Pool, computed once per test binary.
func text3PoolOnce(d *segData) []rune {
	text3PoolCache.once.Do(func() { text3PoolCache.pool = text3Pool(d) })
	return text3PoolCache.pool
}

// text3Random is n strings of 0 to maxLen scalars, each from the pool or,
// one time in ascii, a printable ASCII byte.
func text3Random(pool []rune, seed uint64, n, maxLen, ascii int) [][]rune {
	r := rand.New(rand.NewPCG(seed, 3))
	out := make([][]rune, n)
	for i := range out {
		s := make([]rune, r.IntN(maxLen+1))
		for j := range s {
			if r.IntN(ascii) == 0 {
				s[j] = rune(0x20 + r.IntN(0x5F))
			} else {
				s[j] = pool[r.IntN(len(pool))]
			}
		}
		out[i] = s
	}
	return out
}

// TestSTDTEXT_WordsDifferentialAgainstModels compares both iterators with the
// Go models on generated strings over every class pair.
func TestSTDTEXT_WordsDifferentialAgainstModels(t *testing.T) {
	d := needSeg(t)
	pool := text3PoolOnce(d)
	cases := text3Random(pool, 1, 4000, 24, 3)
	cases = append(cases, text3Random(pool, 2, 1000, 200, 2)...)
	text3Builds(t, func(t *testing.T, release bool) {
		text3Check(t, release, 'w', "generated", cases, func(i int) []int {
			return byteBounds(cases[i], d.mWordBounds(cases[i]))
		})
		text3Check(t, release, 's', "generated", cases, func(i int) []int {
			return byteBounds(cases[i], d.mSentenceBounds(cases[i]))
		})
	})
}

// text3Rep is head, then unit n times, then tail.
func text3Rep(head, unit string, n int, tail string) string {
	return head + strings.Repeat(unit, n) + tail
}

// TestSTDTEXT_WordsAdversarial feeds the shapes that reach the look-aheads
// and the long left contexts, at a size where overlapping scans would be
// quadratic (10^12 steps), and checks each answer by its structure. The
// same shapes at a size the Go models can afford are checked against
// them.
func TestSTDTEXT_WordsAdversarial(t *testing.T) {
	d := needSeg(t)
	const big = 1 << 20
	type adv struct {
		name string
		mode byte
		s    func(n int) string
		// want gives the boundaries of s(n) as byte offsets.
		want func(n int) []int
	}
	ext := "̈" // Extend (WB and SB), two bytes
	cases := []adv{
		{"word: letter, apostrophe, marks, letter", 'w',
			func(n int) string { return text3Rep("a'", ext, n, "b") },
			func(n int) []int { return []int{0, 2*n + 3} }},
		{"word: letter, apostrophe, marks, end", 'w',
			func(n int) string { return text3Rep("a'", ext, n, "") },
			func(n int) []int { return []int{0, 1, 2*n + 2} }},
		{"word: digits and commas", 'w',
			func(n int) string { return text3Rep("1", ",1", n, "") },
			func(n int) []int { return []int{0, 2*n + 1} }},
		{"word: regional indicators", 'w',
			func(n int) string { return strings.Repeat("\U0001F1E6", 2*n+1) },
			func(n int) []int {
				var b []int
				for i := 0; i <= n; i++ {
					b = append(b, 8*i)
				}
				return append(b, 8*n+4)
			}},
		{"word: one mark run", 'w',
			func(n int) string { return text3Rep("a", ext, n, "") },
			func(n int) []int { return []int{0, 2*n + 1} }},
		{"sentence: spaces after a full stop, then upper", 's',
			func(n int) string { return text3Rep("a. ", " ", n, "X") },
			func(n int) []int { return []int{0, n + 3, n + 4} }},
		{"sentence: spaces after a full stop, then lower", 's',
			func(n int) string { return text3Rep("a.", " ", n, "x") },
			func(n int) []int { return []int{0, n + 3} }},
		{"sentence: closes after a full stop, digits, then lower", 's',
			func(n int) string { return text3Rep("a.", ")", n, text3Rep(" ", "1", n, "x")) },
			func(n int) []int { return []int{0, 2*n + 4} }},
		{"sentence: digits after a full stop, then end", 's',
			func(n int) string { return text3Rep("a. ", "1", n, "") },
			func(n int) []int {
				if n == 0 {
					return []int{0, 3}
				}
				return []int{0, 3, n + 3}
			}},
		{"sentence: many short sentences", 's',
			func(n int) string { return strings.Repeat("A. ", n) },
			func(n int) []int {
				var b []int
				for i := 0; i <= n && n > 0; i++ {
					b = append(b, 3*i)
				}
				return b
			}},
	}
	text3Builds(t, func(t *testing.T, release bool) {
		for _, c := range cases {
			small := c.s(300)
			if w, m := c.want(300), text3ModelBytes(d, c.mode, small); !sameInts(w, m) {
				t.Fatalf("%s: the expectation %v disagrees with the model %v", c.name, w, m)
			}
			for _, n := range []int{0, 1, 2, 300, big} {
				got := text3Ask(t, release, c.mode, c.s(n))
				if w := c.want(n); !sameInts(got, w) {
					t.Errorf("%s, n = %d: got %v, want %v", c.name, n, text3Head(got), text3Head(w))
				}
			}
		}
	})
}

// TestSTDTEXT_WordsTestBlocks runs the test blocks of std/text/tests_bounds.mk
// through mk test --std=std.text, dev and release.
func TestSTDTEXT_WordsTestBlocks(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("text", "tests_bounds.mk"))
	if err != nil {
		t.Fatal(err)
	}
	names := testNames(string(src))
	if len(names) < 6 {
		t.Fatalf("tests_bounds.mk has %d blocks, want at least 6", len(names))
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

// text3ModelBytes is the model's answer for s in mode, as byte offsets.
func text3ModelBytes(d *segData, mode byte, s string) []int {
	rs := []rune(s)
	if mode == 's' {
		return byteBounds(rs, d.mSentenceBounds(rs))
	}
	return byteBounds(rs, d.mWordBounds(rs))
}

// text3Head is at most the first 8 values of b, then its length.
func text3Head(b []int) string {
	if len(b) <= 8 {
		return fmt.Sprint(b)
	}
	return fmt.Sprintf("%v... (%d)", b[:8], len(b))
}

// text3FuzzText turns fuzz bytes into text: valid UTF-8 stays, and each
// byte of an invalid sequence picks a scalar of the pool, so mutations
// reach every class.
func text3FuzzText(pool []rune, data []byte) []rune {
	var out []rune
	for len(data) > 0 {
		c, n := utf8.DecodeRune(data)
		if c == utf8.RuneError && n <= 1 {
			c = pool[int(data[0])%len(pool)]
			n = 1
		}
		out = append(out, c)
		data = data[n:]
	}
	return out
}

func FuzzSTDTEXT_Bounds(f *testing.F) {
	if ucdDir() == "" {
		f.Skip("UCD files absent")
	}
	for _, file := range []string{"WordBreakTest.txt", "SentenceBreakTest.txt"} {
		for _, c := range readBreakTest(f, file) {
			f.Add([]byte(string(c.s)))
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d := needSeg(t)
		s := text3FuzzText(text3PoolOnce(d), data)
		if w, g := byteBounds(s, d.mWordBounds(s)), text3Ask(t, false, 'w', string(s)); !sameInts(g, w) {
			t.Errorf("WordBounds %s: got %v, want %v", hexLine(s), g, w)
		}
		if w, g := byteBounds(s, d.mSentenceBounds(s)), text3Ask(t, false, 's', string(s)); !sameInts(g, w) {
			t.Errorf("SentenceBounds %s: got %v, want %v", hexLine(s), g, w)
		}
	})
}
