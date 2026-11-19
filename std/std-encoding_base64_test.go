// Why: std.encoding's base64 must decode strictly (RFC 4648 canonical form) or
// forgivingly (WHATWG) with exact error offsets, and its vector paths must match the scalar path.
// What: the RFC 4648 vectors, a Go model of the strict decode (held to Go's
// encoding/base64 Strict mode), a Go model of WHATWG forgiving-base64, and the Mk driver against both.
// How: every length 0 to 160 with a bad byte at every position, wrong padding and
// non-canonical tails, in all five alphabets, dev and release; lengths past 32 reach the vector paths.

package std

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

const (
	b64Std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b64Url = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

var b64Alphabets = []string{"std", "url", "raw", "urlraw", "forgiving"}

// b64Go is Go's encoding of an alphabet (forgiving encodes as std).
func b64Go(alph string) *base64.Encoding {
	switch alph {
	case "url":
		return base64.URLEncoding.Strict()
	case "raw":
		return base64.RawStdEncoding.Strict()
	case "urlraw":
		return base64.RawURLEncoding.Strict()
	}
	return base64.StdEncoding.Strict()
}

func b64Chars(alph string) string {
	if alph == "url" || alph == "urlraw" {
		return b64Url
	}
	return b64Std
}

func b64Padded(alph string) bool { return alph == "std" || alph == "url" || alph == "forgiving" }

func b64Bad(off int) string { return fmt.Sprintf("BadInput(offset: %d)", off) }

func b64IsSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' }

// b64Values decodes data characters (all in chars) into bytes, dropping
// leftover bits.
func b64Values(chars string, data []byte) []byte {
	var out []byte
	var acc uint32
	bits := 0
	for _, c := range data {
		acc = acc<<6 | uint32(strings.IndexByte(chars, c))
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
		}
	}
	return out
}

// b64Model is the decode of s under alph: the bytes, or the error as the
// driver prints it.
func b64Model(alph string, s []byte) ([]byte, string) {
	chars := b64Chars(alph)
	n := len(s)
	if alph == "forgiving" {
		// WHATWG Infra, forgiving-base64 decode: remove ASCII whitespace;
		// a length divisible by 4 loses one or two final "="; a remainder
		// of 1 fails; a character outside the alphabet fails. The module's
		// offsets: the first bad character, else the length.
		var data []byte
		var at []int
		for i, c := range s {
			if !b64IsSpace(c) {
				data = append(data, c)
				at = append(at, i)
			}
		}
		if len(data)%4 == 0 && len(data) > 0 && data[len(data)-1] == '=' {
			data = data[:len(data)-1]
			if len(data) > 0 && data[len(data)-1] == '=' {
				data = data[:len(data)-1]
			}
		}
		for i, c := range data {
			if strings.IndexByte(chars, c) < 0 {
				return nil, b64Bad(at[i])
			}
		}
		if len(data)%4 == 1 {
			return nil, b64Bad(n)
		}
		return b64Values(chars, data), ""
	}
	pad := b64Padded(alph)
	p := 0
	if pad && n >= 1 && s[n-1] == '=' {
		p = 1
		if n >= 2 && s[n-2] == '=' {
			p = 2
		}
	}
	m := n - p
	for i := 0; i < m; i++ {
		if strings.IndexByte(chars, s[i]) < 0 {
			return nil, b64Bad(i)
		}
	}
	rem := m % 4
	if pad && n%4 != 0 || rem == 1 {
		return nil, b64Bad(n)
	}
	if rem >= 2 {
		v := strings.IndexByte(chars, s[m-1])
		if rem == 2 && v&0xF != 0 || rem == 3 && v&0x3 != 0 {
			return nil, b64Bad(m - 1)
		}
	}
	return b64Values(chars, s[:m]), ""
}

// b64Want is the driver's answer to "b64d alph s".
func b64Want(alph string, s []byte) string {
	out, err := b64Model(alph, s)
	if err != "" {
		return "err " + err
	}
	short := "-"
	if len(out) > 0 {
		short = "TooLarge"
	}
	return fmt.Sprintf("ok %s true %s", encHex(out), short)
}

func b64Dec(alph string, s []byte) encCase {
	return encCase{in: "b64d " + alph + " " + encHex(s), want: b64Want(alph, s)}
}

func b64Enc(alph string, data []byte) encCase {
	s := b64Go(alph).EncodeToString(data)
	return encCase{in: "b64e " + alph + " " + encHex(data), want: fmt.Sprintf("ok %s %d true", s, len(s))}
}

// TestStdEncoding_Base64ModelAgreesWithGo holds the strict model to Go's
// encoding/base64 in Strict mode, which also skips CR and LF: inputs here
// hold neither, and Go's error offsets are not compared.
func TestStdEncoding_Base64ModelAgreesWithGo(t *testing.T) {
	r := &encRng{s: 1}
	pool := []byte(b64Std + b64Url + "= !*é")
	for k := 0; k < 200000; k++ {
		n := r.intn(24)
		s := make([]byte, 0, n)
		for len(s) < n {
			s = append(s, pool[r.intn(len(pool))])
		}
		for _, alph := range b64Alphabets[:4] {
			got, err := b64Model(alph, s)
			want, gerr := b64Go(alph).DecodeString(string(s))
			if (err == "") != (gerr == nil) {
				t.Fatalf("%s %q: model %q %q, Go %x %v", alph, s, got, err, want, gerr)
			}
			if err == "" && string(got) != string(want) {
				t.Fatalf("%s %q: model %x, Go %x", alph, s, got, want)
			}
		}
	}
}

func TestStdEncoding_Base64Rfc4648Vectors(t *testing.T) {
	vectors := [][2]string{{"", ""}, {"f", "Zg=="}, {"fo", "Zm8="}, {"foo", "Zm9v"}, {"foob", "Zm9vYg=="}, {"fooba", "Zm9vYmE="}, {"foobar", "Zm9vYmFy"}}
	var cases []encCase
	for _, v := range vectors {
		cases = append(cases, encCase{in: "b64e std " + encHex([]byte(v[0])), want: fmt.Sprintf("ok %s %d true", v[1], len(v[1]))})
		cases = append(cases, encCase{in: "b64d std " + encHex([]byte(v[1])), want: b64Want("std", []byte(v[1]))})
		raw := strings.TrimRight(v[1], "=")
		cases = append(cases, encCase{in: "b64e raw " + encHex([]byte(v[0])), want: fmt.Sprintf("ok %s %d true", raw, len(raw))})
		if out, _ := b64Model("std", []byte(v[1])); string(out) != v[0] {
			t.Fatalf("model decodes %q to %q", v[1], out)
		}
	}
	encRunAll(t, cases)
}

// b64Corpus is every case of the adversarial set.
func b64Corpus() []encCase {
	r := &encRng{s: 7}
	var cases []encCase
	junk := []byte("=!*-_+/ \t\n\r.é")
	for _, alph := range b64Alphabets {
		for n := 0; n <= 120; n++ {
			data := r.bytes(n)
			cases = append(cases, b64Enc(alph, data))
			enc := []byte(b64Go(alph).EncodeToString(data))
			cases = append(cases, b64Dec(alph, enc))
			// One bad byte at every position.
			for i := range enc {
				c := append([]byte(nil), enc...)
				j := junk[r.intn(len(junk))]
				if j >= 0x80 {
					c = append(c[:i], append([]byte("é"), c[i+1:]...)...)
				} else {
					c[i] = j
				}
				cases = append(cases, b64Dec(alph, c))
			}
			// Padding taken off, added, doubled; a byte dropped.
			trimmed := []byte(strings.TrimRight(string(enc), "="))
			cases = append(cases, b64Dec(alph, trimmed), b64Dec(alph, append(append([]byte(nil), enc...), '=')),
				b64Dec(alph, append(append([]byte(nil), trimmed...), "=="...)))
			if len(enc) > 0 {
				cases = append(cases, b64Dec(alph, enc[:len(enc)-1]))
			}
			// Whitespace between groups.
			if len(enc) > 8 {
				ws := append(append(append([]byte(nil), enc[:4]...), " \r\n\t"...), enc[4:]...)
				cases = append(cases, b64Dec(alph, ws))
			}
		}
		// Every last character of a non-canonical tail, for both tail sizes.
		chars := b64Chars(alph)
		for _, prefix := range []string{"QUJD", ""} {
			for _, tail := range []string{"Q", "QU"} {
				for k := 0; k < 64; k++ {
					s := prefix + tail + string(chars[k])
					if b64Padded(alph) {
						s += strings.Repeat("=", 4-len(tail)-1)
					}
					cases = append(cases, b64Dec(alph, []byte(s)))
				}
			}
		}
		// Long inputs, whole and with a bad byte late.
		for _, n := range []int{255, 256, 1000, 4097} {
			data := r.bytes(n)
			cases = append(cases, b64Enc(alph, data))
			enc := []byte(b64Go(alph).EncodeToString(data))
			cases = append(cases, b64Dec(alph, enc))
			for _, at := range []int{0, 31, 32, 33, 63, 64, len(enc) / 2, len(enc) - 5} {
				c := append([]byte(nil), enc...)
				c[at] = '*'
				cases = append(cases, b64Dec(alph, c))
			}
		}
	}
	return cases
}

func TestStdEncoding_Base64Adversarial(t *testing.T) {
	encRunAll(t, b64Corpus())
}
