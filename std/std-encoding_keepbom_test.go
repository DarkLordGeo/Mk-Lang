// Why: Encoding.DecodeKeepBom and PercentSet.Has need the same oracles as the rest of std.encoding.
// What: their tests against the models of std-encoding_text_test.go and std-encoding_hexpct_test.go.
// How: through the harness's "deck" and "pcth" lines, in dev and release.

package std

import (
	"strings"
	"testing"
)

// TestStdEncoding_DecodeKeepBom: every encoding decodes as the standard's
// decoder with the byte order mark kept (TextDecoder's ignoreBOM true):
// random bytes biased to marks and malformed units, each behind no mark,
// its own mark, another encoding's, and its own twice.
func TestStdEncoding_DecodeKeepBom(t *testing.T) {
	marks := [][]byte{nil, {0xEF, 0xBB, 0xBF}, {0xFF, 0xFE}, {0xFE, 0xFF}, {0xEF, 0xBB, 0xBF, 0xEF, 0xBB, 0xBF}, {0xFF, 0xFE, 0xFF, 0xFE}}
	r := &encRng{s: 712}
	var cases []encCase
	for _, m := range encModels(t) {
		count := 40
		if m.index != nil {
			count = 4
		}
		for k := 0; k < count; k++ {
			body := encFuzzBytes(r, r.intn(30))
			for _, mk := range marks {
				b := append(append([]byte{}, mk...), body...)
				s, _ := whatwgDecodeBom(m, b, true)
				cases = append(cases, encCase{in: "deck " + m.name + " " + encHex(b), want: "ok " + encHex([]byte(s))})
			}
		}
		// Valid text behind the mark: the UTF-8 copy path.
		text := append([]byte{0xEF, 0xBB, 0xBF, 0xEF, 0xBB, 0xBF}, "café € \U0001F600"...)
		s, _ := whatwgDecodeBom(m, text, true)
		cases = append(cases, encCase{in: "deck " + m.name + " " + encHex(text), want: "ok " + encHex([]byte(s))})
	}
	encRunAll(t, cases)
}

// TestStdEncoding_PercentSetHas: each set's membership of all 256 bytes is
// the URL Standard's set (pctIn), and FormUrlencoded holds the space.
func TestStdEncoding_PercentSetHas(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	var cases []encCase
	for k, set := range pctSets {
		var want strings.Builder
		for _, c := range all {
			if pctIn(k, c) {
				want.WriteByte('1')
			} else {
				want.WriteByte('0')
			}
		}
		cases = append(cases, encCase{in: "pcth " + set.name + " " + encHex(all), want: "ok " + want.String()})
	}
	if !pctIn(7, ' ') {
		t.Fatal("the model's form set lacks the space")
	}
	encRunAll(t, cases)
}
