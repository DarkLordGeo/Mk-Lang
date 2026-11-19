// Why: std.text's Unicode property tables are generated data; they must match
// the UCD files they come from and the tables std.regex uses.
// What: the props_tables.mk generator and TestStd_PropsTablesUpToDate,
// TestStd_PropsMatchUCD, TestStd_PropsShareRegexSnapshot.
// How: parses the UCD 18.0.0 files in $MK_EVIDENCE/ucd and compares every
// scalar's category, script, extensions and property bits with a Mk program.

package std

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode"
)

var std10Update = flag.Bool("std10-update", false, "rewrite std/text/props_tables.mk")

// std10Cats is General_Category in the UCD's order: short, long.
var std10Cats = [][2]string{
	{"Lu", "Uppercase_Letter"}, {"Ll", "Lowercase_Letter"}, {"Lt", "Titlecase_Letter"}, {"Lm", "Modifier_Letter"},
	{"Lo", "Other_Letter"}, {"Mn", "Nonspacing_Mark"}, {"Mc", "Spacing_Mark"}, {"Me", "Enclosing_Mark"},
	{"Nd", "Decimal_Number"}, {"Nl", "Letter_Number"}, {"No", "Other_Number"}, {"Pc", "Connector_Punctuation"},
	{"Pd", "Dash_Punctuation"}, {"Ps", "Open_Punctuation"}, {"Pe", "Close_Punctuation"}, {"Pi", "Initial_Punctuation"},
	{"Pf", "Final_Punctuation"}, {"Po", "Other_Punctuation"}, {"Sm", "Math_Symbol"}, {"Sc", "Currency_Symbol"},
	{"Sk", "Modifier_Symbol"}, {"So", "Other_Symbol"}, {"Zs", "Space_Separator"}, {"Zl", "Line_Separator"},
	{"Zp", "Paragraph_Separator"}, {"Cc", "Control"}, {"Cf", "Format"}, {"Cs", "Surrogate"}, {"Co", "Private_Use"},
	{"Cn", "Unassigned"},
}

// std10Props are the binary properties Has answers: ECMA-262's table of
// binary Unicode properties (the \p{..} of a u-flag regular expression),
// which holds ID_Start, ID_Continue, White_Space and the emoji properties.
// Any, ASCII and Assigned are derived; Bidi_Mirrored is UnicodeData's
// field 9; the rest are read from the property files.
var std10Props = []string{
	"ASCII", "ASCII_Hex_Digit", "Alphabetic", "Any", "Assigned", "Bidi_Control", "Bidi_Mirrored", "Case_Ignorable",
	"Cased", "Changes_When_Casefolded", "Changes_When_Casemapped", "Changes_When_Lowercased",
	"Changes_When_NFKC_Casefolded", "Changes_When_Titlecased", "Changes_When_Uppercased", "Dash",
	"Default_Ignorable_Code_Point", "Deprecated", "Diacritic", "Emoji", "Emoji_Component", "Emoji_Modifier",
	"Emoji_Modifier_Base", "Emoji_Presentation", "Extended_Pictographic", "Extender", "Grapheme_Base",
	"Grapheme_Extend", "Hex_Digit", "IDS_Binary_Operator", "IDS_Trinary_Operator", "ID_Continue", "ID_Start",
	"Ideographic", "Join_Control", "Logical_Order_Exception", "Lowercase", "Math", "Noncharacter_Code_Point",
	"Pattern_Syntax", "Pattern_White_Space", "Quotation_Mark", "Radical", "Regional_Indicator", "Sentence_Terminal",
	"Soft_Dotted", "Terminal_Punctuation", "Unified_Ideograph", "Uppercase", "Variation_Selector", "White_Space",
	"XID_Continue", "XID_Start",
}

// std10MkName is a UCD name as an Mk identifier: the parts between '_'
// each start upper case, and a part written all in capitals keeps only
// its first ("ID_Start" IdStart, "Old_Italic" OldItalic, "SignWriting").
func std10MkName(s string) string {
	var b strings.Builder
	for _, p := range strings.Split(s, "_") {
		if p == "" {
			continue
		}
		if len(p) > 1 && strings.ToUpper(p) == p {
			p = p[:1] + strings.ToLower(p[1:])
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

type std10Ucp struct {
	version   string
	scripts   [][2]string // long, short; sorted by long name
	gc        []int       // per scalar: index in std10Cats
	sc        []int       // index in scripts
	scx       [][]int     // nil: {sc}
	props     []uint64    // bit i: std10Props[i]
	byShort   map[string]int
	propFiles map[string]bool
}

const std10N = 0x110000

func std10Parse(dir string) (*std10Ucp, error) {
	u := &std10Ucp{gc: make([]int, std10N), sc: make([]int, std10N), scx: make([][]int, std10N),
		props: make([]uint64, std10N), byShort: map[string]int{}}
	catAt := map[string]int{}
	for i, c := range std10Cats {
		catAt[c[0]] = i
	}
	for c := range u.gc {
		u.gc[c] = catAt["Cn"]
	}
	mirrored := make([]bool, std10N)
	first := rune(-1)
	ver, err := ucdLines(dir, "UnicodeData.txt", func(f []string) error {
		lo, _, err := ucdRange(f[0])
		if err != nil {
			return err
		}
		g, ok := catAt[f[2]]
		if !ok {
			return fmt.Errorf("category %q", f[2])
		}
		switch {
		case strings.HasSuffix(f[1], ", First>"):
			first = lo
			return nil
		case strings.HasSuffix(f[1], ", Last>"):
			for x := first; x <= lo; x++ {
				u.gc[x] = g
				mirrored[x] = f[9] == "Y"
			}
			first = -1
			return nil
		}
		u.gc[lo] = g
		mirrored[lo] = f[9] == "Y"
		return nil
	})
	if err != nil {
		return nil, err
	}
	u.version = ver
	if u.version == "" {
		// UnicodeData.txt has no header; Scripts.txt names the version.
		u.version = "?"
	}
	_, err = ucdLines(dir, "PropertyValueAliases.txt", func(f []string) error {
		if f[0] == "sc" {
			u.scripts = append(u.scripts, [2]string{f[2], f[1]})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(u.scripts, func(i, j int) bool { return u.scripts[i][0] < u.scripts[j][0] })
	byLong := map[string]int{}
	for i, s := range u.scripts {
		byLong[s[0]] = i
		u.byShort[s[1]] = i
	}
	unknown, ok := byLong["Unknown"]
	if !ok {
		return nil, fmt.Errorf("no Unknown script")
	}
	for c := range u.sc {
		u.sc[c] = unknown
	}
	v, err := ucdLines(dir, "Scripts.txt", func(f []string) error {
		lo, hi, err := ucdRange(f[0])
		if err != nil {
			return err
		}
		s, ok := byLong[f[1]]
		if !ok {
			return fmt.Errorf("script %q", f[1])
		}
		for x := lo; x <= hi; x++ {
			u.sc[x] = s
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	u.version = v
	_, err = ucdLines(dir, "ScriptExtensions.txt", func(f []string) error {
		lo, hi, err := ucdRange(f[0])
		if err != nil {
			return err
		}
		var set []int
		for _, w := range strings.Fields(f[1]) {
			s, ok := u.byShort[w]
			if !ok {
				return fmt.Errorf("script code %q", w)
			}
			set = append(set, s)
		}
		for x := lo; x <= hi; x++ {
			u.scx[x] = set
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	bit := map[string]int{}
	for i, p := range std10Props {
		bit[p] = i
	}
	seen := map[string]bool{}
	for _, file := range []string{"PropList.txt", "DerivedCoreProperties.txt", "DerivedNormalizationProps.txt", "emoji/emoji-data.txt"} {
		fv, err := ucdLines(dir, file, func(f []string) error {
			if len(f) != 2 {
				return nil // a property with values (InCB, NFKC_CF, ...)
			}
			b, ok := bit[f[1]]
			if !ok {
				return nil
			}
			seen[f[1]] = true
			lo, hi, err := ucdRange(f[0])
			if err != nil {
				return err
			}
			for x := lo; x <= hi; x++ {
				u.props[x] |= 1 << b
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if fv != u.version {
			return nil, fmt.Errorf("UCD versions differ: Scripts %s, %s %s", u.version, file, fv)
		}
	}
	for c := 0; c < std10N; c++ {
		u.props[c] |= 1 << bit["Any"]
		if c < 0x80 {
			u.props[c] |= 1 << bit["ASCII"]
		}
		if std10Cats[u.gc[c]][0] != "Cn" {
			u.props[c] |= 1 << bit["Assigned"]
		}
		if mirrored[c] {
			u.props[c] |= 1 << bit["Bidi_Mirrored"]
		}
	}
	for _, p := range []string{"Any", "ASCII", "Assigned", "Bidi_Mirrored"} {
		seen[p] = true
	}
	for _, p := range std10Props {
		if !seen[p] {
			return nil, fmt.Errorf("property %s is in no file", p)
		}
	}
	return u, nil
}

var std10UcpCache struct {
	sync.Once
	u   *std10Ucp
	err error
}

func std10NeedUcp(t *testing.T) *std10Ucp {
	t.Helper()
	dir := ucdDir()
	if dir == "" {
		t.Skip("UCD files absent: set MK_EVIDENCE to a directory holding ucd/ (UCD 18.0.0)")
	}
	std10UcpCache.Do(func() { std10UcpCache.u, std10UcpCache.err = std10Parse(dir) })
	if std10UcpCache.err != nil {
		t.Fatal(std10UcpCache.err)
	}
	return std10UcpCache.u
}

// std10Scx is c's script extensions, as indices in u.scripts.
func (u *std10Ucp) std10Scx(c int) []int {
	if s := u.scx[c]; s != nil {
		return s
	}
	return []int{u.sc[c]}
}

func std10GenProps(u *std10Ucp) string {
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by std/std-text_propstables_test.go from the Unicode Character Database. DO NOT EDIT.
// The property enums and data of props.mk (General_Category, Script, Script_Extensions,
// binary properties), from the UCD %s files in $MK_EVIDENCE/ucd. UCD data Copyright Unicode,
// Inc., under the Unicode License v3. Regenerate with go test ./std -run TestStd_PropsTablesUpToDate -std10-update.
// UCD version: %s

// The Unicode version of these tables, for UnicodeVersion.
const UcpVersion: str = %q

`, u.version, u.version, u.version)
	// members writes an enum's members with their UCD names as comments,
	// aligned as mk fmt aligns a block's trailing comments.
	members := func(names, notes []string) {
		w := 0
		for _, n := range names {
			w = max(w, len(n))
		}
		for i, n := range names {
			fmt.Fprintf(&b, "    %-*s // %s\n", w, n, notes[i])
		}
		b.WriteString("}\n\n")
	}
	var names, notes []string
	b.WriteString("/// A scalar's General_Category, by its long name in the UCD (Lu is\n/// UppercaseLetter). The set of values is closed by Unicode's stability\n/// policy.\nexport enum Category {\n")
	for _, c := range std10Cats {
		names, notes = append(names, std10MkName(c[1])), append(notes, c[0])
	}
	members(names, notes)
	names, notes = nil, nil
	b.WriteString("/// A Script value, by its long name in the UCD (Common, Inherited and\n/// Unknown among them; Old_Italic is OldItalic). A later Unicode adds\n/// scripts.\nexport open enum Script {\n")
	for _, s := range u.scripts {
		names, notes = append(names, std10MkName(s[0])), append(notes, s[1])
	}
	members(names, notes)
	names, notes = nil, nil
	b.WriteString("/// A binary property Has answers: ECMA-262's binary Unicode properties,\n/// by their UCD names (ID_Start is IdStart). A later version may add\n/// properties.\nexport open enum Property {\n")
	for _, p := range std10Props {
		names, notes = append(names, std10MkName(p)), append(notes, p)
	}
	members(names, notes)

	// Records and the script-extension sets.
	type rec struct {
		gc, sc, scx int
		props       uint64
	}
	recAt := map[rec]int{}
	var recs []rec
	scxAt := map[string]int{}
	var scxStart, scxLen []int
	var pool []int
	ids := make([]int, std10N)
	for c := 0; c < std10N; c++ {
		set := u.std10Scx(c)
		key := fmt.Sprint(set)
		si, ok := scxAt[key]
		if !ok {
			si = len(scxStart)
			scxAt[key] = si
			scxStart = append(scxStart, len(pool))
			scxLen = append(scxLen, len(set))
			pool = append(pool, set...)
		}
		r := rec{u.gc[c], u.sc[c], si, u.props[c]}
		id, ok := recAt[r]
		if !ok {
			id = len(recs)
			recAt[r] = id
			recs = append(recs, r)
		}
		ids[c] = id
	}
	tr := buildTrie3(ids)
	b.WriteString("// A scalar's record, through a three-stage trie (buildTrie3): UcpIndex1\n// picks a block of UcpIndex2, which picks a block of UcpData.\n")
	fmt.Fprintf(&b, "const UcpShift2: u32 = %d\nconst UcpShift3: u32 = %d\n\n", tr.s2, tr.s3)
	writeArray(&b, "UcpIndex1", elemType(tr.i1), tr.i1, false)
	writeArray(&b, "UcpIndex2", elemType(tr.i2), tr.i2, false)
	writeArray(&b, "UcpData", elemType(tr.data), tr.data, false)
	gcs := make([]int, len(recs))
	scs := make([]int, len(recs))
	scxs := make([]int, len(recs))
	for i, r := range recs {
		gcs[i], scs[i], scxs[i] = r.gc, r.sc, r.scx
	}
	b.WriteString("// Each record's category, script and script-extension set.\n")
	writeArray(&b, "UcpRecCat", "u8", gcs, false)
	writeArray(&b, "UcpRecScript", elemType(scs), scs, false)
	writeArray(&b, "UcpRecScx", elemType(scxs), scxs, false)
	fmt.Fprintf(&b, "// Each record's property bits: bit i is Property member i.\nconst UcpRecProps: [%d]u64 = [\n", len(recs))
	for i := 0; i < len(recs); i += 8 {
		b.WriteString("   ")
		for _, r := range recs[i:min(i+8, len(recs))] {
			fmt.Fprintf(&b, " 0x%X,", r.props)
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n\n")
	b.WriteString("// Set i of the script extensions is UcpScxPool[UcpScxStart[i]..][..UcpScxLen[i]].\n")
	writeArray(&b, "UcpScxStart", elemType(scxStart), scxStart, false)
	writeArray(&b, "UcpScxLen", "u8", scxLen, false)
	b.WriteString("const UcpScxPool: []Script = [\n")
	for _, s := range pool {
		fmt.Fprintf(&b, "    Script.%s,\n", std10MkName(u.scripts[s][0]))
	}
	b.WriteString("]\n\n")
	b.WriteString("// The categories and scripts by number.\nconst UcpCats: []Category = [\n")
	for _, c := range std10Cats {
		fmt.Fprintf(&b, "    Category.%s,\n", std10MkName(c[1]))
	}
	b.WriteString("]\n\nconst UcpScripts: []Script = [\n")
	for _, s := range u.scripts {
		fmt.Fprintf(&b, "    Script.%s,\n", std10MkName(s[0]))
	}
	b.WriteString("]\n\n")
	b.WriteString("// UcpBit is p's bit in UcpRecProps.\nUcpBit(p: Property): u32 {\n    return match p {\n")
	for i, p := range std10Props {
		fmt.Fprintf(&b, "        .%s { %d }\n", std10MkName(p), i)
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

const std10PropsFile = "text/props_tables.mk"

// TestStd_PropsTablesUpToDate: std/text/props_tables.mk equals the
// generator's output (-std10-update rewrites it). The file holds the three
// enums and a three-stage trie from each scalar to a record (category,
// script, script-extension set, property bits). The tables cover std.text's
// public Unicode properties: CategoryOf, ScriptOf, ScriptExtensions, Has and
// UnicodeVersion, from the same UCD snapshot std.regex's tables.mk and
// std.text's other tables are generated from.
func TestStd_PropsTablesUpToDate(t *testing.T) {
	u := std10NeedUcp(t)
	want := std10GenProps(u)
	path := filepath.FromSlash(std10PropsFile)
	if *std10Update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(want))
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is stale: regenerate with -std10-update", path)
	}
}

// TestStd_PropsTableBytes logs the data's size: the trie, the records
// and the script-extension sets.
func TestStd_PropsTableBytes(t *testing.T) {
	src, err := os.ReadFile(filepath.FromSlash(std10PropsFile))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^const (Ucp\w+): \[(\d+)\](u8|u16|u32|u64) =`)
	size := map[string]int{"u8": 1, "u16": 2, "u32": 4, "u64": 8}
	total := 0
	var parts []string
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		var n int
		fmt.Sscan(m[2], &n)
		total += n * size[m[3]]
		parts = append(parts, fmt.Sprintf("%s %d", m[1], n*size[m[3]]))
	}
	t.Logf("props_tables.mk data: %d bytes (%s), plus the []Script pools", total, strings.Join(parts, ", "))
}

func std10PropsProgram(u *std10Ucp) string {
	var b strings.Builder
	b.WriteString("import std.text\n\nconst Props: []text.Property = [\n")
	for _, p := range std10Props {
		fmt.Fprintf(&b, "    text.Property.%s,\n", std10MkName(p))
	}
	b.WriteString("]\n\nconst Cats: []text.Category = [\n")
	for _, c := range std10Cats {
		fmt.Fprintf(&b, "    text.Category.%s,\n", std10MkName(c[1]))
	}
	b.WriteString("]\n\nconst Scripts: []text.Script = [\n")
	for _, s := range u.scripts {
		fmt.Fprintf(&b, "    text.Script.%s,\n", std10MkName(s[0]))
	}
	b.WriteString(`]

CatNo(c: text.Category): int {
    for i in 0..Cats.Len() {
        if Cats[i] == c {
            return i
        }
    }
    return -1
}

ScriptNo(s: text.Script): int {
    for i in 0..Scripts.Len() {
        if Scripts[i] == s {
            return i
        }
    }
    return -1
}

// One line a scalar: category, script, the extensions, the property bits.
export Main(sys: &System) {
    var w := sys.Stdout()
    Print(mut w, "{v}\n", (v: text.UnicodeVersion())) else {}
    for c0 in 0..0x110000 {
        if c0 >= 0xD800 && c0 < 0xE000 {
            continue
        }
        c := Cast[char](Cast[u32](c0))
        var bits: u64 = 0
        for i in 0..Props.Len() {
            if text.Has(c, Props[i]) {
                bits = bits | (Trunc[u64](1) << Cast[u64](i))
            }
        }
        Print(mut w, "{g} {s} {b:x}", (g: CatNo(text.CategoryOf(c)), s: ScriptNo(text.ScriptOf(c)), b: bits)) else {}
        for x in text.ScriptExtensions(c) {
            Print(mut w, " {x}", (x: ScriptNo(x))) else {}
        }
        w.Str("\n") else {}
    }
    w.Flush() else {}
}
`)
	return b.String()
}

func std10PropsLine(u *std10Ucp, c int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %d %x", u.gc[c], u.sc[c], u.props[c])
	for _, s := range u.std10Scx(c) {
		fmt.Fprintf(&b, " %d", s)
	}
	return b.String()
}

// TestStd_PropsMatchUCD: a Mk program prints every scalar's category,
// script, script extensions and each property's bit, dev and release, and
// each line is compared with the files read directly (UnicodeData, Scripts,
// ScriptExtensions, PropertyValueAliases, PropList, DerivedCoreProperties,
// DerivedNormalizationProps, emoji/emoji-data), for all 1,112,064 scalars.
func TestStd_PropsMatchUCD(t *testing.T) {
	u := std10NeedUcp(t)
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			var flags []string
			if prof == "release" {
				flags = append(flags, "--release")
			}
			exe := buildNative(t, nil, std10PropsProgram(u), flags...)
			ctx, cancel := context.WithTimeout(context.Background(), std10RunTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe)
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v\n%s", err, errb.String())
			}
			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			if lines[0] != u.version {
				t.Errorf("UnicodeVersion() %q, the files %q", lines[0], u.version)
			}
			lines = lines[1:]
			if len(lines) != 0x110000-0x800 {
				t.Fatalf("%d lines, want %d", len(lines), 0x110000-0x800)
			}
			bad, k := 0, 0
			for c := 0; c < std10N; c++ {
				if c >= 0xD800 && c < 0xE000 {
					continue
				}
				if want := std10PropsLine(u, c); lines[k] != want {
					bad++
					if bad <= 20 {
						t.Errorf("U+%04X: got %q, want %q", c, lines[k], want)
					}
				}
				k++
			}
			if bad > 0 {
				t.Fatalf("%d of %d scalars differ", bad, len(lines))
			}
			t.Logf("%s: all %d scalars equal to the UCD %s files (%d scripts, %d properties)", prof, len(lines), u.version, len(u.scripts), len(std10Props))
		})
	}
}

// TestStd_PropsShareRegexSnapshot: props_tables.mk, std.regex's tables.mk
// and std.text's tables.mk name one UCD version, and std.regex's \p{..} sets
// for every category and script equal the scalars CategoryOf and ScriptOf
// give them.
func TestStd_PropsShareRegexSnapshot(t *testing.T) {
	u := std10NeedUcp(t)
	ver := regexp.MustCompile(`(?m)^const (?:UcpVersion|UnicodeVersion|TablesVersion): str = "([^"]+)"$`)
	for _, f := range []string{std10PropsFile, "regex/tables.mk", "text/tables.mk"} {
		src, err := os.ReadFile(filepath.FromSlash(f))
		if err != nil {
			t.Fatal(err)
		}
		m := ver.FindSubmatch(src)
		if m == nil || string(m[1]) != u.version {
			t.Errorf("%s: UCD version %q, the files %q", f, m, u.version)
		}
	}
	// std.regex's \p{..} sets are its own tables' (rxUcd): compare each
	// category and script with the scalars this module's data gives it.
	rx := rxUcd(t)
	for i, c := range std10Cats {
		want := rxNewSet()
		for x := 0; x < std10N; x++ {
			if u.gc[x] == i {
				want[x] = true
			}
		}
		std10SameSet(t, rx, c[0], want)
	}
	for i, s := range u.scripts {
		if s[0] == "Unknown" {
			continue // Go's regexp names no \p{Unknown}, so std.regex has none
		}
		want := rxNewSet()
		for x := 0; x < std10N; x++ {
			if u.sc[x] == i {
				want[x] = true
			}
		}
		std10SameSet(t, rx, s[0], want)
	}
}

func std10SameSet(t *testing.T, rx *rxTables, name string, want rxSet) {
	t.Helper()
	got, ok := rx.names[rxCanon(name)]
	if !ok {
		empty := true
		for _, b := range want {
			if b {
				empty = false
				break
			}
		}
		if !empty {
			t.Errorf("std.regex has no \\p{%s}", name)
		}
		return
	}
	for x := 0; x <= unicode.MaxRune; x++ {
		if got[x] != want[x] {
			t.Errorf("\\p{%s}: U+%04X is %t in std.regex, %t in std.text", name, x, got[x], want[x])
			return
		}
	}
}
