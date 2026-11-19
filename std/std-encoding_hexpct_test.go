// Why: hex and percent-encoding sit under URL and key handling, where one wrong byte matters.
// What: HexEncode against Go's encoding/hex, HexDecode against a model Go's encoding/hex decides, and
// PercentEncode and PercentDecode against a Go model of the WHATWG URL Standard's percent-encode sets.
// How: every byte in every set, plus random and adversarial strings, in dev and release.

package std

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// hexModel is HexDecode's answer: the first byte that is not a hex digit
// in pair order, else the last byte of an odd-length input.
func hexModel(s []byte) string {
	n := len(s)
	for i := 0; i < n/2*2; i++ {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(s[i])) {
			return "err " + b64Bad(i)
		}
	}
	if n%2 == 1 {
		return "err " + b64Bad(n-1)
	}
	b, err := hex.DecodeString(string(s))
	if err != nil {
		panic(err)
	}
	return "ok " + encHex(b)
}

func TestStdEncoding_Hex(t *testing.T) {
	r := &encRng{s: 3}
	var cases []encCase
	for n := 0; n <= 140; n++ {
		data := r.bytes(n)
		cases = append(cases, encCase{in: "hexe " + encHex(data), want: "ok " + hex.EncodeToString(data)})
		up := []byte(strings.ToUpper(hex.EncodeToString(data)))
		cases = append(cases, encCase{in: "hexd " + encHex(up), want: hexModel(up)})
		for i := range up {
			c := append([]byte(nil), up...)
			c[i] = "g/:@`G \x00"[r.intn(8)]
			cases = append(cases, encCase{in: "hexd " + encHex(c), want: hexModel(c)})
		}
		odd := append(append([]byte(nil), up...), '7')
		cases = append(cases, encCase{in: "hexd " + encHex(odd), want: hexModel(odd)})
	}
	// Every byte as a digit candidate, in both places of a pair.
	for b := 1; b < 128; b++ {
		for _, s := range []string{string([]byte{byte(b), '0'}), string([]byte{'0', byte(b)})} {
			cases = append(cases, encCase{in: "hexd " + encHex([]byte(s)), want: hexModel([]byte(s))})
		}
	}
	encRunAll(t, cases)
}

// pctSets is the URL Standard's percent-encode sets, in the driver's names,
// each with the ASCII it adds to the one before it.
var pctSets = []struct{ name, adds string }{
	{"c0", ""},
	{"fragment", " \"<>`"},
	{"query", " \"#<>"},
	{"specialquery", "'"},
	{"path", "?^`{}"},
	{"userinfo", "/:;=@[\\]|"},
	{"component", "$%&+,"},
	{"form", "!'()~"},
}

// pctBase is the set each set is built on, by the URL Standard's text:
// fragment and query on the C0 control set, special-query and path on
// query, userinfo on path, component on userinfo, form on component.
var pctBase = []int{-1, 0, 0, 2, 2, 4, 5, 6}

// pctIn is whether byte c is in set k: C0 controls and everything above
// U+007E are in every set; each set holds the set it is built on and what
// it adds.
func pctIn(k int, c byte) bool {
	if c < 0x20 || c > 0x7E {
		return true
	}
	for j := k; j > 0; j = pctBase[j] {
		if strings.IndexByte(pctSets[j].adds, c) >= 0 {
			return true
		}
	}
	return false
}

func pctEncodeModel(k int, s []byte) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case k == 7 && c == ' ':
			b.WriteByte('+')
		case pctIn(k, c):
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func pctDecodeModel(s []byte) []byte {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHexDigit(s[i+1]) && isHexDigit(s[i+2]) {
			v, _ := hex.DecodeString(string(s[i+1 : i+3]))
			out = append(out, v[0])
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

func isHexDigit(c byte) bool { return strings.IndexByte("0123456789abcdefABCDEF", c) >= 0 }

func TestStdEncoding_Percent(t *testing.T) {
	// The model's query set is the URL Standard's: C0, space, ", #, <, >.
	if pctIn(2, '`') || !pctIn(2, '#') || pctIn(1, '#') || !pctIn(4, '`') || !pctIn(5, '^') || pctIn(4, '\'') || !pctIn(7, '\'') {
		t.Fatal("the model's sets are wrong")
	}
	var cases []encCase
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for k, set := range pctSets {
		cases = append(cases, encCase{in: "pcte " + set.name + " " + encHex(all), want: "ok " + pctEncodeModel(k, all)})
	}
	r := &encRng{s: 5}
	pool := []byte("%%%0aF9g/?#&= +~é\x00\x7f")
	for i := 0; i < 3000; i++ {
		n := r.intn(20)
		s := make([]byte, 0, n)
		for len(s) < n {
			s = append(s, pool[r.intn(len(pool))])
		}
		k := r.intn(len(pctSets))
		cases = append(cases, encCase{in: "pcte " + pctSets[k].name + " " + encHex(s), want: "ok " + pctEncodeModel(k, s)})
		if strings.ToValidUTF8(string(s), "") == string(s) {
			cases = append(cases, encCase{in: "pctd " + encHex(s), want: "ok " + encHex(pctDecodeModel(s))})
		}
	}
	for _, s := range []string{"", "%", "%4", "%41", "%%41", "%4%41", "%zz", "a%2fb", "%C3%A9", "100%", "+", "%2B%2b"} {
		cases = append(cases, encCase{in: "pctd " + encHex([]byte(s)), want: "ok " + encHex(pctDecodeModel([]byte(s)))})
	}
	encRunAll(t, cases)
}
