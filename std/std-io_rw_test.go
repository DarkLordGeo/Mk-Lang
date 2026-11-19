// Why: Writer and Reader must make the same calls bufio does, and the
// process streams must keep every byte.
// What: TestWriterMatchesBufio and TestReaderMatchesBufio, TestStreamsKeepEveryByte,
// TestStdinRoundTrip and TestEnvNames (streams, stdin, System.Env).
// How: each program is built natively over a copy of std (native_test.go);
// a std-internal probe (stdioProbe, a fixture file in std.core) drives
// Writer and Reader over recording tables and prints every table call,
// which seeded runs compare with what bufio does.

package std

import (
	"bufio"
	"io"
	"math/rand"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// stdioProbe is the fixture core/io_zprobe.mk: recording tables.
const stdioProbe = `// Test fixture (std/std-io_rw_test.go): drives Writer and Reader over
// recording tables and prints every table call, for a differential test
// against Go's bufio.

import std.raw

struct ProbeLog {
    n: int
    calls: [8192]i64
    pos: int
    bad: int
    chunks: *int
    nc: int
    next: int
}

ProbeByte(i: int): u8 {
    return Trunc[u8](i *% 7 +% 3)
}

ProbeLogOf(ctx: uint): *ProbeLog {
    unsafe "the probe's ctx is the address of its live ProbeLog" {
        return raw.FromAddr[ProbeLog](ctx)
    }
}

ProbeRecord(p: *ProbeLog, v: i64) {
    unsafe "the live ProbeLog" {
        if (*p).n < 8192 {
            (*p).calls[(*p).n] = v
            (*p).n += 1
        }
    }
}

ProbeW(w: mut Writer, b: []u8): WriteError!void {
    p := ProbeLogOf(WriterCtx(&*w))
    ProbeRecord(p, i64(b.Len()))
    unsafe "the live ProbeLog" {
        for x in b {
            if x != ProbeByte((*p).pos) {
                (*p).bad += 1
            }
            (*p).pos += 1
        }
    }
}

ProbeF(w: mut Writer): WriteError!void {
    ProbeRecord(ProbeLogOf(WriterCtx(&*w)), -1)
}

// The source serves chunks[next] bytes per call (or fewer when buf is
// smaller), the pattern bytes in order, until chunks run out: then 0.
ProbeR(r: mut Reader, buf: mut []u8): ReadError!int {
    p := ProbeLogOf(ReaderCtx(&*r))
    unsafe "the live ProbeLog" {
        ProbeRecord(p, i64(buf.Len()))
        if (*p).next >= (*p).nc {
            ProbeRecord(p, 0)
            return 0
        }
        var n := raw.Read(raw.Offset((*p).chunks, (*p).next))
        (*p).next += 1
        if n > buf.Len() {
            n = buf.Len()
        }
        for i in 0..n {
            buf[i] = ProbeByte((*p).pos + i)
        }
        (*p).pos += n
        ProbeRecord(p, i64(n))
        return n
    }
}

const ProbeWT = WriterTable(write: ProbeW, flush: ProbeF)
const ProbeRT = ReaderTable(read: ProbeR, line: ReadLineBytewise)

// args: w cap n1 n2 ...: Bytes of each size, then Flush.
// args: r cap c1 c2 ... x q1 q2 ...: a source of chunks c, Reads of sizes q.
export ProbeRun(sys: &System): ParseError|WriteError|ReadError!void {
    var out := sys.Stdout()
    mode := sys.args[1]
    cap := int(try sys.args[2].ParseInt())
    var chunks: [4096]int = [0; 4096]
    var log := ProbeLog(n: 0, calls: [0; 8192], pos: 0, bad: 0, chunks: raw.Null[int](), nc: 0, next: 0)
    var buf: [65536]u8 = [0; 65536]
    var src: [300000]u8 = [0; 300000]
    for i in 0..300000 {
        src[i] = ProbeByte(i)
    }
    if mode == "w" {
        unsafe "the probe's own log, alive for the writer" {
            var w := NewWriter(sys.io, raw.Of(&ProbeWT), raw.Addr(raw.OfMut(mut log)), mut buf[0..cap])
            var s := 0
            for k in 3..sys.args.Len() {
                n := int(try sys.args[k].ParseInt())
                try w.Bytes(src[s..s + n])
                s += n
            }
            try w.Flush()
        }
    } else {
        var nc := 0
        var k := 3
        for k < sys.args.Len() && sys.args[k] != "x" {
            chunks[nc] = int(try sys.args[k].ParseInt())
            nc += 1
            k += 1
        }
        k += 1
        log.chunks = raw.OfMut(mut chunks[0])
        log.nc = nc
        var got: [65536]u8 = [0; 65536]
        var pos := 0
        unsafe "the probe's own log, alive for the reader" {
            var r := NewReader(sys.io, raw.Of(&ProbeRT), raw.Addr(raw.OfMut(mut log)), mut buf[0..cap])
            for k < sys.args.Len() {
                q := int(try sys.args[k].ParseInt())
                n := try r.Read(mut got[0..q])
                try Print(mut out, "got {n}\n", (n))
                for i in 0..n {
                    if got[i] != ProbeByte(pos + i) {
                        log.bad += 1
                    }
                }
                pos += n
                k += 1
            }
        }
    }
    for i in 0..log.n {
        v := log.calls[i]
        try Print(mut out, "{v}\n", (v))
    }
    bad := log.bad
    try Print(mut out, "bad {bad}\n", (bad))
}
`

const stdioProbeTime = `// Test fixture: lets a program reach std.core's I/O probe.
import std.core

export ProbeRun(sys: &System): ParseError|WriteError|ReadError!void {
    try core.ProbeRun(sys)
}
`

const stdioProbeMain = `import std.time

export Main(sys: &System): ParseError|WriteError|ReadError!void {
    try time.ProbeRun(sys)
}
`

// buildProbe builds the probe program.
func buildProbe(t *testing.T, h ioHost) string {
	t.Helper()
	return h.build(t, map[string]string{"core/io_zprobe.mk": stdioProbe, "time/zprobe.mk": stdioProbeTime}, stdioProbeMain)
}

// probeByte is the probe's pattern byte at stream position i.
func probeByte(i int) byte { return byte(i*7 + 3) }

// recWriter records every Write's length, as the probe's table does.
type recWriter struct{ calls []string }

func (r *recWriter) Write(p []byte) (int, error) {
	r.calls = append(r.calls, strconv.Itoa(len(p)))
	return len(p), nil
}

// bufioWriterCalls is the reference: the calls a bufio.Writer of size cap
// makes for these writes and a Flush, then the probe's flush marker -1. A
// cap of 0 has no bufio form: every non-empty write passes through.
func bufioWriterCalls(cap int, sizes []int) []string {
	r := &recWriter{}
	src := make([]byte, 300000)
	for i := range src {
		src[i] = probeByte(i)
	}
	s := 0
	if cap == 0 {
		for _, n := range sizes {
			if n > 0 {
				r.Write(src[s : s+n])
			}
			s += n
		}
	} else {
		w := bufio.NewWriterSize(r, cap)
		for _, n := range sizes {
			w.Write(src[s : s+n])
			s += n
		}
		w.Flush()
	}
	return append(r.calls, "-1")
}

// scriptReader serves chunks[i] bytes (or fewer) per call, then io.EOF,
// logging (requested, returned) as the probe's table does.
type scriptReader struct {
	chunks []int
	next   int
	pos    int
	log    []string
}

func (s *scriptReader) Read(p []byte) (int, error) {
	s.log = append(s.log, strconv.Itoa(len(p)))
	if s.next >= len(s.chunks) {
		s.log = append(s.log, "0")
		return 0, io.EOF
	}
	n := min(s.chunks[s.next], len(p))
	s.next++
	for i := 0; i < n; i++ {
		p[i] = probeByte(s.pos + i)
	}
	s.pos += n
	s.log = append(s.log, strconv.Itoa(n))
	return n, nil
}

// bufioReaderTrace is the reference for a Reader of size cap: the count
// each Read returns, then the table calls.
func bufioReaderTrace(cap int, chunks, reads []int) []string {
	src := &scriptReader{chunks: chunks}
	r := bufio.NewReaderSize(src, cap)
	var out []string
	for _, q := range reads {
		n, _ := r.Read(make([]byte, q))
		out = append(out, "got "+strconv.Itoa(n))
	}
	return append(out, src.log...)
}

// probeLines is the probe's output without its last line, which must say
// the tables saw the pattern bytes in order.
func probeLines(t *testing.T, r nativeRun) []string {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("probe exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if last := lines[len(lines)-1]; last != "bad 0" {
		t.Fatalf("the bytes the tables saw differ from the pattern: %q", last)
	}
	return lines[:len(lines)-1]
}

// TestWriterMatchesBufio: for seeded write sequences over buffers of
// 0 to 4096 bytes, Writer.Bytes and Flush make exactly the table calls a
// bufio.Writer of the same size makes (Go's rule: fill, hand over full,
// write a large rest directly), and every
// byte arrives once, in order. Edge sequences: empty writes, a write of
// exactly the free space, of exactly cap, of cap + 1, a full buffer then
// one byte.
func TestWriterMatchesBufio(t *testing.T) { ioWriterMatchesBufio(t, hostIo) }

func ioWriterMatchesBufio(t *testing.T, h ioHost) {
	exe := buildProbe(t, h)
	type tc struct {
		cap   int
		sizes []int
	}
	cases := []tc{
		{8, []int{3, 5, 20, 1, 1, 7}},
		{8, []int{0, 0, 8, 0, 8, 9, 1}},
		{8, []int{8, 1, 7, 1, 16, 15, 17}},
		{1, []int{1, 1, 2, 0, 3}},
		{0, []int{0, 5, 0, 1, 4096}},
		{4096, []int{4095, 1, 1, 4096, 8192, 1, 4097}},
	}
	rng := rand.New(rand.NewSource(108))
	caps := []int{0, 1, 2, 7, 8, 16, 63, 64, 512, 4096}
	for seed := 0; seed < 60; seed++ {
		c := caps[rng.Intn(len(caps))]
		var sizes []int
		total := 0
		for k := 0; k < 40; k++ {
			var n int
			switch rng.Intn(4) {
			case 0:
				n = rng.Intn(4)
			case 1:
				n = rng.Intn(c + 2)
			case 2:
				n = max(0, c-1+rng.Intn(3))
			default:
				n = rng.Intn(3*c + 50)
			}
			if total+n > 290000 {
				break
			}
			total += n
			sizes = append(sizes, n)
		}
		cases = append(cases, tc{c, sizes})
	}
	calls := make([]ioCall, len(cases))
	for i, c := range cases {
		args := []string{"w", strconv.Itoa(c.cap)}
		for _, n := range c.sizes {
			args = append(args, strconv.Itoa(n))
		}
		calls[i] = ioCall{args: args}
	}
	runs := h.runMany(t, exe, calls)
	for i, c := range cases {
		got := probeLines(t, runs[i])
		want := bufioWriterCalls(c.cap, c.sizes)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("case %d cap %d sizes %v:\n got  %v\n want %v", i, c.cap, c.sizes, got, want)
		}
	}
}

// TestReaderMatchesBufio: for seeded sources (chunk sizes per table
// read) and read sizes over buffers of 16 to 4096 bytes, Reader.Read
// returns the counts bufio.Reader returns and makes the same table calls
// with the same sizes (a read at least as large as the buffer goes
// straight to the table), and the bytes arrive in order.
func TestReaderMatchesBufio(t *testing.T) { ioReaderMatchesBufio(t, hostIo) }

func ioReaderMatchesBufio(t *testing.T, h ioHost) {
	exe := buildProbe(t, h)
	type tc struct {
		cap           int
		chunks, reads []int
	}
	cases := []tc{
		{16, []int{5, 100, 3}, []int{4, 4, 20, 30, 100}},
		{16, []int{16, 16, 1}, []int{15, 1, 16, 1, 1, 1}},
		{16, nil, []int{1, 16, 0}},
		{4096, []int{1, 2, 3, 4096, 9000}, []int{4096, 1, 1, 4095, 4096, 4096, 4096}},
	}
	rng := rand.New(rand.NewSource(109))
	caps := []int{16, 17, 64, 100, 1024, 4096}
	for seed := 0; seed < 60; seed++ {
		c := caps[rng.Intn(len(caps))]
		var chunks, reads []int
		for k := 0; k < 30; k++ {
			chunks = append(chunks, 1+rng.Intn(2*c+10))
		}
		for k := 0; k < 40; k++ {
			if rng.Intn(3) == 0 {
				reads = append(reads, c+rng.Intn(c+5))
			} else {
				reads = append(reads, rng.Intn(c+1))
			}
		}
		cases = append(cases, tc{c, chunks, reads})
	}
	calls := make([]ioCall, len(cases))
	for i, c := range cases {
		args := []string{"r", strconv.Itoa(c.cap)}
		for _, n := range c.chunks {
			args = append(args, strconv.Itoa(n))
		}
		args = append(args, "x")
		for _, n := range c.reads {
			args = append(args, strconv.Itoa(n))
		}
		calls[i] = ioCall{args: args}
	}
	runs := h.runMany(t, exe, calls)
	for i, c := range cases {
		got := probeLines(t, runs[i])
		want := bufioReaderTrace(c.cap, c.chunks, c.reads)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("case %d cap %d chunks %v reads %v:\n got  %v\n want %v", i, c.cap, c.chunks, c.reads, got, want)
		}
	}
}

// streamsProgram prints, for each size, that many letters to stdout,
// alternating between two Writers from Stdout() (one shared buffer), and
// every third piece to stderr too, so pieces cross the 8 KiB buffer's edge
// in every way; with args[1] "panic" it then panics, so the report's flush
// must write what was buffered.
const streamsProgram = `Piece(n: int, k: int, b: mut [20000]u8): []u8 from b {
    for i in 0..n {
        b[i] = Trunc[u8](0x61 + (i +% k) % 26)
    }
    return b[0..n]
}

export Main(sys: &System): WriteError|ParseError!void {
    var a := sys.Stdout()
    var b := sys.Stdout()
    var e := sys.Stderr()
    var buf: [20000]u8 = [0; 20000]
    for k in 2..sys.args.Len() {
        n := int(try sys.args[k].ParseInt())
        p := Piece(n, k, mut buf)
        if k % 2 == 0 {
            try a.Bytes(p)
        } else {
            try b.Bytes(p)
        }
        if k % 3 == 0 {
            try e.Bytes(p)
        }
    }
    if sys.args[1] == "panic" {
        Assert(false)
    }
}
`

func streamsPiece(n, k int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(byte(0x61 + (i+k)%26))
	}
	return b.String()
}

// TestStreamsKeepEveryByte: the process streams' shared buffer
// (io_stream.mk StreamPut) delivers every byte of every writer in program
// order, on stdout and stderr, for pieces from 0 bytes to more than twice
// the 8 KiB buffer; and a panic's report still flushes what was buffered
// (TryFlushStd).
func TestStreamsKeepEveryByte(t *testing.T) { ioStreamsKeepEveryByte(t, hostIo) }

func ioStreamsKeepEveryByte(t *testing.T, h ioHost) {
	exe := h.build(t, nil, streamsProgram)
	rng := rand.New(rand.NewSource(114))
	var calls []ioCall
	var wantOut, wantErr []string
	for run := 0; run < 12; run++ {
		mode := "ok"
		if run%4 == 3 {
			mode = "panic"
		}
		args := []string{mode}
		var out, errb strings.Builder
		for k := 2; k < 60; k++ {
			var n int
			switch rng.Intn(5) {
			case 0:
				n = 0
			case 1:
				n = 8192 - rng.Intn(3)
			case 2:
				n = 8192 + rng.Intn(9000)
			default:
				n = rng.Intn(3000)
			}
			args = append(args, strconv.Itoa(n))
			out.WriteString(streamsPiece(n, k))
			if k%3 == 0 {
				errb.WriteString(streamsPiece(n, k))
			}
		}
		calls = append(calls, ioCall{args: args})
		wantOut, wantErr = append(wantOut, out.String()), append(wantErr, errb.String())
	}
	for run, r := range h.runMany(t, exe, calls) {
		mode := calls[run].args[0]
		wantCode := 0
		if mode == "panic" {
			wantCode = 101
		}
		if r.code != wantCode {
			t.Fatalf("run %d: exit %d, want %d\nstderr tail: %q", run, r.code, wantCode, stdioTail(r.stderr))
		}
		if r.stdout != wantOut[run] {
			t.Errorf("run %d: stdout differs: got %d bytes, want %d", run, len(r.stdout), len(wantOut[run]))
		}
		gotErr := r.stderr
		if mode == "panic" {
			if i := strings.LastIndex(gotErr, "panic: "); i >= 0 {
				gotErr = gotErr[:i]
			}
		}
		if gotErr != wantErr[run] {
			t.Errorf("run %d: stderr differs: got %d bytes, want %d", run, len(gotErr), len(wantErr[run]))
		}
	}
}

func stdioTail(s string) string {
	if len(s) > 300 {
		return s[len(s)-300:]
	}
	return s
}

// stdinProgram copies standard input to standard output: by lines through
// Reader.Line (each printed with \n) when args[1] is "line", else by Read
// into a buffer of args[2] bytes.
const stdinProgram = `export Main(sys: &System): WriteError|ReadError|OutOfMemory|ParseError!void {
    var input := sys.Stdin()
    var out := sys.Stdout()
    if sys.args[1] == "line" {
        for {
            l := try input.Line(sys.arena) else {
                break
            }
            try out.Str(l)
            try out.Str("\n")
        }
        return
    }
    n := int(try sys.args[2].ParseInt())
    var buf: [70000]u8 = [0; 70000]
    for {
        got := try input.Read(mut buf[0..n])
        if got == 0 {
            break
        }
        try out.Bytes(buf[0..got])
    }
}
`

// TestStdinRoundTrip: standard input read with Read at buffer sizes
// 1, 7, 4096 and 70,000 (more than the 64 KiB process buffer) comes back
// byte for byte; read by Line, it gives the lines bufio.Scanner's
// ScanLines gives (\n and one \r before it stripped, a last line without
// \n kept).
func TestStdinRoundTrip(t *testing.T) { ioStdinRoundTrip(t, hostIo) }

func ioStdinRoundTrip(t *testing.T, h ioHost) {
	exe := h.build(t, nil, stdinProgram)
	rng := rand.New(rand.NewSource(115))
	var in strings.Builder
	for in.Len() < 200000 {
		n := rng.Intn(120)
		for i := 0; i < n; i++ {
			in.WriteByte(byte(0x20 + rng.Intn(95)))
		}
		if rng.Intn(4) == 0 {
			in.WriteString("\r\n")
		} else {
			in.WriteString("\n")
		}
	}
	// The SWAR newline scan (UptoNewline): a \n at every offset of a word,
	// next to bytes that differ from 0x0A only in their high bit (U+028A is
	// CA 8A, U+010A is C4 8A, U+0A0A is E0 A8 8A) and next to 0x09 and 0x0B.
	for n := 0; n < 40; n++ {
		for i := 0; i < n; i++ {
			in.WriteString([]string{"ʊ", "Ċ", "ਊ", "\t", "\v", "x"}[(i+n)%6])
		}
		in.WriteString("\n")
	}
	in.WriteString("last line, no newline")
	text := in.String()
	sizes := []string{"7", "4096", "70000"}
	calls := []ioCall{{stdin: text[:5000], args: []string{"read", "1"}}, {stdin: text, args: []string{"line"}}}
	for _, n := range sizes {
		calls = append(calls, ioCall{stdin: text, args: []string{"read", n}})
	}
	runs := h.runMany(t, exe, calls)
	wantRun(t, runs[0], 0, text[:5000])
	for i, n := range sizes {
		if r := runs[2+i]; r.code != 0 || r.stdout != text {
			t.Errorf("read %s: exit %d, %d bytes back of %d", n, r.code, len(r.stdout), len(text))
		}
	}
	var want strings.Builder
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		want.WriteString(sc.Text() + "\n")
	}
	r := runs[1]
	if r.code != 0 || r.stdout != want.String() {
		t.Errorf("line: exit %d, %d bytes, want %d\nstderr: %s", r.code, len(r.stdout), want.Len(), r.stderr)
	}
}

// envProgram prints name=value, or name=none, for each argument.
const envProgram = `export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for k in 1..sys.args.Len() {
        n := sys.args[k]
        v := sys.Env(n) else {
            try Print(mut out, "{n}=none\n", (n))
            continue
        }
        try Print(mut out, "{n}={v}\n", (n, v))
    }
}
`

// TestEnvNames: System.Env finds a variable by name; on Windows the
// name compares ASCII case-insensitively, as the OS's own lookup does
// (io_system.mk EnvNameIs), and elsewhere exactly; an unset name and the
// empty name are none, and a value may hold '=' and non-ASCII text.
func TestEnvNames(t *testing.T) { ioEnvNames(t, hostIo) }

func ioEnvNames(t *testing.T, h ioHost) {
	exe := h.build(t, nil, envProgram)
	r := h.runMany(t, exe, []ioCall{{
		env:  []string{"MK_STDIO_A=1", "MK_STDIO_B=x=y é"},
		args: []string{"MK_STDIO_A", "mk_stdio_a", "MK_STDIO_B", "MK_STDIO_UNSET", ""},
	}})[0]
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	out := r.stdout
	lower := "mk_stdio_a=none"
	if !h.linux && runtime.GOOS == "windows" {
		lower = "mk_stdio_a=1"
	}
	want := "MK_STDIO_A=1\n" + lower + "\nMK_STDIO_B=x=y é\nMK_STDIO_UNSET=none\n=none\n"
	if string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
