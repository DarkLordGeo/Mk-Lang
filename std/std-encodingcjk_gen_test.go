// Why: std/encoding/cjk/tables.mk is generated from the WHATWG indexes and must stay equal to them.
// What: the generator of the CJK indexes (jis0208, jis0212, EUC-KR, gb18030, Big5, ISO-2022-JP katakana),
// forward and reversed per encoder, the encoders' inline side tables, and the multi-byte labels.
// How: reads $MK_EVIDENCE/whatwg/encoding, each file checked against its SHA-256 and against the WPT
// copies; TestStdEncodingCjk_TablesCurrent compares, -args -encoding.gen writes (-encoding.cjkout names the directory).

package std

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var encCjkOut = flag.String("encoding.cjkout", filepath.Join("encoding", "cjk"), "the directory -encoding.gen writes std/encoding/cjk's table to")

// cjkPins is the SHA-256 of each evidence file the generator reads: the
// index files and encoding.bs of whatwg/encoding commit
// be17ac5c49881336538acfc7b40dba3c8009144b, the commit of the
// single-byte indexes and encodings.json beside them.
var cjkPins = map[string]string{
	"index-big5.txt":                 "08e24270c8e95d998c994c03f907e972480dc01f58743e078654cc466203c8ff",
	"index-euc-kr.txt":               "89af20dd867c84cefb710b1790229786cfef2bf11916361a210d81b90381e267",
	"index-gb18030-ranges.txt":       "874c6b6f6f74cf7d427ad228d5b41ddd9354fffd92a2259bf429f86e6baa7a1e",
	"index-gb18030.txt":              "746b3c55f1a8ec4b90b451f384437a17fd37cd51cd668456a28071a758d10784",
	"index-iso-2022-jp-katakana.txt": "185c3c20c896eca514cde0aabf570523d3ae3624d0a3d39f71455f2d3ff0e207",
	"index-jis0208.txt":              "341dcde7e8b984e9c7bbf5ed75c8da7c6087d47083a1a2b3ed558bfd5bef9468",
	"index-jis0212.txt":              "6ac698a473822b282ff4f4d4f17873b970612ef9bd232e03941854c09b67c7ee",
	"encoding.bs":                    "ba15016affa714efc01daeeedadfb09c73f63eeb3d0378b4df7cfed3a7560f5b",
}

// cjkEvidence is encEvidence() when it holds every file of cjkPins, else
// "" and the first file missing.
func cjkEvidence() (string, string) {
	dir := encEvidence()
	if dir == "" {
		return "", "encodings.json"
	}
	names := make([]string, 0, len(cjkPins))
	for n := range cjkPins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			return "", n
		}
	}
	return dir, ""
}

// cjkDir is cjkEvidence's directory, or skips t naming the missing file.
func cjkDir(t testing.TB) string {
	t.Helper()
	dir, missing := cjkEvidence()
	if dir == "" {
		t.Skipf("Mk-evidence/whatwg/encoding/%s not found (set MK_EVIDENCE; the CJK indexes of whatwg/encoding be17ac5c)", missing)
	}
	return dir
}

// cjkRead is the evidence file name, checked against its pin.
func cjkRead(dir, name string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != cjkPins[name] {
		return nil, fmt.Errorf("%s: SHA-256 %s, want %s", name, got, cjkPins[name])
	}
	return b, nil
}

// cjkIndex reads index-<name>.txt: code point by pointer, -1 for a pointer
// the index leaves out. Lines split on whitespace, as the standard's TAB
// with the files' leading spaces; "#" lines and empty lines are skipped.
func cjkIndex(dir, name string) ([]int32, error) {
	b, err := cjkRead(dir, "index-"+name+".txt")
	if err != nil {
		return nil, err
	}
	var idx []int32
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		p, err1 := strconv.Atoi(fs[0])
		cp, err2 := strconv.ParseUint(strings.TrimPrefix(fs[1], "0x"), 16, 32)
		if err1 != nil || err2 != nil || p < 0 || p > 2000000 || cp == 0 || cp > 0x10FFFF {
			return nil, fmt.Errorf("index-%s.txt: bad line %q", name, line)
		}
		for len(idx) <= p {
			idx = append(idx, -1)
		}
		if idx[p] != -1 {
			return nil, fmt.Errorf("index-%s.txt: pointer %d twice", name, p)
		}
		idx[p] = int32(cp)
	}
	return idx, nil
}

// cjkIndexes is every index the module reads, by its file name's part.
type cjkIndexes struct {
	jis0208, jis0212, euckr, gb18030, big5, katakana []int32
	// ranges is index gb18030 ranges: pointer and code point, in order.
	ranges [][2]int32
	// side is gb18030's encoder table: code point to two bytes.
	side [][2]int32
	// pairs is Big5's four pointers that decode to two code points.
	pairs [][3]int32
}

func cjkLoad(dir string) (*cjkIndexes, error) {
	var x cjkIndexes
	var err error
	for _, f := range []struct {
		name string
		dst  *[]int32
	}{
		{"jis0208", &x.jis0208}, {"jis0212", &x.jis0212}, {"euc-kr", &x.euckr},
		{"gb18030", &x.gb18030}, {"big5", &x.big5}, {"iso-2022-jp-katakana", &x.katakana},
	} {
		if *f.dst, err = cjkIndex(dir, f.name); err != nil {
			return nil, err
		}
	}
	r, err := cjkIndex(dir, "gb18030-ranges")
	if err != nil {
		return nil, err
	}
	for p, cp := range r {
		if cp >= 0 {
			x.ranges = append(x.ranges, [2]int32{int32(p), cp})
		}
	}
	bs, err := cjkRead(dir, "encoding.bs")
	if err != nil {
		return nil, err
	}
	if x.side, err = cjkSideTable(string(bs)); err != nil {
		return nil, err
	}
	if x.pairs, err = cjkBig5Pairs(string(bs)); err != nil {
		return nil, err
	}
	return &x, nil
}

// cjkSideTable reads the table of encoding.bs's gb18030 encoder: the rows
// "<td>U+XXXX" then "<td>0xAA 0xBB" between its "Code point" header and
// the end of the table.
func cjkSideTable(bs string) ([][2]int32, error) {
	start := strings.Index(bs, "<th>Code point")
	if start < 0 {
		return nil, fmt.Errorf("encoding.bs: no gb18030 encoder table")
	}
	end := strings.Index(bs[start:], "</table>")
	if end < 0 {
		return nil, fmt.Errorf("encoding.bs: the gb18030 encoder table does not end")
	}
	re := regexp.MustCompile(`<td>U\+([0-9A-F]{4})\s*<td>0x([0-9A-F]{2}) 0x([0-9A-F]{2})`)
	var out [][2]int32
	for _, m := range re.FindAllStringSubmatch(bs[start:start+end], -1) {
		cp, _ := strconv.ParseUint(m[1], 16, 32)
		b1, _ := strconv.ParseUint(m[2], 16, 8)
		b2, _ := strconv.ParseUint(m[3], 16, 8)
		out = append(out, [2]int32{int32(cp), int32(b1<<8 | b2)})
	}
	if len(out) != 18 {
		return nil, fmt.Errorf("encoding.bs: %d rows in the gb18030 encoder table, want 18", len(out))
	}
	return out, nil
}

// cjkBig5Pairs reads encoding.bs's Big5 decoder table: "<tr><td>1133...
// <td>U+00CA U+0304".
func cjkBig5Pairs(bs string) ([][3]int32, error) {
	re := regexp.MustCompile(`<tr><td>(\d+)(?:<!--[^>]*-->)?<td>U\+([0-9A-F]{4}) U\+([0-9A-F]{4})<td>`)
	var out [][3]int32
	for _, m := range re.FindAllStringSubmatch(bs, -1) {
		p, _ := strconv.Atoi(m[1])
		a, _ := strconv.ParseUint(m[2], 16, 32)
		b, _ := strconv.ParseUint(m[3], 16, 32)
		out = append(out, [3]int32{int32(p), int32(a), int32(b)})
	}
	if len(out) != 4 {
		return nil, fmt.Errorf("encoding.bs: %d Big5 pairs, want 4", len(out))
	}
	return out, nil
}

// cjkWpt checks one index against the copy web-platform-tests keeps in
// file (an array "var name = [cp, null, ...]" by pointer).
func cjkWpt(dir, file string, idx []int32) error {
	b, err := os.ReadFile(filepath.Join(dir, "wpt-encoding", filepath.FromSlash(file)))
	if err != nil {
		return err
	}
	s := string(b)
	loc := regexp.MustCompile(`=\s*\[`).FindStringIndex(s)
	j := strings.LastIndex(s, "]")
	if loc == nil || j < loc[1] {
		return fmt.Errorf("%s: no array", file)
	}
	items := strings.Split(s[loc[1]:j], ",")
	n := len(idx)
	for len(items) > 0 && strings.TrimSpace(items[len(items)-1]) == "null" {
		items = items[:len(items)-1]
	}
	for n > 0 && idx[n-1] < 0 {
		n--
	}
	if len(items) != n {
		return fmt.Errorf("%s: %d pointers, the index %d", file, len(items), n)
	}
	for p, it := range items {
		it = strings.TrimSpace(it)
		want := int64(-1)
		if it != "null" {
			if want, err = strconv.ParseInt(it, 10, 32); err != nil {
				return fmt.Errorf("%s: pointer %d: %q", file, p, it)
			}
		}
		if int64(idx[p]) != want {
			return fmt.Errorf("%s: pointer %d is %d, the index %d", file, p, want, idx[p])
		}
	}
	return nil
}

// cjkFirst is the standard's index pointer: code point -> its first
// pointer in idx, over the pointers keep accepts.
func cjkFirst(idx []int32, keep func(p int) bool) map[int32]int {
	m := map[int32]int{}
	for p, cp := range idx {
		if cp < 0 || !keep(p) {
			continue
		}
		if _, ok := m[cp]; !ok {
			m[cp] = p
		}
	}
	return m
}

// cjkRev is m as records sorted by code point.
func cjkRev(m map[int32]int) [][2]int32 {
	out := make([][2]int32, 0, len(m))
	for cp, p := range m {
		out = append(out, [2]int32{cp, int32(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// cjkEncTables is each encoder's reversed index, by the standard's rules.
type cjkEncTables struct {
	jis0208, sjis, euckr, gb18030, big5 map[int32]int
}

func cjkEncoders(x *cjkIndexes) cjkEncTables {
	all := func(int) bool { return true }
	big5 := cjkFirst(x.big5, func(p int) bool { return p >= (0xA1-0x81)*157 })
	for _, cp := range []int32{0x2550, 0x255E, 0x2561, 0x256A, 0x5341, 0x5345} {
		for p := len(x.big5) - 1; p >= (0xA1-0x81)*157; p-- {
			if x.big5[p] == cp {
				big5[cp] = p
				break
			}
		}
	}
	return cjkEncTables{
		jis0208: cjkFirst(x.jis0208, all),
		sjis:    cjkFirst(x.jis0208, func(p int) bool { return p < 8272 || p > 8835 }),
		euckr:   cjkFirst(x.euckr, all),
		gb18030: cjkFirst(x.gb18030, all),
		big5:    big5,
	}
}

// cjkDoc writes doc as // comment lines of at most 76 columns.
func cjkDoc(sb *strings.Builder, doc string) {
	line := "//"
	for _, w := range strings.Fields(doc) {
		if len(line)+1+len(w) > 76 && line != "//" {
			sb.WriteString(line + "\n")
			line = "//"
		}
		line += " " + w
	}
	sb.WriteString(line + "\n")
}

// cjkBytes writes b as an Mk byte string literal on one line, as
// zones.mk's are.
func cjkBytes(sb *strings.Builder, name, doc string, b []byte) {
	sb.WriteString("\n")
	cjkDoc(sb, doc)
	fmt.Fprintf(sb, "const %s: []u8 = b\"", name)
	time2Bytes(sb, b)
	sb.WriteString("\"\n")
}

// cjkDec is idx as width-byte little-endian code points by pointer, 0 for
// none.
func cjkDec(idx []int32, width int) []byte {
	out := make([]byte, 0, len(idx)*width)
	for _, cp := range idx {
		v := uint32(0)
		if cp > 0 {
			v = uint32(cp)
		}
		for k := 0; k < width; k++ {
			out = append(out, byte(v>>(8*k)))
		}
	}
	return out
}

// cjkRecs is rev as five-byte records: the code point in three bytes and
// the pointer in two, little-endian.
func cjkRecs(rev [][2]int32) []byte {
	out := make([]byte, 0, len(rev)*5)
	for _, r := range rev {
		cp, p := uint32(r[0]), uint32(r[1])
		out = append(out, byte(cp), byte(cp>>8), byte(cp>>16), byte(p), byte(p>>8))
	}
	return out
}

func cjkInts(sb *strings.Builder, name, typ, doc string, vals []int64) {
	sb.WriteString("\n")
	cjkDoc(sb, doc)
	fmt.Fprintf(sb, "const %s: []%s = [\n", name, typ)
	for i := 0; i < len(vals); i += 8 {
		sb.WriteString("   ")
		for j := i; j < i+8 && j < len(vals); j++ {
			fmt.Fprintf(sb, " 0x%X", vals[j])
			if j < len(vals)-1 {
				sb.WriteString(",")
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]\n")
}

// cjkEncodings is the module's encodings in the order of its label ids.
var cjkEncodings = []struct{ name, ident string }{
	{"GBK", "Gbk"}, {"gb18030", "Gb18030"}, {"Big5", "Big5"}, {"EUC-JP", "EucJp"},
	{"ISO-2022-JP", "Iso2022Jp"}, {"Shift_JIS", "ShiftJis"}, {"EUC-KR", "EucKr"},
}

// cjkLabels reads encodings.json's multi-byte groups: label -> index into
// cjkEncodings. Every label std.encoding's Lookup answers Unsupported for
// must be one of them.
func cjkLabels(dir string) (map[string]int, error) {
	all, err := encLabels(dir)
	if err != nil {
		return nil, err
	}
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
	id := map[string]int{}
	for i, e := range cjkEncodings {
		id[e.name] = i
	}
	out := map[string]int{}
	for _, g := range groups {
		for _, e := range g.Encodings {
			i, ok := id[e.Name]
			if !ok {
				continue
			}
			for _, l := range e.Labels {
				if all[l] != encUnsupported {
					return nil, fmt.Errorf("label %q of %q is std.encoding's", l, e.Name)
				}
				out[l] = i
			}
			delete(id, e.Name)
		}
	}
	if len(id) > 0 {
		return nil, fmt.Errorf("encodings.json lacks %v", id)
	}
	for l, n := range all {
		if _, ok := out[l]; n == encUnsupported && !ok {
			return nil, fmt.Errorf("label %q is neither std.encoding's nor std.encoding.cjk's", l)
		}
	}
	return out, nil
}

// cjkGenerate is the text of std/encoding/cjk/tables.mk.
func cjkGenerate(dir string) (string, error) {
	x, err := cjkLoad(dir)
	if err != nil {
		return "", err
	}
	for _, w := range []struct {
		file string
		idx  []int32
	}{
		{"legacy-mb-japanese/euc-jp/jis0208_index.js", x.jis0208},
		{"legacy-mb-japanese/euc-jp/jis0212_index.js", x.jis0212},
		{"legacy-mb-korean/euc-kr/euckr_index.js", x.euckr},
		{"legacy-mb-tchinese/big5/big5_index.js", x.big5},
	} {
		if err := cjkWpt(dir, w.file, w.idx); err != nil {
			return "", err
		}
	}
	for name, idx := range map[string][]int32{"jis0208": x.jis0208, "jis0212": x.jis0212, "euc-kr": x.euckr, "gb18030": x.gb18030} {
		for p, cp := range idx {
			if cp > 0xFFFF {
				return "", fmt.Errorf("index %s: pointer %d is U+%X, past the two-byte table", name, p, cp)
			}
		}
	}
	for p, cp := range x.big5 {
		if cp > 0xFFFFFF {
			return "", fmt.Errorf("index big5: pointer %d is U+%X, past the three-byte table", p, cp)
		}
	}
	if len(x.katakana) != 63 {
		return "", fmt.Errorf("index iso-2022-jp-katakana: %d pointers, want 63", len(x.katakana))
	}
	enc := cjkEncoders(x)
	for cp, p := range enc.jis0208 {
		if p >= 8836 {
			return "", fmt.Errorf("index jis0208: U+%X's first pointer %d is not below 8836", cp, p)
		}
	}
	// Shift_JIS differs from the first pointer only where that pointer is
	// in 8272 to 8835: its records name the pointer it uses instead, 0xFFFF
	// where it has none.
	var sjis [][2]int32
	for cp, p := range enc.jis0208 {
		if q, ok := enc.sjis[cp]; !ok {
			sjis = append(sjis, [2]int32{cp, 0xFFFF})
		} else if q != p {
			sjis = append(sjis, [2]int32{cp, int32(q)})
		}
	}
	sort.Slice(sjis, func(i, j int) bool { return sjis[i][0] < sjis[j][0] })
	labels, err := cjkLabels(dir)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(`// Code generated by std/std-encodingcjk_gen_test.go from the WHATWG Encoding Standard. DO NOT EDIT.
// The CJK indexes, from the standard's CJK index files, encoding.bs and encodings.json
// (whatwg/encoding commit be17ac5c49881336538acfc7b40dba3c8009144b; Copyright WHATWG, under CC BY 4.0).

// A ...Dec table is the index by pointer, each code point little-endian in
// two bytes (three for Big5), 0 where the index has none. A ...Enc table is
// the encoder's reversed index: five-byte records, the code point in three
// bytes and the pointer in two, little-endian, sorted by code point.
// Each table is reached only through the function fields of the encodings
// that read it, so a program links only the tables of the encodings it
// names.

import std.encoding
`)
	cjkBytes(&sb, "Jis0208Dec", "Jis0208Dec is index jis0208.", cjkDec(x.jis0208, 2))
	cjkBytes(&sb, "Jis0212Dec", "Jis0212Dec is index jis0212, which only EUC-JP's decoder reads.", cjkDec(x.jis0212, 2))
	cjkBytes(&sb, "EucKrDec", "EucKrDec is index EUC-KR.", cjkDec(x.euckr, 2))
	cjkBytes(&sb, "Gb18030Dec", "Gb18030Dec is index gb18030.", cjkDec(x.gb18030, 2))
	cjkBytes(&sb, "Big5Dec", "Big5Dec is index Big5, three bytes a code point.", cjkDec(x.big5, 3))
	cjkBytes(&sb, "Jis0208Enc", "Jis0208Enc is index jis0208's index pointer: each code point's first pointer, for EUC-JP and ISO-2022-JP.", cjkRecs(cjkRev(enc.jis0208)))
	cjkBytes(&sb, "SjisEnc", "SjisEnc is where index Shift_JIS pointer differs from Jis0208Enc (pointers 8272 to 8835 left out): the pointer it uses, 0xFFFF for none.", cjkRecs(sjis))
	cjkBytes(&sb, "EucKrEnc", "EucKrEnc is index EUC-KR's index pointer.", cjkRecs(cjkRev(enc.euckr)))
	cjkBytes(&sb, "Gb18030Enc", "Gb18030Enc is index gb18030's index pointer.", cjkRecs(cjkRev(enc.gb18030)))
	cjkBytes(&sb, "Big5Enc", "Big5Enc is index Big5 pointer: pointers below (0xA1 - 0x81) * 157 left out, the last pointer for U+2550, U+255E, U+2561, U+256A, U+5341 and U+5345, the first for the rest.", cjkRecs(cjkRev(enc.big5)))
	var rp, rc []int64
	for _, r := range x.ranges {
		rp = append(rp, int64(r[0]))
		rc = append(rc, int64(r[1]))
	}
	cjkInts(&sb, "RangePtr", "u32", "RangePtr is index gb18030 ranges' pointers, ascending.", rp)
	cjkInts(&sb, "RangeCp", "u32", "RangeCp is the code point of each RangePtr, ascending.", rc)
	var side []int64
	for _, s := range x.side {
		side = append(side, int64(s[0])<<16|int64(s[1]))
	}
	cjkInts(&sb, "Gb18030Side", "u32", "Gb18030Side is the gb18030 encoder's table: code point << 16 | its two bytes, in the standard's order (ascending).", side)
	var pairs []int64
	for _, p := range x.pairs {
		pairs = append(pairs, int64(p[0]), int64(p[1]), int64(p[2]))
	}
	cjkInts(&sb, "Big5Pairs", "u32", "Big5Pairs is the Big5 decoder's table: a pointer, then the two code points it decodes to.", pairs)
	var kana []int64
	for _, k := range x.katakana {
		kana = append(kana, int64(k))
	}
	cjkInts(&sb, "Katakana", "u16", "Katakana is index ISO-2022-JP katakana: U+FF61 + i becomes Katakana[i].", kana)

	keys := make([]string, 0, len(labels))
	longest := 0
	for k := range labels {
		keys = append(keys, k)
		longest = max(longest, len(k))
	}
	sort.Strings(keys)
	fmt.Fprintf(&sb, "\n// LongestLabel is the length of the longest label of Labels.\nconst LongestLabel: int = %d\n", longest)
	sb.WriteString("\n// Labels is every label of the standard's multi-byte encodings, sorted\n// by byte, and LabelIds the index of ById each names.\nconst Labels: []str = [\n")
	for i, k := range keys {
		sep := ","
		if i == len(keys)-1 {
			sep = ""
		}
		fmt.Fprintf(&sb, "    %q%s\n", k, sep)
	}
	sb.WriteString("]\n\nconst LabelIds: []u8 = [\n   ")
	for i, k := range keys {
		fmt.Fprintf(&sb, " %d", labels[k])
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
	}
	sb.WriteString("\n]\n\n// ById is each encoding, by label id; naming it links every table.\nconst ById: []encoding.Encoding = [\n")
	for i, e := range cjkEncodings {
		sep := ","
		if i == len(cjkEncodings)-1 {
			sep = ""
		}
		fmt.Fprintf(&sb, "    %s%s\n", e.ident, sep)
	}
	sb.WriteString("]\n")
	return sb.String(), nil
}

func TestStdEncodingCjk_TablesCurrent(t *testing.T) {
	dir := cjkDir(t)
	want, err := cjkGenerate(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("encoding", "cjk", "tables.mk")
	if *encGen {
		if err := os.WriteFile(filepath.Join(*encCjkOut, "tables.mk"), []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
		t.Errorf("%s differs from what the evidence generates: run go test ./std -run TestStdEncodingCjk_TablesCurrent -args -encoding.gen", path)
	}
}
