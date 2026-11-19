// Why: the text code's generated tables must match their generators: the
// 128-bit powers of ten (core_fmt.mk), the 5^k left-shift cutoffs
// (core_parse.mk) and Unicode's simple case mapping (text_case.mk).
// What: TestTextTablesUpToDate; go test ./std -update rewrites the blocks.
// How: each table sits between BEGIN and END GENERATED markers in the file
// that reads it, since std/tables_test.go regenerates core/core_tables.mk.

package std

import (
	"fmt"
	"math/big"
	"os"
	"strings"
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

// genCaseBlock is unicode.CaseRanges: each run lo..hi maps by adding its
// upper or lower delta, or, when the delta is UpperLower (0x110000), by
// alternating upper and lower case from lo.
func genCaseBlock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "// unicode.Version: %s. unicode.CaseRanges, ascending: scalars lo..=hi map\n", unicode.Version)
	b.WriteString("// to scalar + up (upper) and scalar + low (lower); a delta of\n")
	b.WriteString("// CaseAlternate means upper at even offsets from lo, lower at odd ones.\n")
	fmt.Fprintf(&b, "const CaseTable: [%d]CaseRange = [\n", len(unicode.CaseRanges))
	for _, r := range unicode.CaseRanges {
		fmt.Fprintf(&b, "    CaseRange(lo: 0x%04X, hi: 0x%04X, up: %d, low: %d),\n", r.Lo, r.Hi, r.Delta[unicode.UpperCase], r.Delta[unicode.LowerCase])
	}
	b.WriteString("]\n")
	return b.String()
}

// textTables names each generated block and the file that holds it.
var textTables = []struct {
	file, name string
	gen        func() string
}{
	{"core/core_fmt.mk", "Pow10Tab", genPow10Block},
	{"core/core_parse.mk", "LeftCheats", genLeftCheatsBlock},
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
		src := readText(t, tb.file)
		before, body, after, ok := splitBlock(src, tb.name)
		want := tb.gen()
		if !ok {
			t.Errorf("%s has no generated block %s", tb.file, tb.name)
			continue
		}
		if body == want {
			continue
		}
		if *update {
			if err := os.WriteFile(tb.file, []byte(before+want+after), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("%s: block %s is stale; rerun go test ./std -run TestTextTablesUpToDate -update and read the diff", tb.file, tb.name)
	}
}
