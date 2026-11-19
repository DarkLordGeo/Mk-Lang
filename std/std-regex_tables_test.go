// Why: \p{..}, \P{..} and (?i) read Unicode tables that must come from one
// UCD version and use Go regexp/syntax's names.
// What: the generator of std/regex/tables.mk, TestSTDREGEX_UcdAgainstGo
// and TestSTDREGEX_CorpusOffDiff.
// How: reads the UCD files ($MK_EVIDENCE/ucd), pins where they disagree
// with Go's unicode package, and keeps test inputs off that list.

package std

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// rxTablesPath is the generated file, relative to std/.
const rxTablesPath = "regex/tables.mk"

// rxSpan is an inclusive range of scalars (this file's own type, so a
// change to other tests' helpers cannot break these files).
type rxSpan struct{ lo, hi rune }

// rxCanon is Go's regexp/syntax canonicalName: separators _ - and space
// dropped, the first letter upper case and the rest lower case (ASCII).
func rxCanon(name string) string {
	var b []byte
	first := true
	for i := range len(name) {
		c := name[i]
		switch {
		case c == '_' || c == '-' || c == ' ':
			continue
		case first:
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			first = false
		default:
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
		}
		b = append(b, c)
	}
	return string(b)
}

// rxSet is a set of scalars, one bool each.
type rxSet []bool

func rxNewSet() rxSet { return make(rxSet, unicode.MaxRune+1) }

func (s rxSet) add(lo, hi rune) {
	for c := lo; c <= hi; c++ {
		s[c] = true
	}
}

func (s rxSet) union(t rxSet) rxSet {
	u := rxNewSet()
	for c := range u {
		u[c] = s[c] || t[c]
	}
	return u
}

func (s rxSet) not() rxSet {
	u := rxNewSet()
	for c := range u {
		u[c] = !s[c]
	}
	return u
}

// spans is s as ascending, merged, inclusive runs.
func (s rxSet) spans() []rxSpan {
	var out []rxSpan
	for c := 0; c < len(s); c++ {
		if !s[c] {
			continue
		}
		lo := c
		for c+1 < len(s) && s[c+1] {
			c++
		}
		out = append(out, rxSpan{rune(lo), rune(c)})
	}
	return out
}

func rxSetOf(t *unicode.RangeTable) rxSet {
	s := rxNewSet()
	for _, r := range t.R16 {
		for c := rune(r.Lo); c <= rune(r.Hi); c += rune(r.Stride) {
			s[c] = true
		}
	}
	for _, r := range t.R32 {
		for c := rune(r.Lo); c <= rune(r.Hi); c += rune(r.Stride) {
			s[c] = true
		}
	}
	return s
}

// rxTables is every name \p{..} accepts, canonical, with its set, and the
// simple fold orbits as each scalar's next (itself when alone).
type rxTables struct {
	version string
	names   map[string]rxSet
	next    []rune
}

// rxNamer puts names in Go's unicodeTable order of precedence: the
// special names, the categories and scripts under their canonical keys,
// the category aliases, then every script by its canonical name.
type rxNamer struct {
	names map[string]rxSet
}

func (n *rxNamer) put(name string, s rxSet) {
	if _, ok := n.names[name]; !ok {
		n.names[name] = s
	}
}

func (n *rxNamer) build(cats, scripts map[string]rxSet, aliases map[string]string) {
	all := rxNewSet()
	all.add(0, unicode.MaxRune)
	ascii := rxNewSet()
	ascii.add(0, 0x7F)
	n.put("Any", all)
	n.put("Assigned", cats["Cn"].not())
	n.put("Ascii", ascii)
	n.put("Lc", cats["LC"])
	for _, m := range []map[string]rxSet{cats, scripts} {
		for _, k := range slices.Sorted(rxKeys(m)) {
			if rxCanon(k) == k {
				n.put(k, m[k])
			}
		}
	}
	for _, k := range slices.Sorted(rxKeys(aliases)) {
		n.put(rxCanon(k), cats[aliases[k]])
	}
	for _, k := range slices.Sorted(rxKeys(scripts)) {
		n.put(rxCanon(k), scripts[k])
	}
}

func rxKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// rxGoTables is the same tables from Go's unicode package: the model's.
func rxGoTables() *rxTables {
	cats := map[string]rxSet{}
	for k, t := range unicode.Categories {
		cats[k] = rxSetOf(t)
	}
	scripts := map[string]rxSet{}
	for k, t := range unicode.Scripts {
		scripts[k] = rxSetOf(t)
	}
	n := &rxNamer{names: map[string]rxSet{}}
	n.build(cats, scripts, unicode.CategoryAliases)
	next := make([]rune, unicode.MaxRune+1)
	for c := range next {
		next[c] = unicode.SimpleFold(rune(c))
	}
	return &rxTables{version: unicode.Version, names: n.names, next: next}
}

// rxUcdDir is $MK_EVIDENCE/ucd, else Mk-evidence/ucd beside the repository.
func rxUcdDir() string {
	if d := os.Getenv("MK_EVIDENCE"); d != "" {
		return filepath.Join(d, "ucd")
	}
	return filepath.Join("..", "..", "Mk-evidence", "ucd")
}

// rxUcdLines calls f with the semicolon-separated, trimmed fields of every
// data line of file, comments dropped, and returns the version its first
// line names (# Name-18.0.0.txt).
func rxUcdLines(dir, file string, f func(fields []string) error) (string, error) {
	fh, err := os.Open(filepath.Join(dir, file))
	if err != nil {
		return "", err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	version := ""
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if line == 1 && strings.HasPrefix(text, "# ") {
			name := strings.TrimSuffix(strings.TrimPrefix(text, "# "), ".txt")
			if i := strings.LastIndexByte(name, '-'); i >= 0 {
				version = name[i+1:]
			}
		}
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields := strings.Split(text, ";")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if err := f(fields); err != nil {
			return "", fmt.Errorf("%s:%d: %v", file, line, err)
		}
	}
	return version, sc.Err()
}

func rxHex(s string) (rune, error) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || v > unicode.MaxRune {
		return 0, fmt.Errorf("bad scalar %q", s)
	}
	return rune(v), nil
}

func rxHexRange(s string) (rune, rune, error) {
	a, b, ok := strings.Cut(s, "..")
	lo, err := rxHex(a)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return lo, lo, nil
	}
	hi, err := rxHex(b)
	return lo, hi, err
}

// rxUcdTables reads the tables from the UCD files in dir: UnicodeData.txt
// (general categories; a <..., First> and <..., Last> pair is a range;
// a scalar not listed is Cn), Scripts.txt, CaseFolding.txt (statuses C
// and S: simple case folding) and PropertyValueAliases.txt (gc aliases).
func rxUcdTables(dir string) (*rxTables, error) {
	gc := make([]string, unicode.MaxRune+1)
	for c := range gc {
		gc[c] = "Cn"
	}
	first := rune(-1)
	vData, err := rxUcdLines(dir, "UnicodeData.txt", func(f []string) error {
		if len(f) < 3 {
			return fmt.Errorf("short line")
		}
		c, err := rxHex(f[0])
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(f[1], ", First>"):
			first = c
			return nil
		case strings.HasSuffix(f[1], ", Last>"):
			if first < 0 {
				return fmt.Errorf("Last without First")
			}
			for x := first; x <= c; x++ {
				gc[x] = f[2]
			}
			first = -1
			return nil
		}
		gc[c] = f[2]
		return nil
	})
	if err != nil {
		return nil, err
	}
	cats := map[string]rxSet{}
	for c, g := range gc {
		for _, k := range []string{g, g[:1]} {
			if cats[k] == nil {
				cats[k] = rxNewSet()
			}
			cats[k][c] = true
		}
		if g == "Lu" || g == "Ll" || g == "Lt" {
			if cats["LC"] == nil {
				cats["LC"] = rxNewSet()
			}
			cats["LC"][c] = true
		}
	}
	scripts := map[string]rxSet{}
	vScripts, err := rxUcdLines(dir, "Scripts.txt", func(f []string) error {
		if len(f) < 2 {
			return fmt.Errorf("short line")
		}
		lo, hi, err := rxHexRange(f[0])
		if err != nil {
			return err
		}
		if scripts[f[1]] == nil {
			scripts[f[1]] = rxNewSet()
		}
		scripts[f[1]].add(lo, hi)
		return nil
	})
	if err != nil {
		return nil, err
	}
	aliases := map[string]string{}
	vAliases, err := rxUcdLines(dir, "PropertyValueAliases.txt", func(f []string) error {
		if f[0] != "gc" {
			return nil
		}
		if len(f) < 3 {
			return fmt.Errorf("short gc line")
		}
		for _, a := range f[2:] {
			if a != "" {
				aliases[a] = f[1]
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	fold := map[rune]rune{}
	vFold, err := rxUcdLines(dir, "CaseFolding.txt", func(f []string) error {
		if len(f) < 3 || f[1] != "C" && f[1] != "S" {
			return nil
		}
		c, err := rxHex(f[0])
		if err != nil {
			return err
		}
		to, err := rxHex(f[2])
		if err != nil {
			return err
		}
		fold[c] = to
		return nil
	})
	if err != nil {
		return nil, err
	}
	// UnicodeData.txt names no version; ReadMe.txt names the directory's.
	readme, err := os.ReadFile(filepath.Join(dir, "ReadMe.txt"))
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(readme), "data files for version "+vScripts+" of") {
		return nil, fmt.Errorf("ReadMe.txt does not name version %q", vScripts)
	}
	vData = vScripts
	if vData == "" || vData != vScripts || vData != vAliases || vData != vFold {
		return nil, fmt.Errorf("UCD files of different versions: UnicodeData %q, Scripts %q, PropertyValueAliases %q, CaseFolding %q", vData, vScripts, vAliases, vFold)
	}
	// An orbit is every scalar folding to one target, with the target.
	orbits := map[rune][]rune{}
	for c, to := range fold {
		orbits[to] = append(orbits[to], c)
	}
	next := make([]rune, unicode.MaxRune+1)
	for c := range next {
		next[c] = rune(c)
	}
	for to, cs := range orbits {
		o := append(cs, to)
		slices.Sort(o)
		o = slices.Compact(o)
		for i, c := range o {
			next[c] = o[(i+1)%len(o)]
		}
	}
	n := &rxNamer{names: map[string]rxSet{}}
	n.build(cats, scripts, aliases)
	return &rxTables{version: vData, names: n.names, next: next}, nil
}

var rxUcdCache struct {
	t   *rxTables
	err error
	ok  bool
}

// rxUcd is the UCD tables, read once; the test is skipped without them.
func rxUcd(t *testing.T) *rxTables {
	t.Helper()
	if !rxUcdCache.ok {
		rxUcdCache.ok = true
		dir := rxUcdDir()
		if _, err := os.Stat(filepath.Join(dir, "UnicodeData.txt")); err != nil {
			rxUcdCache.err = fmt.Errorf("UCD files absent: set MK_EVIDENCE to a directory holding ucd/ (UCD 18.0.0), or put Mk-evidence/ucd beside the repository")
		} else {
			rxUcdCache.t, rxUcdCache.err = rxUcdTables(dir)
		}
	}
	if rxUcdCache.err != nil {
		if rxUcdCache.t == nil && strings.HasPrefix(rxUcdCache.err.Error(), "UCD files absent") {
			t.Skip(rxUcdCache.err)
		}
		t.Fatal(rxUcdCache.err)
	}
	return rxUcdCache.t
}

// rxWriteU32s writes a const static slice of u32, 12 numbers a line.
func rxWriteU32s(b *strings.Builder, name string, xs []uint32) {
	fmt.Fprintf(b, "const %s: []u32 = [\n", name)
	for i := 0; i < len(xs); i += 12 {
		b.WriteString("   ")
		for _, x := range xs[i:min(i+12, len(xs))] {
			fmt.Fprintf(b, " 0x%X,", x)
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n\n")
}

// genRegexTables returns the text of regex/tables.mk for tabs.
func genRegexTables(tabs *rxTables) string {
	names := slices.Sorted(rxKeys(tabs.names))
	var b strings.Builder
	b.WriteString("// Code generated by std/std-regex_tables_test.go from the Unicode Character Database. DO NOT EDIT.\n")
	b.WriteString("// The names \\p{..} accepts (Go's regexp/syntax canonical names), their scalar ranges, and the\n")
	b.WriteString("// simple case-fold orbits (?i) reads. UCD data Copyright Unicode, Inc., under the Unicode License v3.\n")
	b.WriteString("// Regenerate with go test ./std -run TestSTDREGEX_TablesUpToDate -update (UCD files in $MK_EVIDENCE/ucd).\n")
	fmt.Fprintf(&b, "// UCD version: %s\n\n", tabs.version)
	fmt.Fprintf(&b, "const UnicodeVersion: str = %q\n\n", tabs.version)
	b.WriteString("// UcdNames are the canonical names, sorted bytewise. Table i is the\n")
	b.WriteString("// ranges UcdRanges[2 * k], UcdRanges[2 * k + 1] for k from UcdStart[i]\n")
	b.WriteString("// to UcdStart[i] + UcdCount[i] - 1, inclusive and ascending; an alias\n")
	b.WriteString("// shares its table's ranges.\n")
	b.WriteString("const UcdNames: []str = [\n")
	for _, n := range names {
		fmt.Fprintf(&b, "    %q,\n", n)
	}
	b.WriteString("]\n\n")
	b.WriteString("// UcdLetter and UcdDigit index UcdNames at L and Nd, the sets a Replace\n")
	b.WriteString("// template's reference name is read with (Go's unicode.IsLetter and\n")
	b.WriteString("// unicode.IsDigit).\n")
	fmt.Fprintf(&b, "const UcdLetter: int = %d\n", slices.Index(names, "L"))
	fmt.Fprintf(&b, "const UcdDigit: int = %d\n\n", slices.Index(names, "Nd"))
	var starts, counts, flat []uint32
	seen := map[string]uint32{}
	for _, n := range names {
		spans := tabs.names[n].spans()
		var key strings.Builder
		for _, r := range spans {
			fmt.Fprintf(&key, "%x-%x,", r.lo, r.hi)
		}
		at, ok := seen[key.String()]
		if !ok {
			at = uint32(len(flat) / 2)
			seen[key.String()] = at
			for _, r := range spans {
				flat = append(flat, uint32(r.lo), uint32(r.hi))
			}
		}
		starts = append(starts, at)
		counts = append(counts, uint32(len(spans)))
	}
	rxWriteU32s(&b, "UcdStart", starts)
	rxWriteU32s(&b, "UcdCount", counts)
	rxWriteU32s(&b, "UcdRanges", flat)
	var keys, next []uint32
	for c, f := range tabs.next {
		if f != rune(c) {
			keys = append(keys, uint32(c))
			next = append(next, uint32(f))
		}
	}
	b.WriteString("// FoldKeys are the scalars whose simple case-fold orbit holds more than\n")
	b.WriteString("// themselves, ascending; FoldNext[i] is the next scalar of FoldKeys[i]'s\n")
	b.WriteString("// orbit: the least one above it, else the least.\n")
	rxWriteU32s(&b, "FoldKeys", keys)
	rxWriteU32s(&b, "FoldNext", next)
	return strings.TrimSuffix(b.String(), "\n")
}

// TestSTDREGEX_TablesUpToDate generates regex/tables.mk from the UCD
// files and requires the committed file to equal it; -update rewrites it.
//
// The tables are the general categories, scripts and simple case-fold
// orbits, taken from the UCD files of one version (18.0.0), with Go
// regexp/syntax's names: the special names Any, Assigned, ASCII and LC;
// the categories (a one-letter category is the union of its two-letter
// ones, Cn included, as Go's C); the scripts; the category aliases of
// PropertyValueAliases.txt; all keyed by Go's canonicalName. Go's regexp is
// the differential tests' model and its unicode package is another version
// (17.0.0 in Go 1.27), so TestSTDREGEX_UcdAgainstGo computes where the two
// disagree, by name and by fold orbit, and pins that list; the differential
// tests keep their inputs and literals off it (TestSTDREGEX_CorpusOffDiff).
func TestSTDREGEX_TablesUpToDate(t *testing.T) {
	tabs := rxUcd(t)
	want := genRegexTables(tabs)
	if *update {
		if err := os.WriteFile(rxTablesPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes, UCD %s, %d names)", rxTablesPath, len(want), tabs.version, len(tabs.names))
		return
	}
	got, err := os.ReadFile(rxTablesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is not what UCD %s gives; run go test ./std -run TestSTDREGEX_TablesUpToDate -update", rxTablesPath, tabs.version)
	}
}

// rxDiff is where the UCD tables and Go's disagree: the names only one
// accepts, and every scalar some name puts in one set and not the other,
// or whose fold orbit differs (both orbits' members).
func rxDiff(ucd, gox *rxTables) (onlyUcd, onlyGo []string, d rxSet) {
	d = rxNewSet()
	for _, n := range slices.Sorted(rxKeys(ucd.names)) {
		g, ok := gox.names[n]
		if !ok {
			onlyUcd = append(onlyUcd, n)
			continue
		}
		u := ucd.names[n]
		for c := range u {
			if u[c] != g[c] {
				d[c] = true
			}
		}
	}
	for _, n := range slices.Sorted(rxKeys(gox.names)) {
		if _, ok := ucd.names[n]; !ok {
			onlyGo = append(onlyGo, n)
		}
	}
	orbit := func(next []rune, c rune) []rune {
		o := []rune{c}
		for f := next[c]; f != c; f = next[f] {
			o = append(o, f)
		}
		return o
	}
	for c := range ucd.next {
		if ucd.next[c] != gox.next[c] {
			for _, x := range orbit(ucd.next, rune(c)) {
				d[x] = true
			}
			for _, x := range orbit(gox.next, rune(c)) {
				d[x] = true
			}
		}
	}
	return onlyUcd, onlyGo, d
}

// rxDiffSet is rxUcdGoDiff as a set, built once.
var rxDiffSet = func() rxSet {
	s := rxNewSet()
	for _, r := range rxUcdGoDiff {
		s.add(r.lo, r.hi)
	}
	return s
}()

// rxDiffLiteral prints the pin as Go source, to paste into
// std-regex_ucddiff_test.go when the UCD or Go's version moves.
func rxDiffLiteral(ucdV, goV string, onlyUcd, onlyGo []string, d rxSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "const rxUcdGoVersions = %q\n\n", ucdV+" "+goV)
	fmt.Fprintf(&b, "var rxUcdOnlyNames = %#v\n\n", onlyUcd)
	fmt.Fprintf(&b, "var rxGoOnlyNames = %#v\n\n", onlyGo)
	b.WriteString("var rxUcdGoDiff = []rxSpan{")
	for i, r := range d.spans() {
		if i%6 == 0 {
			b.WriteString("\n\t")
		} else {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "{0x%X, 0x%X},", r.lo, r.hi)
	}
	b.WriteString("\n}\n")
	return b.String()
}

// TestSTDREGEX_UcdAgainstGo computes where the UCD tables and Go's
// unicode package (the model's) disagree and requires the pin in
// std-regex_ucddiff_test.go to be exactly that (compare the scalars both
// versions agree on and pin the list of the rest).
func TestSTDREGEX_UcdAgainstGo(t *testing.T) {
	ucd := rxUcd(t)
	gox := rxGoTables()
	onlyUcd, onlyGo, d := rxDiff(ucd, gox)
	got := rxDiffLiteral(ucd.version, gox.version, onlyUcd, onlyGo, d)
	want := rxDiffLiteral(strings.Fields(rxUcdGoVersions)[0], strings.Fields(rxUcdGoVersions)[1], rxUcdOnlyNames, rxGoOnlyNames, rxDiffSet)
	n := 0
	for _, x := range d {
		if x {
			n++
		}
	}
	t.Logf("UCD %s against Go %s: %d names only in UCD, %d only in Go, %d scalars in %d ranges differ", ucd.version, gox.version, len(onlyUcd), len(onlyGo), n, len(d.spans()))
	if got != want {
		path := filepath.Join(os.TempDir(), "std-regex-ucddiff.txt")
		_ = os.WriteFile(path, []byte(got), 0o644)
		t.Fatalf("the pinned disagreement (std-regex_ucddiff_test.go) is not what the tables give; the computed pin is in %s", path)
	}
}

// TestSTDREGEX_CorpusOffDiff: no scalar of the differential tests' inputs
// and literals is one where the UCD tables and Go's disagree, so Go's
// regexp stays an exact model for them.
func TestSTDREGEX_CorpusOffDiff(t *testing.T) {
	var all []string
	all = append(all, rxInputAlphabet...)
	all = append(all, rxEdgeIns...)
	all = append(all, rxLits...)
	all = append(all, rxClasses...)
	all = append(all, rxEdgePats...)
	all = append(all, rxGenUnicodePats...)
	for _, s := range all {
		for _, c := range s {
			if rxDiffSet[c] {
				t.Errorf("%q holds U+%04X, where the UCD tables and Go's (%s) disagree", s, c, rxUcdGoVersions)
			}
		}
	}
	// Class ranges such as [α-ω] are folded scalar by scalar; a scalar of
	// a range whose orbit differs reaches only scalars of that orbit, all
	// in the pin, so the inputs' check above covers them.
}
