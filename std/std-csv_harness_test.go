// Why: every std.csv test builds and runs Mk programs and compares their
// output with encoding/csv; the shared plumbing lives here.
// What: csvStd, csvBuild, csvRun, the oracle csvOracle and the model csvExpect.
// How: programs build against a copy of MK_CSV_BASE (a snapshot of a whole
// std) with this tree's std/csv, or against a copy of this tree's std.

package std

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// csvStd returns the std directory the csv programs build against.
func csvStd(t *testing.T, root string) string {
	t.Helper()
	base := os.Getenv("MK_CSV_BASE")
	if base == "" {
		return copyStd(t, root, nil)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "csv")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "csv"), os.DirFS("csv")); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// csvBuild writes src as the package's main.mk and builds it with flags;
// it returns the executable. A missing toolchain skips the test.
func csvBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	return csvBuildIn(t, t.TempDir(), src, flags...)
}

// csvBuildIn is csvBuild into the directory root.
func csvBuildIn(t *testing.T, root, src string, flags ...string) string {
	t.Helper()
	stdDir := csvStd(t, root)
	pkgDir := filepath.Join(root, "p")
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
	exe := filepath.Join(root, "p")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, ".")
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, pkgDir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// csvDriver reads framed inputs from stdin ("<len>\n" then len bytes,
// repeated) and, for each, writes it to a file of a simulated disk and
// reads it back through csv.Records with a buf of args[1] bytes and the
// options args[2] to args[5]: the delimiter and the quote (byte values),
// field_bytes and record_fields. It prints "=== k" before input k,
// then per record "R <line> <fields>" and per field "<len>:<bytes>", or
// "!" for a field that is not UTF-8, and at the end "END", "Q <line>
// <column>", "L <line>" or "RE".
const csvDriver = `import std.csv
import std.fs
import std.sim

export Main(sys: &System): WriteError|OutOfMemory|ReadError|Utf8Error|ParseError|fs.Error!void {
    size := Cast[int](try sys.args[1].ParseInt())
    var o := csv.DefaultOptions
    o.delimiter = Trunc[u8](try sys.args[2].ParseInt())
    o.quote_byte = Trunc[u8](try sys.args[3].ParseInt())
    o.field_bytes = Cast[int](try sys.args[4].ParseInt())
    o.record_fields = Cast[int](try sys.args[5].ParseInt())
    var inp := sys.Stdin()
    all := try sys.arena.Slice[u8](1 << 24)
    total := try inp.ReadAll(all)
    data := all[0..total]
    buf := try sys.arena.Slice[u8](size)
    var out := sys.Stdout()
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    var i := 0
    var k := 0
    for i < total {
        var j := i
        for data[j] != 0x0A {
            j += 1
        }
        head := try str.From(data[i..j])
        n := Cast[int](try head.ParseInt())
        text := data[j + 1..j + 1 + n]
        i = j + 1 + n
        try Print(mut out, "=== {k}\n", (k: k))
        k += 1
        try fs.WriteAll(io, "in", text)
        rb := try sys.arena.Slice[u8](64)
        var f := try fs.Open(io, "in")
        onerr f.Abandon()
        var r := f.Reader(io, rb)
        try ReadAllRecords(mut out, mut r, mut buf[..], &o)
        f.Close(io) else {
            f.Abandon()
        }
    }
}

` + csvRecordsFn

// csvRecordsFn prints every record of r as csvDriver's comment says.
const csvRecordsFn = `
ReadAllRecords(out: mut Writer, r: mut Reader, buf: mut []u8, o: &csv.Options): WriteError!void {
    var rs := csv.Records.New(r, buf, o)
    for {
        match rs.Next() {
            ok(some(rec)) {
                try Print(out, "R {line} {n}\n", (line: rec.Line(), n: rec.Len()))
                for i in 0..rec.Len() {
                    match rec.Field(i) {
                        some(f) {
                            try Print(out, "{n}:{f}\n", (n: f.Len(), f: f))
                        }
                        none {
                            try out.Str("!\n")
                        }
                    }
                }
            }
            ok(none) {
                try out.Str("END\n")
                return
            }
            err(csv.Quote(line, column)) {
                try Print(out, "Q {line} {column}\n", (line: line, column: column))
                return
            }
            err(csv.TooLarge(line)) {
                try Print(out, "L {line}\n", (line: line))
                return
            }
            err(_) {
                try out.Str("RE\n")
                return
            }
        }
    }
}
`

// csvFrame encodes inputs for csvDriver.
func csvFrame(inputs []string) string {
	var sb strings.Builder
	for _, in := range inputs {
		sb.WriteString(strconv.Itoa(len(in)))
		sb.WriteByte('\n')
		sb.WriteString(in)
	}
	return sb.String()
}

// csvOpts are the options of one reader run: the delimiter, the quote
// byte, field_bytes and record_fields.
type csvOpts struct {
	comma, quote             byte
	fieldBytes, recordFields int
}

// csvDefault is csv.DefaultOptions.
var csvDefault = csvOpts{',', '"', 1 << 20, 4096}

func (o csvOpts) args() []string {
	return []string{strconv.Itoa(int(o.comma)), strconv.Itoa(int(o.quote)), strconv.Itoa(o.fieldBytes), strconv.Itoa(o.recordFields)}
}

// csvSwap exchanges the bytes q and '"' in s: encoding/csv knows only '"'
// as the quote, so an input read with the quote q is given to it swapped,
// and its fields are swapped back. Positions do not move.
func csvSwap(s string, q byte) string {
	if q == '"' {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c == q {
			b[i] = '"'
		} else if c == '"' {
			b[i] = q
		}
	}
	return string(b)
}

// csvRec is one record of the oracle: its first line and its fields.
type csvRec struct {
	line   int
	fields []string
}

// csvOracle reads in with encoding/csv (FieldsPerRecord -1, since std.csv
// checks no field count) under o's delimiter and quote, and returns its
// records, then its quote error's line and column, if any. encoding/csv
// reads a CR LF inside a quoted field as LF, where RFC 4180 and std.csv
// keep it: a quoted field is therefore re-read from the input at
// encoding/csv's FieldPos, "" undoubled and nothing else changed, and the
// result must equal encoding/csv's field once its CR LF are folded, or the
// oracle itself is wrong (err).
func csvOracle(in string, o csvOpts) (recs []csvRec, qline, qcol int, err error) {
	src := csvSwap(in, o.quote)
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	r := csv.NewReader(strings.NewReader(src))
	r.Comma = rune(o.comma)
	r.FieldsPerRecord = -1
	for {
		rec, e := r.Read()
		if e == io.EOF {
			return recs, 0, 0, nil
		}
		if e != nil {
			var pe *csv.ParseError
			if errors.As(e, &pe) && (pe.Err == csv.ErrQuote || pe.Err == csv.ErrBareQuote) {
				return recs, pe.Line, pe.Column, nil
			}
			return recs, 0, 0, e
		}
		line, _ := r.FieldPos(0)
		out := csvRec{line: line}
		for i, f := range rec {
			l, c := r.FieldPos(i)
			off := starts[l-1] + c - 1
			if off < len(src) && src[off] == '"' {
				raw, ok := csvUnquoteRaw(src, off)
				if !ok || strings.ReplaceAll(raw, "\r\n", "\n") != f {
					return recs, 0, 0, fmt.Errorf("oracle: the quoted field at %d:%d reads %q from the input, encoding/csv %q", l, c, raw, f)
				}
				f = raw
			}
			out.fields = append(out.fields, csvSwap(f, o.quote))
		}
		recs = append(recs, out)
	}
}

// csvUnquoteRaw is the content of the quoted field whose opening quote is
// at src[off], "" read as one quote and every other byte as it is.
func csvUnquoteRaw(src string, off int) (string, bool) {
	var b strings.Builder
	for i := off + 1; i < len(src); i++ {
		if src[i] != '"' {
			b.WriteByte(src[i])
			continue
		}
		if i+1 < len(src) && src[i+1] == '"' {
			b.WriteByte('"')
			i++
			continue
		}
		return b.String(), true
	}
	return b.String(), false
}

// csvExpectation is what csvDriver must print for one input: exact, or,
// when the oracle ends in a quote error and a limit is tight enough to be
// reached, prefix then "L <n>" with n at most qline: a record that both
// breaks a limit and holds a stray quote fails with whichever the scan
// meets first, which depends on how the bytes arrived.
type csvExpectation struct {
	exact  string
	prefix string
	qline  int
	alt    bool
	quotes bool
}

func (e csvExpectation) ok(got string) bool {
	if got == e.exact {
		return true
	}
	if !e.alt || !strings.HasPrefix(got, e.prefix) {
		return false
	}
	var n int
	if _, err := fmt.Sscanf(got[len(e.prefix):], "L %d\n", &n); err != nil {
		return false
	}
	return got == e.prefix+fmt.Sprintf("L %d\n", n) && n >= 1 && n <= e.qline
}

func (e csvExpectation) String() string {
	if e.alt {
		return fmt.Sprintf("%q (or L at a line up to %d after the records)", e.exact, e.qline)
	}
	return strconv.Quote(e.exact)
}

// csvExpect is the oracle's outcome for in, read with o through a buf of
// bufLen bytes: encoding/csv's records (csvOracle), cut at the first that
// breaks a limit (a field over field_bytes, or more fields than
// record_fields capped at bufLen / 32, Records' index room), which is
// "L <its line>". Inputs here never reach the data-area limit.
func csvExpect(t testing.TB, in string, o csvOpts, bufLen int) csvExpectation {
	t.Helper()
	recs, qline, qcol, err := csvOracle(in, o)
	if err != nil {
		t.Fatalf("input %q: %v", in, err)
	}
	slots := min(o.recordFields, bufLen/32)
	var sb strings.Builder
	for _, rec := range recs {
		over := len(rec.fields) > slots
		for _, f := range rec.fields {
			over = over || len(f) > o.fieldBytes
		}
		if over {
			fmt.Fprintf(&sb, "L %d\n", rec.line)
			return csvExpectation{exact: sb.String()}
		}
		fmt.Fprintf(&sb, "R %d %d\n", rec.line, len(rec.fields))
		for _, f := range rec.fields {
			if utf8.ValidString(f) {
				fmt.Fprintf(&sb, "%d:%s\n", len(f), f)
			} else {
				sb.WriteString("!\n")
			}
		}
	}
	if qline == 0 {
		sb.WriteString("END\n")
		return csvExpectation{exact: sb.String()}
	}
	prefix := sb.String()
	fmt.Fprintf(&sb, "Q %d %d\n", qline, qcol)
	tight := o.fieldBytes < len(in) || slots <= strings.Count(in, string(o.comma))
	return csvExpectation{exact: sb.String(), prefix: prefix, qline: qline, alt: tight, quotes: true}
}

// csvSplit splits csvDriver's output into one outcome per input, reading
// each field by its length so a field's own newlines stay inside it.
func csvSplit(t *testing.T, out string, n int) []string {
	t.Helper()
	var res []string
	i := 0
	for k := 0; k < n; k++ {
		head := fmt.Sprintf("=== %d\n", k)
		if !strings.HasPrefix(out[i:], head) {
			t.Fatalf("output for input %d does not start at byte %d: %q", k, i, clip(out[i:]))
		}
		i += len(head)
		start := i
		for {
			nl := strings.IndexByte(out[i:], '\n')
			if nl < 0 {
				t.Fatalf("output for input %d ends early: %q", k, clip(out[start:]))
			}
			line := out[i : i+nl]
			if c := strings.IndexByte(line, ':'); c > 0 && !strings.HasPrefix(line, "R ") {
				if fl, err := strconv.Atoi(line[:c]); err == nil {
					i += c + 1 + fl + 1
					continue
				}
			}
			i += nl + 1
			if line == "END" || line == "RE" || strings.HasPrefix(line, "Q ") || strings.HasPrefix(line, "L ") {
				break
			}
		}
		res = append(res, out[start:i])
	}
	if i != len(out) {
		t.Fatalf("%d bytes after the last input's output: %q", len(out)-i, clip(out[i:]))
	}
	return res
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// csvRun runs exe over inputs with a buf of size bytes and the options o,
// and returns one outcome per input.
func csvRun(t *testing.T, exe string, inputs []string, size int, o csvOpts) []string {
	t.Helper()
	r := runNative(t, exe, csvFrame(inputs), append([]string{strconv.Itoa(size)}, o.args()...)...)
	if r.code != 0 {
		t.Fatalf("exit %d\nstderr: %s\nstdout: %s", r.code, r.stderr, clip(r.stdout))
	}
	return csvSplit(t, r.stdout, len(inputs))
}

// csvEscape shows an input in a failure message.
func csvEscape(s string) string { return strconv.Quote(s) }

var _ = bytes.Equal
