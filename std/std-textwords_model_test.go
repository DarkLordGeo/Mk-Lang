// Why: the Mk iterators need an oracle that follows UAX 29's rules as
// written, so that a bug in the Mk scan cannot hide in the oracle too.
// What: the word and sentence boundary models and
// TestSTDTEXT_WordsModelsMatchUCDTests.
// How: each position is decided alone, rule by rule in the annex's order,
// with no state carried between positions.

// The Go reference models of UAX 29's word and sentence
// boundaries for the tests of text.WordBounds and text.SentenceBounds, and
// checks them against every line of the UCD's WordBreakTest.txt and
// SentenceBreakTest.txt. Each model decides one position at a time by the
// annex's rules in order, walking left and right through the transformed
// sequence (WB4: X (Extend | Format | ZWJ)* -> X; SB5: X (Extend |
// Format)* -> X) as the rules are written, with no state carried between
// positions.

package std

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// segModel holds the classes of one string's scalars.
type segModel struct {
	wb []uint8 // Word_Break, no ExtPict bit
	ep []bool  // Extended_Pictographic
	sb []uint8
}

func (d *segData) model(s []rune) *segModel {
	m := &segModel{wb: make([]uint8, len(s)), ep: make([]bool, len(s)), sb: make([]uint8, len(s))}
	for i, c := range s {
		m.wb[i] = d.wb[c] &^ wbExtPict
		m.ep[i] = d.wb[c]&wbExtPict != 0
		m.sb[i] = d.sb[c]
	}
	return m
}

func wbIgnorable(v uint8) bool { return v == wbExtend || v == wbFormat || v == wbZWJ }
func wbNewlineOf(v uint8) bool { return v == wbCR || v == wbLF || v == wbNewline }
func wbAH(v uint8) bool        { return v == wbALetter || v == wbHebrewLetter }
func wbMidLetQ(v uint8) bool   { return v == wbMidLetter || v == wbMidNumLet || v == wbSingleQuote }
func wbMidNumQ(v uint8) bool   { return v == wbMidNum || v == wbMidNumLet || v == wbSingleQuote }

// wordX is the index of the transformed element (WB4) that holds scalar
// i: the scalar itself unless it is Extend, Format or ZWJ after some X
// that is not CR, LF or Newline.
func (m *segModel) wordX(i int) int {
	j := i
	for j > 0 && wbIgnorable(m.wb[j]) && !wbNewlineOf(m.wb[j-1]) {
		j--
	}
	return j
}

// wordPrev is the class of the transformed element before the one that
// starts at x, or -1 at the start of text.
func (m *segModel) wordPrev(x int) int {
	if x == 0 {
		return -1
	}
	return int(m.wb[m.wordX(x-1)])
}

// wordNext is the class of the first element after scalar k's (k itself
// not ignorable), or -1 at the end of text.
func (m *segModel) wordNext(k int) int {
	j := k + 1
	for j < len(m.wb) && wbIgnorable(m.wb[j]) {
		j++
	}
	if j == len(m.wb) {
		return -1
	}
	return int(m.wb[j])
}

// wordBreak reports whether UAX 29 puts a word boundary before scalar k,
// 0 < k < len.
func (m *segModel) wordBreak(k int) bool {
	a, b := m.wb[k-1], m.wb[k]
	switch {
	case a == wbCR && b == wbLF: // WB3
		return false
	case wbNewlineOf(a): // WB3a
		return true
	case wbNewlineOf(b): // WB3b
		return true
	case a == wbZWJ && m.ep[k]: // WB3c
		return false
	case a == wbWSegSpace && b == wbWSegSpace: // WB3d
		return false
	case wbIgnorable(b): // WB4
		return false
	}
	lx := m.wordX(k - 1)
	l := m.wb[lx]
	ll := m.wordPrev(lx)
	next := m.wordNext(k)
	switch {
	case wbAH(l) && wbAH(b): // WB5
		return false
	case wbAH(l) && wbMidLetQ(b) && next >= 0 && wbAH(uint8(next)): // WB6
		return false
	case ll >= 0 && wbAH(uint8(ll)) && wbMidLetQ(l) && wbAH(b): // WB7
		return false
	case l == wbHebrewLetter && b == wbSingleQuote: // WB7a
		return false
	case l == wbHebrewLetter && b == wbDoubleQuote && next == wbHebrewLetter: // WB7b
		return false
	case ll == wbHebrewLetter && l == wbDoubleQuote && b == wbHebrewLetter: // WB7c
		return false
	case l == wbNumeric && b == wbNumeric: // WB8
		return false
	case wbAH(l) && b == wbNumeric: // WB9
		return false
	case l == wbNumeric && wbAH(b): // WB10
		return false
	case ll == wbNumeric && wbMidNumQ(l) && b == wbNumeric: // WB11
		return false
	case l == wbNumeric && wbMidNumQ(b) && next == wbNumeric: // WB12
		return false
	case l == wbKatakana && b == wbKatakana: // WB13
		return false
	case (wbAH(l) || l == wbNumeric || l == wbKatakana || l == wbExtendNumLet) && b == wbExtendNumLet: // WB13a
		return false
	case l == wbExtendNumLet && (wbAH(b) || b == wbNumeric || b == wbKatakana): // WB13b
		return false
	case l == wbRI && b == wbRI: // WB15, WB16: an odd run of RI elements before
		n := 0
		for x := lx; ; {
			if m.wb[x] != wbRI {
				break
			}
			n++
			if x == 0 {
				break
			}
			x = m.wordX(x - 1)
		}
		return n%2 == 0
	}
	return true // WB999
}

func sbParaSep(v uint8) bool { return v == sbSep || v == sbCR || v == sbLF }
func sbSATerm(v uint8) bool  { return v == sbATerm || v == sbSTerm }
func sbIgnorable(v uint8) bool {
	return v == sbExtend || v == sbFormat
}

// sentX is wordX for SB5.
func (m *segModel) sentX(i int) int {
	j := i
	for j > 0 && sbIgnorable(m.sb[j]) && !sbParaSep(m.sb[j-1]) {
		j--
	}
	return j
}

// sentenceBreak reports whether UAX 29 puts a sentence boundary before
// scalar k, 0 < k < len.
func (m *segModel) sentenceBreak(k int) bool {
	a, b := m.sb[k-1], m.sb[k]
	switch {
	case a == sbCR && b == sbLF: // SB3
		return false
	case sbParaSep(a): // SB4
		return true
	case sbIgnorable(b): // SB5
		return false
	}
	lx := m.sentX(k - 1)
	l := m.sb[lx]
	ll := -1
	if lx > 0 {
		ll = int(m.sb[m.sentX(lx-1)])
	}
	if l == sbATerm && b == sbNumeric { // SB6
		return false
	}
	if (ll == sbUpper || ll == sbLower) && l == sbATerm && b == sbUpper { // SB7
		return false
	}
	// Match SATerm Close* Sp* ending at lx, walking left by elements.
	x := lx
	sp, closes := 0, 0
	for m.sb[x] == sbSp && x > 0 {
		sp++
		x = m.sentX(x - 1)
	}
	for m.sb[x] == sbClose && x > 0 {
		closes++
		x = m.sentX(x - 1)
	}
	if !sbSATerm(m.sb[x]) {
		return false // SB998
	}
	_ = closes
	if m.sb[x] == sbATerm { // SB8
		for j := k; j < len(m.sb); j++ {
			c := m.sb[j]
			if c == sbLower {
				return false
			}
			if c == sbOLetter || c == sbUpper || sbParaSep(c) || sbSATerm(c) {
				break
			}
		}
	}
	if b == sbSContinue || sbSATerm(b) { // SB8a
		return false
	}
	if sp == 0 && (b == sbClose || b == sbSp || sbParaSep(b)) { // SB9
		return false
	}
	if b == sbSp || sbParaSep(b) { // SB10
		return false
	}
	return true // SB11
}

// bounds lists the boundaries of s, as scalar indices, by f: 0, each k
// that f breaks before, and len(s); none for an empty s.
func bounds(n int, f func(k int) bool) []int {
	if n == 0 {
		return nil
	}
	out := []int{0}
	for k := 1; k < n; k++ {
		if f(k) {
			out = append(out, k)
		}
	}
	return append(out, n)
}

func (d *segData) mWordBounds(s []rune) []int {
	m := d.model(s)
	return bounds(len(s), m.wordBreak)
}

func (d *segData) mSentenceBounds(s []rune) []int {
	m := d.model(s)
	return bounds(len(s), m.sentenceBreak)
}

// breakCase is one line of a UCD break test: the scalars and the
// boundaries, as scalar indices.
type breakCase struct {
	s      []rune
	bounds []int
}

func readBreakTest(t testing.TB, name string) []breakCase {
	t.Helper()
	f, err := os.Open(filepath.Join(ucdDir(), "auxiliary", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []breakCase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		var c breakCase
		for _, w := range fields {
			switch w {
			case "÷":
				c.bounds = append(c.bounds, len(c.s))
			case "×":
			default:
				rs, err := ucdScalars(w)
				if err != nil {
					t.Fatal(err)
				}
				c.s = append(c.s, rs...)
			}
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatalf("%s: no cases", name)
	}
	return cases
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSTDTEXT_WordsModelsMatchUCDTests requires both models to give every
// line of WordBreakTest.txt and SentenceBreakTest.txt.
func TestSTDTEXT_WordsModelsMatchUCDTests(t *testing.T) {
	d := needSeg(t)
	for _, f := range []struct {
		file  string
		model func([]rune) []int
	}{{"WordBreakTest.txt", d.mWordBounds}, {"SentenceBreakTest.txt", d.mSentenceBounds}} {
		cases := readBreakTest(t, f.file)
		bad := 0
		for _, c := range cases {
			if got := f.model(c.s); !sameInts(got, c.bounds) {
				bad++
				if bad <= 10 {
					t.Errorf("%s: %s: got %v, want %v", f.file, hexLine(c.s), got, c.bounds)
				}
			}
		}
		t.Logf("%s: %d of %d lines", f.file, len(cases)-bad, len(cases))
	}
}
