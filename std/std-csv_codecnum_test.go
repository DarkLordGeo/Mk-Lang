// Why: the codec's number fields must read and write exactly as Go's strconv
// does, at every width and at each type's edges.
// What: TestSTDCSV_CodecNumbersMatchStrconv and TestSTDCSV_CodecF32.
// How: seeded random values and edge values per type, judged by strconv;
// floats compare by their bits.

package std

// Gen csv.Codec's emitted FromCsv and WriteCsv over every
// number type the first codec test leaves out (i8, u16, u32, i64, uint, f32,
// ?f32), ?bool and ?f64, on seeded random values and each type's edges,
// against Go's strconv as the oracle: a record reads when strconv reads
// every field in its type's range (ParseInt and ParseUint at the type's
// width, ParseFloat at 64 and 32 bits), else it is ParseError; what
// WriteCsv writes reads back, by strconv, to the same value, floats by
// their bits.

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

const csvCodecNumTypes = `import std.csv

struct Row2 {
    export a: i8
    export b: u16
    export c: u32
    export d: i64
    export e: uint
    export f: ?bool
    export g: ?f64
    export h: f32
    export k: ?f32
}

gen csv.Codec(Row2)
`

// csvNumField is one field's text and what the oracle makes of it.
type csvNumField struct {
	text string
	bad  bool
}

// csvIntText is a random text for an integer of bits bits (signed or
// not): a random value or an edge, or with fault a text that is likely
// outside the type (one past an edge, a sign on an unsigned type, a
// fraction, hex, empty), which the oracle then judges.
func csvIntText(r *rand.Rand, bits int, signed, fault bool) string {
	var lo, hi int64
	var uhi uint64
	if signed {
		hi = int64(1)<<(bits-1) - 1
		lo = -hi - 1
	} else {
		uhi = uint64(1)<<bits - 1
		if bits == 64 {
			uhi = math.MaxUint64
		}
	}
	if fault {
		switch r.Intn(7) {
		case 0: // one past the top
			if signed {
				if bits == 64 {
					return "9223372036854775808"
				}
				return strconv.FormatInt(hi+1, 10)
			}
			if bits == 64 {
				return "18446744073709551616"
			}
			return strconv.FormatUint(uhi+1, 10)
		case 1: // one below the bottom
			if signed {
				if bits == 64 {
					return "-9223372036854775809"
				}
				return strconv.FormatInt(lo-1, 10)
			}
			return "-1"
		case 2:
			return "-0"
		case 3:
			return "+1"
		case 4:
			return "1.5"
		case 5:
			return "0x10"
		default:
			return ""
		}
	}
	switch r.Intn(6) {
	case 0:
		if signed {
			return strconv.FormatInt(lo, 10)
		}
		return "0"
	case 1:
		if signed {
			return strconv.FormatInt(hi, 10)
		}
		return strconv.FormatUint(uhi, 10)
	default:
		if signed {
			v := r.Int63n(hi) * int64(1-2*r.Intn(2))
			if bits == 64 {
				v = int64(r.Uint64())
			}
			return strconv.FormatInt(v, 10)
		}
		if bits == 64 {
			return strconv.FormatUint(r.Uint64(), 10)
		}
		return strconv.FormatUint(uint64(r.Int63n(int64(uhi)+1)), 10)
	}
}

// csvIntOk is the oracle for an integer field.
func csvIntOk(s string, bits int, signed bool) bool {
	if signed {
		_, err := strconv.ParseInt(s, 10, bits)
		return err == nil
	}
	_, err := strconv.ParseUint(s, 10, bits)
	return err == nil
}

// csvFloatText is a random decimal text (Go's hexadecimal floats, which
// strconv reads and str.ParseFloat does not, are outside the codec's text
// form) for a float of bits bits: a shortest
// form, a long expansion, an f32 midpoint (exact, or a hair above), NaN,
// the infinities, -0, subnormals; with fault, a value near or past the
// type's range or a text that is not a number, which the oracle judges.
func csvFloatText(r *rand.Rand, bits int, fault bool) string {
	if fault {
		return []string{"1e400", "-1e400", "3.4028235e38", "3.4028236e38", "3.40282356779733661637539395458142568448e38",
			"-3.40282356779733661637539395458142568448e38", "1.7976931348623157e308", "1e", "x", ""}[r.Intn(10)]
	}
	switch r.Intn(12) {
	case 0:
		return "nan"
	case 1:
		return []string{"inf", "-inf"}[r.Intn(2)]
	case 2:
		return "-0"
	case 3:
		return []string{"1e-310", "4.9e-324", "1.401298464324817e-45", "7e-46", "7.006492321624085e-46", "1.1754942e-38", "1e-50"}[r.Intn(7)]
	case 4, 5:
		if bits == 32 {
			// An exact midpoint of two adjacent f32 values, or a hair above.
			a := math.Float32frombits(r.Uint32() &^ (1 << 31) % 0x7f7fffff)
			b := math.Nextafter32(a, float32(math.Inf(1)))
			m := (float64(a) + float64(b)) / 2
			s := strconv.FormatFloat(m, 'e', 200, 64)
			if r.Intn(2) == 0 {
				i := strings.IndexByte(s, 'e')
				s = s[:i] + "1" + s[i:]
			}
			if r.Intn(2) == 0 {
				s = "-" + s
			}
			return s
		}
		return strconv.FormatFloat(math.Float64frombits(r.Uint64()), 'e', 40, 64)
	default:
		if bits == 32 {
			f := math.Float32frombits(r.Uint32())
			if r.Intn(3) == 0 {
				return strconv.FormatFloat(float64(f), 'e', 20, 64)
			}
			return strconv.FormatFloat(float64(f), 'g', -1, 32)
		}
		return strconv.FormatFloat(math.Float64frombits(r.Uint64()), 'g', -1, 64)
	}
}

// csvFloatOk is the oracle for a float field: strconv reads it with no
// error (an overflow is ErrRange) or it is nan or an infinity.
func csvFloatOk(s string, bits int) bool {
	_, err := strconv.ParseFloat(s, bits)
	return err == nil
}

// csvSameFloat reports whether got, read as a float of bits bits, is the
// value want reads as: the same bits, or both NaN.
func csvSameFloat(got, want string, bits int) bool {
	x, e1 := strconv.ParseFloat(got, bits)
	y, e2 := strconv.ParseFloat(want, bits)
	if e1 != nil || e2 != nil {
		return false
	}
	if math.IsNaN(x) || math.IsNaN(y) {
		return math.IsNaN(x) && math.IsNaN(y)
	}
	if bits == 32 {
		return math.Float32bits(float32(x)) == math.Float32bits(float32(y))
	}
	return math.Float64bits(x) == math.Float64bits(y)
}

func TestSTDCSV_CodecNumbersMatchStrconv(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	type spec struct {
		bits     int
		signed   bool
		float    bool
		optional bool
		boolean  bool
	}
	specs := []spec{{8, true, false, false, false}, {16, false, false, false, false}, {32, false, false, false, false},
		{64, true, false, false, false}, {64, false, false, false, false}, {0, false, false, true, true},
		{64, false, true, true, false}, {32, false, true, false, false}, {32, false, true, true, false}}
	var in strings.Builder
	var rows [][]csvNumField
	for k := 0; k < 3000; k++ {
		// A third of the records carry one fault field.
		faulty := -1
		if r.Intn(3) == 0 {
			faulty = r.Intn(len(specs))
		}
		var row []csvNumField
		for i, sp := range specs {
			fault := i == faulty
			var f csvNumField
			switch {
			case sp.optional && !fault && r.Intn(6) == 0:
				f = csvNumField{text: ""}
			case sp.boolean:
				f.text = []string{"true", "false"}[r.Intn(2)]
				if fault {
					f.text = []string{"True", "1", "t", "yes"}[r.Intn(4)]
				}
				f.bad = f.text != "true" && f.text != "false" && f.text != ""
			case sp.float:
				f.text = csvFloatText(r, sp.bits, fault)
				f.bad = !(sp.optional && f.text == "") && !csvFloatOk(f.text, sp.bits)
			default:
				f.text = csvIntText(r, sp.bits, sp.signed, fault)
				f.bad = !csvIntOk(f.text, sp.bits, sp.signed)
			}
			row = append(row, f)
		}
		rows = append(rows, row)
		for i, f := range row {
			if i > 0 {
				in.WriteByte(',')
			}
			in.WriteString(f.text)
		}
		in.WriteString("\r\n")
	}
	main := strings.ReplaceAll(csvCodecMain, "Row.", "Row2.")
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := csvBuild(t, csvCodecNumTypes+main, flags...)
			res := runNative(t, exe, in.String())
			if res.code != 0 {
				t.Fatalf("exit %d\n%s", res.code, res.stderr)
			}
			lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(res.stdout, "\r\n", "\n"), "\n"), "\n")
			if len(lines) != len(rows)+1 || lines[0] != "a,b,c,d,e,f,g,h,k" {
				t.Fatalf("%d lines for %d records; header %q", len(lines), len(rows), lines[0])
			}
			bad, parsed := 0, 0
			for k, row := range rows {
				got := lines[k+1]
				wantBad := false
				for _, f := range row {
					wantBad = wantBad || f.bad
				}
				var why string
				if wantBad {
					parsed++
					if got != fmt.Sprintf("parse %d", k+1) {
						why = "want ParseError"
					}
				} else {
					out := strings.Split(got, ",")
					if len(out) != len(row) {
						why = "field count"
					} else {
						for i, sp := range specs {
							w, g := row[i].text, out[i]
							ok := true
							switch {
							case w == "" || sp.boolean:
								ok = g == w
							case sp.float:
								ok = csvSameFloat(g, w, sp.bits)
							case sp.signed:
								x, e1 := strconv.ParseInt(g, 10, sp.bits)
								y, _ := strconv.ParseInt(w, 10, sp.bits)
								ok = e1 == nil && x == y
							default:
								x, e1 := strconv.ParseUint(g, 10, sp.bits)
								y, _ := strconv.ParseUint(w, 10, sp.bits)
								ok = e1 == nil && x == y
							}
							if !ok {
								why = fmt.Sprintf("field %d: wrote %q for %q", i, g, w)
								break
							}
						}
					}
				}
				if why != "" {
					bad++
					if bad <= 10 {
						t.Errorf("record %d %q: got %q: %s", k+1, csvRowText(row), got, why)
					}
				}
			}
			t.Logf("%d records, %d ParseError by the oracle, %d differ", len(rows), parsed, bad)
		})
	}
}

func csvRowText(row []csvNumField) string {
	var parts []string
	for _, f := range row {
		parts = append(parts, f.text)
	}
	return strings.Join(parts, ",")
}

// TestSTDCSV_CodecF32: an f32 field reads
// to the correctly rounded f32 of its text, as strconv.ParseFloat(s, 32),
// at the cases that tell a correct reader from Cast[f32] of the f64: an
// exact midpoint (ties to even) and a hair above it, f32's rounding edge
// to infinity, the smallest subnormal; and nan, inf, -inf and -0 read as
// themselves. What WriteCsv writes reads back to the same bits.
func TestSTDCSV_CodecF32(t *testing.T) {
	src := `import std.csv

struct F {
    export x: f32
}

gen csv.Codec(F)
` + strings.ReplaceAll(csvCodecMain, "Row.", "F.")
	cases := []string{"nan", "inf", "-inf", "-0", "0.1", "3.4028234663852886e38",
		"3.40282356779733661637539395458142568448e38", "3.40282356779733661637539395458142568447e38",
		"1.000000059604644775390625", "1.0000000596046447753906251", "1.000000178813934326171875",
		"1e-45", "7e-46", "1e39"}
	exe := csvBuild(t, src)
	res := runNative(t, exe, strings.Join(cases, "\r\n")+"\r\n")
	if res.code != 0 {
		t.Fatalf("exit %d\n%s", res.code, res.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(res.stdout, "\r\n", "\n"), "\n"), "\n")
	if len(lines) != len(cases)+1 || lines[0] != "x" {
		t.Fatalf("output %q", res.stdout)
	}
	for k, c := range cases {
		got := lines[k+1]
		if !csvFloatOk(c, 32) {
			if got != fmt.Sprintf("parse %d", k+1) {
				t.Errorf("%q: got %q, want ParseError (strconv: out of range)", c, got)
			}
			continue
		}
		if !csvSameFloat(got, c, 32) {
			w, _ := strconv.ParseFloat(c, 32)
			t.Errorf("%q: wrote %q, want the f32 %v", c, got, float32(w))
		}
	}
}
