// Why: big.Parse reads untrusted text, so it must never panic and must
// agree with a model of its grammar on any input.
// What: FuzzBig_Parse.
// How: big.Parse, built --release with the std.big driver
// (std-big_harness_test.go), on any bytes, any base and any max_digits,
// against a Go model of Parse's grammar and error order over math/big's
// SetString. The driver must exit 0 (no panic) and print the model's line.
// The seeds are oracle data: math/big's Text of random values in every
// base (some past the split's 20,000 digits), and the error cases of
// TestSTDBIG_TextFormatParseMatchMathBig. go test runs the seeds; go test
// -fuzz FuzzBig_Parse -parallel 2 explores (at most 5 minutes a run).
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// bigParseModel is the driver's "parsehex" line for data, base and max:
// big.Parse's documented order (a base outside 2 to 36, no digits, more
// than max digits, a bad digit) over SetString's grammar for base 2 to 36.
func bigParseModel(data []byte, base, max int) string {
	if !utf8.Valid(data) {
		return "notutf8"
	}
	s := string(data)
	if base < 2 || base > 36 {
		return "ParseError"
	}
	digits := strings.TrimPrefix(strings.TrimPrefix(s, "+"), "-")
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		digits = s[1:]
	}
	if len(digits) == 0 {
		return "ParseError"
	}
	if len(digits) > max {
		return "TooLarge"
	}
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		v := 99
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case c >= 'a' && c <= 'z':
			v = int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			v = int(c-'A') + 10
		}
		if v >= base {
			return "ParseError"
		}
	}
	x, ok := new(big.Int).SetString(s, base)
	if !ok {
		return "model: SetString refused " + s
	}
	return bigInt(x)
}

func FuzzBig_Parse(f *testing.F) {
	g := newBigGen(6)
	for i := 0; i < 24; i++ {
		x := g.int(g.size() % 9)
		base := 2 + i%35
		f.Add([]byte(x.Text(base)), int16(base), int32(400))
	}
	for _, s := range []string{"", "+", "-", "12a", " 1", "1_0", "0x10", "--1", "+-1", "é", "\xff1", "9999999999999999999999"} {
		f.Add([]byte(s), int16(10), int32(100))
	}
	// Past ParseLeafDigits (20,000), so mutations reach the split path
	// (parsedc.mk) in bases that are not powers of two, and long texts in
	// bases that are, for the bit path.
	for i, base := range []int{10, 7, 3, 36, 16, 2, 32} {
		x := g.nat(1100 + 150*i)
		f.Add([]byte(x.Text(base)), int16(base), int32(100000))
	}
	f.Add([]byte(strings.Repeat("0", 20001)+"1"), int16(10), int32(100000))
	f.Add([]byte("123456"), int16(10), int32(5))
	f.Add([]byte("1"), int16(37), int32(5))
	f.Add([]byte("1"), int16(-1), int32(-5))
	dir, err := os.MkdirTemp("", "mk-big-fuzz")
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { os.RemoveAll(dir) })
	var once sync.Once
	var exe string
	f.Fuzz(func(t *testing.T, data []byte, base int16, max int32) {
		once.Do(func() {
			e, out, status := bigBuildIn(t, dir, bigDriver, "--release")
			if status != 0 {
				t.Fatalf("mk build: exit %d\n%s", status, out)
			}
			exe = e
		})
		if exe == "" {
			t.Skip("the driver did not build")
		}
		if len(data) > 1<<16 {
			data = data[:1<<16]
		}
		in := fmt.Sprintf("parsehex\n%d\n%d\n%x\n", base, max, data)
		want := bigParseModel(data, int(base), int(max))
		r := runNative(t, exe, in)
		if r.code != 0 {
			t.Fatalf("driver exit %d on %q (base %d, max %d)\n%s", r.code, data, base, max, r.stderr)
		}
		got := strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n")
		if got != want {
			t.Fatalf("%q (base %d, max %d):\ngot  %.300s\nwant %.300s", data, base, max, got, want)
		}
	})
}
