// Why: the text code's generated tables must match their generators: the
// 128-bit powers of ten (core_fmt.mk), the 5^k left-shift cutoffs
// (core_parse.mk) and Unicode's simple case mapping (text_case.mk).
// What: TestTextTablesUpToDate; go test ./std -update rewrites the blocks;
// needSimpleCase, the UCD's simple case mappings the generator reads.
// How: each table sits between BEGIN and END GENERATED markers in the file
// that reads it, since std/tables_test.go regenerates core/core_tables.mk.
// The case rows come from UnicodeData.txt in $MK_EVIDENCE/ucd, and the
// parse checks that the rows, read as CaseOf reads them, give every
// scalar's mapping, and the generator that the parse equals goCase
// (std-text_casego_test.go), an oracle the parse does not feed.

package std

import (
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"
)

// pow10Min and pow10Max bound the table, as in Go 1.27's
// internal/strconv/pow10tab.go.
const (
	textPow10Min = -348
	textPow10Max = 347
)

// pow10Entry is 10^e scaled into [2^127, 2^128) and stored as hi<<64 - lo,
// the representation Go's uscale reads: hi is the ceiling of the high word
// and lo the correction (internal/strconv/pow10gen.go, ported).
func pow10Entry(e int64) (hi, lo uint64, be int) {
	one, ten := big.NewInt(1), big.NewInt(10)
	b1p64 := new(big.Int).Lsh(one, 64)
	r1p128 := new(big.Rat).SetInt(new(big.Int).Lsh(one, 128))
	r2 := big.NewRat(2, 1)
	var r *big.Rat
	if e >= 0 {
		r = new(big.Rat).SetInt(new(big.Int).Exp(ten, big.NewInt(e), nil))
	} else {
		r = new(big.Rat).SetFrac(one, new(big.Int).Exp(ten, big.NewInt(-e), nil))
	}
	for r.Cmp(r1p128) < 0 {
		r.Mul(r, r2)
		be++
	}
	for r.Cmp(r1p128) >= 0 {
		r.Quo(r, r2)
		be--
	}
	d := new(big.Int).Div(r.Num(), r.Denom())
	h, l := new(big.Int).DivMod(d, b1p64, new(big.Int))
	hi, lo = h.Uint64(), l.Uint64()
	if !r.IsInt() {
		lo++
		if lo == 0 {
			hi++
		}
	}
	if lo != 0 {
		hi++
		lo = -lo
	}
	return hi, lo, be
}

func genPow10Block() string {
	var b strings.Builder
	b.WriteString("// 10^e for e in Pow10Min..=347, scaled into [2^127, 2^128), as\n")
	b.WriteString("// hi * 2^64 - lo (Go 1.27 internal/strconv/pow10tab.go).\n")
	n := textPow10Max - textPow10Min + 1
	fmt.Fprintf(&b, "const Pow10Tab: [%d]Pow128 = [\n", n)
	for e := textPow10Min; e <= textPow10Max; e += 2 {
		b.WriteString("   ")
		for k := e; k < e+2 && k <= textPow10Max; k++ {
			hi, lo, _ := pow10Entry(int64(k))
			fmt.Fprintf(&b, " Pow128(hi: 0x%016x, lo: 0x%016x),", hi, lo)
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	return b.String()
}

// genLeftCheatsBlock is decimal.go's leftcheats: for k in 0..60, the number
// of digits a left shift by k adds, and the digits of 5^k (a shift adds one
// digit fewer when the number's digits are below them).
func genLeftCheatsBlock() string {
	var b strings.Builder
	b.WriteString("// For a left shift by k: the digits it adds, one fewer when the\n")
	b.WriteString("// number's leading digits are below the digits of 5^k (Go's\n")
	b.WriteString("// decimal.go leftcheats).\n")
	b.WriteString("const LeftDelta: [61]int = [\n    0,")
	five := big.NewInt(5)
	for k := 1; k <= 60; k++ {
		delta := int(float64(k)*0.30102999566398120) + 1 // ⌊k·log10(2)⌋ + 1
		fmt.Fprintf(&b, " %d,", delta)
		if k%20 == 0 && k < 60 {
			// No break after the last row: a row of only spaces is
			// trimmed by editors and mk fmt, which made the block stale.
			b.WriteString("\n   ")
		}
	}
	b.WriteString("\n]\n\nconst LeftCutoff: [61]str = [\n    \"\",\n")
	for k := 1; k <= 60; k++ {
		p := new(big.Int).Exp(five, big.NewInt(int64(k)), nil)
		fmt.Fprintf(&b, "    \"%s\",\n", p.String())
	}
	b.WriteString("]\n")
	return b.String()
}

// simpleCase is UnicodeData.txt's simple uppercase (field 12) and
// lowercase (field 13) mappings, the ones str.ToUpper and str.ToLower
// apply, at the UCD version of std.text's tables.
type simpleCase struct {
	version string
	up, low map[rune]rune
	ranges  []caseRange
}

// caseRange is one CaseTable row: lo..hi map by adding up or low, or, when
// both are caseAlternate, upper at even offsets from lo and lower at odd.
type caseRange struct {
	lo, hi  rune
	up, low int32
}

// caseAlternate is CaseAlternate of text_case.mk.
const caseAlternate = 0x110000

var (
	simpleCaseOnce sync.Once
	simpleCaseVal  *simpleCase
	simpleCaseErr  error
)

// needSimpleCase returns the UCD's simple case mappings, or skips the test
// as needUCD does.
func needSimpleCase(t *testing.T) *simpleCase {
	t.Helper()
	u := needUCD(t)
	simpleCaseOnce.Do(func() { simpleCaseVal, simpleCaseErr = parseSimpleCase(ucdDir(), u.version) })
	if simpleCaseErr != nil {
		t.Fatal(simpleCaseErr)
	}
	return simpleCaseVal
}

// parseSimpleCase reads UnicodeData.txt in dir into a simpleCase stamped
// version, builds its rows and checks that the rows, read as CaseOf reads
// them, give every scalar's mapping.
func parseSimpleCase(dir, version string) (*simpleCase, error) {
	sc := &simpleCase{version: version, up: map[rune]rune{}, low: map[rune]rune{}}
	if _, err := ucdLines(dir, "UnicodeData.txt", func(fl []string) error {
		if len(fl) < 14 {
			return fmt.Errorf("%d fields", len(fl))
		}
		c, _, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		for _, m := range []struct {
			field string
			to    map[rune]rune
		}{{fl[12], sc.up}, {fl[13], sc.low}} {
			if m.field == "" {
				continue
			}
			v, _, err := ucdRange(m.field)
			if err != nil {
				return err
			}
			m.to[c] = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sc.ranges = sc.buildRanges()
	for c := rune(0); c <= unicode.MaxRune; c++ {
		if u, l := sc.caseOf(c, true), sc.caseOf(c, false); u != sc.to(c, true) || l != sc.to(c, false) {
			return nil, fmt.Errorf("CaseTable maps U+%04X to %04X and %04X, UnicodeData to %04X and %04X", c, u, l, sc.to(c, true), sc.to(c, false))
		}
	}
	return sc, nil
}

// to is c's simple mapping, c itself when UnicodeData gives none.
func (sc *simpleCase) to(c rune, upper bool) rune {
	m := sc.low
	if upper {
		m = sc.up
	}
	if v, ok := m[c]; ok {
		return v
	}
	return c
}

// mapString maps every scalar of s, as str.ToUpper or str.ToLower does.
func (sc *simpleCase) mapString(s string, upper bool) string {
	rs := []rune(s)
	for i, r := range rs {
		rs[i] = sc.to(r, upper)
	}
	return string(rs)
}

// buildRanges compresses the mappings into ascending CaseTable rows: a run
// of scalars with one pair of deltas, or a run of pairs (an uppercase
// scalar whose lowercase is the next, which uppercases back to it) as one
// alternating row.
func (sc *simpleCase) buildRanges() []caseRange {
	delta := func(c rune) (int32, int32) {
		return int32(sc.to(c, true) - c), int32(sc.to(c, false) - c)
	}
	pairAt := func(c rune) bool {
		u0, l0 := delta(c)
		u1, l1 := delta(c + 1)
		return u0 == 0 && l0 == 1 && u1 == -1 && l1 == 0
	}
	var out []caseRange
	for c := rune(0); c <= unicode.MaxRune; {
		u, l := delta(c)
		n := len(out)
		switch {
		case u == 0 && l == 0:
			c++
		case pairAt(c):
			if n > 0 && out[n-1].up == caseAlternate && out[n-1].hi+1 == c {
				out[n-1].hi = c + 1
			} else {
				out = append(out, caseRange{c, c + 1, caseAlternate, caseAlternate})
			}
			c += 2
		default:
			if n > 0 && out[n-1].hi+1 == c && out[n-1].up == u && out[n-1].low == l {
				out[n-1].hi = c
			} else {
				out = append(out, caseRange{c, c, u, l})
			}
			c++
		}
	}
	return out
}

// caseOf is text_case.mk's CaseOf over the rows.
func (sc *simpleCase) caseOf(v rune, upper bool) rune {
	rs := sc.ranges
	i := sort.Search(len(rs), func(i int) bool { return rs[i].hi >= v })
	if i == len(rs) || v < rs[i].lo {
		return v
	}
	r := rs[i]
	d := r.low
	if upper {
		d = r.up
	}
	if d == caseAlternate {
		bit := rune(1)
		if upper {
			bit = 0
		}
		return r.lo + ((v-r.lo)&^1 | bit)
	}
	return v + rune(d)
}

// genCaseBlock is the CaseTable rows of UnicodeData's simple mappings.
func genCaseBlock(t *testing.T) string {
	sc := needSimpleCase(t)
	// An oracle the parse does not feed: a wrong field, or a UnicodeData.txt
	// of another version, differs from it.
	if d := caseDiffGo(sc.to); len(d) > 0 {
		t.Fatalf("UnicodeData.txt's simple mappings differ from Go's unicode with the pinned 18.0.0 changes:\n%v", d)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "// UCD version: %s. UnicodeData's simple case mappings, ascending: lo..=hi\n", sc.version)
	b.WriteString("// map to scalar + up (upper) and scalar + low (lower); a delta of\n")
	b.WriteString("// CaseAlternate means upper at even offsets from lo, lower at odd ones.\n")
	fmt.Fprintf(&b, "const CaseTable: [%d]CaseRange = [\n", len(sc.ranges))
	for _, r := range sc.ranges {
		fmt.Fprintf(&b, "    CaseRange(lo: 0x%04X, hi: 0x%04X, up: %d, low: %d),\n", r.lo, r.hi, r.up, r.low)
	}
	b.WriteString("]\n")
	return b.String()
}

// textTables names each generated block and the file that holds it.
var textTables = []struct {
	file, name string
	gen        func(t *testing.T) string
}{
	{"core/core_fmt.mk", "Pow10Tab", func(*testing.T) string { return genPow10Block() }},
	{"core/core_parse.mk", "LeftCheats", func(*testing.T) string { return genLeftCheatsBlock() }},
	{"core/text_case.mk", "CaseTable", genCaseBlock},
}

func beginMark(name string) string {
	return "// BEGIN GENERATED " + name + " by TestTextTablesUpToDate (std/std-text_tables_test.go); do not edit\n"
}

func endMark(name string) string { return "// END GENERATED " + name + "\n" }

// splitBlock returns the file around the block and the block's body.
func splitBlock(src, name string) (before, body, after string, ok bool) {
	b, e := beginMark(name), endMark(name)
	i := strings.Index(src, b)
	if i < 0 {
		return "", "", "", false
	}
	j := strings.Index(src[i:], e)
	if j < 0 {
		return "", "", "", false
	}
	return src[:i+len(b)], src[i+len(b) : i+j], src[i+j:], true
}

// TestTextTablesUpToDate regenerates each block and compares it;
// -update rewrites it. The generator's first and last powers of ten are
// pinned to Go 1.27's pow10tab.go, so the port of pow10gen is checked.
func TestTextTablesUpToDate(t *testing.T) {
	for _, c := range []struct {
		e      int64
		hi, lo uint64
	}{
		{-348, 0xfa8fd5a0081c0289, 0xe8cd3796329f1bac},
		{-347, 0x9c99e58405118196, 0xf18042bddfa3714b},
		{344, 0xd6444e39c3db9b0a, 0x7b731cb986544fe3},
		{347, 0xd13eb46469447568, 0xb48e6a0d2d2e5604},
	} {
		if hi, lo, _ := pow10Entry(c.e); hi != c.hi || lo != c.lo {
			t.Errorf("pow10Entry(%d) = %#x, %#x; Go's table has %#x, %#x", c.e, hi, lo, c.hi, c.lo)
		}
	}
	for _, tb := range textTables {
		t.Run(tb.name, func(t *testing.T) {
			src := readText(t, tb.file)
			before, body, after, ok := splitBlock(src, tb.name)
			if !ok {
				t.Fatalf("%s has no generated block %s", tb.file, tb.name)
			}
			want := tb.gen(t)
			if body == want {
				return
			}
			if *update {
				if err := os.WriteFile(tb.file, []byte(before+want+after), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			t.Errorf("%s: block %s is stale; rerun go test ./std -run TestTextTablesUpToDate -update and read the diff", tb.file, tb.name)
		})
	}
}
