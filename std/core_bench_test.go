// Why: a local check that std.core's ports, Capped and the erasure cost about
// what Go pays for the same task.
// What: TestCore_BenchLocal (only with MK_CORE8_BENCH=1, on a quiet machine).
// How: whole Mk (--release, x86-64-v2) and Go (GOAMD64=v2, -pgo=off) programs fed the
// same bytes, timed by core8Time; each checksum must be the Go model's (core8BenchSum).
//
// They assert nothing about time, and their numbers are not published: a
// published number comes only from tools/bench (tests/bench-lanes/core8).

package std

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// core8BenchMain drives one measurement: bench <kind> <n>.
const core8BenchMain = `import std.coreprobe

export Main(sys: &System): OutOfMemory|ParseError|WriteError|ReadError!void {
    n := Cast[int](try sys.args[2].ParseInt())
    kind := sys.args[1]
    var sum := 0
    if kind == "port64" {
        sum = try coreprobe.BenchPortRead(sys.io, n, 64, 4096)
    } else if kind == "port16direct" {
        sum = try coreprobe.BenchPortRead(sys.io, n, 16, 0)
    } else if kind == "capped" {
        sum = coreprobe.BenchCapped(n)
    } else if kind == "conn" {
        sum = coreprobe.BenchConn(sys.heap, sys.io, n) else {
            fail ReadError
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
`

// core8BenchProbe is the probe module's measured functions.
const core8BenchProbe = `import std.core

// Pat fills from a 4 KiB pattern, forever.
struct Pat {
    data: [4096]u8
    pos: int
}

PatFill(p: mut Pat, io: Io, dst: mut []u8): ReadError!int {
    var n := 4096 - p.pos
    if n > dst.Len() {
        n = dst.Len()
    }
    (mut dst[0..n]).CopyFrom(p.data[p.pos..p.pos + n])
    p.pos = (p.pos + n) % 4096
    return n
}

const PatPort = core.ReadPortMut[Pat].New(PatFill)

/// n Reads of want bytes through a Reader over Pat with a cap-byte buffer.
export BenchPortRead(io: Io, n: int, want: int, cap: int): ReadError!int {
    var p := Pat(data: [7; 4096], pos: 0)
    var buf: [4096]u8 = [0; 4096]
    var r := (&PatPort).Reader(io, mut p, mut buf[0..cap])
    var d: [64]u8 = [0; 64]
    var sum := 0
    for _ in 0..n {
        sum += try r.Read(mut d[0..want])
    }
    return sum
}

/// n 100-byte records formatted into a Capped of 4 KiB.
export BenchCapped(n: int): int {
    var buf: [4096]u8 = [0; 4096]
    var c := core.Capped.New(mut buf[..])
    var sum := 0
    for i in 0..n {
        c.Reset()
        var w := c.Writer()
        Print(mut w, "level=info i={i} msg=a record of about one hundred bytes in all, as a log line is\n", (i)) else {
            return -1
        }
        sum += c.Bytes().Len()
    }
    return sum
}

/// n Reads of 8 bytes through an erased Conn over Mem.
export BenchConn(h: &Heap, io: Io, n: int): Error|OutOfMemory!int {
    m := BenchMem(total: Mutex[int].New(0))
    c := NewConn(h, m) else e {
        m.Abandon()
        fail e
    }
    defer c.Abandon()
    var buf: [8]u8 = [0; 8]
    var sum := 0
    for _ in 0..n {
        sum += try c.Read(io, mut buf[..])
    }
    return sum
}

linear struct BenchMem {
    total: Mutex[int]
}

impl Transport for BenchMem {
    Read(&self, io: Io, buf: mut []u8): Error!int {
        return buf.Len()
    }
    WriteAll(&self, io: Io, data: []u8): Error!void {
    }
    Close(&self, io: Io): Error!void {
    }
    Abandon(self) {
        BenchMem(total) := self
        _ = total.Into()
    }
}
`

// core8BenchGo is the Go side, a program of its own: the same tasks over
// the same pattern, printing the same checksum.
//   - port64: 64-byte Reads from a bufio.Reader of 4 KiB over an io.Reader
//     of the pattern (Mk: a ReadPortMut Reader with a 4 KiB buffer);
//   - port16direct: 16-byte Reads straight from that io.Reader, one
//     interface call each (Mk: the same port Reader with no buffer);
//   - capped: the record written as Mk's Print writes it, the text before
//     the hole, the integer, the text after, into a fixed 4 KiB buffer that
//     keeps what fits and counts the rest (Mk: Capped);
//   - conn: 8-byte Reads through a mutex and an interface call (Mk: an
//     erased Conn: its mutex, the key check and two indirect calls).
//
// opaque keeps the reader's concrete type from Go's devirtualizer, so each
// call is one indirect call, as the Mk side's table calls are.
const core8BenchGo = `package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
)

type pat struct {
	data [4096]byte
	pos  int
}

func (p *pat) Read(b []byte) (int, error) {
	n := copy(b, p.data[p.pos:])
	p.pos = (p.pos + n) % 4096
	return n, nil
}

func newPat() *pat {
	p := &pat{}
	for i := range p.data {
		p.data[i] = 7
	}
	return p
}

//go:noinline
func opaque(r io.Reader) io.Reader { return r }

type capped struct {
	buf        [4096]byte
	kept, over int
}

func (c *capped) write(b []byte) {
	k := min(len(c.buf)-c.kept, len(b))
	copy(c.buf[c.kept:], b[:k])
	c.kept += k
	c.over += len(b) - k
}

type full struct{}

func (full) Read(b []byte) (int, error) { return len(b), nil }

type conn struct {
	mu sync.Mutex
	t  io.Reader
}

func (c *conn) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t.Read(b)
}

func main() {
	n, _ := strconv.Atoi(os.Args[2])
	sum := 0
	switch os.Args[1] {
	case "port64":
		r := bufio.NewReaderSize(opaque(newPat()), 4096)
		d := make([]byte, 64)
		for range n {
			k, _ := r.Read(d)
			sum += k
		}
	case "port16direct":
		r := opaque(newPat())
		d := make([]byte, 16)
		for range n {
			k, _ := r.Read(d)
			sum += k
		}
	case "capped":
		c := &capped{}
		pre := []byte("level=info i=")
		post := []byte(" msg=a record of about one hundred bytes in all, as a log line is\n")
		var num [20]byte
		for i := range n {
			c.kept, c.over = 0, 0
			c.write(pre)
			c.write(strconv.AppendInt(num[:0], int64(i), 10))
			c.write(post)
			sum += c.kept
		}
	case "conn":
		c := &conn{t: opaque(full{})}
		d := make([]byte, 8)
		for range n {
			k, _ := c.Read(d)
			sum += k
		}
	}
	fmt.Println(sum)
}
`

// core8BenchKinds are the measured tasks.
var core8BenchKinds = []string{"port64", "port16direct", "capped", "conn"}

// core8BenchSum is the checksum of n operations of kind: the bytes read,
// or the bytes kept of the records.
func core8BenchSum(kind string, n int) int {
	switch kind {
	case "port64":
		return 64 * n
	case "port16direct":
		return 16 * n
	case "conn":
		return 8 * n
	}
	sum := 0
	for i := range n {
		sum += min(4096, len(fmt.Sprintf("level=info i=%d msg=a record of about one hundred bytes in all, as a log line is\n", i)))
	}
	return sum
}

// core8Times runs exe kind n five times, checks each run's checksum, and
// returns the wall times, sorted.
func core8Times(t *testing.T, exe, kind string, n int) []time.Duration {
	t.Helper()
	want := fmt.Sprint(core8BenchSum(kind, n))
	var ds []time.Duration
	for range 5 {
		start := time.Now()
		out, err := exec.Command(exe, kind, fmt.Sprint(n)).CombinedOutput()
		d := time.Since(start)
		if err != nil {
			t.Fatalf("%s %s %d: %v\n%s", exe, kind, n, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Fatalf("%s %s %d: checksum %s, want %s", exe, kind, n, got, want)
		}
		ds = append(ds, d)
	}
	slices.Sort(ds)
	return ds
}

// core8Row times n operations of kind by exe, minus a run of none (the
// process's start), and formats ns per operation: the minimum, the median
// and the spread (the slowest run over the fastest).
func core8Row(t *testing.T, side, exe, kind string, n int) string {
	t.Helper()
	full := core8Times(t, exe, kind, n)
	empty := core8Times(t, exe, kind, 0)
	per := func(d time.Duration) float64 { return float64(d.Nanoseconds()) / float64(n) }
	return fmt.Sprintf("%s %-13s min %6.2f  median %6.2f ns/op  spread %.2f", side, kind,
		per(full[0]-empty[0]), per(full[2]-empty[2]), float64(full[4])/float64(full[0]))
}

func TestCore_BenchLocal(t *testing.T) {
	if os.Getenv("MK_CORE8_BENCH") != "1" {
		t.Skip("MK_CORE8_BENCH=1 not set")
	}
	fx := core8Fixtures(map[string]string{"bench.mk": core8BenchProbe})
	mkExe := buildNative(t, fx, core8BenchMain, "--release")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(core8BenchGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module core8bench\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goExe := filepath.Join(dir, "core8bench.exe")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-pgo=off", "-o", goExe, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOAMD64=v2", "CGO_ENABLED=0", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	const n = 20_000_000
	var lines []string
	for _, kind := range core8BenchKinds {
		lines = append(lines, core8Row(t, "mk", mkExe, kind, n), core8Row(t, "go", goExe, kind, n))
	}
	t.Logf("\n%s", strings.Join(lines, "\n"))
}
