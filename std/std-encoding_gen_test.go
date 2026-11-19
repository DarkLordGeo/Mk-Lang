// Why: std.encoding's tables come from the WHATWG Encoding Standard and must stay equal to it.
// What: the generator of std/encoding/single_tables.mk (every legacy single-byte index, forward and
// reversed) and std/encoding/labels.mk (every label of encodings.json, sorted, with its encoding).
// How: reads $MK_EVIDENCE/whatwg/encoding (or Mk-evidence beside the repository); go test ./std -run
// TestStdEncoding_TablesCurrent compares the checked-in files, and -args -encoding.gen writes them.

package std

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	encGen = flag.Bool("encoding.gen", false, "write std/encoding's generated tables from Mk-evidence")
	encOut = flag.String("encoding.out", "encoding", "the directory -encoding.gen writes")
)

// encEvidence is $MK_EVIDENCE/whatwg/encoding, else Mk-evidence beside the
// repository; "" when neither exists.
func encEvidence() string {
	ev := os.Getenv("MK_EVIDENCE")
	if ev == "" {
		ev = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(ev, "whatwg", "encoding")
	if _, err := os.Stat(filepath.Join(dir, "encodings.json")); err != nil {
		return ""
	}
	return dir
}

// encSpec is one encoding std.encoding builds: its WHATWG name, the
// constant naming it, its id (the index of Names in labels.mk) and, for a
// single-byte encoding, its index file.
type encSpec struct {
	name, ident string
	id          int
	index       string
}

// encSpecs lists the encodings in id order. Ids 0 to 4 have no table; 5 on
// are the single-byte encodings in encodings.json's order.
var encSpecs = []encSpec{
	{"UTF-8", "Utf8", 0, ""},
	{"UTF-16LE", "Utf16Le", 1, ""},
	{"UTF-16BE", "Utf16Be", 2, ""},
	{"replacement", "Replacement", 3, ""},
	{"x-user-defined", "XUserDefined", 4, ""},
	{"IBM866", "Ibm866", 5, "ibm866"},
	{"ISO-8859-2", "Iso88592", 6, "iso-8859-2"},
	{"ISO-8859-3", "Iso88593", 7, "iso-8859-3"},
	{"ISO-8859-4", "Iso88594", 8, "iso-8859-4"},
	{"ISO-8859-5", "Iso88595", 9, "iso-8859-5"},
	{"ISO-8859-6", "Iso88596", 10, "iso-8859-6"},
	{"ISO-8859-7", "Iso88597", 11, "iso-8859-7"},
	{"ISO-8859-8", "Iso88598", 12, "iso-8859-8"},
	{"ISO-8859-8-I", "Iso88598I", 13, "iso-8859-8"},
	{"ISO-8859-10", "Iso885910", 14, "iso-8859-10"},
	{"ISO-8859-13", "Iso885913", 15, "iso-8859-13"},
	{"ISO-8859-14", "Iso885914", 16, "iso-8859-14"},
	{"ISO-8859-15", "Iso885915", 17, "iso-8859-15"},
	{"ISO-8859-16", "Iso885916", 18, "iso-8859-16"},
	{"KOI8-R", "Koi8R", 19, "koi8-r"},
	{"KOI8-U", "Koi8U", 20, "koi8-u"},
	{"macintosh", "Macintosh", 21, "macintosh"},
	{"windows-874", "Windows874", 22, "windows-874"},
	{"windows-1250", "Windows1250", 23, "windows-1250"},
	{"windows-1251", "Windows1251", 24, "windows-1251"},
	{"windows-1252", "Windows1252", 25, "windows-1252"},
	{"windows-1253", "Windows1253", 26, "windows-1253"},
	{"windows-1254", "Windows1254", 27, "windows-1254"},
	{"windows-1255", "Windows1255", 28, "windows-1255"},
	{"windows-1256", "Windows1256", 29, "windows-1256"},
	{"windows-1257", "Windows1257", 30, "windows-1257"},
	{"windows-1258", "Windows1258", 31, "windows-1258"},
	{"x-mac-cyrillic", "XMacCyrillic", 32, "x-mac-cyrillic"},
}

// encUnsupported is the id Lookup reads as a label of an encoding this
// module does not build (the multi-byte CJK set, std.encoding.cjk's).
const encUnsupported = 255

// encIndex reads index-<name>.txt: pointer -> code point, 128 slots, 0 for
// a pointer the index leaves out.
func encIndex(dir, name string) ([128]uint16, error) {
	var t [128]uint16
	f, err := os.Open(filepath.Join(dir, "index-"+name+".txt"))
	if err != nil {
		return t, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		p, err1 := strconv.Atoi(fs[0])
		cp, err2 := strconv.ParseUint(strings.TrimPrefix(fs[1], "0x"), 16, 32)
		if err1 != nil || err2 != nil || p < 0 || p >= 128 || cp == 0 || cp > 0xFFFF {
			return t, fmt.Errorf("index-%s.txt: bad line %q", name, line)
		}
		t[p] = uint16(cp)
	}
	return t, sc.Err()
}

// encLabels reads encodings.json: label -> id (encUnsupported for the
// encodings this module does not build).
func encLabels(dir string) (map[string]int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "encodings.json"))
	if err != nil {
		return nil, err
	}
	var groups []struct {
		Heading   string `json:"heading"`
		Encodings []struct {
			Name   string   `json:"name"`
			Labels []string `json:"labels"`
		} `json:"encodings"`
	}
	if err := json.Unmarshal(b, &groups); err != nil {
		return nil, err
	}
	byName := map[string]int{}
	for _, s := range encSpecs {
		byName[s.name] = s.id
	}
	out := map[string]int{}
	for _, g := range groups {
		for _, e := range g.Encodings {
			id, ok := byName[e.Name]
			if !ok {
				if !strings.Contains(g.Heading, "multi-byte") {
					return nil, fmt.Errorf("encoding %q of %q is neither built nor multi-byte", e.Name, g.Heading)
				}
				id = encUnsupported
			}
			for _, l := range e.Labels {
				if _, dup := out[l]; dup {
					return nil, fmt.Errorf("label %q twice", l)
				}
				out[l] = id
			}
		}
	}
	return out, nil
}

// encGenerate returns the text of single_tables.mk and labels.mk.
func encGenerate(dir string) (string, string, error) {
	var tb strings.Builder
	tb.WriteString(`// Code generated by std/std-encoding_gen_test.go from the WHATWG Encoding Standard. DO NOT EDIT.
// The tables of the legacy single-byte encodings, from the standard's index files (whatwg/encoding
// commit be17ac5c49881336538acfc7b40dba3c8009144b; Copyright WHATWG, under CC BY 4.0).

// Each legacy single-byte encoding is a named constant whose Encoding holds a
// function returning its tables, so a program links only the tables of the
// encodings it names. Map is the code point of bytes 0x80 to 0xFF (0 where
// the index has none); Rev is every mapped (code point << 8 | byte), sorted,
// for Encode's binary search.
`)
	done := map[string]bool{}
	for _, s := range encSpecs {
		if s.index == "" || done[s.index] {
			continue
		}
		done[s.index] = true
		t, err := encIndex(dir, s.index)
		if err != nil {
			return "", "", err
		}
		base := encTableName(s.index)
		var rev []uint32
		for p, cp := range t {
			if cp != 0 {
				rev = append(rev, uint32(cp)<<8|uint32(0x80+p))
			}
		}
		sort.Slice(rev, func(i, j int) bool { return rev[i] < rev[j] })
		fmt.Fprintf(&tb, "\nconst %sMap: []u16 = [\n", base)
		for i := 0; i < 128; i += 8 {
			tb.WriteString("    ")
			for k := i; k < i+8; k++ {
				fmt.Fprintf(&tb, "0x%04X", t[k])
				if k < i+7 {
					tb.WriteString(", ")
				} else if k < 127 {
					tb.WriteString(",")
				}
			}
			tb.WriteString("\n")
		}
		tb.WriteString("]\n")
		fmt.Fprintf(&tb, "\nconst %sRev: []u32 = [\n", base)
		for i := 0; i < len(rev); i += 6 {
			tb.WriteString("    ")
			for k := i; k < i+6 && k < len(rev); k++ {
				fmt.Fprintf(&tb, "0x%06X", rev[k])
				if k < i+5 && k < len(rev)-1 {
					tb.WriteString(", ")
				} else if k < len(rev)-1 {
					tb.WriteString(",")
				}
			}
			tb.WriteString("\n")
		}
		tb.WriteString("]\n")
		fmt.Fprintf(&tb, "\n%sTable(): []u16 {\n    return %sMap\n}\n", base, base)
		fmt.Fprintf(&tb, "\n%sBack(): []u32 {\n    return %sRev\n}\n", base, base)
	}
	for _, s := range encSpecs {
		if s.index == "" {
			continue
		}
		base := encTableName(s.index)
		fmt.Fprintf(&tb, "\n/// %s is the WHATWG %s encoding.\nexport const %s: Encoding = Encoding(id: %d, table: %sTable, back: %sBack, run: RunSingle, put: PutSingle, codec: NoCodec)\n", s.ident, s.name, s.ident, s.id, base, base)
	}

	labels, err := encLabels(dir)
	if err != nil {
		return "", "", err
	}
	keys := make([]string, 0, len(labels))
	longest := 0
	for k := range labels {
		keys = append(keys, k)
		longest = max(longest, len(k))
	}
	sort.Strings(keys)
	var lb strings.Builder
	lb.WriteString(`// Code generated by std/std-encoding_gen_test.go from the WHATWG Encoding Standard. DO NOT EDIT.
// The encoding labels that Lookup reads, from the standard's encodings.json (whatwg/encoding
// commit be17ac5c49881336538acfc7b40dba3c8009144b; Copyright WHATWG, under CC BY 4.0).

// Labels is every label of the standard, sorted by byte, and LabelIds the id
// of the encoding each names (Names' index; 255 for the multi-byte CJK
// encodings, which std.encoding.cjk builds).
`)
	fmt.Fprintf(&lb, "\n// LongestLabel is the length of the longest label.\nconst LongestLabel: int = %d\n", longest)
	lb.WriteString("\nconst Labels: []str = [\n")
	for i, k := range keys {
		sep := ","
		if i == len(keys)-1 {
			sep = ""
		}
		fmt.Fprintf(&lb, "    %q%s\n", k, sep)
	}
	lb.WriteString("]\n\nconst LabelIds: []u8 = [\n")
	for i := 0; i < len(keys); i += 12 {
		lb.WriteString("    ")
		for k := i; k < i+12 && k < len(keys); k++ {
			fmt.Fprintf(&lb, "%d", labels[keys[k]])
			if k < i+11 && k < len(keys)-1 {
				lb.WriteString(", ")
			} else if k < len(keys)-1 {
				lb.WriteString(",")
			}
		}
		lb.WriteString("\n")
	}
	lb.WriteString("]\n\n// Names is the WHATWG name of each encoding, by id.\nconst Names: []str = [\n")
	for i, s := range encSpecs {
		sep := ","
		if i == len(encSpecs)-1 {
			sep = ""
		}
		fmt.Fprintf(&lb, "    %q%s\n", s.name, sep)
	}
	lb.WriteString("]\n\n// ById is each encoding, by id; naming it links every table.\nconst ById: []Encoding = [\n")
	for i, s := range encSpecs {
		sep := ","
		if i == len(encSpecs)-1 {
			sep = ""
		}
		fmt.Fprintf(&lb, "    %s%s\n", s.ident, sep)
	}
	lb.WriteString("]\n")
	return tb.String(), lb.String(), nil
}

// encTableName is the Mk name of an index's tables: "iso-8859-2" ->
// "Iso88592", "x-mac-cyrillic" -> "XMacCyrillic".
func encTableName(index string) string {
	for _, s := range encSpecs {
		if s.index == index {
			return s.ident
		}
	}
	panic(index)
}

func TestStdEncoding_TablesCurrent(t *testing.T) {
	dir := encEvidence()
	if dir == "" {
		t.Skip("Mk-evidence/whatwg/encoding not found (set MK_EVIDENCE)")
	}
	tables, labels, err := encGenerate(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join("encoding", "single_tables.mk"): tables,
		filepath.Join("encoding", "labels.mk"):        labels,
	}
	for path, want := range files {
		if *encGen {
			if err := os.WriteFile(filepath.Join(*encOut, filepath.Base(path)), []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("%s differs from what the evidence generates: run go test ./std -run TestStdEncoding_TablesCurrent -args -encoding.gen", path)
		}
	}
}
