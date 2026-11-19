// Why: std.encoding's decoders take untrusted bytes, so any input must give the model's answer.
// What: fuzz targets for base64, hex, Encoding.Decode (DecodeStrict, split Decoder), DER, ParsePem and PercentDecode.
// How: the release driver against its Go model on any bytes; a panic, a hang (runNative's bound) or a
// wrong answer fails. Seeds come from the tests' corpora; explore with go test -fuzz FuzzStdEncoding_<name> -parallel 2.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"encoding/pem"
	"strings"
	"testing"
	"unicode/utf8"
)

// encFuzzOne runs one record through the release driver and compares.
func encFuzzOne(t *testing.T, c encCase) {
	t.Helper()
	exe := encExe(t, "release")
	r := runNative(t, exe, c.in+"\n")
	got := strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n")
	if r.code != 0 || got != c.want {
		t.Fatalf("%s\n got  %s (exit %d, stderr %s)\n want %s", encShort(c.in), encShort(got), r.code, r.stderr, encShort(c.want))
	}
}

func FuzzStdEncoding_Base64(f *testing.F) {
	for i, c := range b64Corpus() {
		if i%211 == 0 && strings.HasPrefix(c.in, "b64d ") {
			parts := strings.Fields(c.in)
			f.Add(cryptoUnhexF(parts[2]), uint8(i))
		}
	}
	f.Fuzz(func(t *testing.T, s []byte, alph uint8) {
		if len(s) > 4096 || !utf8.Valid(s) {
			return
		}
		a := b64Alphabets[int(alph)%len(b64Alphabets)]
		encFuzzOne(t, b64Dec(a, s))
	})
}

func FuzzStdEncoding_Hex(f *testing.F) {
	for _, s := range []string{"", "0", "00", "0g", "deadBEEF", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff0", "zz"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, s []byte) {
		if len(s) > 4096 || !utf8.Valid(s) {
			return
		}
		encFuzzOne(t, encCase{in: "hexd " + encHex(s), want: hexModel(s)})
	})
}

func FuzzStdEncoding_Decode(f *testing.F) {
	r := &encRng{s: 31}
	for k := 0; k < 40; k++ {
		f.Add(encFuzzBytes(r, r.intn(40)), uint8(k), uint8(r.next()))
	}
	// textdecoder-fatal's UTF-8 inputs and the UTF-16 surrogate cases.
	for _, h := range []string{"ff", "c0", "e0", "c080", "e08080", "f0808080", "c1bf", "e09fbf", "f08fbfbf", "eda080", "edb080", "eda080edb080", "f09f41", "f041f0", "efbbbf41"} {
		f.Add(cryptoUnhexF(h), uint8(0), uint8(1))
	}
	for _, h := range []string{"00d8", "00dc", "00d80000", "00dc00d8", "fffe3dd800de"} {
		f.Add(cryptoUnhexF(h), uint8(1), uint8(2))
	}
	// Long text past the block paths (utf8.mk's validated blocks from 64
	// bytes, single.mk's tables from 64 bytes), valid and with malformed
	// units, for every encoding.
	for k := 0; k < 33; k++ {
		b := encMixed(r, 60+r.intn(1500))
		if k%2 == 1 {
			b = encBreak(r, b, r.intn(len(b)+1))
		}
		f.Add(b, uint8(k), uint8(r.next()))
	}
	// UTF-16LE (which 1) and BE (which 2) of one-, two- and three-byte
	// scalars, some with surrogates, past utf16.mk's vector write (eight
	// units, 32 bytes of output left).
	for k := 0; k < 12; k++ {
		kinds := []string{"123", "3", "1233s", "123l"}[k%4]
		b := encUnitBytes(encUnits(r, 20+r.intn(200), kinds), k%2 == 1)
		f.Add(b, uint8(1+k%2), uint8(r.next()))
	}
	var models []encModel
	f.Fuzz(func(t *testing.T, b []byte, which uint8, cut uint8) {
		if models == nil {
			models = encModels(t)
		}
		if len(b) > 4096 {
			return
		}
		m := models[int(which)%len(models)]
		encFuzzOne(t, decCase(m, b))
		encFuzzOne(t, decsCase(m, b))
		encFuzzOne(t, pushCase(m, b, []int{int(cut) % (len(b) + 1), int(cut) % 3}))
	})
}

func FuzzStdEncoding_Der(f *testing.F) {
	r := &encRng{s: 37}
	for k := 0; k < 40; k++ {
		f.Add(derRandom(r, 3))
	}
	// The pinned certificates, so the fuzzer starts from
	// the elements ReadTlv's short form reads (std-encoding_derfast_test.go).
	derPinnedSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			return
		}
		encFuzzOne(t, derCase(b))
	})
}

func FuzzStdEncoding_Pem(f *testing.F) {
	f.Add([]byte("-----BEGIN X-----\nQUJD\n-----END X-----\n"))
	f.Add([]byte("text\r\n-----BEGIN A B-----\r\nQQ==\r\n-----END A B-----\r\nmore"))
	f.Add([]byte("-----BEGIN X-----\nK: v\n\nQQ==\n-----END X-----\n"))
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnop")}))
	f.Add(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: nil}))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 || !utf8.Valid(b) {
			return
		}
		encFuzzOne(t, pemCase(b))
	})
}

// FuzzStdEncoding_Percent: PercentDecode of any UTF-8 text against the URL
// Standard model, and PercentEncode in the fuzzed set followed by
// PercentDecode of any bytes (pctRoundTrip). Seeded from the Percent
// test's decode edge cases and long mixed strings.
func FuzzStdEncoding_Percent(f *testing.F) {
	for _, s := range []string{"", "%", "%4", "%41", "%%41", "%4%41", "%zz", "a%2fb", "%C3%A9", "100%", "+", "%2B%2b", "x%A", "%%%"} {
		f.Add([]byte(s), uint8(len(s)))
	}
	r := &encRng{s: 61}
	pool := []byte("%%%0aF9g/?#&= +~é\x00\x7f")
	for k := 0; k < 12; k++ {
		n := 40 + r.intn(400)
		s := make([]byte, 0, n)
		for len(s) < n {
			s = append(s, pool[r.intn(len(pool))])
		}
		f.Add(s, uint8(k))
	}
	f.Fuzz(func(t *testing.T, s []byte, set uint8) {
		if len(s) > 4096 {
			return
		}
		if utf8.Valid(s) {
			encFuzzOne(t, encCase{in: "pctd " + encHex(s), want: "ok " + encHex(pctDecodeModel(s))})
		}
		encFuzzOne(t, pctRoundTrip(int(set)%len(pctSets), s))
	})
}

// cryptoUnhexF decodes hex the tests wrote themselves.
func cryptoUnhexF(s string) []byte {
	var out []byte
	for i := 0; i+1 < len(s); i += 2 {
		var v byte
		for _, c := range s[i : i+2] {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= byte(c - '0')
			default:
				v |= byte(c-'a') + 10
			}
		}
		out = append(out, v)
	}
	return out
}
