// Why: Punycode (RFC 3492) is easy to get wrong at its edges and easy to make
// quadratic.
// What: TestStdUrl_PunycodeSamples, TestStdUrl_PunycodeModel and
// TestStdUrl_PunycodeLong over std/url/idna_punycode.mk.
// How: the RFC's samples, a Go model of its pseudocode on random and invalid
// labels, and labels too long for quadratic work to finish.

package std

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	urlPunyBase = 36
	urlPunyTMin = 1
	urlPunyTMax = 26
	urlPunyMax  = 1<<32 - 1
)

func urlPunyAdapt(delta, points int, first bool) int {
	if first {
		delta /= 700
	} else {
		delta /= 2
	}
	delta += delta / points
	k := 0
	for delta > (urlPunyBase-urlPunyTMin)*urlPunyTMax/2 {
		delta /= urlPunyBase - urlPunyTMin
		k += urlPunyBase
	}
	return k + (urlPunyBase-urlPunyTMin+1)*delta/(delta+38)
}

func urlPunyT(k, bias int) int {
	switch {
	case k <= bias:
		return urlPunyTMin
	case k >= bias+urlPunyTMax:
		return urlPunyTMax
	}
	return k - bias
}

// urlPunyDecodeModel is RFC 3492 section 6.2; ok is false for failure.
func urlPunyDecodeModel(s string) (string, bool) {
	b := strings.LastIndexByte(s, '-')
	if b < 0 {
		b = 0
	}
	var out []rune
	for i := 0; i < b; i++ {
		if s[i] >= 0x80 {
			return "", false
		}
		out = append(out, rune(s[i]))
	}
	in := 0
	if b > 0 {
		in = b + 1
	}
	n, i, bias := 128, 0, 72
	for in < len(s) {
		old, w := i, 1
		for k := urlPunyBase; ; k += urlPunyBase {
			if in >= len(s) {
				return "", false
			}
			c := s[in] | 0x20
			in++
			var d int
			switch {
			case c >= 'a' && c <= 'z':
				d = int(c - 'a')
			case s[in-1] >= '0' && s[in-1] <= '9':
				d = int(s[in-1]-'0') + 26
			default:
				return "", false
			}
			if d > (urlPunyMax-i)/w {
				return "", false
			}
			i += d * w
			t := urlPunyT(k, bias)
			if d < t {
				break
			}
			if w > urlPunyMax/(urlPunyBase-t) {
				return "", false
			}
			w *= urlPunyBase - t
		}
		bias = urlPunyAdapt(i-old, len(out)+1, old == 0)
		if i/(len(out)+1) > urlPunyMax-n {
			return "", false
		}
		n += i / (len(out) + 1)
		i %= len(out) + 1
		if n > 0x10FFFF || n >= 0xD800 && n <= 0xDFFF {
			return "", false
		}
		out = append(out[:i], append([]rune{rune(n)}, out[i:]...)...)
		i++
	}
	return string(out), true
}

// urlPunyEncodeModel is RFC 3492 section 6.3.
func urlPunyEncodeModel(s string) (string, bool) {
	in := []rune(s)
	var out []byte
	for _, c := range in {
		if c < 0x80 {
			out = append(out, byte(c))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	n, delta, bias := 128, 0, 72
	for h < len(in) {
		m := 0x7FFFFFFF
		for _, c := range in {
			if int(c) >= n && int(c) < m {
				m = int(c)
			}
		}
		if m-n > (urlPunyMax-delta)/(h+1) {
			return "", false
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, c := range in {
			if int(c) < n {
				delta++
				if delta > urlPunyMax {
					return "", false
				}
			}
			if int(c) == n {
				q := delta
				for k := urlPunyBase; ; k += urlPunyBase {
					t := urlPunyT(k, bias)
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(urlPunyBase-t)))
					q = (q - t) / (urlPunyBase - t)
				}
				out = append(out, digit(q))
				bias = urlPunyAdapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out), true
}

// urlPunyAnswer is the driver's answer for a result.
func urlPunyAnswer(s string, ok bool) string {
	if !ok {
		return "ok none"
	}
	return "ok " + urlHex(s)
}

// TestStdUrl_PunycodeSamples: RFC 3492 section 7.1's samples both ways.
func TestStdUrl_PunycodeSamples(t *testing.T) {
	samples := [][2]string{
		{"ليهمابتكلموشعربي؟", "egbpdaj6bu4bxfgehfvwxn"},
		{"他们为什么不说中文", "ihqwcrb4cv8a8dqg056pqjye"},
		{"他們爲什麽不說中文", "ihqwctvzc91f659drss3x8bo0yb"},
		{"Pročprostěnemluvíčesky", "Proprostnemluvesky-uyb24dma41a"},
		{"למההםפשוטלאמדבריםעברית", "4dbcagdahymbxekheh6e0a7fei0b"},
		{"यहलोगहिन्दीक्योंनहींबोलसकतेहैं", "i1baa7eci9glrd9b2ae1bj0hfcgg6iyaf8o0a1dig0cd"},
		{"なぜみんな日本語を話してくれないのか", "n8jok5ay5dzabd5bym9f0cm5685rrjetr6pdxa"},
		{"3年B組金八先生", "3B-ww4c5e180e575a65lsy2b"},
		{"安室奈美恵-with-SUPER-MONKEYS", "-with-SUPER-MONKEYS-pc58ag80a8qai00g7n9n"},
		{"Hello-Another-Way-それぞれの場所", "Hello-Another-Way--fc4qua05auwb3674vfr0b"},
		{"ひとつ屋根の下2", "2-u9tlzr9756bt3uc0v"},
		{"MajiでKoiする5秒前", "MajiKoi5-783gue6qz075azm5e"},
		{"パフィーdeルンバ", "de-jg4avhby1noc0d"},
		{"そのスピードで", "d9juau41awczczp"},
		{"bücher", "bcher-kva"},
		{"ß", "zca"},
	}
	var records, wants []string
	for _, s := range samples {
		records = append(records, "pye "+urlHex(s[0]), "pyd "+urlHex(s[1]))
		wants = append(wants, "ok "+urlHex(s[1]), "ok "+urlHex(s[0]))
	}
	for _, mode := range urlModes {
		for i, a := range urlIdnaAsk(t, mode, records) {
			if a != wants[i] {
				t.Errorf("%s: %s: got %s, want %s", mode, records[i], a, wants[i])
			}
		}
	}
}

// TestStdUrl_PunycodeModel: random labels both ways against the Go model,
// random ASCII text decoded, and overflowing deltas refused.
func TestStdUrl_PunycodeModel(t *testing.T) {
	r := &urlRng{s: 0x9E3779B9}
	pools := [][]rune{
		[]rune("abcxyz019-"), []rune("äöüßéèñçøå"), []rune("中文日本語한국어"),
		[]rune("ليهمابتكلم"), {0x80, 0x7FF, 0x800, 0xFFFF, 0x10000, 0x10FFFF, 0x1F600},
	}
	var records, wants []string
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for k := 1 + r.intn(20); k > 0; k-- {
			p := pools[r.intn(len(pools))]
			b.WriteRune(p[r.intn(len(p))])
		}
		s := b.String()
		e, ok := urlPunyEncodeModel(s)
		records = append(records, "pye "+urlHex(s))
		wants = append(wants, urlPunyAnswer(e, ok))
		if ok {
			records = append(records, "pyd "+urlHex(e))
			wants = append(wants, urlPunyAnswer(urlPunyDecodeModel(e)))
		}
	}
	const digits = "abcdefghijklmnopqrstuvwxyz0123456789-ABZ"
	for i := 0; i < 3000; i++ {
		var b strings.Builder
		for k := r.intn(16); k > 0; k-- {
			b.WriteByte(digits[r.intn(len(digits))])
		}
		s := b.String()
		records = append(records, "pyd "+urlHex(s))
		wants = append(wants, urlPunyAnswer(urlPunyDecodeModel(s)))
	}
	for _, s := range []string{"99999999999999", "zzzzzzzzzzzz", "a-99999999", "-", "--", "a-", "-a", "xn--"} {
		records = append(records, "pyd "+urlHex(s))
		wants = append(wants, urlPunyAnswer(urlPunyDecodeModel(s)))
	}
	for _, mode := range urlModes {
		bad := 0
		for i, a := range urlIdnaAsk(t, mode, records) {
			if a != wants[i] {
				t.Errorf("%s: %s: got %s, want %s", mode, records[i], a, wants[i])
				bad++
				if bad == 10 {
					t.FailNow()
				}
			}
		}
	}
}

// TestStdUrl_PunycodeLong: a label of 8,000 code points, most distinct, both
// ways (quadratic work would be 6 * 10^7 steps a direction, against
// runNative's bound), checked by round trip.
func TestStdUrl_PunycodeLong(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 8000; i++ {
		c := rune(0x4E00 + (i*7919)%8000)
		if i%3 == 0 {
			c = rune('a' + i%26)
		}
		b.WriteRune(c)
	}
	s := b.String()
	a := urlIdnaAsk(t, "release", []string{"pye " + urlHex(s)})[0]
	enc, ok := strings.CutPrefix(a, "ok ")
	if !ok || enc == "none" {
		t.Fatalf("encode: %s", urlShortText(a))
	}
	e := urlUnhex(t, enc)
	if want, _ := urlPunyEncodeModel(s); e != want {
		t.Fatalf("encode differs from the model")
	}
	d := urlIdnaAsk(t, "release", []string{"pyd " + urlHex(e)})[0]
	if d != "ok "+urlHex(s) || !utf8.ValidString(s) {
		t.Fatalf("round trip: %s", urlShortText(d))
	}
}
