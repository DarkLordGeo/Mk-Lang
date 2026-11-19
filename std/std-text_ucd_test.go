// Why: std.text's tables must follow the UCD files exactly, and Go's unicode
// package is an older Unicode version, so they are generated from the files.
// What: the UCD parser, genTextTables and TestSTDTEXT_TablesUpToDate, which
// keeps std/text/tables.mk equal to the output (-update rewrites it).
// How: reads $MK_EVIDENCE/ucd, builds two-stage tries (buildTrie) and the
// composition and folding tables; the same parse feeds the Go models.

package std

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// The parsed UCD, read once per test binary.
var (
	ucdOnce  sync.Once
	ucdVal   *ucd
	ucdErr   string
	ucdFatal bool
)

// The Grapheme_Cluster_Break values, in the order std/text/tables.mk
// numbers them (the low four bits of a Prop byte).
const (
	gcbOther = iota
	gcbCR
	gcbLF
	gcbControl
	gcbExtend
	gcbZWJ
	gcbRI
	gcbPrepend
	gcbSpacingMark
	gcbL
	gcbV
	gcbT
	gcbLV
	gcbLVT
	gcbCount
)

var gcbNames = map[string]int{
	"Other": gcbOther, "CR": gcbCR, "LF": gcbLF, "Control": gcbControl,
	"Extend": gcbExtend, "ZWJ": gcbZWJ, "Regional_Indicator": gcbRI,
	"Prepend": gcbPrepend, "SpacingMark": gcbSpacingMark, "L": gcbL,
	"V": gcbV, "T": gcbT, "LV": gcbLV, "LVT": gcbLVT,
}

// The Indic_Conjunct_Break values (bits 5 and 6 of a Prop byte).
const (
	incbNone = iota
	incbLinker
	incbConsonant
	incbExtend
)

// Bits of a Prop byte above the GCB value.
const (
	propExtPict = 1 << 4
	propWide    = 1 << 7
)

// Bits of NormRec.flags.
const (
	nfcNo     = 1 << 0
	nfcMaybe  = 1 << 1
	nfkcNo    = 1 << 2
	nfkcMaybe = 1 << 3
	nfdNo     = 1 << 4
	nfkdNo    = 1 << 5
	hangulLVT = 1 << 6
)

// Hangul syllable arithmetic (Unicode section 3.12).
const (
	hSBase  = 0xAC00
	hLBase  = 0x1100
	hVBase  = 0x1161
	hTBase  = 0x11A7
	hLCount = 19
	hVCount = 21
	hTCount = 28
	hNCount = hVCount * hTCount
	hSCount = hLCount * hNCount
)

// ucd holds what std.text needs from the UCD, per scalar.
type ucd struct {
	version string
	gcb     []uint8 // Grapheme_Cluster_Break
	incb    []uint8 // Indic_Conjunct_Break
	extPict []bool
	emojiP  []bool  // Emoji_Presentation
	eaw     []uint8 // 'W', 'F', 'A', 'N', 'H' or 'n' (Na)
	ccc     []uint8
	// decomp is UnicodeData field 5's mapping; compat says it was tagged.
	decomp map[rune][]rune
	compat map[rune]bool
	// fullExcl is Full_Composition_Exclusion; qc[form] the quick-check
	// value 'Y', 'N' or 'M' of NFD, NFC, NFKD, NFKC (index by normForm).
	fullExcl []bool
	qc       [4][]byte
	fold     map[rune][]rune // CaseFolding C and F
	// pairs is compPairs, built once by the models.
	pairsOnce sync.Once
	pairs     map[[2]rune]rune
}

// The four forms, in the order of enum Form in std/text.
const (
	formNFC = iota
	formNFD
	formNFKC
	formNFKD
)

// ucdDir is the UCD directory: $MK_EVIDENCE/ucd, else Mk-evidence/ucd
// beside the repository. It returns "" when the files are absent.
func ucdDir() string {
	var dirs []string
	if d := os.Getenv("MK_EVIDENCE"); d != "" {
		dirs = append(dirs, filepath.Join(d, "ucd"))
	}
	dirs = append(dirs, filepath.Join("..", "..", "Mk-evidence", "ucd"))
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "UnicodeData.txt")); err == nil {
			return d
		}
	}
	return ""
}

// needUCD returns the parsed UCD, or skips the test with the reason.
func needUCD(t *testing.T) *ucd {
	t.Helper()
	ucdOnce.Do(func() {
		dir := ucdDir()
		if dir == "" {
			ucdErr = "UCD files absent: set MK_EVIDENCE to a directory holding ucd/ (UnicodeData.txt and the rest of UCD 18.0.0), or put Mk-evidence/ucd beside the repository"
			return
		}
		u, err := parseUCD(dir)
		if err != nil {
			ucdErr = "parse UCD: " + err.Error()
			ucdFatal = true
			return
		}
		ucdVal = u
	})
	if ucdErr != "" {
		if ucdFatal {
			t.Fatal(ucdErr)
		}
		t.Skip(ucdErr)
	}
	return ucdVal
}

// ucdLines calls f with the fields of every data line of the file (the
// part before '#', split at ';', trimmed), and returns the version from
// its header ("# Name-18.0.0.txt", or "# Version: 18.0.0" in emoji/).
func ucdLines(dir, name string, f func(fields []string) error) (string, error) {
	file, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	version := ""
	for sc.Scan() {
		line := sc.Text()
		if version == "" && strings.HasPrefix(line, "#") {
			if m := ucdVersionRE.FindStringSubmatch(line); m != nil {
				version = m[1]
			}
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, ";")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if err := f(fields); err != nil {
			return "", fmt.Errorf("%s: %q: %v", name, line, err)
		}
	}
	return version, sc.Err()
}

var ucdVersionRE = regexp.MustCompile(`(?:-|Version: )(\d+\.\d+\.\d+)(?:\.txt|$)`)

// ucdRange parses "0041" or "0041..005A".
func ucdRange(s string) (rune, rune, error) {
	lo, hi, ok := strings.Cut(s, "..")
	a, err := strconv.ParseUint(lo, 16, 32)
	if err != nil {
		return 0, 0, err
	}
	b := a
	if ok {
		if b, err = strconv.ParseUint(hi, 16, 32); err != nil {
			return 0, 0, err
		}
	}
	return rune(a), rune(b), nil
}

// ucdScalars parses "0061 0301".
func ucdScalars(s string) ([]rune, error) {
	var out []rune
	for _, w := range strings.Fields(s) {
		v, err := strconv.ParseUint(w, 16, 32)
		if err != nil {
			return nil, err
		}
		out = append(out, rune(v))
	}
	return out, nil
}

func parseUCD(dir string) (*ucd, error) {
	const n = 0x110000
	u := &ucd{
		gcb: make([]uint8, n), incb: make([]uint8, n), extPict: make([]bool, n),
		emojiP: make([]bool, n), eaw: make([]uint8, n), ccc: make([]uint8, n),
		decomp: map[rune][]rune{}, compat: map[rune]bool{}, fullExcl: make([]bool, n),
		fold: map[rune][]rune{},
	}
	for i := range u.eaw {
		u.eaw[i] = 'N'
	}
	for f := range u.qc {
		u.qc[f] = make([]byte, n)
		for i := range u.qc[f] {
			u.qc[f][i] = 'Y'
		}
	}
	versions := map[string]string{}
	add := func(name string, f func([]string) error) error {
		v, err := ucdLines(dir, name, f)
		versions[name] = v
		return err
	}
	if err := add("auxiliary/GraphemeBreakProperty.txt", func(fl []string) error {
		lo, hi, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		g, ok := gcbNames[fl[1]]
		if !ok {
			return fmt.Errorf("unknown GCB %q", fl[1])
		}
		for c := lo; c <= hi; c++ {
			u.gcb[c] = uint8(g)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := add("DerivedCoreProperties.txt", func(fl []string) error {
		if len(fl) < 3 || fl[1] != "InCB" {
			return nil
		}
		lo, hi, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		v := map[string]uint8{"Linker": incbLinker, "Consonant": incbConsonant, "Extend": incbExtend}[fl[2]]
		if v == 0 {
			return fmt.Errorf("unknown InCB %q", fl[2])
		}
		for c := lo; c <= hi; c++ {
			u.incb[c] = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := add("emoji/emoji-data.txt", func(fl []string) error {
		lo, hi, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		for c := lo; c <= hi; c++ {
			switch fl[1] {
			case "Extended_Pictographic":
				u.extPict[c] = true
			case "Emoji_Presentation":
				u.emojiP[c] = true
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := add("EastAsianWidth.txt", func(fl []string) error {
		lo, hi, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		v := fl[1][0]
		if fl[1] == "Na" {
			v = 'n'
		}
		for c := lo; c <= hi; c++ {
			u.eaw[c] = v
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := add("UnicodeData.txt", func(fl []string) error {
		c, _, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		ccc, err := strconv.Atoi(fl[3])
		if err != nil {
			return err
		}
		u.ccc[c] = uint8(ccc)
		if d := fl[5]; d != "" {
			if strings.HasPrefix(d, "<") {
				u.compat[c] = true
				d = d[strings.IndexByte(d, '>')+1:]
			}
			rs, err := ucdScalars(d)
			if err != nil {
				return err
			}
			u.decomp[c] = rs
		}
		return nil
	}); err != nil {
		return nil, err
	}
	qcIndex := map[string]int{"NFC_QC": formNFC, "NFD_QC": formNFD, "NFKC_QC": formNFKC, "NFKD_QC": formNFKD}
	if err := add("DerivedNormalizationProps.txt", func(fl []string) error {
		if fl[1] == "Full_Composition_Exclusion" {
			lo, hi, err := ucdRange(fl[0])
			if err != nil {
				return err
			}
			for c := lo; c <= hi; c++ {
				u.fullExcl[c] = true
			}
			return nil
		}
		f, ok := qcIndex[fl[1]]
		if !ok {
			return nil
		}
		lo, hi, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		for c := lo; c <= hi; c++ {
			u.qc[f][c] = fl[2][0]
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := add("CaseFolding.txt", func(fl []string) error {
		if fl[1] != "C" && fl[1] != "F" {
			return nil
		}
		c, _, err := ucdRange(fl[0])
		if err != nil {
			return err
		}
		rs, err := ucdScalars(fl[2])
		if err != nil {
			return err
		}
		u.fold[c] = rs
		return nil
	}); err != nil {
		return nil, err
	}
	for name, v := range versions {
		if v == "" && name == "UnicodeData.txt" {
			continue // the one file with no header
		}
		if v == "" {
			return nil, fmt.Errorf("%s: no version in its header", name)
		}
		if u.version == "" {
			u.version = v
		} else if v != u.version {
			return nil, fmt.Errorf("%s is version %s, others %s", name, v, u.version)
		}
	}
	return u, nil
}

// isHangulSyllable reports whether c is a precomposed Hangul syllable.
func isHangulSyllable(c rune) bool { return c >= hSBase && c < hSBase+hSCount }

// fullDecomp is c's full canonical (compat false) or compatibility
// decomposition, Hangul included, or nil when c decomposes to itself.
func (u *ucd) fullDecomp(c rune, compat bool) []rune {
	if isHangulSyllable(c) {
		s := c - hSBase
		out := []rune{hLBase + s/hNCount, hVBase + (s%hNCount)/hTCount}
		if t := s % hTCount; t != 0 {
			out = append(out, hTBase+t)
		}
		return out
	}
	d, ok := u.decomp[c]
	if !ok || (u.compat[c] && !compat) {
		return nil
	}
	var out []rune
	for _, x := range d {
		if e := u.fullDecomp(x, compat); e != nil {
			out = append(out, e...)
		} else {
			out = append(out, x)
		}
	}
	return out
}

// compPairs is the canonical composition table: (first, second) to the
// primary composite, without Full_Composition_Exclusion and without
// Hangul (composed by arithmetic).
func (u *ucd) compPairs() map[[2]rune]rune {
	m := map[[2]rune]rune{}
	for c, d := range u.decomp {
		if u.compat[c] || len(d) != 2 || u.fullExcl[c] {
			continue
		}
		m[[2]rune{d[0], d[1]}] = c
	}
	return m
}

// prop is the Prop byte of c: GCB, ExtPict, InCB and the wide bit.
func (u *ucd) prop(c rune) uint8 {
	p := u.gcb[c] | u.incb[c]<<5
	if u.extPict[c] {
		p |= propExtPict
	}
	if u.eaw[c] == 'W' || u.eaw[c] == 'F' || u.emojiP[c] {
		p |= propWide
	}
	return p
}

// trie is a two-stage table: data[index[c >> shift] << shift | c & mask].
type trie struct {
	shift int
	index []int
	data  []int
}

// buildTrie chooses the block size with the fewest bytes, counting an
// index entry as one byte when there are at most 256 blocks, else two.
func buildTrie(v []int, width int) trie {
	var best trie
	bestSize := -1
	for shift := 4; shift <= 9; shift++ {
		size := 1 << shift
		seen := map[string]int{}
		var tr trie
		tr.shift = shift
		for lo := 0; lo < len(v); lo += size {
			key := fmt.Sprint(v[lo : lo+size])
			b, ok := seen[key]
			if !ok {
				b = len(tr.data) >> shift
				seen[key] = b
				tr.data = append(tr.data, v[lo:lo+size]...)
			}
			tr.index = append(tr.index, b)
		}
		iw := 1
		if len(tr.data)>>shift > 256 {
			iw = 2
		}
		bytes := len(tr.index)*iw + len(tr.data)*width
		if bestSize < 0 || bytes < bestSize {
			best, bestSize = tr, bytes
		}
	}
	return best
}

// elemType is the smallest unsigned Mk type that holds every value.
func elemType(v []int) string {
	m := 0
	for _, x := range v {
		m = max(m, x)
	}
	switch {
	case m < 1<<8:
		return "u8"
	case m < 1<<16:
		return "u16"
	}
	return "u32"
}

// writeArray writes `const name: [n]T = [...]` with 16 values a line.
func writeArray(b *strings.Builder, name, typ string, v []int, hex bool) {
	fmt.Fprintf(b, "const %s: [%d]%s = [\n", name, len(v), typ)
	for i := 0; i < len(v); i += 16 {
		b.WriteString("   ")
		for _, x := range v[i:min(i+16, len(v))] {
			if hex {
				fmt.Fprintf(b, " 0x%X,", x)
			} else {
				fmt.Fprintf(b, " %d,", x)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n\n")
}

func writeTrie(b *strings.Builder, name string, tr trie, valType string) {
	fmt.Fprintf(b, "const %sShift: u32 = %d\n\n", name, tr.shift)
	writeArray(b, name+"Index", elemType(tr.index), tr.index, false)
	writeArray(b, name+"Data", valType, tr.data, false)
}

// genTextTables returns the text of std/text/tables.mk.
func genTextTables(u *ucd) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by std/std-text_ucd_test.go from the Unicode Character Database. DO NOT EDIT.
// std.text's segmentation, width, normalization and case-fold data. UCD data
// Copyright Unicode, Inc., under the Unicode License v3. Regenerate with
// go test ./std -run TestSTDTEXT_TablesUpToDate -update (UCD files in $MK_EVIDENCE/ucd).
// UCD version: %s

// The Unicode version these tables were generated from.
const TablesVersion: str = "%s"

`, u.version, u.version)

	// Segmentation and width: one byte per scalar.
	const n = 0x110000
	props := make([]int, n)
	for c := range props {
		props[c] = int(u.prop(rune(c)))
		// ClusterEnd's flags rely on these: no flag survives a control.
		if g := u.gcb[c]; (g == gcbControl || g == gcbCR || g == gcbLF) && (u.incb[c] != incbNone || u.extPict[c]) {
			return "", fmt.Errorf("U+%04X is a control with InCB or ExtPict", c)
		}
	}
	b.WriteString(`// Prop bytes: bits 0 to 3 Grapheme_Cluster_Break (GcbOther to GcbLVT),
// bit 4 Extended_Pictographic, bits 5 and 6 Indic_Conjunct_Break (InCB*),
// bit 7 wide (East_Asian_Width W or F, or Emoji_Presentation).
`)
	writeTrie(&b, "Prop", buildTrie(props, 1), "u8")

	// Normalization records.
	pairs := u.compPairs()
	bySecond := map[rune][][2]rune{} // second -> (first, composite)
	for k, c := range pairs {
		bySecond[k[1]] = append(bySecond[k[1]], [2]rune{k[0], c})
		if utf8.RuneLen(c) > utf8.RuneLen(k[0])+utf8.RuneLen(k[1]) {
			return "", fmt.Errorf("composite U+%04X is longer than its parts", c)
		}
		if u.ccc[c] != 0 || u.ccc[k[0]] != 0 {
			// Compose writes a composite as a starter (class 0).
			return "", fmt.Errorf("composite U+%04X or its first part is not a starter", c)
		}
	}
	type normRec struct {
		ccc, flags, dlen, doff, klen, koff, clen, coff, dbytes, kbytes int
	}
	var decomp []int
	decompAt := map[string]int{}
	putDecomp := func(rs []rune) int {
		key := fmt.Sprint(rs)
		if off, ok := decompAt[key]; ok {
			return off
		}
		off := len(decomp)
		decompAt[key] = off
		for _, r := range rs {
			decomp = append(decomp, int(u.ccc[r])<<24|int(r))
		}
		return off
	}
	var compFirst, compResult []int
	bytesOf := func(rs []rune) int {
		n := 0
		for _, r := range rs {
			n += utf8.RuneLen(r)
		}
		return n
	}
	recs := []normRec{{}}
	recAt := map[normRec]int{{}: 0}
	normVals := make([]int, n)
	for c := rune(0); c < n; c++ {
		var r normRec
		r.ccc = int(u.ccc[c])
		switch u.qc[formNFC][c] {
		case 'N':
			r.flags |= nfcNo
		case 'M':
			r.flags |= nfcMaybe
		}
		switch u.qc[formNFKC][c] {
		case 'N':
			r.flags |= nfkcNo
		case 'M':
			r.flags |= nfkcMaybe
		}
		if u.qc[formNFD][c] == 'N' {
			r.flags |= nfdNo
		}
		if u.qc[formNFKD][c] == 'N' {
			r.flags |= nfkdNo
		}
		if isHangulSyllable(c) {
			r.flags |= hangulLVT
		} else {
			if d := u.fullDecomp(c, false); d != nil {
				r.dlen, r.doff, r.dbytes = len(d), putDecomp(d), bytesOf(d)
			}
			if k := u.fullDecomp(c, true); k != nil {
				r.klen, r.koff, r.kbytes = len(k), putDecomp(k), bytesOf(k)
			}
		}
		if (r.dlen > 0) != (u.qc[formNFD][c] == 'N') && !isHangulSyllable(c) {
			return "", fmt.Errorf("U+%04X: NFD_QC disagrees with its decomposition", c)
		}
		if (r.klen > 0) != (u.qc[formNFKD][c] == 'N') && !isHangulSyllable(c) {
			return "", fmt.Errorf("U+%04X: NFKD_QC disagrees with its decomposition", c)
		}
		if ps := bySecond[c]; len(ps) > 0 {
			sort.Slice(ps, func(i, j int) bool { return ps[i][0] < ps[j][0] })
			r.coff, r.clen = len(compFirst), len(ps)
			for _, p := range ps {
				compFirst = append(compFirst, int(p[0]))
				compResult = append(compResult, int(p[1]))
			}
		}
		id, ok := recAt[r]
		if !ok {
			id = len(recs)
			recs = append(recs, r)
			recAt[r] = id
		}
		normVals[c] = id
	}
	if len(recs) >= 1<<16 || len(decomp) >= 1<<16 || len(compFirst) >= 1<<16 {
		return "", fmt.Errorf("normalization tables outgrow u16: %d records, %d decomposition scalars, %d pairs", len(recs), len(decomp), len(compFirst))
	}
	for _, r := range recs {
		if r.dlen >= 1<<5 || r.klen >= 1<<5 || r.clen >= 1<<7 || r.flags >= 1<<7 || r.dbytes >= 1<<8 || r.kbytes >= 1<<7 {
			return "", fmt.Errorf("a normalization record outgrows its bits: %+v", r)
		}
	}
	b.WriteString(`// A scalar's normalization facts are record NormData[...] of NormMeta
// and NormOff. Record 0 is a starter that decomposes to itself, is
// quick-check Yes in every form and composes with nothing before it.
// NormMeta: ccc (bits 0 to 7), flags (8 to 14: NfcNo, NfcMaybe, NfkcNo,
// NfkcMaybe, NfdNo, NfkdNo, HangulLvt), dlen (15 to 19), klen (20 to 24),
// clen (25 to 31). NormOff: doff (bits 0 to 15), koff (16 to 31), coff
// (32 to 47), dbytes (48 to 55), kbytes (56 to 63). dlen scalars at
// Decomp[doff..] are the full canonical decomposition (none when dlen is
// 0), klen at koff the full compatibility one, dbytes and kbytes their
// UTF-8 lengths. A scalar that combines with a starter before it has clen
// pairs at CompFirst[coff..] and CompResult[coff..], sorted by the first.
`)
	writeTrie(&b, "Norm", buildTrie(normVals, 2), elemType(normVals))
	meta := make([]int, len(recs))
	offs := make([]int, len(recs))
	for i, r := range recs {
		meta[i] = r.ccc | r.flags<<8 | r.dlen<<15 | r.klen<<20 | r.clen<<25
		offs[i] = r.doff | r.koff<<16 | r.coff<<32 | r.dbytes<<48 | r.kbytes<<56
	}
	writeArray(&b, "NormMeta", "u32", meta, true)
	writeArray(&b, "NormOff", "u64", offs, true)
	b.WriteString("// Decomposition scalars: ccc << 24 | scalar.\n")
	writeArray(&b, "Decomp", "u32", decomp, true)
	b.WriteString("// Composition pairs, grouped by the second scalar (NormRec.coff).\n")
	writeArray(&b, "CompFirst", "u32", compFirst, true)
	writeArray(&b, "CompResult", "u32", compResult, true)

	// Case folding: offset << 2 | length into FoldPool, 0 for none.
	var pool []int
	poolAt := map[string]int{}
	foldVals := make([]int, n)
	keys := make([]rune, 0, len(u.fold))
	for c := range u.fold {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, c := range keys {
		rs := u.fold[c]
		if len(rs) < 1 || len(rs) > 3 {
			return "", fmt.Errorf("U+%04X folds to %d scalars", c, len(rs))
		}
		key := fmt.Sprint(rs)
		off, ok := poolAt[key]
		if !ok {
			off = len(pool)
			poolAt[key] = off
			for _, r := range rs {
				pool = append(pool, int(r))
			}
		}
		foldVals[c] = off<<2 | len(rs)
	}
	b.WriteString(`// Full case folding (CaseFolding.txt, statuses C and F): FoldData is
// offset << 2 | length into FoldPool, 0 when a scalar folds to itself.
`)
	writeTrie(&b, "Fold", buildTrie(foldVals, 2), elemType(foldVals))
	writeArray(&b, "FoldPool", "u32", pool, true)
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// textTablesPath is the generated file, relative to std/.
const textTablesPath = "text/tables.mk"

// TestSTDTEXT_TablesUpToDate regenerates std/text/tables.mk from the UCD
// and requires the committed file to equal it; -update rewrites it.
func TestSTDTEXT_TablesUpToDate(t *testing.T) {
	u := needUCD(t)
	want, err := genTextTables(u)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(textTablesPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes), UCD %s", textTablesPath, len(want), u.version)
		return
	}
	got, err := os.ReadFile(textTablesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is not what the UCD %s files give; run go test ./std -run TestSTDTEXT_TablesUpToDate -update", textTablesPath, u.version)
	}
}
