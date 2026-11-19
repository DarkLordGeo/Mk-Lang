// Why: text.BidiLevels, text.Mirror and text.LineBreaks read tables that
// must come from the same UCD version as std.text's other tables.
// What: the generator of std/text/bidi_tables.mk and linebreak_tables.mk,
// and TestSTDTEXT_LinesTablesUpToDate, TableBytes and Premises.
// How: reads the UAX 9 and UAX 14 data from the UCD files under
// $MK_EVIDENCE/ucd and bakes the rules' remapping into each byte.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The resolved Line_Break classes, in the order std/text/linebreak.mk
// numbers them (Lb*, the low six bits of a LineProp byte).
var lbNames = []string{
	"AL", "BK", "CR", "LF", "NL", "CM", "ZWJ", "SP", "ZW", "WJ", "GL", "BA",
	"BB", "B2", "HY", "HH", "CB", "CL", "CP", "EX", "IN", "NS", "OP", "QU",
	"QU_Pi", "QU_Pf", "IS", "NU", "PO", "PR", "SY", "HL", "ID", "EB", "EM",
	"H2", "H3", "JL", "JV", "JT", "RI", "AK", "AP", "AS", "VF", "VI",
	"DottedCircle",
}

// The LineProp flag bits.
const (
	lbEastAsian = 1 << 6
	lbExtPictCn = 1 << 7
)

// The bracket type bits of a BidiProp byte (bits 5 and 6).
const (
	bidiOpen  = 1 << 5
	bidiClose = 2 << 5
)

// uaxData is the generator's data and the tests' oracle tables.
type uaxData struct {
	version string
	// bidi is Bidi_Class (urlBidiNames' order) | bracket type, per scalar.
	bidi []int
	// bracketKey is a paired bracket's match key: for an opening bracket
	// the canonical form of its pair, for a closing one its own (BD16
	// matches U+2329 with U+3009 through their singleton decompositions).
	bracketKey map[rune]rune
	// mirror is Bidi_Mirroring_Glyph.
	mirror map[rune]rune
	// line is the LineProp byte per scalar.
	line []int
	// lbRaw is each scalar's Line_Break value as the file gives it.
	lbRaw []string
}

var (
	uaxOnce sync.Once
	uaxVal  *uaxData
	uaxErr  error
)

// needUax parses the files once per test binary, skipping the test when
// the UCD is absent (as needUCD does).
func needUax(t *testing.T) *uaxData {
	t.Helper()
	u := needUCD(t)
	uaxOnce.Do(func() { uaxVal, uaxErr = parseUax(ucdDir(), u) })
	if uaxErr != nil {
		t.Fatal(uaxErr)
	}
	return uaxVal
}

// eawLong maps East_Asian_Width's long names (the @missing lines) to the
// short ones.
var eawLong = map[string]string{"Neutral": "N", "Ambiguous": "A", "Halfwidth": "H", "Wide": "W", "Fullwidth": "F", "Narrow": "Na"}

func parseUax(dir string, u *ucd) (*uaxData, error) {
	const n = 0x110000
	d := &uaxData{bidi: make([]int, n), bracketKey: map[rune]rune{}, mirror: map[rune]rune{}, line: make([]int, n), lbRaw: make([]string, n)}
	versions := map[string]string{}
	v, err := urlReadRanges(filepath.Join(dir, "extracted", "DerivedBidiClass.txt"), func(lo, hi int, value string) error {
		k, err := urlIndexOf(urlBidiNames, value)
		for c := lo; c <= hi; c++ {
			d.bidi[c] = k
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	versions["DerivedBidiClass.txt"] = v
	gc := make([]string, n)
	v, err = urlReadRanges(filepath.Join(dir, "extracted", "DerivedGeneralCategory.txt"), func(lo, hi int, value string) error {
		for c := lo; c <= hi; c++ {
			gc[c] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	versions["DerivedGeneralCategory.txt"] = v
	ea := make([]string, n)
	v, err = urlReadRanges(filepath.Join(dir, "extracted", "DerivedEastAsianWidth.txt"), func(lo, hi int, value string) error {
		if s, ok := eawLong[value]; ok {
			value = s
		}
		for c := lo; c <= hi; c++ {
			ea[c] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	versions["DerivedEastAsianWidth.txt"] = v
	for c := range d.lbRaw {
		d.lbRaw[c] = "XX" // LineBreak.txt's @missing line
	}
	v, err = ucdLines(dir, "LineBreak.txt", func(f []string) error {
		lo, hi, err := ucdRange(f[0])
		if err != nil {
			return err
		}
		for c := lo; c <= hi; c++ {
			d.lbRaw[c] = f[1]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	versions["LineBreak.txt"] = v
	v, err = ucdLines(dir, "BidiBrackets.txt", func(f []string) error {
		c, _, err := ucdRange(f[0])
		if err != nil {
			return err
		}
		pair, _, err := ucdRange(f[1])
		if err != nil {
			return err
		}
		canon := func(r rune) rune {
			if dc := u.decomp[r]; len(dc) == 1 && !u.compat[r] {
				return dc[0]
			}
			return r
		}
		switch f[2] {
		case "o":
			d.bidi[c] |= bidiOpen
			d.bracketKey[c] = canon(pair)
		case "c":
			d.bidi[c] |= bidiClose
			d.bracketKey[c] = canon(c)
		default:
			return fmt.Errorf("unknown bracket type %q", f[2])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	versions["BidiBrackets.txt"] = v
	v, err = ucdLines(dir, "BidiMirroring.txt", func(f []string) error {
		c, _, err := ucdRange(f[0])
		if err != nil {
			return err
		}
		m, _, err := ucdRange(f[1])
		if err != nil {
			return err
		}
		d.mirror[c] = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	versions["BidiMirroring.txt"] = v
	for name, v := range versions {
		if v != u.version {
			return nil, fmt.Errorf("UCD versions differ: UnicodeData %s, %s %s", u.version, name, v)
		}
	}
	d.version = u.version
	idx := map[string]int{}
	for i, s := range lbNames {
		idx[s] = i
	}
	for c := range d.line {
		raw := d.lbRaw[c]
		var cls string
		switch raw {
		case "AI", "SG", "XX":
			cls = "AL"
		case "SA":
			if gc[c] == "Mn" || gc[c] == "Mc" {
				cls = "CM"
			} else {
				cls = "AL"
			}
		case "CJ":
			cls = "NS"
		case "QU":
			switch gc[c] {
			case "Pi":
				cls = "QU_Pi"
			case "Pf":
				cls = "QU_Pf"
			default:
				cls = "QU"
			}
		default:
			cls = raw
		}
		if c == 0x25CC {
			if cls != "AL" {
				return nil, fmt.Errorf("U+25CC is Line_Break %s, not AL", raw)
			}
			cls = "DottedCircle"
		}
		k, ok := idx[cls]
		if !ok {
			return nil, fmt.Errorf("U+%04X: unknown Line_Break %q", c, raw)
		}
		if e := ea[c]; e == "F" || e == "W" || e == "H" {
			k |= lbEastAsian
		}
		if u.extPict[c] && gc[c] == "Cn" {
			k |= lbExtPictCn
		}
		d.line[c] = k
	}
	return d, nil
}

// genBidiTables returns the text of std/text/bidi_tables.mk.
func genBidiTables(d *uaxData) string {
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by std/std-textlines_ucd_test.go from the Unicode Character Database. DO NOT EDIT.
// The UAX 9 Bidi_Class, Bidi_Paired_Bracket and Bidi_Mirroring_Glyph data that BidiLevels
// and Mirror read. UCD data Copyright Unicode, Inc., under the Unicode License v3. Regenerate
// with go test ./std -run TestSTDTEXT_LinesTablesUpToDate -update (UCD files in $MK_EVIDENCE/ucd).
// UCD version: %s

`, d.version)
	b.WriteString(`// BidiProp bytes: bits 0 to 4 Bidi_Class (Bc*), bits 5 and 6 the
// bracket type (BidiOpen, BidiClose). BidiAscii is the first 128.
`)
	writeArray(&b, "BidiAscii", "u8", d.bidi[:128], false)
	writeTrie3(&b, "BidiProp", buildTrie3(d.bidi))
	var cps, keys []int
	for c, k := range d.bracketKey {
		cps = append(cps, int(c))
		_ = k
	}
	slices.Sort(cps)
	for _, c := range cps {
		keys = append(keys, int(d.bracketKey[rune(c)]))
	}
	b.WriteString(`// The paired brackets in scalar order, and each one's match key: an
// opening bracket's pair, a closing one itself, either through its
// singleton canonical decomposition (BD16).
`)
	writeArray(&b, "BidiBracketCp", "u32", cps, true)
	writeArray(&b, "BidiBracketKey", "u32", keys, true)
	var mcs, mto []int
	for c := range d.mirror {
		mcs = append(mcs, int(c))
	}
	slices.Sort(mcs)
	for _, c := range mcs {
		mto = append(mto, int(d.mirror[rune(c)]))
	}
	b.WriteString("// Bidi_Mirroring_Glyph: the scalars in order, and each one's glyph.\n")
	writeArray(&b, "BidiMirrorCp", "u32", mcs, true)
	writeArray(&b, "BidiMirrorTo", "u32", mto, true)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// genLineTables returns the text of std/text/linebreak_tables.mk.
func genLineTables(d *uaxData) string {
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by std/std-textlines_ucd_test.go from the Unicode Character Database. DO NOT EDIT.
// The UAX 14 Line_Break data that LineBreaks reads, with rule LB1 applied. UCD data
// Copyright Unicode, Inc., under the Unicode License v3. Regenerate with
// go test ./std -run TestSTDTEXT_LinesTablesUpToDate -update (UCD files in $MK_EVIDENCE/ucd).
// UCD version: %s

`, d.version)
	b.WriteString(`// LineProp bytes: bits 0 to 5 the resolved Line_Break class (Lb*), bit 6
// LbEastAsian, bit 7 LbExtPictCn. LineAscii is the first 128.
`)
	writeArray(&b, "LineAscii", "u8", d.line[:128], false)
	writeTrie3(&b, "LineProp", buildTrie3(d.line))
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// The generated files, relative to std/.
const (
	bidiTablesPath = "text/bidi_tables.mk"
	lineTablesPath = "text/linebreak_tables.mk"
)

// TestSTDTEXT_LinesTablesUpToDate regenerates std/text/bidi_tables.mk and
// std/text/linebreak_tables.mk from the UCD and requires the committed
// files to equal them; -update rewrites them.
//
// The Line_Break byte bakes in LB1's resolution and the sets the rules of
// auxiliary/LineBreakTest.html name, so the Mk scan remaps nothing: AI, SG,
// XX and Complex_Context other than Mn and Mc are AL, Complex_Context Mn
// and Mc are CM, CJ is NS, QU splits by Pi and Pf, U+25CC is its own class
// (DottedCircle, an AL), and two flag bits carry EastAsian (ea F, W or H)
// and ExtPictUnassigned (Extended_Pictographic and gc Cn).
func TestSTDTEXT_LinesTablesUpToDate(t *testing.T) {
	d := needUax(t)
	for _, f := range []struct {
		path string
		want string
	}{{bidiTablesPath, genBidiTables(d)}, {lineTablesPath, genLineTables(d)}} {
		if *update {
			if err := os.WriteFile(f.path, []byte(f.want), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s (%d bytes), UCD %s", f.path, len(f.want), d.version)
			continue
		}
		got, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ReplaceAll(string(got), "\r\n", "\n") != f.want {
			t.Errorf("%s is not what the UCD %s files give; run go test ./std -run TestSTDTEXT_LinesTablesUpToDate -update", f.path, d.version)
		}
	}
}

// TestSTDTEXT_LinesTableBytes logs the static bytes of the generated tables.
func TestSTDTEXT_LinesTableBytes(t *testing.T) {
	d := needUax(t)
	bt, lt := buildTrie3(d.bidi), buildTrie3(d.line)
	t.Logf("BidiProp: %d bytes, brackets %d entries, mirrors %d entries (%d bytes)", bt.bytes()+128, len(d.bracketKey), len(d.mirror), 8*(len(d.bracketKey)+len(d.mirror)))
	t.Logf("LineProp: %d bytes", lt.bytes()+128)
}

// TestSTDTEXT_Premises checks what the Mk code assumes of the data: every
// paired bracket is ON and its key a bracket, every mirroring glyph is a
// scalar, and the classes fit below LbSot (63).
func TestSTDTEXT_Premises(t *testing.T) {
	d := needUax(t)
	on := 13 // ON in urlBidiNames
	for c, k := range d.bracketKey {
		if d.bidi[c]&0x1F != on {
			t.Errorf("U+%04X: a paired bracket of Bidi_Class %s", c, urlBidiNames[d.bidi[c]&0x1F][0])
		}
		if d.bidi[k]&0x60 == 0 {
			t.Errorf("U+%04X: its key U+%04X is no bracket", c, k)
		}
	}
	for c, m := range d.mirror {
		if m < 0 || m >= 0x110000 || (m >= 0xD800 && m < 0xE000) {
			t.Errorf("U+%04X mirrors to %X", c, m)
		}
	}
	if len(lbNames) > 63 {
		t.Fatalf("%d Line_Break classes, the LineProp byte holds 63", len(lbNames))
	}
}
