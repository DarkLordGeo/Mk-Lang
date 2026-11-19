// Why: std.json's error offsets and output need an independent reference;
// Go's encoding/json reports neither the same offsets nor the same limits.
// What: a strict RFC 8259 parser in Go (jmParse) with std.json's documented
// error offsets, Value.Format's compact formatter (jmFormat), and corpora.
// How: the model is checked against encoding/json (TestSTDJSON_ModelMatchesGo)
// and the Mk code against the model (std-json_parse_test.go).

package std

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// jmKind is the kind of a model value.
type jmKind int

const (
	jmNull jmKind = iota
	jmBool
	jmNumber
	jmText
	jmArray
	jmObject
)

// jmValue is a model value: numbers keep their text, objects their order
// and duplicates.
type jmValue struct {
	kind  jmKind
	b     bool
	text  string // Number's text, Text's decoded string
	items []*jmValue
	keys  []string
	vals  []*jmValue
}

// jmErr is a model error: "syntax", "depth" or "utf8", and an offset.
type jmErr struct {
	kind   string
	offset int
}

func (e *jmErr) Error() string { return fmt.Sprintf("%s %d", e.kind, e.offset) }

const jmMaxDepth = 512

type jmParser struct {
	b     []byte
	depth int
}

// jmParse parses b as std.json.Parse does, after str.From's UTF-8 check.
func jmParse(b []byte) (*jmValue, *jmErr) {
	if !utf8.Valid(b) {
		return nil, &jmErr{"utf8", -1}
	}
	p := &jmParser{b: b}
	i := p.ws(0)
	v, i, err := p.value(i)
	if err != nil {
		return nil, err
	}
	i = p.ws(i)
	if i != len(b) {
		return nil, &jmErr{"syntax", i}
	}
	return v, nil
}

func (p *jmParser) syntax(i int) *jmErr { return &jmErr{"syntax", i} }

func (p *jmParser) ws(i int) int {
	for i < len(p.b) {
		switch p.b[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func (p *jmParser) value(i int) (*jmValue, int, *jmErr) {
	b := p.b
	if i >= len(b) {
		return nil, 0, p.syntax(i)
	}
	switch c := b[i]; {
	case c == '"':
		s, e, err := p.str(i + 1)
		return &jmValue{kind: jmText, text: s}, e, err
	case c == '{':
		return p.object(i)
	case c == '[':
		return p.array(i)
	case c == '-' || c >= '0' && c <= '9':
		e, err := p.number(i)
		if err != nil {
			return nil, 0, err
		}
		return &jmValue{kind: jmNumber, text: string(b[i:e])}, e, nil
	case c == 't':
		e, err := p.word(i, "true")
		return &jmValue{kind: jmBool, b: true}, e, err
	case c == 'f':
		e, err := p.word(i, "false")
		return &jmValue{kind: jmBool}, e, err
	case c == 'n':
		e, err := p.word(i, "null")
		return &jmValue{kind: jmNull}, e, err
	}
	return nil, 0, p.syntax(i)
}

func (p *jmParser) array(i int) (*jmValue, int, *jmErr) {
	b, n := p.b, len(p.b)
	if p.depth == jmMaxDepth {
		return nil, 0, &jmErr{"depth", i}
	}
	p.depth++
	v := &jmValue{kind: jmArray}
	i = p.ws(i + 1)
	if i < n && b[i] == ']' {
		p.depth--
		return v, i + 1, nil
	}
	for {
		x, e, err := p.value(i)
		if err != nil {
			return nil, 0, err
		}
		v.items = append(v.items, x)
		i = p.ws(e)
		if i >= n {
			return nil, 0, p.syntax(n)
		}
		if b[i] == ',' {
			i = p.ws(i + 1)
			continue
		}
		if b[i] == ']' {
			break
		}
		return nil, 0, p.syntax(i)
	}
	p.depth--
	return v, i + 1, nil
}

func (p *jmParser) object(i int) (*jmValue, int, *jmErr) {
	b, n := p.b, len(p.b)
	if p.depth == jmMaxDepth {
		return nil, 0, &jmErr{"depth", i}
	}
	p.depth++
	v := &jmValue{kind: jmObject}
	i = p.ws(i + 1)
	if i < n && b[i] == '}' {
		p.depth--
		return v, i + 1, nil
	}
	for {
		if i >= n {
			return nil, 0, p.syntax(n)
		}
		if b[i] != '"' {
			return nil, 0, p.syntax(i)
		}
		k, e, err := p.str(i + 1)
		if err != nil {
			return nil, 0, err
		}
		i = p.ws(e)
		if i >= n {
			return nil, 0, p.syntax(n)
		}
		if b[i] != ':' {
			return nil, 0, p.syntax(i)
		}
		x, e, err := p.value(p.ws(i + 1))
		if err != nil {
			return nil, 0, err
		}
		v.keys = append(v.keys, k)
		v.vals = append(v.vals, x)
		i = p.ws(e)
		if i >= n {
			return nil, 0, p.syntax(n)
		}
		if b[i] == ',' {
			i = p.ws(i + 1)
			continue
		}
		if b[i] == '}' {
			break
		}
		return nil, 0, p.syntax(i)
	}
	p.depth--
	return v, i + 1, nil
}

// str: i is the index after the opening quote; returns the decoded string and
// the index after the closing quote.
func (p *jmParser) str(start int) (string, int, *jmErr) {
	b, n := p.b, len(p.b)
	i := start
	for {
		if i >= n {
			return "", 0, p.syntax(n)
		}
		c := b[i]
		if c == '"' {
			return string(b[start:i]), i + 1, nil
		}
		if c == '\\' {
			return p.unescape(start, i)
		}
		if c < 0x20 {
			return "", 0, p.syntax(i)
		}
		i++
	}
}

// unescape decodes as it scans, in one pass: the first fault in
// the text is the one reported. A bad escape is Syntax at its backslash; the
// text ending inside an escape, or before the closing quote, is Syntax at
// len(text); a bare control byte is Syntax at itself. It shares no code
// with Mk's two-pass Unescape (scan and check, then decode), and the
// offsets of TestSTDJSON_EscapeOffsets are written by hand, so the model is
// not the only oracle.
func (p *jmParser) unescape(start, first int) (string, int, *jmErr) {
	b, n := p.b, len(p.b)
	out := append([]byte(nil), b[start:first]...)
	// hex reads 4 hex digits at i: the value, or -1 on a bad digit, or -2
	// when the text ends first.
	hex := func(i int) int {
		v := 0
		for k := range 4 {
			if i+k >= n {
				return -2
			}
			d, ok := jmHexDigit(b[i+k])
			if !ok {
				return -1
			}
			v = v<<4 | d
		}
		return v
	}
	simple := map[byte]byte{'"': '"', 0x5C: 0x5C, '/': '/', 'b': 8, 'f': 12, 'n': 10, 'r': 13, 't': 9}
	j := first
	for {
		if j >= n {
			return "", 0, p.syntax(n)
		}
		c := b[j]
		switch {
		case c == '"':
			return string(out), j + 1, nil
		case c < 0x20:
			return "", 0, p.syntax(j)
		case c != 0x5C:
			out = append(out, c)
			j++
			continue
		}
		if j+1 >= n {
			return "", 0, p.syntax(n)
		}
		if d, ok := simple[b[j+1]]; ok {
			out = append(out, d)
			j += 2
			continue
		}
		if b[j+1] != 'u' {
			return "", 0, p.syntax(j)
		}
		cp := hex(j + 2)
		switch {
		case cp == -2:
			return "", 0, p.syntax(n)
		case cp < 0, cp >= 0xDC00 && cp <= 0xDFFF:
			return "", 0, p.syntax(j)
		case cp < 0xD800 || cp > 0xDBFF:
			out = utf8.AppendRune(out, rune(cp))
			j += 6
			continue
		}
		// A high surrogate: a \u escape of a low one must follow.
		for k, want := range []byte{0x5C, 'u'} {
			if j+6+k >= n {
				return "", 0, p.syntax(n)
			}
			if b[j+6+k] != want {
				return "", 0, p.syntax(j)
			}
		}
		lo := hex(j + 8)
		if lo == -2 {
			return "", 0, p.syntax(n)
		}
		if lo < 0xDC00 || lo > 0xDFFF {
			return "", 0, p.syntax(j)
		}
		out = utf8.AppendRune(out, utf16.DecodeRune(rune(cp), rune(lo)))
		j += 12
	}
}

func jmHexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

func jmHex4(b []byte, i, end int) int {
	if i+4 > end {
		return -1
	}
	v := 0
	for k := range 4 {
		c := b[i+k]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c|0x20 >= 'a' && c|0x20 <= 'f':
			d = int(c|0x20-'a') + 10
		default:
			return -1
		}
		v = v*16 + d
	}
	return v
}

func (p *jmParser) number(i int) (int, *jmErr) {
	b, n := p.b, len(p.b)
	digit := func(k int) bool { return k < n && b[k] >= '0' && b[k] <= '9' }
	digits := func(k int) int {
		for digit(k) {
			k++
		}
		return k
	}
	if b[i] == '-' {
		i++
	}
	if i >= n {
		return 0, p.syntax(n)
	}
	switch {
	case b[i] == '0':
		i++
	case b[i] >= '1' && b[i] <= '9':
		i = digits(i + 1)
	default:
		return 0, p.syntax(i)
	}
	if i < n && b[i] == '.' {
		i++
		if i >= n {
			return 0, p.syntax(n)
		}
		if !digit(i) {
			return 0, p.syntax(i)
		}
		i = digits(i + 1)
	}
	if i < n && b[i]|0x20 == 'e' {
		i++
		if i < n && (b[i] == '+' || b[i] == '-') {
			i++
		}
		if i >= n {
			return 0, p.syntax(n)
		}
		if !digit(i) {
			return 0, p.syntax(i)
		}
		i = digits(i + 1)
	}
	return i, nil
}

func (p *jmParser) word(i int, w string) (int, *jmErr) {
	for k := 0; k < len(w); k++ {
		if i+k >= len(p.b) {
			return 0, p.syntax(len(p.b))
		}
		if p.b[i+k] != w[k] {
			return 0, p.syntax(i + k)
		}
	}
	return i + len(w), nil
}

// jmFormat writes v as Value.Format does.
func jmFormat(sb *strings.Builder, v *jmValue) {
	switch v.kind {
	case jmNull:
		sb.WriteString("null")
	case jmBool:
		sb.WriteString(strconv.FormatBool(v.b))
	case jmNumber:
		sb.WriteString(v.text)
	case jmText:
		jmQuote(sb, v.text)
	case jmArray:
		sb.WriteByte('[')
		for k, x := range v.items {
			if k > 0 {
				sb.WriteByte(',')
			}
			jmFormat(sb, x)
		}
		sb.WriteByte(']')
	case jmObject:
		sb.WriteByte('{')
		for k := range v.keys {
			if k > 0 {
				sb.WriteByte(',')
			}
			jmQuote(sb, v.keys[k])
			sb.WriteByte(':')
			jmFormat(sb, v.vals[k])
		}
		sb.WriteByte('}')
	}
}

func jmQuote(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			sb.WriteString(`\"`)
		case c == '\\':
			sb.WriteString(`\\`)
		case c == 8:
			sb.WriteString(`\b`)
		case c == 12:
			sb.WriteString(`\f`)
		case c == 10:
			sb.WriteString(`\n`)
		case c == 13:
			sb.WriteString(`\r`)
		case c == 9:
			sb.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(sb, `\u00%02x`, c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
}

// jmOutcome is the line the Mk driver prints for one input: "ok <json>",
// "syntax <offset>", "depth <offset>" or "utf8".
func jmOutcome(b []byte) string {
	v, err := jmParse(b)
	if err != nil {
		if err.kind == "utf8" {
			return "utf8"
		}
		return err.Error()
	}
	var sb strings.Builder
	jmFormat(&sb, v)
	return "ok " + sb.String()
}

// jmCanon renders a model value with object keys sorted and the last of
// duplicates kept, so it compares with what encoding/json decodes.
func jmCanon(v *jmValue) any {
	switch v.kind {
	case jmNull:
		return nil
	case jmBool:
		return v.b
	case jmNumber:
		return json.Number(v.text)
	case jmText:
		return v.text
	case jmArray:
		xs := []any{}
		for _, x := range v.items {
			xs = append(xs, jmCanon(x))
		}
		return xs
	}
	m := map[string]any{}
	for k := range v.keys {
		m[v.keys[k]] = jmCanon(v.vals[k])
	}
	return m
}

// jmHasLoneSurrogate reports whether b holds a \u escape of a surrogate
// that is not a high-low pair, which Go replaces with U+FFFD and the model
// refuses.
func jmHasLoneSurrogate(b []byte) bool {
	for i := 0; i+5 < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		if b[i+1] != 'u' {
			i++
			continue
		}
		cp := jmHex4(b, i+2, len(b))
		if cp >= 0xDC00 && cp <= 0xDFFF {
			return true
		}
		if cp >= 0xD800 && cp <= 0xDBFF {
			lo := -1
			if i+11 < len(b) && b[i+6] == '\\' && b[i+7] == 'u' {
				lo = jmHex4(b, i+8, len(b))
			}
			if lo < 0xDC00 || lo > 0xDFFF {
				return true
			}
			i += 11
			continue
		}
		i += 5
	}
	return false
}

// jmGoAgrees checks the model against encoding/json on one input: the same
// accept or reject, and, when both accept, the same tree. Depth past 512
// (Go allows 10000), invalid UTF-8 and lone surrogates (Go replaces them
// with U+FFFD) are where the model is stricter by design.
func jmGoAgrees(b []byte) error {
	v, err := jmParse(b)
	goOK := json.Valid(b)
	if err != nil {
		if err.kind == "depth" || err.kind == "utf8" || jmHasLoneSurrogate(b) {
			return nil
		}
		if goOK {
			return fmt.Errorf("model rejects (%v), Go accepts", err)
		}
		return nil
	}
	if !goOK {
		return fmt.Errorf("model accepts, Go rejects")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var g any
	if err := d.Decode(&g); err != nil {
		return fmt.Errorf("Go decode: %v", err)
	}
	if a, c := fmt.Sprint(jmCanon(v)), fmt.Sprint(g); a != c {
		return fmt.Errorf("trees differ:\nmodel %s\ngo    %s", a, c)
	}
	return nil
}

// jmEdgeCases are hand-written inputs at every branch of the grammar.
var jmEdgeCases = []string{
	``, ` `, `null`, `true`, `false`, `nul`, `nulL`, `tru`, `fals`, `truex`, ` true `, "\ttrue\r\n",
	`0`, `-0`, `-`, `-a`, `01`, `00`, `1.`, `1.e1`, `.5`, `+1`, `1e`, `1e+`, `1E-2`, `1e05`, `-0.0e-0`,
	`123456789012345678901234567890`, `1.5e999`, `-1.5e-999`, `0.1`, `2.`, `1x`, `[1,]`, `[,1]`, `[1 2]`,
	`[]`, `[ ]`, `[[]]`, `[[[]]`, `{}`, `{ }`, `{"a":1}`, `{"a" 1}`, `{"a":}`, `{"a":1,}`, `{,}`, `{1:2}`,
	`{"a":1,"a":2}`, `{"a":{"b":[1,{"c":null}]}}`, `"`, `""`, `"abc`, `"a\"b"`, `"a\\"`, `"\/"`,
	`"\b\f\n\r\t"`, `"\x"`, `"\u"`, `"\u12"`, `"\u12G4"`, `"é"`, `"é"`, `"😀"`,
	`"\ud83d"`, `"\ude00"`, `"\ud83dA"`, `"\ud83dx"`, `"\ud83d\ud83d"`, "\"a\x01b\"", "\"\x1f\"",
	"\"\x7f\"", `"é😀"`, "\"\xe9\"", `"\"`, `"\\\"`, `["a\n"]`, `"\u0000"`, `"\u001f"`, `" "`,
	`"abcdefghijklmnopqrstuvwxyz\"0123456789"`, `"0123456\\abc"`, `"01234567\nab"`, `1 2`, `[] []`,
	`{"a":[1,2,{"b":"c\"d"}],"e":-1.5e+10}`, "[1,\n2]", `[true,false,null]`, `{"":""}`, `[""]`,
}

// jmRandomDoc writes a random document of at most depth levels.
func jmRandomDoc(r *rand.Rand, sb *strings.Builder, depth int) {
	ws := func() {
		if r.IntN(4) == 0 {
			sb.WriteString([]string{" ", "\n", "\t", "\r\n", "  "}[r.IntN(5)])
		}
	}
	ws()
	k := r.IntN(10)
	if depth <= 0 && k >= 7 {
		k = r.IntN(7)
	}
	switch k {
	case 0:
		sb.WriteString("null")
	case 1:
		sb.WriteString([]string{"true", "false"}[r.IntN(2)])
	case 2, 3:
		jmRandomNumber(r, sb)
	case 4, 5, 6:
		jmRandomString(r, sb)
	case 7, 8:
		sb.WriteByte('[')
		n := r.IntN(6)
		for i := range n {
			if i > 0 {
				sb.WriteByte(',')
			}
			jmRandomDoc(r, sb, depth-1)
		}
		ws()
		sb.WriteByte(']')
	default:
		sb.WriteByte('{')
		n := r.IntN(6)
		for i := range n {
			if i > 0 {
				sb.WriteByte(',')
			}
			ws()
			jmRandomString(r, sb)
			ws()
			sb.WriteByte(':')
			jmRandomDoc(r, sb, depth-1)
		}
		ws()
		sb.WriteByte('}')
	}
	ws()
}

func jmRandomNumber(r *rand.Rand, sb *strings.Builder) {
	if r.IntN(3) == 0 {
		sb.WriteByte('-')
	}
	if r.IntN(4) == 0 {
		sb.WriteByte('0')
	} else {
		sb.WriteString(strconv.FormatUint(r.Uint64()>>uint(r.IntN(64)), 10))
	}
	if r.IntN(3) == 0 {
		sb.WriteString("." + strconv.Itoa(r.IntN(100000)))
	}
	if r.IntN(4) == 0 {
		sb.WriteString([]string{"e", "E", "e+", "e-", "E-"}[r.IntN(5)] + strconv.Itoa(r.IntN(400)))
	}
}

func jmRandomString(r *rand.Rand, sb *strings.Builder) {
	sb.WriteByte('"')
	n := r.IntN(40)
	for range n {
		switch k := r.IntN(30); {
		case k == 0:
			sb.WriteString([]string{`\"`, `\\`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`}[r.IntN(8)])
		case k == 1:
			fmt.Fprintf(sb, `\u%04x`, []int{r.IntN(0x80), r.IntN(0x800), 0xE000 + r.IntN(0x2000), r.IntN(0xD800)}[r.IntN(4)])
		case k == 2:
			c := 0x10000 + r.IntN(0x100000) - 0x10000
			fmt.Fprintf(sb, `\u%04X\u%04x`, 0xD800+(c>>10), 0xDC00+(c&0x3FF))
		case k == 3:
			sb.WriteString([]string{"é", "😀", " ", "中文", "\u0080"}[r.IntN(5)])
		default:
			c := byte(0x20 + r.IntN(95))
			if c == '"' || c == '\\' {
				c = 'x'
			}
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
}

// jmRandomInputs is the differential corpus: valid documents, and each
// with one byte changed, dropped or duplicated, so every reject branch is
// reached with a known offset.
func jmRandomInputs(seed uint64, n int) []string {
	r := rand.New(rand.NewPCG(seed, 0x6a736f6e))
	var out []string
	for range n {
		var sb strings.Builder
		jmRandomDoc(r, &sb, 1+r.IntN(5))
		doc := sb.String()
		out = append(out, doc)
		if len(doc) == 0 {
			continue
		}
		b := []byte(doc)
		i := r.IntN(len(b))
		switch r.IntN(3) {
		case 0:
			b[i] = "{}[]\":,\\0-.eE tfn\x01"[r.IntN(18)]
		case 1:
			b = append(b[:i], b[i+1:]...)
		default:
			b = append(b[:i+1], b[i:]...)
		}
		out = append(out, string(b))
	}
	return out
}

func jmNested(open, close string, n int) string {
	return strings.Repeat(open, n) + strings.Repeat(close, n)
}

func TestSTDJSON_ModelMatchesGo(t *testing.T) {
	inputs := append([]string{}, jmEdgeCases...)
	inputs = append(inputs, jmRandomInputs(1, 3000)...)
	inputs = append(inputs, jmNested("[", "]", 512), jmNested("[", "]", 513), jmNested(`{"a":`, "}", 513))
	bad := 0
	for _, in := range inputs {
		if err := jmGoAgrees([]byte(in)); err != nil {
			bad++
			if bad <= 10 {
				t.Errorf("%q: %v", in, err)
			}
		}
	}
	if v, err := jmParse([]byte(jmNested("[", "]", 512))); v == nil || err != nil {
		t.Errorf("512 levels: %v", err)
	}
	if _, err := jmParse([]byte(jmNested("[", "]", 513))); err == nil || err.kind != "depth" || err.offset != 512 {
		t.Errorf("513 levels: %v, want depth 512", err)
	}
	keys := map[string]bool{}
	for _, in := range inputs {
		o := jmOutcome([]byte(in))
		keys[strings.SplitN(o, " ", 2)[0]] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	if strings.Join(ks, ",") != "depth,ok,syntax,utf8" {
		t.Errorf("outcome kinds %v, want every kind reached", ks)
	}
}
