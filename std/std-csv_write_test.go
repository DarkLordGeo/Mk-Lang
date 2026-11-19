// Why: csv.WriteRecord must quote exactly as RFC 4180's minimal quoting asks,
// and what it writes must read back to the same fields.
// What: TestSTDCSV_WriteRecordMatchesModel.
// How: seeded random records at dev and release, four delimiters, quotes '"'
// and '\'', byte for byte against a Go model; the output is read back by
// encoding/csv (a CR LF in a field reads back as LF there, as encoding/csv
// documents) and by csv.Records to exactly the same fields.

package std

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// csvWriteDriver reads records from stdin, each "<fields>\n" then per
// field "<len>\n" and its bytes, and writes each with csv.WriteRecord, the
// delimiter args[1] and the quote args[2] (byte values) to stdout.
const csvWriteDriver = `import std.csv

export Main(sys: &System): WriteError|OutOfMemory|ReadError|Utf8Error|ParseError!void {
    var o := csv.DefaultOptions
    o.delimiter = Trunc[u8](try sys.args[1].ParseInt())
    o.quote_byte = Trunc[u8](try sys.args[2].ParseInt())
    var inp := sys.Stdin()
    all := try sys.arena.Slice[u8](1 << 24)
    total := try inp.ReadAll(all)
    data := all[0..total]
    var out := sys.Stdout()
    var vals := List[str].New(sys.arena)
    var i := 0
    for i < total {
        nf := Cast[int](try Num(data, mut i))
        vals.Clear()
        for _ in 0..nf {
            n := Cast[int](try Num(data, mut i))
            try vals.Push(try str.From(data[i..i + n]))
            i += n
        }
        try csv.WriteRecord(mut out, vals.Items(), &o)
    }
}

// Num reads the decimal number at data[i..] up to its LF and moves i past it.
Num(data: []u8, i: mut int): Utf8Error|ParseError!i64 {
    var j := *i
    for data[j] != 0x0A {
        j += 1
    }
    v := try (try str.From(data[*i..j])).ParseInt()
    *i = j + 1
    return v
}
`

// csvWriteModel is RFC 4180's minimal quoting as WriteRecord does it, with
// the quote q.
func csvWriteModel(rec []string, comma, q byte) string {
	var sb strings.Builder
	qs := string(q)
	if len(rec) == 1 && rec[0] == "" {
		return qs + qs + "\r\n"
	}
	for i, f := range rec {
		if i > 0 {
			sb.WriteByte(comma)
		}
		if strings.ContainsAny(f, string(comma)+qs+"\r\n") {
			sb.WriteByte(q)
			sb.WriteString(strings.ReplaceAll(f, qs, qs+qs))
			sb.WriteByte(q)
		} else {
			sb.WriteString(f)
		}
	}
	sb.WriteString("\r\n")
	return sb.String()
}

func TestSTDCSV_WriteRecordMatchesModel(t *testing.T) {
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := csvBuild(t, csvWriteDriver, flags...)
			reader := csvBuild(t, csvDriver, flags...)
			for _, cq := range [][2]byte{{',', '"'}, {';', '"'}, {'\t', '"'}, {'|', '"'}, {',', '\''}, {';', '\''}} {
				comma, q := cq[0], cq[1]
				r := rand.New(rand.NewSource(int64(comma) + 256*int64(q)))
				alpha := []string{"a", "bc", " ", string(comma), "\"", "'", "\n", "\r", "\r\n", "é", "\\.", "0", ""}
				var in, want strings.Builder
				var recs [][]string
				for k := 0; k < 2000; k++ {
					nf := 1 + r.Intn(6)
					var rec []string
					for f := 0; f < nf; f++ {
						var fb strings.Builder
						for c := r.Intn(6); c > 0; c-- {
							fb.WriteString(alpha[r.Intn(len(alpha))])
						}
						rec = append(rec, fb.String())
					}
					// Long fields cross the vector widths of the search.
					if k%50 == 0 {
						rec = append(rec, strings.Repeat("x", 100+r.Intn(200))+string(comma)+strings.Repeat("y", r.Intn(100)))
					}
					recs = append(recs, rec)
					fmt.Fprintf(&in, "%d\n", len(rec))
					for _, f := range rec {
						fmt.Fprintf(&in, "%d\n%s", len(f), f)
					}
					want.WriteString(csvWriteModel(rec, comma, q))
				}
				res := runNative(t, exe, in.String(), strconv.Itoa(int(comma)), strconv.Itoa(int(q)))
				if res.code != 0 {
					t.Fatalf("exit %d\n%s", res.code, res.stderr)
				}
				if res.stdout != want.String() {
					got, w := res.stdout, want.String()
					i := 0
					for i < len(got) && i < len(w) && got[i] == w[i] {
						i++
					}
					t.Fatalf("delimiter %q: output differs from the model at byte %d:\n got %q\nwant %q", comma, i, clip(got[max(0, i-40):]), clip(w[max(0, i-40):]))
				}
				rd := csv.NewReader(strings.NewReader(csvSwap(res.stdout, q)))
				rd.Comma = rune(comma)
				rd.FieldsPerRecord = -1
				back, err := rd.ReadAll()
				if err != nil || len(back) != len(recs) {
					t.Fatalf("delimiter %q: encoding/csv read %d of %d records: %v", comma, len(back), len(recs), err)
				}
				for k, rec := range recs {
					for f := range rec {
						if want := strings.ReplaceAll(rec[f], "\r\n", "\n"); csvSwap(back[k][f], q) != want {
							t.Errorf("delimiter %q, quote %q, record %d field %d: encoding/csv read back %q, wrote %q", comma, q, k, f, back[k][f], rec[f])
						}
					}
				}
				// csv.Records reads every field back byte for byte, CR LF
				// included.
				o := csvOpts{comma, q, 1 << 20, 4096}
				got := csvRun(t, reader, []string{res.stdout}, 1<<16, o)[0]
				var exp strings.Builder
				line := 1
				for _, rec := range recs {
					fmt.Fprintf(&exp, "R %d %d\n", line, len(rec))
					for _, f := range rec {
						fmt.Fprintf(&exp, "%d:%s\n", len(f), f)
						line += strings.Count(f, "\n")
					}
					line++
				}
				exp.WriteString("END\n")
				if got != exp.String() {
					t.Errorf("delimiter %q, quote %q: csv.Records read back differs:\n got %q\nwant %q", comma, q, clip(got), clip(exp.String()))
				}
			}
		})
	}
}
