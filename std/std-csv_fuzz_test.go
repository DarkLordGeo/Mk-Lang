// Why: csv.Records reads untrusted bytes; no input may panic, hang or split
// differently from encoding/csv.
// What: FuzzSTDCSV_Records and its option decoder csvFuzzOptions.
// How: one long-running driver process reads each input through a large and
// a small buffer and compares both outcomes with the model.

// The fuzz target for csv.Records. The first two bytes of a fuzz input
// pick the options (csvFuzzOptions: field_bytes, record_fields, the
// delimiter and the quote byte; two zero bytes are csv.DefaultOptions),
// the rest is the CSV. Every
// input goes to one long-running driver process (a dev build, every check
// on) that reads it from a simulated disk through a 64 KiB buffer and
// through an 80-byte one; the first outcome must be the model's
// (csvExpect: encoding/csv with the quote mapped, cut at the first record
// past a limit), the second the first or its records up to a TooLarge. A
// panic, a hang or a differing outcome fails the input. Seeds: the inputs
// of encoding/csv's own reader tests (its oracle's data, read from GOROOT
// with their position marks removed) and csvAdversarial, under the default
// options and under others.
//
//	go test ./std -run '^$' -fuzz FuzzSTDCSV_Records -fuzztime 5m -parallel 2
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// csvServe answers one framed input at a time ("<len>\n", then the
// delimiter, the quote, field_bytes and record_fields, one decimal line
// each, then len bytes): its outcome through a 64 KiB buffer, then through
// an 80-byte one, each as csvDriver prints it, then a flush.
const csvServe = `import std.csv
import std.fs
import std.sim

export Main(sys: &System): WriteError|OutOfMemory|ReadError|Utf8Error|ParseError|fs.Error!void {
    var inp := sys.Stdin()
    var out := sys.Stdout()
    big := try sys.arena.Slice[u8](1 << 16)
    small := try sys.arena.Slice[u8](80)
    data := try sys.arena.Slice[u8](1 << 16)
    rb := try sys.arena.Slice[u8](16)
    var head: [32]u8 = [0; 32]
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    for {
        n := Cast[int](try Num(mut inp, mut head[..]))
        if n < 0 {
            return
        }
        var o := csv.DefaultOptions
        o.delimiter = Trunc[u8](try Num(mut inp, mut head[..]))
        o.quote_byte = Trunc[u8](try Num(mut inp, mut head[..]))
        o.field_bytes = Cast[int](try Num(mut inp, mut head[..]))
        o.record_fields = Cast[int](try Num(mut inp, mut head[..]))
        got := try inp.ReadAll(mut data[0..n])
        try fs.WriteAll(io, "in", data[0..got])
        try Outcome(mut out, io, mut rb[..], mut big[..], &o)
        try Outcome(mut out, io, mut rb[..], mut small[..], &o)
        try out.Flush()
    }
}

// Num reads one decimal line of a frame's head; -1 at the end of input.
Num(inp: mut Reader, head: mut []u8): ReadError|Utf8Error|ParseError!i64 {
    h := try inp.Until(0x0A, head)
    if h == 0 {
        return -1
    }
    return try (try str.From(head[0..h - 1])).ParseInt()
}

Outcome(out: mut Writer, io: Io, rb: mut []u8, buf: mut []u8, o: &csv.Options): WriteError|fs.Error!void {
    var f := try fs.Open(io, "in")
    onerr f.Abandon()
    var r := f.Reader(io, rb)
    try ReadAllRecords(out, mut r, buf, o)
    try f.Close(io)
}
` + csvRecordsFn

// csvSrv is the running driver of the fuzz target, shared by its workers.
var csvSrv struct {
	sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	err *bytes.Buffer
}

// csvAsk returns the server's two outcomes for in under o, starting it
// first.
func csvAsk(t *testing.T, in []byte, o csvOpts) (string, string) {
	csvSrv.Lock()
	defer csvSrv.Unlock()
	if csvSrv.cmd == nil {
		// Built outside t.TempDir, which ends with the test that made it,
		// while the server serves every later input; TestMain removes it.
		dir, err := packageTempDir("mkcsv")
		if err != nil {
			t.Fatal(err)
		}
		exe := csvBuildIn(t, dir, csvServe)
		cmd := exec.Command(exe)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var eb bytes.Buffer
		cmd.Stderr = &eb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		csvSrv.cmd, csvSrv.in, csvSrv.out, csvSrv.err = cmd, stdin, bufio.NewReader(stdout), &eb
	}
	if _, err := fmt.Fprintf(csvSrv.in, "%d\n%d\n%d\n%d\n%d\n%s", len(in), o.comma, o.quote, o.fieldBytes, o.recordFields, in); err != nil {
		t.Fatalf("driver gone: %v\n%s", err, csvSrv.err.String())
	}
	a := csvReadOutcome(t)
	b := csvReadOutcome(t)
	return a, b
}

// csvStop ends the server, so its executable can be removed.
func csvStop() {
	csvSrv.Lock()
	defer csvSrv.Unlock()
	if csvSrv.cmd == nil {
		return
	}
	csvSrv.in.Close()
	_ = csvSrv.cmd.Wait()
	csvSrv.cmd = nil
}

// csvReadOutcome reads one outcome from the server, each field by its
// length.
func csvReadOutcome(t *testing.T) string {
	var sb strings.Builder
	for {
		line, err := csvSrv.out.ReadString('\n')
		if err != nil {
			t.Fatalf("driver gone: %v\n%s", err, csvSrv.err.String())
		}
		if c := strings.IndexByte(line, ':'); c > 0 && !strings.HasPrefix(line, "R ") {
			if fl, err := strconv.Atoi(line[:c]); err == nil {
				rest := line[c+1:]
				for len(rest) < fl+1 {
					more, err := csvSrv.out.ReadString('\n')
					if err != nil {
						t.Fatalf("driver gone: %v\n%s", err, csvSrv.err.String())
					}
					rest += more
				}
				sb.WriteString(line[:c+1] + rest)
				continue
			}
		}
		sb.WriteString(line)
		if line == "END\n" || line == "RE\n" || strings.HasPrefix(line, "Q ") || strings.HasPrefix(line, "L ") {
			return sb.String()
		}
	}
}

// csvGoTestInputs are the Input strings of encoding/csv's reader tests,
// with their position marks (§, ¶, ∑) removed.
func csvGoTestInputs(t testing.TB) []string {
	path := filepath.Join(runtime.GOROOT(), "src", "encoding", "csv", "reader_test.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Logf("no encoding/csv tests to seed from: %v", err)
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "Input" {
			return true
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		out = append(out, strings.NewReplacer("§", "", "¶", "", "∑", "").Replace(s))
		return true
	})
	return out
}

// csvFuzzOptions reads the options from a fuzz input's first two bytes
// and returns them with the CSV bytes after them: b0 picks
// field_bytes and record_fields, b1 the delimiter and the quote; zeros are
// csv.DefaultOptions. An input shorter than two bytes is read with the
// default options.
func csvFuzzOptions(in []byte) (csvOpts, []byte) {
	if len(in) < 2 {
		return csvDefault, in
	}
	b0, b1 := int(in[0]), int(in[1])
	fbs := []int{1 << 20, 0, 1, 3, 64, math.MaxInt}
	rfs := []int{4096, 1, 2, 5}
	commas := []byte{',', ';', '\t', '|', ' '}
	quotes := []byte{'"', '\''}
	o := csvOpts{commas[b1%len(commas)], quotes[(b1/len(commas))%len(quotes)], fbs[b0%len(fbs)], rfs[(b0/len(fbs))%len(rfs)]}
	return o, in[2:]
}

func FuzzSTDCSV_Records(f *testing.F) {
	for _, s := range csvGoTestInputs(f) {
		f.Add(append([]byte{0, 0}, s...))
	}
	for k, s := range csvAdversarial {
		f.Add(append([]byte{0, 0}, s...))
		// The same input under other options: the quote '\'' (b1 5),
		// tight limits and the largest field_bytes (b0).
		f.Add(append([]byte{byte(k % 24), 5}, csvSwap(s, '\'')...))
		f.Add(append([]byte{byte(5 + 6*(k%4)), byte(k % 5)}, s...))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		o, in := csvFuzzOptions(raw)
		if len(in) > 8192 {
			in = in[:8192]
		}
		big, small := csvAsk(t, in, o)
		if want := csvExpect(t, string(in), o, 1<<16); !want.ok(big) {
			t.Fatalf("options %+v, input %q:\n got %q\nwant %v", o, in, big, want)
		}
		// 80 bytes: the model at that size (two index slots), or its
		// records up to a TooLarge for the 64-byte data area.
		if want := csvExpect(t, string(in), o, 80); !want.ok(small) {
			i := strings.LastIndex(small, "L ")
			if i < 0 || !strings.HasPrefix(want.exact, small[:i]) {
				t.Fatalf("options %+v, input %q, 80-byte buffer:\n got %q\nwant %v", o, in, small, want)
			}
		}
	})
}

// TestSTDCSV_GoReaderTestInputs runs encoding/csv's own reader-test inputs
// through the reader at both buffer sizes against encoding/csv.
func TestSTDCSV_GoReaderTestInputs(t *testing.T) {
	inputs := csvGoTestInputs(t)
	t.Cleanup(csvStop)
	if len(inputs) < 40 {
		t.Skipf("found %d encoding/csv test inputs in GOROOT", len(inputs))
	}
	for _, in := range inputs {
		big, small := csvAsk(t, []byte(in), csvDefault)
		if want := csvExpect(t, in, csvDefault, 1<<16); !want.ok(big) {
			t.Errorf("input %q:\n got %q\nwant %v", in, big, want)
		}
		if small != big && !strings.Contains(small, "L ") {
			t.Errorf("input %q, 80-byte buffer:\n got %q\nwant %q", in, small, big)
		}
	}
	t.Logf("%d encoding/csv reader-test inputs", len(inputs))
}
