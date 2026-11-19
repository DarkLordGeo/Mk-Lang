// Why: std.text needs a reference written independently of its own tables
// and code, to compare against on generated strings.
// What: Go models of UAX 29 grapheme clusters, UAX 15 normalization, full
// case folding and terminal width, and TestSTDTEXT_ModelsMatchUCDTests.
// How: each model evaluates the rules over the parsed UCD
// (std-text_ucd_test.go) and is first checked against the UCD's test files.

package std

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mGraphemes splits s into extended grapheme clusters by evaluating the
// rules of UAX 29 (GraphemeBreakTest.html's list, UCD 18.0.0) at every
// position between two scalars, in rule order, reading the left context
// backwards.
func (u *ucd) mGraphemes(s []rune) [][]rune {
	var out [][]rune
	start := 0
	for i := 1; i <= len(s); i++ {
		if i == len(s) || u.mBreak(s, i) {
			out = append(out, s[start:i])
			start = i
		}
	}
	return out
}

// mBreak reports whether UAX 29 breaks between s[i-1] and s[i].
func (u *ucd) mBreak(s []rune, i int) bool {
	p, q := u.gcb[s[i-1]], u.gcb[s[i]]
	ctl := func(g uint8) bool { return g == gcbControl || g == gcbCR || g == gcbLF }
	switch {
	case p == gcbCR && q == gcbLF: // GB3
		return false
	case ctl(p), ctl(q): // GB4, GB5
		return true
	case p == gcbL && (q == gcbL || q == gcbV || q == gcbLV || q == gcbLVT): // GB6
		return false
	case (p == gcbLV || p == gcbV) && (q == gcbV || q == gcbT): // GB7
		return false
	case (p == gcbLVT || p == gcbT) && q == gcbT: // GB8
		return false
	case q == gcbExtend || q == gcbZWJ: // GB9
		return false
	case q == gcbSpacingMark: // GB9a
		return false
	case p == gcbPrepend: // GB9b
		return false
	}
	// GB9c: ConjunctLinker ConjunctExtender* x LinkingConsonant.
	if u.incb[s[i]] == incbConsonant {
		k := i - 1
		for k >= 0 && u.incb[s[k]] == incbExtend {
			k--
		}
		if k >= 0 && u.incb[s[k]] == incbLinker {
			return false
		}
	}
	// GB11: ExtPict Extend* ZWJ x ExtPict.
	if u.extPict[s[i]] && p == gcbZWJ {
		k := i - 2
		for k >= 0 && u.gcb[s[k]] == gcbExtend {
			k--
		}
		if k >= 0 && u.extPict[s[k]] {
			return false
		}
	}
	// GB12, GB13: an odd number of RI before.
	if p == gcbRI && q == gcbRI {
		k := i - 1
		for k >= 0 && u.gcb[s[k]] == gcbRI {
			k--
		}
		if (i-1-k)%2 == 1 {
			return false
		}
	}
	return true // GB999
}

// mWidth is the terminal width of s.
func (u *ucd) mWidth(s []rune) int {
	w := 0
	for _, g := range u.mGraphemes(s) {
		c := g[0]
		switch gc := u.gcb[c]; {
		case gc == gcbControl || gc == gcbCR || gc == gcbLF || gc == gcbExtend || gc == gcbZWJ:
			continue
		case u.extPict[c] && len(g) > 1 && g[1] == 0xFE0F:
			w += 2
		case u.extPict[c] && len(g) > 1 && g[1] == 0xFE0E:
			w++
		case u.eaw[c] == 'W' || u.eaw[c] == 'F' || u.emojiP[c] || gc == gcbRI:
			w += 2
		default:
			w++
		}
	}
	return w
}

// mFold is the full case folding of s.
func (u *ucd) mFold(s []rune) []rune {
	var out []rune
	for _, c := range s {
		if f, ok := u.fold[c]; ok {
			out = append(out, f...)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// mNormalize is s in the form (formNFC and the rest) by the definitions
// of Unicode section 3.11: full decomposition, canonical ordering by a
// stable sort, and for the composed forms canonical composition.
func (u *ucd) mNormalize(s []rune, form int) []rune {
	compat := form == formNFKC || form == formNFKD
	var d []rune
	for _, c := range s {
		if e := u.fullDecomp(c, compat); e != nil {
			d = append(d, e...)
		} else {
			d = append(d, c)
		}
	}
	// Canonical ordering: stable sort of each run of non-starters.
	for i := 0; i < len(d); {
		if u.ccc[d[i]] == 0 {
			i++
			continue
		}
		j := i
		for j < len(d) && u.ccc[d[j]] != 0 {
			j++
		}
		slices.SortStableFunc(d[i:j], func(a, b rune) int { return int(u.ccc[a]) - int(u.ccc[b]) })
		i = j
	}
	if form == formNFD || form == formNFKD {
		return d
	}
	pairs := u.mPairs()
	// Canonical composition (D117): for each C, find the last starter L
	// before it; if nothing between blocks C and L+C has a primary
	// composite, replace L and delete C.
	out := slices.Clone(d)
	for i := 1; i < len(out); i++ {
		c := out[i]
		l := i - 1
		for l >= 0 && u.ccc[out[l]] != 0 {
			l--
		}
		if l < 0 {
			continue
		}
		blocked := false
		if l < i-1 {
			b := out[i-1]
			blocked = u.ccc[b] == 0 || u.ccc[b] >= u.ccc[c]
		}
		if blocked {
			continue
		}
		if x, ok := u.mCompose(pairs, out[l], c); ok {
			out[l] = x
			out = slices.Delete(out, i, i+1)
			i--
		}
	}
	return out
}

func (u *ucd) mCompose(pairs map[[2]rune]rune, a, b rune) (rune, bool) {
	if a >= hLBase && a < hLBase+hLCount && b >= hVBase && b < hVBase+hVCount {
		return hSBase + ((a-hLBase)*hVCount+(b-hVBase))*hTCount, true
	}
	if isHangulSyllable(a) && (a-hSBase)%hTCount == 0 && b > hTBase && b < hTBase+hTCount {
		return a + (b - hTBase), true
	}
	x, ok := pairs[[2]rune{a, b}]
	return x, ok
}

func (u *ucd) mPairs() map[[2]rune]rune {
	u.pairsOnce.Do(func() { u.pairs = u.compPairs() })
	return u.pairs
}

// graphemeCase is one line of GraphemeBreakTest.txt: the scalars and the
// cluster lengths in scalars.
type graphemeCase struct {
	s    []rune
	lens []int
}

func readGraphemeTest(t *testing.T, dir string) []graphemeCase {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "auxiliary", "GraphemeBreakTest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []graphemeCase
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		var c graphemeCase
		n := 0
		for _, w := range fields {
			switch w {
			case "÷":
				if n > 0 {
					c.lens = append(c.lens, n)
				}
				n = 0
			case "×":
			default:
				rs, err := ucdScalars(w)
				if err != nil {
					t.Fatal(err)
				}
				c.s = append(c.s, rs...)
				n++
			}
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

// normCase is one line of NormalizationTest.txt: c1 to c5, and its part.
type normCase struct {
	c    [5][]rune
	part int
}

func readNormTest(t *testing.T, dir string) []normCase {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "NormalizationTest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []normCase
	part := -1
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@Part") {
			part++
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, ";")
		var c normCase
		c.part = part
		for k := 0; k < 5; k++ {
			rs, err := ucdScalars(fields[k])
			if err != nil {
				t.Fatal(err)
			}
			c.c[k] = rs
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

// normWant is NormalizationTest.txt's conformance table: the column a
// column normalizes to, per form (index by normForm) and source column.
var normWant = [4][5]int{
	formNFC:  {1, 1, 1, 3, 3},
	formNFD:  {2, 2, 2, 4, 4},
	formNFKC: {3, 3, 3, 3, 3},
	formNFKD: {4, 4, 4, 4, 4},
}

// TestSTDTEXT_ModelsMatchUCDTests checks the Go models against the UCD's
// own test files: every GraphemeBreakTest line, every NormalizationTest
// invariant, and part 1's rule that a scalar it does not list is its own
// normalization in every form.
func TestSTDTEXT_ModelsMatchUCDTests(t *testing.T) {
	u := needUCD(t)
	dir := ucdDir()
	gcases := readGraphemeTest(t, dir)
	for _, c := range gcases {
		var got []int
		for _, g := range u.mGraphemes(c.s) {
			got = append(got, len(g))
		}
		if !slices.Equal(got, c.lens) {
			t.Errorf("graphemes %X: got %v, want %v", c.s, got, c.lens)
		}
	}
	ncases := readNormTest(t, dir)
	part1 := map[rune]bool{}
	for _, c := range ncases {
		if c.part == 1 {
			part1[c.c[0][0]] = true
		}
		for form := 0; form < 4; form++ {
			for k := 0; k < 5; k++ {
				got := u.mNormalize(c.c[k], form)
				if want := c.c[normWant[form][k]]; !slices.Equal(got, want) {
					t.Fatalf("form %d of c%d %X: got %X, want %X", form, k+1, c.c[k], got, want)
				}
			}
		}
	}
	for c := rune(0); c < 0x110000; c++ {
		if c >= 0xD800 && c <= 0xDFFF || part1[c] {
			continue
		}
		for form := 0; form < 4; form++ {
			if got := u.mNormalize([]rune{c}, form); len(got) != 1 || got[0] != c {
				t.Fatalf("U+%04X not in part 1, form %d gives %X", c, form, got)
			}
		}
	}
	t.Logf("UCD %s: %d grapheme lines, %d normalization lines (%d scalars in part 1)", u.version, len(gcases), len(ncases), len(part1))
}
