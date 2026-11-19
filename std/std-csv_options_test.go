// Why: the codec's writers once skipped the option checks csv.WriteRecord
// makes, and a str holding CR LF must survive a round trip.
// What: TestSTDCSV_CodecChecksOptions and the CR LF round trip.
// How: builds Mk programs over the emitted WriteCsv, WriteCsvHeader and
// FromCsv and compares with csv.WriteRecord and the original values.

package std

// Regression tests of the codec: the emitted WriteCsv and
// WriteCsvHeader check the options as csv.WriteRecord does, and
// FromCsv(WriteCsv(x)) == x for a str holding CR LF.

import (
	"strconv"
	"strings"
	"testing"
)

// csvOptsProgram writes one Row with WriteCsv (args[2] 0), WriteCsvHeader
// (1) or csv.WriteRecord (2), under the options case args[1].
const csvOptsProgram = `import std.csv

struct Row {
    export a: str
    export b: str
}

gen csv.Codec(Row)

struct Num {
    export a: str
    export n: int
}

gen csv.Codec(Num)

export Main(sys: &System): WriteError|ParseError!void {
    var o := csv.DefaultOptions
    k := try sys.args[1].ParseInt()
    if k == 0 {
        o.quote_byte = 0x2C
    } else if k == 1 {
        o.delimiter = 0x0A
    } else if k == 2 {
        o.quote_byte = 0x0D
    } else if k == 3 {
        o.delimiter = 0x0D
    } else if k == 4 {
        o.quote_byte = 0x0A
    } else if k == 5 {
        o.field_bytes = -1
    } else if k == 6 {
        o.record_fields = 0
    } else if k == 7 {
        o.delimiter = 0x2E
    } else if k == 8 {
        o.delimiter = 0x3B
        o.quote_byte = 0x27
    }
    var out := sys.Stdout()
    how := try sys.args[2].ParseInt()
    if how == 0 {
        try Row(a: "x,y", b: "z'").WriteCsv(mut out, &o)
    } else if how == 1 {
        try Row.WriteCsvHeader(mut out, &o)
    } else if how == 2 {
        try csv.WriteRecord(mut out, ["x,y", "z'"][..], &o)
    } else {
        try Num(a: "x", n: 15).WriteCsv(mut out, &o)
    }
}
`

func TestSTDCSV_CodecChecksOptions(t *testing.T) {
	exe := csvBuild(t, csvOptsProgram)
	cases := []struct {
		name string
		want string // the panic message, or "" for a written record
	}{
		{"quote equal to the delimiter", "csv: the delimiter and the quote must differ"},
		{"delimiter LF", "csv: the delimiter and the quote cannot be CR or LF"},
		{"quote CR", "csv: the delimiter and the quote cannot be CR or LF"},
		{"delimiter CR", "csv: the delimiter and the quote cannot be CR or LF"},
		{"quote LF", "csv: the delimiter and the quote cannot be CR or LF"},
		{"negative field_bytes", "csv: field_bytes must be at least 0 and record_fields at least 1"},
		{"record_fields 0", "csv: field_bytes must be at least 0 and record_fields at least 1"},
	}
	for k, c := range cases {
		// WriteCsv, WriteCsvHeader and WriteRecord: the same panic, nothing
		// written.
		for how, what := range []string{"WriteCsv", "WriteCsvHeader", "WriteRecord"} {
			r := runNative(t, exe, "", strconv.Itoa(k), strconv.Itoa(how))
			if r.code != 101 || !strings.Contains(r.stderr, c.want) || r.stdout != "" {
				t.Errorf("%s, %s: exit %d, stdout %q, stderr %q; want exit 101 with %q and no output", c.name, what, r.code, r.stdout, r.stderr, c.want)
			}
		}
	}
	// A '.' delimiter is usable by WriteRecord and the header, not by a
	// struct with a number (15 would need no quotes, 1.5 would split).
	for how, want := range map[int]string{0: "x,y.z'\r\n", 1: "a.b\r\n", 2: "x,y.z'\r\n"} {
		if r := runNative(t, exe, "", "7", strconv.Itoa(how)); r.code != 0 || r.stdout != want {
			t.Errorf("delimiter '.', how %d: exit %d, %q, want %q (%s)", how, r.code, r.stdout, want, r.stderr)
		}
	}
	if r := runNative(t, exe, "", "7", "3"); r.code != 101 || !strings.Contains(r.stderr, "csv.Codec: WriteCsv writes numbers bare") {
		t.Errorf("delimiter '.' with a number field: exit %d, %q, %q", r.code, r.stdout, r.stderr)
	}
	// quote_byte '\'': the emitted writer quotes and doubles it, as
	// WriteRecord does.
	for how, want := range map[int]string{0: "x,y;'z'''\r\n", 2: "x,y;'z'''\r\n", 3: "x;15\r\n"} {
		if r := runNative(t, exe, "", "8", strconv.Itoa(how)); r.code != 0 || r.stdout != want {
			t.Errorf("quote ', how %d: exit %d, %q, want %q (%s)", how, r.code, r.stdout, want, r.stderr)
		}
	}
}
