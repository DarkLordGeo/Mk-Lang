// Why: UTS 46's CheckBidi and CheckJoiners read Bidi_Class and Joining_Type,
// generated into std/url/props_tables.mk whether or not IDNA ships.
// What: TestStdUrl_TablesUpToDate (the generator; -update rewrites the file)
// and TestStdUrl_PropsLookup.
// How: reads the UCD's DerivedBidiClass.txt, DerivedJoiningType.txt and
// DerivedCombiningClass.txt (18.0.0); the lookup checks every scalar.

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// urlBidiNames is Bidi_Class in the order std/url/props.mk numbers it:
// short and long names.
var urlBidiNames = [][2]string{
	{"L", "Left_To_Right"}, {"R", "Right_To_Left"}, {"AL", "Arabic_Letter"},
	{"EN", "European_Number"}, {"ES", "European_Separator"}, {"ET", "European_Terminator"},
	{"AN", "Arabic_Number"}, {"CS", "Common_Separator"}, {"NSM", "Nonspacing_Mark"},
	{"BN", "Boundary_Neutral"}, {"B", "Paragraph_Separator"}, {"S", "Segment_Separator"},
	{"WS", "White_Space"}, {"ON", "Other_Neutral"}, {"LRE", "Left_To_Right_Embedding"},
	{"LRO", "Left_To_Right_Override"}, {"RLE", "Right_To_Left_Embedding"},
	{"RLO", "Right_To_Left_Override"}, {"PDF", "Pop_Directional_Format"},
	{"LRI", "Left_To_Right_Isolate"}, {"RLI", "Right_To_Left_Isolate"},
	{"FSI", "First_Strong_Isolate"}, {"PDI", "Pop_Directional_Isolate"},
}

// urlJoinNames is Joining_Type in the order std/url/props.mk numbers it.
var urlJoinNames = [][2]string{
	{"U", "Non_Joining"}, {"C", "Join_Causing"}, {"D", "Dual_Joining"},
	{"L", "Left_Joining"}, {"R", "Right_Joining"}, {"T", "Transparent"},
}

// urlProps is the generator's data: per code point, Bidi_Class in bits 0
// to 4 and Joining_Type in bits 5 to 7; and the code points whose
// Canonical_Combining_Class is 9 (Virama).
type urlProps struct {
	version string
	prop    []int
	virama  []int
}

// urlUcdDir is Mk-evidence/ucd, or "" when there is none.
func urlUcdDir() string {
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(root, "ucd")
	if _, err := os.Stat(filepath.Join(dir, "extracted", "DerivedBidiClass.txt")); err != nil {
		return ""
	}
	return dir
}

// urlReadRanges reads a UCD property file: each "@missing" line, then each
// data line, calls set(lo, hi, value) in file order.
func urlReadRanges(path string, set func(lo, hi int, value string) error) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	version := ""
	var data [][3]string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if version == "" && strings.HasPrefix(line, "# Derived") {
			if i := strings.LastIndex(line, "-"); i > 0 {
				version = strings.TrimSuffix(line[i+1:], ".txt")
			}
		}
		if rest, ok := strings.CutPrefix(line, "# @missing: "); ok {
			r, v, _ := strings.Cut(rest, ";")
			if err := urlSetRange(r, strings.TrimSpace(v), set); err != nil {
				return "", err
			}
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		r, v, _ := strings.Cut(line, ";")
		data = append(data, [3]string{r, strings.TrimSpace(v)})
	}
	for _, d := range data {
		if err := urlSetRange(d[0], d[1], set); err != nil {
			return "", err
		}
	}
	return version, nil
}

func urlSetRange(r, value string, set func(lo, hi int, value string) error) error {
	r = strings.TrimSpace(r)
	los, his, ok := strings.Cut(r, "..")
	if !ok {
		his = los
	}
	lo, err := strconv.ParseUint(los, 16, 32)
	if err != nil {
		return err
	}
	hi, err := strconv.ParseUint(his, 16, 32)
	if err != nil {
		return err
	}
	return set(int(lo), int(hi), value)
}

// urlIndexOf is the position of name, short or long, in names.
func urlIndexOf(names [][2]string, name string) (int, error) {
	for i, n := range names {
		if n[0] == name || n[1] == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unknown value %q", name)
}

func urlLoadProps(t *testing.T) *urlProps {
	t.Helper()
	dir := urlUcdDir()
	if dir == "" {
		t.Skip("Mk-evidence/ucd/extracted not found (set MK_EVIDENCE)")
	}
	p := &urlProps{prop: make([]int, 0x110000)}
	v, err := urlReadRanges(filepath.Join(dir, "extracted", "DerivedBidiClass.txt"), func(lo, hi int, value string) error {
		k, err := urlIndexOf(urlBidiNames, value)
		for c := lo; c <= hi; c++ {
			p.prop[c] = p.prop[c]&^0x1F | k
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p.version = v
	if _, err := urlReadRanges(filepath.Join(dir, "extracted", "DerivedJoiningType.txt"), func(lo, hi int, value string) error {
		k, err := urlIndexOf(urlJoinNames, value)
		for c := lo; c <= hi; c++ {
			p.prop[c] = p.prop[c]&0x1F | k<<5
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := urlReadRanges(filepath.Join(dir, "extracted", "DerivedCombiningClass.txt"), func(lo, hi int, value string) error {
		if value == "9" {
			for c := lo; c <= hi; c++ {
				p.virama = append(p.virama, c)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// urlTrie is a three-stage table: Index1[c >> (s2 + s3)] is a block of
// Index2, whose entry (c >> s3) & (2^s2 - 1) is a block of Data.
type urlTrie struct {
	s2, s3       int
	i1, i2, data []int
}

func urlWidth(blocks int) int {
	if blocks > 256 {
		return 2
	}
	return 1
}

func (tr urlTrie) bytes() int {
	return len(tr.i1)*urlWidth(len(tr.i2)>>tr.s2) + len(tr.i2)*urlWidth(len(tr.data)>>tr.s3) + len(tr.data)
}

// urlBlocks splits v into blocks of 2^shift, keeps each distinct block
// once, and returns the blocks and each block's id.
func urlBlocks(v []int, shift int) (blocks, ids []int) {
	seen := map[string]int{}
	for lo := 0; lo < len(v); lo += 1 << shift {
		key := fmt.Sprint(v[lo : lo+1<<shift])
		id, ok := seen[key]
		if !ok {
			id = len(blocks) >> shift
			seen[key] = id
			blocks = append(blocks, v[lo:lo+1<<shift]...)
		}
		ids = append(ids, id)
	}
	return blocks, ids
}

// urlBuildTrie chooses the two block sizes with the fewest bytes.
func urlBuildTrie(v []int) urlTrie {
	var best urlTrie
	for s3 := 3; s3 <= 8; s3++ {
		data, ids := urlBlocks(v, s3)
		for s2 := 2; s2 <= 8; s2++ {
			if len(ids)%(1<<s2) != 0 {
				continue
			}
			i2, i1 := urlBlocks(ids, s2)
			tr := urlTrie{s2: s2, s3: s3, i1: i1, i2: i2, data: data}
			if best.data == nil || tr.bytes() < best.bytes() {
				best = tr
			}
		}
	}
	return best
}

func urlElemType(blocks int) string {
	if blocks > 256 {
		return "u16"
	}
	return "u8"
}

func urlWriteArray(b *strings.Builder, name, typ string, v []int, hex bool) {
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

// urlGenTables returns the text of std/url/props_tables.mk.
func urlGenTables(p *urlProps) string {
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by std/std-url_tables_test.go from the Unicode Character Database. DO NOT EDIT.
// The Bidi_Class, Joining_Type and Virama data of UTS 46's CheckBidi (RFC 5893) and CheckJoiners
// (RFC 5892) that props.mk reads. UCD data Copyright Unicode, Inc., under the Unicode License v3.
// Regenerate with go test ./std -run TestStdUrl_TablesUpToDate -update (UCD files in $MK_EVIDENCE/ucd).
// UCD version: %s

`, p.version)
	tr := urlBuildTrie(p.prop)
	b.WriteString("// PropData bytes: bits 0 to 4 Bidi_Class, bits 5 to 7 Joining_Type,\n// numbered as props.mk's constants. PropIndex1[c >> (PropShift2 +\n// PropShift3)] is a block of PropIndex2, whose entry\n// (c >> PropShift3) & (2^PropShift2 - 1) is a block of PropData.\n")
	fmt.Fprintf(&b, "const PropShift2: u32 = %d\nconst PropShift3: u32 = %d\n\n", tr.s2, tr.s3)
	urlWriteArray(&b, "PropIndex1", urlElemType(len(tr.i2)>>tr.s2), tr.i1, false)
	urlWriteArray(&b, "PropIndex2", urlElemType(len(tr.data)>>tr.s3), tr.i2, false)
	urlWriteArray(&b, "PropData", "u8", tr.data, false)
	b.WriteString("// Virama is the code points whose Canonical_Combining_Class is 9, in\n// order.\n")
	urlWriteArray(&b, "Virama", "u32", p.virama, true)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

const urlTablesPath = "url/props_tables.mk"

// TestStdUrl_TablesUpToDate: std/url/props_tables.mk is what the UCD
// gives; -update rewrites it.
func TestStdUrl_TablesUpToDate(t *testing.T) {
	p := urlLoadProps(t)
	want := urlGenTables(p)
	tr := urlBuildTrie(p.prop)
	t.Logf("UCD %s: shifts %d and %d, PropIndex1 %d entries, PropIndex2 %d, PropData %d, Virama %d code points: %d bytes", p.version, tr.s2, tr.s3, len(tr.i1), len(tr.i2), len(tr.data), len(p.virama), tr.bytes()+4*len(p.virama))
	if *update {
		if err := os.WriteFile(urlTablesPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", urlTablesPath, len(want))
		return
	}
	got, err := os.ReadFile(urlTablesPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Fatalf("%s is not what the UCD %s files give; run go test ./std -run TestStdUrl_TablesUpToDate -update", urlTablesPath, p.version)
	}
}

// TestStdUrl_PropsLookup: props.mk's BidiJoining and IsVirama, in the
// driver, for every scalar value, against the UCD.
func TestStdUrl_PropsLookup(t *testing.T) {
	p := urlLoadProps(t)
	isVirama := map[int]bool{}
	for _, c := range p.virama {
		isVirama[c] = true
	}
	var records []string
	var starts []int
	const chunk = 0x8000
	for lo := 0; lo < 0x110000; lo += chunk {
		records = append(records, fmt.Sprintf("props %d %d", lo, chunk))
		starts = append(starts, lo)
	}
	answers := urlAsk(t, "release", records)
	bad := 0
	for i, a := range answers {
		got, ok := strings.CutPrefix(a, "ok ")
		if !ok || len(got) != 4*chunk {
			t.Fatalf("props %d: %s", starts[i], urlShortText(a))
		}
		for k := 0; k < chunk; k++ {
			c := starts[i] + k
			if c >= 0xD800 && c <= 0xDFFF {
				continue
			}
			want := fmt.Sprintf("%02x%02x", p.prop[c], map[bool]int{false: 0, true: 1}[isVirama[c]])
			if got[4*k:4*k+4] != want {
				t.Errorf("U+%04X: got %s, want %s", c, got[4*k:4*k+4], want)
				bad++
				if bad == 10 {
					t.FailNow()
				}
			}
		}
	}
}
