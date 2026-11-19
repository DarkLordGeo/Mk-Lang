// Why: a float parser must accept exactly its grammar's longest prefix and
// round that prefix correctly; Go's strconv is a trusted reference.
// What: TestStd_ParseFloatPrefixMatchesGo, str.ParseFloatPrefix against Go.
// How: a regular expression finds the expected prefix by brute force, so the
// oracle shares nothing with the Mk scanner; strconv.ParseFloat gives the value.

package std

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var std10FloatGrammar = regexp.MustCompile(`^(?:[+-]?(?:(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?|(?i:inf|infinity))|(?i:nan))$`)

// std10Prefix is the oracle: the longest accepted prefix and its value.
func std10Prefix(s string) (uint64, int) {
	for k := len(s); k > 0; k-- {
		if std10FloatGrammar.MatchString(s[:k]) {
			v, _ := strconv.ParseFloat(s[:k], 64)
			if math.IsNaN(v) {
				return 0x7FF8000000000000, k
			}
			return math.Float64bits(v), k
		}
	}
	return 0x7FF8000000000000, 0
}

func std10ParseInputs() []string {
	r := rand.New(rand.NewSource(91))
	alpha := "0123456789000.....eeEE++--iInNfFtTyYaAx_ :é"
	var in []string
	pick := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			// The alphabet holds one two-byte scalar; pick by scalar.
			rs := []rune(alpha)
			b.WriteRune(rs[r.Intn(len(rs))])
		}
		return b.String()
	}
	for i := 0; i < 60000; i++ {
		in = append(in, pick(r.Intn(16)))
	}
	fmts := []byte{'e', 'f', 'g', 'E'}
	for i := 0; i < 60000; i++ {
		var x float64
		switch i % 3 {
		case 0:
			x = math.Float64frombits(r.Uint64())
		case 1:
			x = r.NormFloat64() * math.Pow(10, float64(r.Intn(40)-20))
		default:
			x = float64(r.Int63n(1<<53)) / math.Pow(2, float64(r.Intn(60)))
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			x = 1.5
		}
		s := strconv.FormatFloat(x, fmts[r.Intn(len(fmts))], r.Intn(30)-1, 64)
		if r.Intn(4) == 0 && s[0] != '-' {
			s = "+" + s
		}
		in = append(in, s+pick(r.Intn(4)))
	}
	for i := 0; i < 3000; i++ {
		n := 20 + r.Intn(800)
		var b strings.Builder
		if r.Intn(2) == 0 {
			b.WriteString("-")
		}
		for j := 0; j < n; j++ {
			b.WriteByte(byte('0' + r.Intn(10)))
			if j == n/3 && r.Intn(2) == 0 {
				b.WriteByte('.')
			}
		}
		if r.Intn(2) == 0 {
			fmt.Fprintf(&b, "e%d", r.Intn(700)-350)
		}
		in = append(in, b.String()+pick(r.Intn(3)))
	}
	// Ties at 19 and more digits: the halfway points between neighbours.
	for i := 0; i < 2000; i++ {
		x := math.Float64frombits(r.Uint64() >> 2)
		h := new(strings.Builder)
		h.WriteString(strconv.FormatFloat(x, 'e', 40, 64))
		in = append(in, h.String()+"x")
	}
	in = append(in, "1e400", "-1e400", "1e309x", "1.7976931348623157e308", "1.7976931348623159e308",
		"1e-400", "-1e-400", "4.9406564584124654e-324", "2.4703282292062327e-324", "2.4703282292062328e-324",
		"1e99999999999999999999", "0e99999999999", "-0", "+0.e5", "0x10", "1_000", "", " 1", "\t1")
	for _, s := range []string{"+1.5e-10x", "-infinityy", "INFINITY", "Infinity", "nan", "NaN1", "-nan", "+inf",
		"-.5e+3.", "00012.0034E-0005e5", "9007199254740993", "0.1e1e1", "123456789012345678901234567890e-10"} {
		for k := 0; k <= len(s); k++ {
			in = append(in, s[:k])
		}
	}
	return in
}

const std10ParseProgram = `export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var w := sys.Stdout()
    var inp := sys.Stdin()
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for true {
        lo := try inp.Line(&ar)
        line := lo else {
            break
        }
        // Each line is one input, quoted as hex so any byte crosses.
        r := Unhex(&ar, line).ParseFloatPrefix()
        try Print(mut w, "{u} {b}\n", (u: r.used, b: r.value.Bits()))
        ar.Reset()
    }
    try w.Flush()
}

Unhex(a: &Arena, h: str): str from a {
    b := h.AsBytes()
    out := a.Slice[u8](b.Len() / 2) else {
        return ""
    }
    for i in 0..b.Len() / 2 {
        out[i] = Nib(b[2 * i]) * 16 + Nib(b[2 * i + 1])
    }
    return str.From(out[..]) else {
        ""
    }
}

Nib(c: u8): u8 {
    if c >= 0x61 {
        return c - 0x61 + 10
    }
    return c - 0x30
}
`

// TestStd_ParseFloatPrefixMatchesGo: str.ParseFloatPrefix against Go. For
// each input the expected length is found by brute force, the longest prefix
// that std's ParseFloat grammar accepts, tried from the whole text down with a
// regular expression (std10FloatGrammar), so it shares nothing with the Mk
// scanner; the expected value is strconv.ParseFloat(prefix, 64), which gives
// an infinity on overflow as ParseFloatPrefix does, and NaN when no prefix is
// a float. Bits are compared exactly (the canonical NaN).
//
// Inputs (std10ParseInputs): random strings over the grammar's bytes and a
// few that end it; strconv's own forms of random values ('e', 'f', 'g', any
// precision, both signs) with a random tail; long digit runs (20 to 820
// significant digits, the multiprecision path) with a tail; overflow and
// underflow texts; and every prefix of a fixed set. Dev and release.
func TestStd_ParseFloatPrefixMatchesGo(t *testing.T) {
	in := std10ParseInputs()
	var feed bytes.Buffer
	for _, s := range in {
		fmt.Fprintf(&feed, "%x\n", s)
	}
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			var flags []string
			if prof == "release" {
				flags = append(flags, "--release")
			}
			exe := buildNative(t, nil, std10ParseProgram, flags...)
			inFile := filepath.Join(t.TempDir(), "in.txt")
			if err := os.WriteFile(inFile, feed.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(inFile)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx, cancel := context.WithTimeout(context.Background(), std10RunTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe)
			cmd.Stdin = f
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v\n%s", err, errb.String())
			}
			got := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(got) != len(in) {
				t.Fatalf("%d answers for %d inputs", len(got), len(in))
			}
			bad := 0
			for i, s := range in {
				wb, wu := std10Prefix(s)
				want := fmt.Sprintf("%d %d", wu, wb)
				if strings.TrimRight(got[i], "\r") != want {
					bad++
					if bad <= 20 {
						t.Errorf("%q: got %q, want %q", s, got[i], want)
					}
				}
			}
			if bad > 0 {
				t.Fatalf("%d of %d inputs differ", bad, len(in))
			}
			t.Logf("%s: %d inputs equal to strconv.ParseFloat on the longest grammar prefix", prof, len(in))
		})
	}
}
