// Why: the word and sentence iterators and their benchmark row each had a
// defect that must not come back.
// What: TestSTDTEXT_BenchRowSizes, T3_2_WordBoundsLinksNoSentenceTables,
// T3_4_DecodesPerScalar and T3_5_MixedShare.
// How: checks the row's sizes and baselines, that a WordBounds-only program
// links no sentence table, the decodes each scalar costs (counted through
// text3Dec), and the share of non-ASCII scalars in the row's mixed text.

package std

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// text3Dec is a Go transliteration of bounds.mk's scanners (WordEnd,
// WordJoins, WordNext, SentenceEnd, SentenceJoins, LowerFollows),
// counting the decodes of each byte offset. old restores the
// earlier SentenceJoins, which handed LowerFollows the start of q.
type text3Dec struct {
	d     *segData
	b     []byte
	count []int
	old   bool
}

func (x *text3Dec) decode(i int) (rune, int) {
	x.count[i]++
	return utf8.DecodeRune(x.b[i:])
}

func text3Bit(set uint32, v uint8) bool { return set>>v&1 == 1 }

var (
	t3WbNewlines = uint32(1<<wbCR | 1<<wbLF | 1<<wbNewline)
	t3WbIgnored  = uint32(1<<wbExtend | 1<<wbZWJ | 1<<wbFormat)
	t3WbAH       = uint32(1<<wbHebrewLetter | 1<<wbALetter)
	t3WbMidLetQ  = uint32(1<<wbSingleQuote | 1<<wbMidNumLet | 1<<wbMidLetter)
	t3WbMidNumQ  = uint32(1<<wbSingleQuote | 1<<wbMidNumLet | 1<<wbMidNum)
	t3WbAlnum    = uint32(1<<wbHebrewLetter | 1<<wbALetter | 1<<wbNumeric)
	t3WordJoin   = [wbCount]uint32{
		wbKatakana:     1<<wbKatakana | 1<<wbExtendNumLet,
		wbHebrewLetter: 1<<wbHebrewLetter | 1<<wbALetter | 1<<wbSingleQuote | 1<<wbNumeric | 1<<wbExtendNumLet,
		wbALetter:      1<<wbHebrewLetter | 1<<wbALetter | 1<<wbNumeric | 1<<wbExtendNumLet,
		wbNumeric:      1<<wbHebrewLetter | 1<<wbALetter | 1<<wbNumeric | 1<<wbExtendNumLet,
		wbExtendNumLet: 1<<wbKatakana | 1<<wbHebrewLetter | 1<<wbALetter | 1<<wbNumeric | 1<<wbExtendNumLet,
	}
	t3SbParaSep   = uint32(1<<sbCR | 1<<sbLF | 1<<sbSep)
	t3SbIgnored   = uint32(1<<sbExtend | 1<<sbFormat)
	t3SbSATerm    = uint32(1<<sbATerm | 1<<sbSTerm)
	t3SbAfterTerm = uint32(1<<sbCR | 1<<sbLF | 1<<sbSep | 1<<sbSp | 1<<sbATerm | 1<<sbSTerm | 1<<sbSContinue)
	t3SbStop      = uint32(1<<sbCR | 1<<sbLF | 1<<sbSep | 1<<sbLower | 1<<sbUpper | 1<<sbOLetter | 1<<sbATerm | 1<<sbSTerm)
)

func (x *text3Dec) wordEnd(i int) int {
	b, n := x.b, len(x.b)
	c, w := x.decode(i)
	raw := x.d.wb[c] & 31
	p, pp, ri := raw, uint8(wbOther), 0
	if raw == wbRI {
		ri = 1
	}
	j := i + w
	for j < n {
		if b[j] < 0x80 && text3Bit(t3WbAlnum, p) && text3Bit(t3WbAlnum, x.d.wb[b[j]]) {
			pp, p = p, x.d.wb[b[j]]
			raw, ri = p, 0
			j++
			continue
		}
		c, w := x.decode(j)
		qe := x.d.wb[c]
		q := qe & 31
		if text3Bit(t3WbNewlines, raw) || text3Bit(t3WbNewlines, q) {
			if raw != wbCR || q != wbLF {
				break
			}
		} else if !(raw == wbZWJ && qe&wbExtPict != 0) && !(raw == wbWSegSpace && q == wbWSegSpace) && !text3Bit(t3WbIgnored, q) {
			if !x.wordJoins(j+w, p, pp, q, ri) {
				break
			}
		}
		if !text3Bit(t3WbIgnored, q) {
			pp, p = p, q
			if q == wbRI {
				ri++
			} else {
				ri = 0
			}
		}
		raw = q
		j += w
	}
	return j
}

func (x *text3Dec) wordJoins(k int, p, pp, q uint8, ri int) bool {
	switch {
	case text3Bit(t3WordJoin[p], q):
		return true
	case text3Bit(t3WbAH, p) && text3Bit(t3WbMidLetQ, q):
		return text3Bit(t3WbAH, x.wordNext(k))
	case p == wbHebrewLetter && q == wbDoubleQuote:
		return x.wordNext(k) == wbHebrewLetter
	case p == wbNumeric && text3Bit(t3WbMidNumQ, q):
		return x.wordNext(k) == wbNumeric
	case text3Bit(t3WbAH, q):
		return text3Bit(t3WbAH, pp) && text3Bit(t3WbMidLetQ, p) || pp == wbHebrewLetter && p == wbDoubleQuote && q == wbHebrewLetter
	case q == wbNumeric:
		return pp == wbNumeric && text3Bit(t3WbMidNumQ, p)
	}
	return p == wbRI && q == wbRI && ri&1 == 1
}

func (x *text3Dec) wordNext(k int) uint8 {
	for j := k; j < len(x.b); {
		c, w := x.decode(j)
		if q := x.d.wb[c] & 31; !text3Bit(t3WbIgnored, q) {
			return q
		}
		j += w
	}
	return wbOther
}

const (
	t3SeqNone = iota
	t3SeqTerm
	t3SeqSpace
)

func (x *text3Dec) sentenceEnd(i int) int {
	b, n := x.b, len(x.b)
	c, w := x.decode(i)
	raw := x.d.sb[c]
	p, pp := raw, uint8(sbOther)
	seq := t3SeqNone
	if text3Bit(t3SbSATerm, raw) {
		seq = t3SeqTerm
	}
	aterm := raw == sbATerm
	j := i + w
	for j < n {
		if cb := b[j]; cb < 0x80 && seq == t3SeqNone && !text3Bit(t3SbParaSep, raw) && !text3Bit(t3SbParaSep|t3SbSATerm, x.d.sb[cb]) {
			pp, p = p, x.d.sb[cb]
			raw = p
			j++
			continue
		}
		c, w := x.decode(j)
		q := x.d.sb[c]
		if text3Bit(t3SbParaSep, raw) {
			if raw != sbCR || q != sbLF {
				break
			}
		} else if !text3Bit(t3SbIgnored, q) {
			if !x.sentenceJoins(j, j+w, p, pp, q, seq, aterm) {
				break
			}
		}
		if !text3Bit(t3SbIgnored, q) {
			switch {
			case text3Bit(t3SbSATerm, q):
				seq = t3SeqTerm
			case q == sbClose && seq == t3SeqTerm:
			case q == sbSp && seq != t3SeqNone:
				seq = t3SeqSpace
			default:
				seq = t3SeqNone
			}
			if text3Bit(t3SbSATerm, q) {
				aterm = q == sbATerm
			}
			pp, p = p, q
		}
		raw = q
		j += w
	}
	return j
}

func (x *text3Dec) sentenceJoins(start, k int, p, pp, q uint8, seq int, aterm bool) bool {
	if p == sbATerm && (q == sbNumeric || q == sbUpper && (pp == sbUpper || pp == sbLower)) {
		return true
	}
	if seq == t3SeqNone {
		return true
	}
	if text3Bit(t3SbAfterTerm, q) || q == sbClose && seq == t3SeqTerm {
		return true
	}
	if x.old {
		return aterm && x.lowerFollows(start)
	}
	if !aterm {
		return false
	}
	if text3Bit(t3SbStop, q) {
		return q == sbLower
	}
	return x.lowerFollows(k)
}

func (x *text3Dec) lowerFollows(k int) bool {
	for j := k; j < len(x.b); {
		c, w := x.decode(j)
		if q := x.d.sb[c]; text3Bit(t3SbStop, q) {
			return q == sbLower
		}
		j += w
	}
	return false
}

// walk is Bounds.Next to the end: the boundaries, and the most decodes
// of one offset and where.
func (x *text3Dec) walk(words bool) (bounds []int, most, at int) {
	x.count = make([]int, len(x.b))
	if len(x.b) > 0 {
		bounds = append(bounds, 0)
		for pos := 0; pos < len(x.b); {
			if words {
				pos = x.wordEnd(pos)
			} else {
				pos = x.sentenceEnd(pos)
			}
			bounds = append(bounds, pos)
		}
	}
	for i, c := range x.count {
		if c > most {
			most, at = c, i
		}
	}
	return bounds, most, at
}

// TestSTDTEXT_DecodesPerScalar checks bounds.mk's stated cost: a
// scalar is decoded at most three times by WordBounds and twice by
// SentenceBounds. The transliteration must give the models' boundaries on
// every case, so it cannot drift from the rules unseen.
func TestSTDTEXT_DecodesPerScalar(t *testing.T) {
	d := needSeg(t)
	var cases [][]rune
	for _, f := range []string{"WordBreakTest.txt", "SentenceBreakTest.txt"} {
		for _, c := range readBreakTest(t, f) {
			cases = append(cases, c.s)
		}
	}
	pool := text3PoolOnce(d)
	cases = append(cases, text3Random(pool, 1, 4000, 24, 3)...)
	cases = append(cases, text3Random(pool, 2, 1000, 200, 2)...)
	cases = append(cases, text3Random(pool, 7, 3000, 40, 1000)...)
	for _, s := range []string{"a'?", "a. A", "a.\" ) A b", "x'y'z'?", "1,2,3,?", "א\"?", "a. 1.", "a.)  ­­b", "e.g. the", "A. B. c"} {
		cases = append(cases, []rune(s))
	}
	worst := [2]int{}
	for _, s := range cases {
		b := []byte(string(s))
		for k, words := range []bool{true, false} {
			x := &text3Dec{d: d, b: b}
			got, most, at := x.walk(words)
			var want []int
			if words {
				want = byteBounds(s, d.mWordBounds(s))
			} else {
				want = byteBounds(s, d.mSentenceBounds(s))
			}
			if !sameInts(got, want) {
				t.Fatalf("transliteration differs from the model (words %v) on %s: %v, want %v", words, hexLine(s), got, want)
			}
			limit := 3
			if !words {
				limit = 2
			}
			if most > limit {
				t.Errorf("words %v: %s: offset %d decoded %d times, the doc says at most %d", words, hexLine(s), at, most, limit)
			}
			worst[k] = max(worst[k], most)
		}
	}
	// The bound is tight for words: "a'?" decodes '?' three times (WB6's
	// look-ahead, the scan that breaks before it, the scan from it).
	x := &text3Dec{d: d, b: []byte("a'?")}
	if _, most, at := x.walk(true); most != 3 || at != 2 {
		t.Errorf("a'?: most %d decodes at %d, want 3 at 2", most, at)
	}
	// The old SentenceJoins decoded 'A' of "a. A" three times; now twice.
	for _, old := range []bool{true, false} {
		x := &text3Dec{d: d, b: []byte("a. A"), old: old}
		_, most, at := x.walk(false)
		want := 2
		if old {
			want = 3
		}
		if most != want || at != 3 {
			t.Errorf("a. A (old %v): most %d decodes at %d, want %d at 3", old, most, at, want)
		}
	}
	t.Logf("%d cases; most decodes of one offset: words %d, sentences %d", len(cases), worst[0], worst[1])
}

// text3BenchDir is the row's directory, from std/.
const text3BenchDir = "../tests/bench-lanes/std-text3/bounds"

// TestSTDTEXT_MixedShare: the row's mixed text has 5 of 16 scalars
// outside ASCII (k = 11 to 15 of x % 16), and the row says so.
func TestSTDTEXT_MixedShare(t *testing.T) {
	s := text3Mixed(1<<20, 1)
	var all, wide int
	for _, c := range s {
		all++
		if c >= 0x80 {
			wide++
		}
	}
	share := float64(wide) / float64(all)
	if share < 5.0/16-0.01 || share > 5.0/16+0.01 {
		t.Errorf("non-ASCII share %.4f, want 5/16 = %.4f", share, 5.0/16)
	}
	for _, f := range []string{"README.md", "bench.json", "mk/text-bounds.mk"} {
		b, err := os.ReadFile(filepath.Join(text3BenchDir, f))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(b)), " ")
		if strings.Contains(text, "6 of 16") || !strings.Contains(text, "5 of 16") {
			t.Errorf("%s: does not say 5 of 16 scalars outside ASCII", f)
		}
	}
}

// text3PerPass is the fastest time of one 16 MiB pass of each variant
// measured on one development machine, in
// seconds; the row's reps must keep the kernel at 1.2 s or more at that
// speed, above the contract's 1 s floor (tests/bench/README.md).
var text3PerPass = map[string]float64{
	"prosewords":     0.0544,
	"prosesentences": 0.0168,
	"words":          0.0748,
	"sentences":      0.0497,
	"closes":         0.0318,
}

// TestSTDTEXT_BenchRowSizes: every variant of text-bounds has a
// reps-0 baseline (its fill timed and subtracted), bytes that are
// the text's length times reps, and a publish size whose kernel runs at
// least 1.2 s at the fastest measured speed.
func TestSTDTEXT_BenchRowSizes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(text3BenchDir, "bench.json"))
	if err != nil {
		t.Fatal(err)
	}
	type variant struct {
		Name     string   `json:"name"`
		Args     []string `json:"args"`
		Checksum string   `json:"checksum"`
		Baseline []string `json:"baseline"`
		Bytes    int64    `json:"bytes"`
	}
	var row struct {
		Args     []string  `json:"args"`
		Checksum string    `json:"checksum"`
		Variants []variant `json:"variants"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	lens := map[string]int64{}
	textLen := func(mode string) int64 {
		if l, ok := lens[mode]; ok {
			return l
		}
		var l int64
		switch mode {
		case "closes":
			n := int64(16 * 1048576 / 2)
			l = 2 + n + 1 + n + 1
		case "prosewords", "prosesentences":
			l = int64(len(text3Prose(16*1048576, 1)))
		default:
			l = int64(len(text3Mixed(16*1048576, 1)))
		}
		lens[mode] = l
		return l
	}
	if len(row.Variants) != len(text3PerPass) {
		t.Fatalf("%d variants, want %d", len(row.Variants), len(text3PerPass))
	}
	for _, v := range row.Variants {
		if len(v.Args) != 4 || v.Args[0] != v.Name || v.Args[1] != "16" || v.Args[3] != "1" {
			t.Errorf("%s: args %q, want [%s 16 reps 1]", v.Name, v.Args, v.Name)
			continue
		}
		reps := 0
		fmt.Sscan(v.Args[2], &reps)
		base := slices.Clone(v.Args)
		base[2] = "0"
		if !slices.Equal(v.Baseline, base) {
			t.Errorf("%s: baseline %q, want %q", v.Name, v.Baseline, base)
		}
		if want := textLen(v.Name) * int64(reps); v.Bytes != want {
			t.Errorf("%s: bytes %d, want %d (the text's length times %d)", v.Name, v.Bytes, want, reps)
		}
		if k := float64(reps) * text3PerPass[v.Name]; k < 1.2 {
			t.Errorf("%s: %d reps run the kernel %.2f s at the fastest measured pass, under 1.2 s", v.Name, reps, k)
		}
		if v.Name == "prosewords" && (!slices.Equal(row.Args, v.Args) || row.Checksum != v.Checksum) {
			t.Errorf("the row's args and checksum (%q, %s) are not its first variant's (%q, %s)", row.Args, row.Checksum, v.Args, v.Checksum)
		}
	}
}

// text3SizeProg walks one iterator through a helper called from two
// sites over a runtime-chosen element, so Next is not specialised to one
// constructor.
func text3SizeProg(ctor string) string {
	return `import std.text

Count(it: mut text.Bounds): int {
    var n := 0
    for {
        b := it.Next() else {
            return n
        }
        n += b
    }
}

export Main(sys: &System): WriteError!void {
    var its: [2]text.Bounds = [text.` + ctor + `(sys.args[0]), text.` + ctor + `("a b. C d")]
    k := sys.args.Len() % 2
    x := Count(mut its[k]) + Count(mut its[1 - k])
    var out := sys.Stdout()
    try Print(mut out, "{x}\n", (x))
}
`
}

// TestSTDTEXT_WordBoundsLinksNoSentenceTables (a binary pays
// only for what it names): a program that names only WordBounds, dev and
// release, holds no 64-byte run of SentPropData; the same program over
// SentenceBounds holds it (the test's positive control).
func TestSTDTEXT_WordBoundsLinksNoSentenceTables(t *testing.T) {
	d := needSeg(t)
	sb := make([]int, len(d.sb))
	wb := make([]int, len(d.wb))
	for c := range sb {
		sb[c], wb[c] = int(d.sb[c]), int(d.wb[c])
	}
	toBytes := func(v []int) []byte {
		out := make([]byte, len(v))
		for i, x := range v {
			out[i] = byte(x)
		}
		return out
	}
	sdata, wdata := toBytes(buildTrie3(sb).data), toBytes(buildTrie3(wb).data)
	var marker []byte
	for i := 0; i+64 <= len(sdata); i += 16 {
		w := sdata[i : i+64]
		distinct := map[byte]bool{}
		for _, c := range w {
			distinct[c] = true
		}
		if len(distinct) >= 5 && !bytes.Contains(wdata, w) {
			marker = w
			break
		}
	}
	if marker == nil {
		t.Fatal("no 64-byte run of SentPropData that WordPropData lacks")
	}
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if len(flags) > 0 {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			for _, c := range []struct {
				ctor string
				want bool
			}{{"WordBounds", false}, {"SentenceBounds", true}} {
				exe := text2BuildSrc(t, text3SizeProg(c.ctor), flags...)
				r := runNative(t, exe, "")
				if r.code != 0 {
					t.Fatalf("%s: exit %d\n%s", c.ctor, r.code, r.stderr)
				}
				b, err := os.ReadFile(exe)
				if err != nil {
					t.Fatal(err)
				}
				if got := bytes.Contains(b, marker); got != c.want {
					t.Errorf("%s: sentence table linked %v, want %v (%d bytes)", c.ctor, got, c.want, len(b))
				}
				t.Logf("%s %s: %d bytes", name, c.ctor, len(b))
			}
		})
	}
}
