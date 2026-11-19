// Why: csv.Records must split CSV exactly as Go's encoding/csv does, under
// every option, and a small buffer must not change the records it returns.
// What: TestSTDCSV_RecordsMatchEncodingCsv, SmallBufferSameRecords and
// Csv6_OptionsMatchModel.
// How: adversarial inputs, seeded byte soups and seeded valid CSV, at dev and
// release with four delimiters, against encoding/csv; the same inputs through
// a small buffer must give the large buffer's records or stop at a TooLarge.

package std

import (
	"encoding/csv"
	"math"
	"math/rand"
	"strings"
	"testing"
)

// csvAdversarial are the edge inputs: line breaks of every kind at every
// place, quotes in every position, the end of input inside each state,
// blank lines, and bytes that are not UTF-8.
var csvAdversarial = []string{
	"", ",", ",,", "\n", "\r", "\r\n", "\n\n\n", "\r\n\r\n", "\r\r\n", "\n\r", "a", "a\n", "a\r", "a\r\n", "a\r\r\n",
	"a,b,c\n1,2,3\n", "a,b,c\n1,2,3", "a,b\r\nc,d\r\n", "a,\n", "a,\r\n", ",a", ",\n,", "a\rb,c\n", "a\r,b",
	`""`, `"",""`, `""` + "\n", `"a"`, `"a"` + "\n", `"a"` + "\r", `"a"` + "\r\n", `"a""b"`, `"a""""b"`, `""""`, `""""""`,
	`"a,b",c`, "\"a\nb\",c", "\"a\r\nb\",c", "\"a\rb\",c", "\"a\r\r\nb\"", "\"\r\n\"", "\"\n\"", "\"\n\n\"\n",
	`"`, `""a`, `"a`, "\"a\n", "\"a\r\n", "\"a\r", "\"a\n\r", "\"a\nb", "\"a\n\n", `"a"b`, `"a" `, `"a"x,b`, "\"a\"\rb", "\"a\"\r\r\n",
	`a"b`, `a""`, `ab"`, `a,b"c`, ` "a"`, "x,\"y\nz\",\"q\"\"\"\n", "\"q\"\"\"x",
	"\n\na,b\n\n\nc\n", "\r\n\r\na\r\n", " \n", "\t\n", "a,b\n\n", "\"\"\n\n\"\"",
	"\xff", "a,\xff,b", "\"\xc3\xa9\",\xc3\xa9", "\xc3", "\xc3,\xa9", "\"\xe2\x82\",x",
	strings.Repeat("a,", 300) + "\n", strings.Repeat("\"\"\"\"", 100), strings.Repeat("\"a\"\"b\",", 50) + "\n",
	strings.Repeat("x", 3000) + "\n" + strings.Repeat("y", 3000),
	"\"" + strings.Repeat("ab\r\n", 500) + "\",z\n",
}

// csvSoup is n random inputs of up to tokens pieces over the bytes that
// matter to the grammar,
// with comma as the delimiter.
func csvSoup(seed int64, n int, comma byte, tokens int) []string {
	r := rand.New(rand.NewSource(seed))
	alpha := []string{"a", "b", " ", string(comma), string(comma), "\"", "\"", "\"\"", "\r", "\n", "\r\n", "\xc3\xa9", "\xff", "x"}
	var out []string
	for i := 0; i < n; i++ {
		var sb strings.Builder
		k := r.Intn(tokens)
		for j := 0; j < k; j++ {
			sb.WriteString(alpha[r.Intn(len(alpha))])
		}
		out = append(out, sb.String())
	}
	return out
}

// csvValid is n random valid documents of fewer than records records,
// written by encoding/csv's Writer,
// with CR LF or LF line breaks, from fields that hold the delimiter,
// quotes, line breaks and UTF-8.
func csvValid(seed int64, n int, comma byte, records int) []string {
	r := rand.New(rand.NewSource(seed))
	alpha := []string{"a", "bc", " ", string(comma), "\"", "\n", "\r\n", "\xc3\xa9", "\xe2\x82\xac", "0", "-1.5"}
	var out []string
	for i := 0; i < n; i++ {
		var sb strings.Builder
		w := csv.NewWriter(&sb)
		w.Comma = rune(comma)
		w.UseCRLF = r.Intn(2) == 0
		recs := r.Intn(records)
		for k := 0; k < recs; k++ {
			nf := 1 + r.Intn(5)
			var rec []string
			for f := 0; f < nf; f++ {
				var fb strings.Builder
				for c := r.Intn(8); c > 0; c-- {
					fb.WriteString(alpha[r.Intn(len(alpha))])
				}
				rec = append(rec, fb.String())
			}
			_ = w.Write(rec)
		}
		w.Flush()
		out = append(out, sb.String())
	}
	return out
}

func TestSTDCSV_RecordsMatchEncodingCsv(t *testing.T) {
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := csvBuild(t, csvDriver, flags...)
			for _, comma := range []byte{',', ';', '\t', '|'} {
				inputs := append([]string{}, csvAdversarial...)
				if comma != ',' {
					for k := range inputs {
						inputs[k] = strings.ReplaceAll(inputs[k], ",", string(comma))
					}
				}
				inputs = append(inputs, csvSoup(int64(comma), 1500, comma, 60)...)
				inputs = append(inputs, csvSoup(int64(comma)+500, 200, comma, 700)...)
				inputs = append(inputs, csvValid(int64(comma)+1000, 500, comma, 6)...)
				inputs = append(inputs, csvValid(int64(comma)+2000, 100, comma, 120)...)
				o := csvDefault
				o.comma = comma
				got := csvRun(t, exe, inputs, 1<<16, o)
				bad, quotes := csvCompare(t, inputs, got, o, 1<<16)
				t.Logf("delimiter %q: %d inputs (%d end in Quote), %d differ from encoding/csv", comma, len(inputs), quotes, bad)
			}
		})
	}
}

// TestSTDCSV_SmallBufferSameRecords reads every input through buffers of
// 64, 100, 512 and 2048 bytes (so the reader refills and compacts mid-record,
// and splits CR LF, "" and the final CR across reads): each outcome is the
// 64 KiB buffer's, or that outcome's records up to one TooLarge.
func TestSTDCSV_SmallBufferSameRecords(t *testing.T) {
	exe := csvBuild(t, csvDriver)
	inputs := append([]string{}, csvAdversarial...)
	inputs = append(inputs, csvSoup(7, 1000, ',', 60)...)
	inputs = append(inputs, csvSoup(9, 200, ',', 700)...)
	inputs = append(inputs, csvValid(8, 500, ',', 6)...)
	inputs = append(inputs, csvValid(10, 100, ',', 120)...)
	big := csvRun(t, exe, inputs, 1<<16, csvDefault)
	for _, size := range []int{64, 100, 512, 2048} {
		got := csvRun(t, exe, inputs, size, csvDefault)
		large := 0
		for k, in := range inputs {
			if got[k] == big[k] {
				continue
			}
			i := strings.LastIndex(got[k], "L ")
			if i >= 0 && strings.HasSuffix(got[k], "\n") && strings.HasPrefix(big[k], got[k][:i]) {
				large++
				continue
			}
			t.Errorf("buffer %d, input %d %s:\n got %q\nwant %q", size, k, csvEscape(in), got[k], big[k])
		}
		t.Logf("buffer %d: %d of %d inputs stop at TooLarge", size, large, len(inputs))
	}
}

// csvCompare checks each outcome against csvExpect and returns how many
// differ and how many the oracle ends in a quote error.
func csvCompare(t *testing.T, inputs, got []string, o csvOpts, bufLen int) (bad, quotes int) {
	t.Helper()
	for k, in := range inputs {
		want := csvExpect(t, in, o, bufLen)
		if want.quotes {
			quotes++
		}
		if !want.ok(got[k]) {
			bad++
			if bad <= 10 {
				t.Errorf("options %+v, input %d %s:\n got %q\nwant %v", o, k, csvEscape(in), got[k], want)
			}
		}
	}
	return bad, quotes
}

// csvQuoted is inputs with '"' and q exchanged, so the quote q plays the
// part '"' played.
func csvQuoted(inputs []string, q byte) []string {
	out := make([]string, len(inputs))
	for k, in := range inputs {
		out[k] = csvSwap(in, q)
	}
	return out
}

// TestSTDCSV_OptionsMatchModel: the reader
// under every combination of field_bytes {0, 1, 3, 64, the largest int}
// and record_fields {1, 2, 5, 4096}, and with the quote byte 0x27 (an apostrophe) beside
// '"', against encoding/csv (the quote mapped by csvSwap) cut at the first
// record past a limit (csvExpect), at dev and release.
func TestSTDCSV_OptionsMatchModel(t *testing.T) {
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := csvBuild(t, csvDriver, flags...)
			inputs := append([]string{}, csvAdversarial...)
			inputs = append(inputs, csvSoup(61, 300, ',', 60)...)
			inputs = append(inputs, csvSoup(62, 40, ',', 700)...)
			inputs = append(inputs, csvValid(63, 150, ',', 6)...)
			inputs = append(inputs, csvValid(64, 20, ',', 120)...)
			total, large := 0, 0
			for _, fb := range []int{0, 1, 3, 64, math.MaxInt} {
				for _, rf := range []int{1, 2, 5, 4096} {
					for _, q := range []byte{'"', '\''} {
						o := csvOpts{',', q, fb, rf}
						in := csvQuoted(inputs, q)
						for _, size := range []int{1 << 16, 100} {
							got := csvRun(t, exe, in, size, o)
							if size == 1<<16 {
								bad, _ := csvCompare(t, in, got, o, size)
								if bad > 0 {
									t.Fatalf("options %+v: %d of %d inputs differ", o, bad, len(in))
								}
								for _, g := range got {
									if strings.Contains(g, "L ") {
										large++
									}
								}
								total += len(in)
								continue
							}
							// 100 bytes: three index slots and 76 bytes of data;
							// the outcome is the model's at that size, or its
							// records up to a TooLarge for the data area.
							for k := range in {
								want := csvExpect(t, in[k], o, size)
								if want.ok(got[k]) {
									continue
								}
								i := strings.LastIndex(got[k], "L ")
								if i >= 0 && strings.HasPrefix(want.exact, got[k][:i]) {
									continue
								}
								t.Fatalf("options %+v, buffer %d, input %s:\n got %q\nwant %v", o, size, csvEscape(in[k]), got[k], want)
							}
						}
					}
				}
			}
			t.Logf("%d inputs over 40 option sets, %d end in TooLarge", total, large)
		})
	}
}
