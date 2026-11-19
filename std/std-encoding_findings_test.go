// Why: regression tests for bugs once found in std.encoding's DER, percent, base64 and PEM code.
// What: DerIter.Next on primitives, the typed readers and DerWriter misuse; PercentEncode round
// trips; Base64Forgiving past 32 characters; ParsePem with a vertical tab.
// How: each test runs the driver in dev and release against a Go model, held to Go's standard
// library where Go has the function.

package std

import (
	"bytes"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
)

// derKidsModel is the driver's "derkids" answer: DerRead, then Next on the
// value's Children until none or an error.
func derKidsModel(data []byte) string {
	v, _, err := derRead(data, 0)
	if err != "" {
		return "read " + err
	}
	if !v.cons {
		// Children's doc line: a primitive fails at its first step.
		return "err " + b64Bad(0) + " after 0"
	}
	rest := v.content
	n := 0
	for len(rest) > 0 {
		_, r, err := derRead(rest, v.depth+1)
		if err != "" {
			var off int
			fmt.Sscanf(err, "BadInput(offset: %d)", &off)
			return fmt.Sprintf("err %s after %d", b64Bad(len(v.content)-len(rest)+off), n)
		}
		rest = r
		n++
	}
	return fmt.Sprintf("ok %d", n)
}

// derTypedModel is the driver's "dertyped" answer: Integer, Boolean and
// BitString of the element, whatever its class, form and tag.
func derTypedModel(data []byte) string {
	v, _, err := derRead(data, 0)
	if err != "" {
		return "read " + err
	}
	c := v.content
	universal := func(tag uint32) bool { return v.class == 0 && !v.cons && v.tag == tag }
	bad := b64Bad(0)
	in, bo, bi := bad, bad, bad
	if universal(2) && len(c) > 0 && !(len(c) >= 2 && (c[0] == 0 && c[1] < 0x80 || c[0] == 0xFF && c[1] >= 0x80)) {
		in = "=" + encHex(c)
	}
	if universal(1) && len(c) == 1 && (c[0] == 0 || c[0] == 0xFF) {
		bo = fmt.Sprintf("=%v", c[0] == 0xFF)
	}
	if universal(3) && len(c) > 0 && c[0] <= 7 && !(len(c) == 1 && c[0] != 0) && !(c[0] != 0 && c[len(c)-1]&(1<<c[0]-1) != 0) {
		bi = "=" + encHex(c[1:])
	}
	return fmt.Sprintf("ok int %s bool %s bits %s", in, bo, bi)
}

// TestStdEncoding_PrimitiveChildren: Children of a primitive value fails
// at its first step, also when the content is empty, so a primitive
// `10 00` (SEQUENCE without the constructed bit) or a NULL never reads as
// an empty SEQUENCE. Constructed values list their children.
func TestStdEncoding_PrimitiveChildren(t *testing.T) {
	var cases []encCase
	for _, h := range []string{"0500", "1000", "1100", "0400", "040100", "0403020100", "020105", "8000", "9f1f00",
		"3000", "3100", "a000", "2300", "3003020101", "30050201010500", "3003020100ff", "30020500", "300205", "30030201",
		"3080", ""} {
		data := cryptoUnhex(t, h)
		cases = append(cases, encCase{in: "derkids " + h, want: derKidsModel(data)})
	}
	for _, c := range cases[:9] {
		if c.want != "err BadInput(offset: 0) after 0" {
			t.Fatalf("model: %s gives %s", c.in, c.want)
		}
	}
	r := &encRng{s: 41}
	for k := 0; k < 300; k++ {
		data := derRandom(r, 3)
		cases = append(cases, encCase{in: "derkids " + encHex(data), want: derKidsModel(data)})
		c := append([]byte(nil), data...)
		c[0] ^= 0x20 // the constructed bit flipped
		cases = append(cases, encCase{in: "derkids " + encHex(c), want: derKidsModel(c)})
	}
	encRunAll(t, cases)
}

// TestStdEncoding_DerRefusals: every documented refusal of the typed
// readers and of DerWriter. The typed readers are called on elements of
// every class and form, so the "not a primitive universal element of this
// tag" branch runs for each; the writer's misuse results are written from
// its doc lines (offsets "at the writer's length").
func TestStdEncoding_DerRefusals(t *testing.T) {
	var cases []encCase
	for _, h := range []string{
		"820105", "420105", "c20105", "220105", "2200", "2203020101", // INTEGER's tag, other class or constructed
		"0101ff", "810101", "2101ff", "21030101ff", // BOOLEAN's
		"030100", "830100", "2300", "2303030100", "430200ff", // BIT STRING's
		"020105", "0101ff", "030200ff", "0500", "3000", // each typed reader on the others' elements
	} {
		data := cryptoUnhex(t, h)
		cases = append(cases, encCase{in: "dertyped " + h, want: derTypedModel(data)})
	}
	// The reader's own answers on these, pinned from the doc lines.
	pinned := map[string]string{
		"dertyped 820105":     "ok int BadInput(offset: 0) bool BadInput(offset: 0) bits BadInput(offset: 0)",
		"dertyped 2203020101": "ok int BadInput(offset: 0) bool BadInput(offset: 0) bits BadInput(offset: 0)",
		"dertyped 020105":     "ok int =05 bool BadInput(offset: 0) bits BadInput(offset: 0)",
		"dertyped 0101ff":     "ok int BadInput(offset: 0) bool =true bits BadInput(offset: 0)",
		"dertyped 030200ff":   "ok int BadInput(offset: 0) bool BadInput(offset: 0) bits =ff",
	}
	for _, c := range cases {
		if w, ok := pinned[c.in]; ok && w != c.want {
			t.Fatalf("model: %s gives %s, the doc lines %s", c.in, c.want, w)
		}
	}
	r := &encRng{s: 43}
	for k := 0; k < 400; k++ {
		content := r.bytes(r.intn(4))
		data := derTLV(byte(r.intn(4)), r.intn(2) == 1, uint32(1+r.intn(3)), content)
		cases = append(cases, encCase{in: "dertyped " + encHex(data), want: derTypedModel(data)})
	}
	// DerWriter: End with nothing open fails with BadInput at the writer's
	// length (0, then 3 after a NULL); a Primitive with the constructed bit
	// writes nothing and makes Bytes fail with BadInput; Bytes with an
	// element open fails with BadInput at the writer's length (an
	// identifier and five reserved length bytes per open Begin); a Begin
	// past depth 32 makes its End and Bytes fail with TooLarge.
	deep := strings.Repeat("b10,", 34) + strings.Repeat("e,", 34)
	deepWant := "ok" + strings.Repeat(" -", 34) + " TooLarge" + strings.Repeat(" -", 33) + " bytes TooLarge"
	for _, c := range []encCase{
		{"derw e", "ok BadInput(offset: 0) bytes ="},
		{"derw p05:,e", "ok - BadInput(offset: 2) bytes =0500"},
		{"derw p20000010:", "ok - bytes BadInput(offset: 0)"},
		{"derw p05:,p20000004:00", "ok - - bytes BadInput(offset: 2)"},
		{"derw b10", "ok - bytes BadInput(offset: 6)"},
		{"derw b10,b80000000", "ok - - bytes BadInput(offset: 12)"},
		{"derw b10,i05,e", "ok - - - bytes =3003020105"},
		{"derw b10,e,e", "ok - - BadInput(offset: 2) bytes =3000"},
		{"derw " + deep, deepWant},
	} {
		cases = append(cases, c)
	}
	encRunAll(t, cases)
}

// TestStdEncoding_PercentRoundTrip: PercentEncode in every set and then
// PercentDecode, against the URL Standard model (the decode gives the
// input back exactly when the model's does: the sets that leave `%` or a
// space as written do not round-trip, by the standard).
func TestStdEncoding_PercentRoundTrip(t *testing.T) {
	var cases []encCase
	r := &encRng{s: 47}
	pool := []byte("%%%0aF9g/?#&= +~é\x00\x7f\xff")
	for i := 0; i < 2000; i++ {
		n := r.intn(24)
		s := make([]byte, 0, n)
		for len(s) < n {
			s = append(s, pool[r.intn(len(pool))])
		}
		k := r.intn(len(pctSets))
		cases = append(cases, pctRoundTrip(k, s))
	}
	for k := range pctSets {
		for _, s := range []string{"", "%", "%4", "%41", "%zz", "a b", "100%", "%%41", "x%A"} {
			cases = append(cases, pctRoundTrip(k, []byte(s)))
		}
	}
	encRunAll(t, cases)
}

// pctRoundTrip is the driver's "pctrt" case for set k and bytes s.
func pctRoundTrip(k int, s []byte) encCase {
	enc := pctEncodeModel(k, s)
	same := bytes.Equal(pctDecodeModel([]byte(enc)), s)
	return encCase{in: "pctrt " + pctSets[k].name + " " + encHex(s), want: fmt.Sprintf("ok %s %v", enc, same)}
}

// TestStdEncoding_ForgivingVector: Base64Forgiving where the vector step
// runs (32 characters and more): clean text; one whitespace byte at every
// position; 64- and 76-column lines in LF and CRLF; a quad split by
// whitespace; a bad byte, `=` or `-` at every position; padding and its
// absence. The model is the Infra Standard's steps, and Go's
// StdEncoding.DecodeString (which skips CR and LF) agrees on every text it
// accepts whose only whitespace is CR and LF.
func TestStdEncoding_ForgivingVector(t *testing.T) {
	r := &encRng{s: 53}
	var cases []encCase
	add := func(s []byte) {
		want := b64Want("forgiving", s)
		if !bytes.ContainsAny(s, " \t\f") {
			if got, err := base64.StdEncoding.DecodeString(string(s)); err == nil && want != fmt.Sprintf("ok %s true %s", encHex(got), map[bool]string{true: "TooLarge", false: "-"}[len(got) > 0]) {
				t.Fatalf("model disagrees with Go on %q: %s", s, want)
			}
		}
		cases = append(cases, encCase{in: "b64d forgiving " + encHex(s), want: want})
	}
	for _, n := range []int{24, 25, 26, 36, 47, 48, 49, 72, 96, 97, 98, 150, 300} {
		text := []byte(base64.StdEncoding.EncodeToString(r.bytes(n)))
		add(text)
		add([]byte(strings.TrimRight(string(text), "=")))
		for p := 0; p <= len(text); p++ {
			for _, ws := range []string{" ", "\n", "\r\n", "\t\f"} {
				if p%7 != 0 && ws != " " {
					continue
				}
				c := append(append(append([]byte(nil), text[:p]...), ws...), text[p:]...)
				add(c)
			}
		}
		for p := 0; p < len(text); p++ {
			for _, bad := range []byte{'=', '-', '*', 0x00, 0x0B} {
				if p%3 != 0 && bad != '=' {
					continue
				}
				c := append([]byte(nil), text...)
				c[p] = bad
				add(c)
			}
		}
	}
	for _, cols := range []int{64, 76} {
		for _, eol := range []string{"\n", "\r\n"} {
			for _, n := range []int{100, 1000, 3000} {
				text := base64.StdEncoding.EncodeToString(r.bytes(n))
				var b strings.Builder
				for i := 0; i < len(text); i += cols {
					b.WriteString(text[i:min(i+cols, len(text))])
					b.WriteString(eol)
				}
				add([]byte(b.String()))
				add([]byte(" " + b.String() + "  "))
			}
		}
	}
	// A quad split by whitespace at each of its places, then a long run.
	long := base64.StdEncoding.EncodeToString(r.bytes(120))
	for q := 1; q < 4; q++ {
		add([]byte(long[:q] + " " + long[q:]))
		add([]byte(long[:40+q] + "\n" + long[40+q:]))
	}
	encRunAll(t, cases)
}

// TestStdEncoding_PemVerticalTab: RFC 7468 section 3's lax form allows
// W = WSP / CR / LF / %x0B / %x0C anywhere in a block's base64, so a
// vertical tab is whitespace there (Go's encoding/pem refuses it, so the
// model, not Go, is the oracle on these).
func TestStdEncoding_PemVerticalTab(t *testing.T) {
	var cases []encCase
	for _, s := range []string{
		"-----BEGIN X-----\nQU\x0bJD\n-----END X-----\n",
		"-----BEGIN X-----\n\x0bQUJD\x0b\n-----END X-----\n",
		"-----BEGIN X-----\nQU\x0cJD\n-----END X-----\n",
		"-----BEGIN X-----\nQ\x0bQ\x0b=\x0b=\n-----END X-----\n",
		"-----BEGIN X-----\nQUJD\n\x0b\n-----END X-----\n",
	} {
		c := pemCase([]byte(s))
		if !strings.HasPrefix(c.want, "ok 1 58:") {
			t.Fatalf("model refuses %q: %s", s, c.want)
		}
		cases = append(cases, c)
	}
	if got := pemModel([]byte("-----BEGIN X-----\nQU\x0bJD\n-----END X-----\n")); got != "ok 1 58:"+encHex([]byte("ABC")) {
		t.Fatalf("model: %s", got)
	}
	r := &encRng{s: 59}
	for k := 0; k < 100; k++ {
		block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.bytes(r.intn(150))})
		c := append([]byte(nil), block...)
		// A vertical tab into the body (after the begin line, before the
		// end line).
		begin := bytes.IndexByte(c, '\n') + 1
		end := bytes.LastIndex(c, []byte("-----END"))
		if end > begin {
			p := begin + r.intn(end-begin)
			c = append(append(append([]byte(nil), c[:p]...), 0x0B), c[p:]...)
		}
		cases = append(cases, pemCase(c))
	}
	encRunAll(t, cases)
}
