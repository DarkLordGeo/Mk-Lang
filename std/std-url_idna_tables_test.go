// Why: std/url/idna_tables.mk is generated from Unicode's IDNA data and must
// stay equal to what the data gives.
// What: TestStdUrl_IdnaTablesUpToDate (the generator; -update rewrites the
// file) and TestStdUrl_IdnaLookup.
// How: reads UTS 46's IdnaMappingTable.txt and the UCD's
// DerivedGeneralCategory.txt (18.0.0); the lookup checks every scalar.

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

// The values of std/url/idna_uts46.mk's IdnaValue.
const (
	urlIdnaValid      = 0 // valid
	urlIdnaMark       = 1 // valid, General_Category=Mark
	urlIdnaDeviation  = 2 // deviation (kept: transitional processing is off)
	urlIdnaIgnored    = 3
	urlIdnaDisallowed = 4
	urlIdnaMapped     = 5 // urlIdnaMapped + k: mapped to mapping k
)

// urlIdna is the generator's data: each code point's value, and the
// mappings' text in the order of their first code point.
type urlIdna struct {
	version string
	value   []int
	maps    []string
}

// urlIdnaFile is Mk-evidence/unicode/idna/name, or "" when there is none.
func urlIdnaFile(name string) string {
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	p := filepath.Join(root, "unicode", "idna", name)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func urlLoadIdna(t *testing.T) *urlIdna {
	t.Helper()
	table := urlIdnaFile("IdnaMappingTable.txt")
	ucd := urlUcdDir()
	if table == "" || ucd == "" {
		t.Skip("Mk-evidence/unicode/idna or Mk-evidence/ucd not found (set MK_EVIDENCE)")
	}
	mark := make([]bool, 0x110000)
	if _, err := urlReadRanges(filepath.Join(ucd, "extracted", "DerivedGeneralCategory.txt"), func(lo, hi int, value string) error {
		for c := lo; c <= hi; c++ {
			mark[c] = value == "Mn" || value == "Mc" || value == "Me"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(table)
	if err != nil {
		t.Fatal(err)
	}
	d := &urlIdna{value: make([]int, 0x110000)}
	set := make([]bool, 0x110000)
	ids := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "# Version: "); ok {
			d.version = strings.TrimSpace(v)
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, ";")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		los, his, ok := strings.Cut(f[0], "..")
		if !ok {
			his = los
		}
		lo, err1 := strconv.ParseUint(los, 16, 32)
		hi, err2 := strconv.ParseUint(his, 16, 32)
		if err1 != nil || err2 != nil {
			t.Fatalf("bad range %q", f[0])
		}
		var mapping string
		if len(f) > 2 && f[2] != "" {
			for _, h := range strings.Fields(f[2]) {
				c, err := strconv.ParseUint(h, 16, 32)
				if err != nil {
					t.Fatalf("bad mapping %q", f[2])
				}
				mapping += string(rune(c))
			}
		}
		for c := int(lo); c <= int(hi); c++ {
			var v int
			switch f[1] {
			case "valid":
				v = urlIdnaValid
				if mark[c] {
					v = urlIdnaMark
				}
			case "deviation":
				// Every deviation is a letter or a joiner, never a mark;
				// the generator checks it, since IdnaValue has no
				// "deviation, Mark" value.
				if mark[c] {
					t.Fatalf("U+%04X: a deviation that is a mark", c)
				}
				v = urlIdnaDeviation
			case "ignored":
				v = urlIdnaIgnored
			case "disallowed":
				v = urlIdnaDisallowed
			case "mapped":
				id, ok := ids[mapping]
				if !ok {
					id = len(d.maps)
					ids[mapping] = id
					d.maps = append(d.maps, mapping)
				}
				v = urlIdnaMapped + id
			default:
				t.Fatalf("U+%04X: unknown status %q", c, f[1])
			}
			d.value[c] = v
			set[c] = true
		}
	}
	for c := range set {
		if !set[c] {
			t.Fatalf("U+%04X: no row in IdnaMappingTable.txt", c)
		}
	}
	if urlIdnaMapped+len(d.maps) > 0xFFFF {
		t.Fatalf("%d mappings do not fit a u16 value", len(d.maps))
	}
	return d
}

// urlTrieBytes is a trie's size with dataWidth bytes a data entry.
func urlTrieBytes(tr urlTrie, dataWidth int) int {
	return len(tr.i1)*urlWidth(len(tr.i2)>>tr.s2) + len(tr.i2)*urlWidth(len(tr.data)>>tr.s3) + dataWidth*len(tr.data)
}

// urlBuildTrie16 is urlBuildTrie for two-byte data.
func urlBuildTrie16(v []int) urlTrie {
	var best urlTrie
	for s3 := 3; s3 <= 8; s3++ {
		data, ids := urlBlocks(v, s3)
		for s2 := 2; s2 <= 8; s2++ {
			if len(ids)%(1<<s2) != 0 {
				continue
			}
			i2, i1 := urlBlocks(ids, s2)
			tr := urlTrie{s2: s2, s3: s3, i1: i1, i2: i2, data: data}
			if best.data == nil || urlTrieBytes(tr, 2) < urlTrieBytes(best, 2) {
				best = tr
			}
		}
	}
	return best
}

// urlIdnaPool is the mappings' UTF-8 bytes one after another, and where
// each ends.
func urlIdnaPool(d *urlIdna) (pool []int, ends []int) {
	ends = append(ends, 0)
	for _, m := range d.maps {
		for _, b := range []byte(m) {
			pool = append(pool, int(b))
		}
		ends = append(ends, len(pool))
	}
	return pool, ends
}

// urlGenIdnaTables returns the text of std/url/idna_tables.mk.
func urlGenIdnaTables(t *testing.T, d *urlIdna) string {
	t.Helper()
	tr := urlBuildTrie16(d.value)
	pool, ends := urlIdnaPool(d)
	if len(pool) > 0xFFFF {
		t.Fatalf("mapping pool of %d bytes does not fit u16 offsets", len(pool))
	}
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by std/std-url_idna_tables_test.go from Unicode data. DO NOT EDIT.
// The UTS 46 IDNA mapping table that idna_uts46.mk's IdnaValue and IdnaMap read, from
// IdnaMappingTable.txt and DerivedGeneralCategory.txt (Copyright Unicode, Inc., Unicode License v3).
// Regenerate with go test ./std -run TestStdUrl_IdnaTablesUpToDate -update (files in $MK_EVIDENCE).
// IDNA mapping table version: %s

`, d.version)
	b.WriteString("// IdnaData values: 0 valid, 1 valid and General_Category=Mark, 2\n// deviation, 3 ignored, 4 disallowed, 5 + k mapped to mapping k.\n// IdnaIndex1[c >> (IdnaShift2 + IdnaShift3)] is a block of IdnaIndex2, whose\n// entry (c >> IdnaShift3) & (2^IdnaShift2 - 1) is a block of IdnaData.\n")
	fmt.Fprintf(&b, "const IdnaShift2: u32 = %d\nconst IdnaShift3: u32 = %d\n\n", tr.s2, tr.s3)
	urlWriteArray(&b, "IdnaIndex1", urlElemType(len(tr.i2)>>tr.s2), tr.i1, false)
	urlWriteArray(&b, "IdnaIndex2", urlElemType(len(tr.data)>>tr.s3), tr.i2, false)
	urlWriteArray(&b, "IdnaData", "u16", tr.data, false)
	b.WriteString("// Mapping k is IdnaMapBytes[IdnaMapEnd[k]..IdnaMapEnd[k + 1]], UTF-8.\n")
	urlWriteArray(&b, "IdnaMapEnd", "u16", ends, false)
	// A slice, not an array, so IdnaMapping's slices of it are static.
	var pb strings.Builder
	urlWriteArray(&pb, "IdnaMapBytes", "u8", pool, true)
	b.WriteString(strings.Replace(pb.String(), fmt.Sprintf(": [%d]u8 = [", len(pool)), ": []u8 = [", 1))
	return strings.TrimRight(b.String(), "\n") + "\n"
}

const urlIdnaTablesPath = "url/idna_tables.mk"

// TestStdUrl_IdnaTablesUpToDate: std/url/idna_tables.mk is what
// IdnaMappingTable.txt gives; -update rewrites it.
func TestStdUrl_IdnaTablesUpToDate(t *testing.T) {
	d := urlLoadIdna(t)
	want := urlGenIdnaTables(t, d)
	tr := urlBuildTrie16(d.value)
	pool, ends := urlIdnaPool(d)
	t.Logf("IDNA %s: shifts %d and %d, IdnaIndex1 %d entries, IdnaIndex2 %d, IdnaData %d, %d mappings in %d bytes: %d bytes", d.version, tr.s2, tr.s3, len(tr.i1), len(tr.i2), len(tr.data), len(d.maps), len(pool), urlTrieBytes(tr, 2)+2*len(ends)+len(pool))
	if *update {
		if err := os.WriteFile(urlIdnaTablesPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", urlIdnaTablesPath, len(want))
		return
	}
	got, err := os.ReadFile(urlIdnaTablesPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Fatalf("%s is not what IdnaMappingTable.txt %s gives; run go test ./std -run TestStdUrl_IdnaTablesUpToDate -update", urlIdnaTablesPath, d.version)
	}
}

// TestStdUrl_IdnaLookup: idna_uts46.mk's IdnaValue for every scalar value,
// and every mapping's text, in the driver, against IdnaMappingTable.txt.
func TestStdUrl_IdnaLookup(t *testing.T) {
	d := urlLoadIdna(t)
	var records []string
	var starts []int
	const chunk = 0x8000
	for lo := 0; lo < 0x110000; lo += chunk {
		records = append(records, fmt.Sprintf("idnavalues %d %d", lo, chunk))
		starts = append(starts, lo)
	}
	records = append(records, fmt.Sprintf("idnamaps %d", len(d.maps)))
	answers := urlIdnaAsk(t, "release", records)
	bad := 0
	for i, a := range answers[:len(starts)] {
		got, ok := strings.CutPrefix(a, "ok ")
		if !ok || len(got) != 4*chunk {
			t.Fatalf("idnavalues %d: %s", starts[i], urlShortText(a))
		}
		for k := 0; k < chunk; k++ {
			c := starts[i] + k
			if c >= 0xD800 && c <= 0xDFFF {
				continue
			}
			if want := fmt.Sprintf("%04x", d.value[c]); got[4*k:4*k+4] != want {
				t.Errorf("U+%04X: got %s, want %s", c, got[4*k:4*k+4], want)
				bad++
				if bad == 10 {
					t.FailNow()
				}
			}
		}
	}
	got, ok := strings.CutPrefix(answers[len(starts)], "ok ")
	if !ok {
		t.Fatalf("idnamaps: %s", urlShortText(answers[len(starts)]))
	}
	maps := strings.Split(got, ",")
	if len(maps) != len(d.maps) {
		t.Fatalf("idnamaps: %d mappings, want %d", len(maps), len(d.maps))
	}
	for k, h := range maps {
		if m := urlUnhex(t, h); m != d.maps[k] {
			t.Errorf("mapping %d: got %q, want %q", k, m, d.maps[k])
		}
	}
}
