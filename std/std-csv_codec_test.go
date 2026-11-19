// Why: gen csv.Codec writes the FromCsv and WriteCsv a user's struct runs,
// so its emitted code must read and write every field shape correctly.
// What: TestSTDCSV_CodecRoundTrip, CodecRefuses, CodecGolden, CodecLoneField.
// How: builds Mk programs that read, write and re-read records, checks the
// output with encoding/csv, E0903 at each refused field, and pins the
// emitted code to std/csv/testdata/codec.golden.

package std

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const csvCodecTypes = `import std.csv

struct Row {
    export name: str
    export ok: bool
    export small: u8
    export big: u64
    export neg: i16
    export n: int
    export price: f64
    export note: ?str
    export qty: ?i32
}

gen csv.Codec(Row)
`

// csvCodecMain reads Row records from stdin and writes each back with
// WriteCsv, or "parse <line>", "utf8 <line>", after a header line from
// WriteCsvHeader.
const csvCodecMain = `
export Main(sys: &System): WriteError|OutOfMemory!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 16)
    var out := sys.Stdout()
    var rs := csv.Records.New(mut inp, buf, &csv.DefaultOptions)
    try Row.WriteCsvHeader(mut out, &csv.DefaultOptions)
    for {
        match rs.Next() {
            ok(some(r)) {
                match Row.FromCsv(sys.arena, &r) {
                    ok(x) {
                        try x.WriteCsv(mut out, &csv.DefaultOptions)
                    }
                    err(ParseError) {
                        try Print(mut out, "parse {line}\n", (line: r.Line()))
                    }
                    err(Utf8Error) {
                        try Print(mut out, "utf8 {line}\n", (line: r.Line()))
                    }
                    err(e) {
                        try Print(mut out, "other {e}\n", (e))
                    }
                }
            }
            ok(none) {
                return
            }
            err(e) {
                try Print(mut out, "csv {e}\n", (e))
                return
            }
        }
    }
}
`

// csvCodecCases: one input record and the line the program writes for it.
var csvCodecCases = []struct{ in, want string }{
	{"a,true,255,18446744073709551615,-32768,-5,1.5,,7", "a,true,255,18446744073709551615,-32768,-5,1.5,,7"},
	{`"x,""y",false,0,0,0,0,1e300,note,`, `"x,""y",false,0,0,0,0,1e+300,note,`},
	// A CR LF inside a quoted field is data, so it reads back.
	{"\"multi\r\nline\",true,1,9223372036854775808,32767,9223372036854775807,-0.25,\"a\"\"b\",-2147483648", "\"multi\r\nline\",true,1,9223372036854775808,32767,9223372036854775807,-0.25,\"a\"\"b\",-2147483648"},
	{"\"cr\rlf\nboth\r\n\",true,1,1,1,1,1,\"\r\n\",", "\"cr\rlf\nboth\r\n\",true,1,1,1,1,1,\"\r\n\","},
	{"é€,true,1,1,1,-9223372036854775808,0.1,\"\",2147483647", "é€,true,1,1,1,-9223372036854775808,0.1,,2147483647"},
	{"b,maybe,1,1,1,1,1,,", "parse"},                   // bool other than true or false
	{"b,True,1,1,1,1,1,,", "parse"},                    // bool is case-sensitive
	{"b,true,256,1,1,1,1,,", "parse"},                  // u8 past 255
	{"b,true,-1,1,1,1,1,,", "parse"},                   // u8 below 0
	{"b,true,1,-1,1,1,1,,", "parse"},                   // u64 with a sign
	{"b,true,1,+1,1,1,1,,", "parse"},                   // u64 with a sign
	{"b,true,1,18446744073709551616,1,1,1,,", "parse"}, // u64 past 2^64 - 1
	{"b,true,1,,1,1,1,,", "parse"},                     // empty u64
	{"b,true,1,1,32768,1,1,,", "parse"},                // i16 past its range
	{"b,true,1,1,1,1.0,1,,", "parse"},                  // int with a fraction
	{"b,true,1,1,1,1,x,,", "parse"},                    // f64 that is not a number
	{"b,true,1,1,1,1,1,,2147483648", "parse"},          // ?i32 past its range
	{"b,true,1,1,1,1,1,", "parse"},                     // one field short
	{"b,true,1,1,1,1,1,,,", "parse"},                   // one field over
	{"b", "parse"},
	{"b,true,1,1,1,1,inf,,", "b,true,1,1,1,1,inf,,"},
	{"\xff,true,1,1,1,1,1,,", "utf8"},
	{"b,true,1,1,1,1,1,\xc3,", "utf8"},
}

func TestSTDCSV_CodecRoundTrip(t *testing.T) {
	var in strings.Builder
	for _, c := range csvCodecCases {
		in.WriteString(c.in)
		in.WriteString("\r\n")
	}
	for _, mode := range []string{"dev", "release"} {
		t.Run(mode, func(t *testing.T) {
			var flags []string
			if mode == "release" {
				flags = []string{"--release"}
			}
			exe := csvBuild(t, csvCodecTypes+csvCodecMain, flags...)
			r := runNative(t, exe, in.String())
			if r.code != 0 {
				t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
			}
			// WriteCsv's records end in CR LF, the Print lines in LF; some
			// records hold CR, LF or CR LF inside a quoted field, which
			// must come back byte for byte.
			lines := csvJoinQuoted(strings.SplitAfter(r.stdout, "\n"))
			if len(lines) == 0 || lines[0] != "name,ok,small,big,neg,n,price,note,qty" {
				t.Fatalf("header %q", lines)
			}
			lines = lines[1:]
			if len(lines) != len(csvCodecCases) {
				t.Fatalf("%d lines for %d cases:\n%q", len(lines), len(csvCodecCases), r.stdout)
			}
			line := 1
			for k, c := range csvCodecCases {
				want := c.want
				if want == "parse" || want == "utf8" {
					want += " " + strconv.Itoa(line)
				}
				line += 1 + strings.Count(c.in, "\n")
				if k >= len(lines) {
					break
				}
				if lines[k] != want {
					t.Errorf("case %d %q:\n got %q\nwant %q", k, c.in, lines[k], want)
				}
			}
			// What WriteCsv wrote reads back, by encoding/csv, to the same
			// values.
			rd := csv.NewReader(strings.NewReader(r.stdout))
			rd.FieldsPerRecord = -1
			recs, err := rd.ReadAll()
			if err != nil || len(recs) != len(csvCodecCases)+1 {
				t.Fatalf("encoding/csv on WriteCsv's output: %d records, %v", len(recs), err)
			}
			for k, c := range csvCodecCases {
				if c.want == "parse" || c.want == "utf8" {
					continue
				}
				want, _ := csv.NewReader(strings.NewReader(c.want)).Read()
				if rec := recs[k+1]; !csvSameValues(rec, want) {
					t.Errorf("case %d read back by encoding/csv: %q, want %q", k, rec, want)
				}
			}
		})
	}
}

// csvJoinQuoted rejoins the pieces of strings.SplitAfter(out, "\n") into
// one line per record (a record split at an LF inside a quoted field is
// joined back, its bytes unchanged) and removes each line's CR LF or LF.
func csvJoinQuoted(pieces []string) []string {
	var out []string
	for i := 0; i < len(pieces); i++ {
		l := pieces[i]
		for strings.Count(l, `"`)%2 == 1 && i+1 < len(pieces) {
			i++
			l += pieces[i]
		}
		if l == "" {
			continue
		}
		l = strings.TrimSuffix(l, "\n")
		out = append(out, strings.TrimSuffix(l, "\r"))
	}
	return out
}

// csvSameValues compares two Row records field by field, the float by
// value.
func csvSameValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if i == 6 {
			x, e1 := strconv.ParseFloat(a[i], 64)
			y, e2 := strconv.ParseFloat(b[i], 64)
			if e1 != nil || e2 != nil || (x != y && !(math.IsNaN(x) && math.IsNaN(y))) {
				return false
			}
			continue
		}
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSTDCSV_CodecRefuses: E0903 at each field the codec has no form for,
// at a struct with no field and at an enum.
func TestSTDCSV_CodecRefuses(t *testing.T) {
	cases := []struct{ src, want string }{
		{"struct A {\n    export x: char\n}\n\ngen csv.Codec(A)\n", "this field's type has no CSV form"},
		{"struct A {\n    export x: int\n    y: int\n}\n\ngen csv.Codec(A)\n", "every field must be exported"},
		{"struct B {\n    export v: int\n}\n\nstruct A {\n    export b: B\n}\n\ngen csv.Codec(A)\n", "this field's type has no CSV form"},
		{"struct A {\n    export xs: []int\n}\n\ngen csv.Codec(A)\n", "this field's type has no CSV form"},
		{"struct A {\n    export x: ??int\n}\n\ngen csv.Codec(A)\n", "this field's type has no CSV form"},
		{"struct A {\n}\n\ngen csv.Codec(A)\n", "a struct with no field has no CSV record"},
		{"enum A {\n    X\n}\n\ngen csv.Codec(A)\n", "only a struct has a CSV record form"},
	}
	for _, c := range cases {
		out, status := csvCheck(t, "import std.csv\n\n"+c.src+"\nexport Main(sys: &System) {\n}\n")
		if status == 0 || !strings.Contains(out, "E0903") || !strings.Contains(out, c.want) {
			t.Errorf("%s:\nexit %d\n%s\nwant E0903 %q", c.src, status, out, c.want)
		}
	}
}

// csvCheck runs mk check on a package whose main.mk is src.
func csvCheck(t *testing.T, src string) (string, int) {
	t.Helper()
	root := t.TempDir()
	stdDir := csvStd(t, root)
	pkgDir := filepath.Join(root, "p")
	csvWritePkg(t, pkgDir, src)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	return mkCmd(t, pkgDir, stdDir, "check", "--target=x86_64-windows", ".")
}

// csvWritePkg writes a one-bin package whose main.mk is src.
func csvWritePkg(t *testing.T, pkgDir, src string) {
	t.Helper()
	files := map[string]string{
		"main.mk": src,
		"pkg.mk": "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
			"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [],\n    links: [],\n)\n",
	}
	for p, text := range files {
		dst := filepath.Join(pkgDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSTDCSV_CodecGolden pins the emitted code of Row (the gen view mk
// writes beside the module) to std/csv/testdata/codec.golden.
// MK_CSV_UPDATE=1 rewrites the golden file.
func TestSTDCSV_CodecGolden(t *testing.T) {
	root := t.TempDir()
	stdDir := csvStd(t, root)
	pkgDir := filepath.Join(root, "p")
	csvWritePkg(t, pkgDir, csvCodecTypes+"\nexport Main(sys: &System) {\n}\n")
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, pkgDir, stdDir, "check", "--target=x86_64-windows", ".")
	if status != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	b, err := os.ReadFile(filepath.Join(pkgDir, "gen", "x86_64-windows", "gen.mk"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if k := strings.LastIndex(got, "\n// mk gen view "); k >= 0 {
		got = got[:k+1]
	}
	golden := filepath.Join("csv", "testdata", "codec.golden")
	if os.Getenv("MK_CSV_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (MK_CSV_UPDATE=1 writes it)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("emitted code differs from %s (MK_CSV_UPDATE=1 rewrites it after reading the diff):\n%s", golden, got)
	}
}

// TestSTDCSV_CodecLoneField: a struct of one field writes an empty value
// as "" (a bare empty field would be a blank line, which readers skip),
// and a ?str none the same way; each reads back.
func TestSTDCSV_CodecLoneField(t *testing.T) {
	src := `import std.csv

struct One {
    export s: ?str
}

gen csv.Codec(One)

export Main(sys: &System): WriteError|OutOfMemory!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 12)
    var out := sys.Stdout()
    var rs := csv.Records.New(mut inp, buf, &csv.DefaultOptions)
    try One.WriteCsvHeader(mut out, &csv.DefaultOptions)
    for {
        match rs.Next() {
            ok(some(r)) {
                x := One.FromCsv(sys.arena, &r) else {
                    try out.Str("bad\n")
                    continue
                }
                try out.Str(if x.s.IsNone() { "none:" } else { "some:" })
                try x.WriteCsv(mut out, &csv.DefaultOptions)
            }
            _ {
                return
            }
        }
    }
}
`
	exe := csvBuild(t, src)
	r := runNative(t, exe, "\"\"\r\nx\r\n\"a\"\"\"\r\n,\r\n")
	want := "s\r\nnone:\"\"\r\nsome:x\r\nsome:\"a\"\"\"\r\nbad\n"
	if r.code != 0 || r.stdout != want {
		t.Errorf("exit %d\n got %q\nwant %q\n%s", r.code, r.stdout, want, r.stderr)
	}
}
