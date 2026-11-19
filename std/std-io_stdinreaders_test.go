// Why: stdin's lock and stdout's lock are taken in a fixed order; readers
// and writers under contention must not deadlock or lose a byte.
// What: TestSTDIO_ConcurrentStdinReaders.
// How: several tasks, each with its own Reader over System.Stdin, read the
// same standard input at once and write every piece to stdout. StreamTake
// holds stdin's lock across its read and flushes stdout and stderr under
// it (the lock order stdin, then stdout or stderr), while the writers take
// stdout's lock alone; so this drives that order at 1 to 16 carriers. Every
// byte must be read exactly once, each piece must be a contiguous run of
// the input, each task's pieces must come in input order, and nothing may
// deadlock (a watchdog). The Linux run is in TestStdIo_LinuxRuns.

package std

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// m5rdCore and m5rdProc are the fixtures core/io_zrd.mk and proc/zrd.mk:
// a stdin Reader and a stdout Writer a spawned task makes for itself.
const m5rdCore = `// Test fixture (std/std-io_stdinreaders_test.go).

import std.raw

export RdProc(sys: &System): uint {
    unsafe "only the address is taken" {
        return raw.Addr(sys.proc)
    }
}

export RdStdin(io: Io, p: uint): Reader {
    unsafe "p is the live Process's address (RdProc)" {
        return ReaderOver(io, &StreamReadTable, ProcCtx(raw.FromAddr[Process](p), 3))
    }
}

export RdStream(io: Io, p: uint, i: int): Writer {
    unsafe "p is the live Process's address (RdProc)" {
        return WriterOver(io, &StreamWriteTable, ProcCtx(raw.FromAddr[Process](p), i))
    }
}
`

const m5rdProc = `import std.core

export RdProc(sys: &System): uint {
    return core.RdProc(sys)
}

export RdStdin(io: Io, p: uint): Reader {
    return core.RdStdin(io, p)
}

export RdStream(io: Io, p: uint, i: int): Writer {
    return core.RdStream(io, p, i)
}
`

// m5rdProgram: args tasks, size. tasks readers (Main is the last one) read
// stdin with Read into size bytes (1 to 256) until end of input; each piece
// goes to stdout as one Bytes call "<task>,<len>:<bytes>". Every 16th piece
// a task also writes "." to stderr, so both writer locks are contended. A
// failed read or write prints "fail <task>" to stderr.
const m5rdProgram = `import std.proc

PutNum(b: mut [300]u8, at: int, x: int): int {
    var d: [20]u8 = [0; 20]
    var n := 0
    var v := x
    for {
        d[n] = Trunc[u8](48 + v % 10)
        n += 1
        v = v / 10
        if v == 0 {
            break
        }
    }
    for i in 0..n {
        b[at + i] = d[n - 1 - i]
    }
    return at + n
}

Read(io: Io, p: uint, tid: int, size: int) {
    var input := proc.RdStdin(io, p)
    var out := proc.RdStream(io, p, 0)
    var err := proc.RdStream(io, p, 1)
    var buf: [256]u8 = [0; 256]
    var rec: [300]u8 = [0; 300]
    var k := 0
    for {
        n := input.Read(mut buf[0..size]) else {
            err.Str("fail read\n") else {}
            return
        }
        if n == 0 {
            return
        }
        var at := PutNum(mut rec, 0, tid)
        rec[at] = 0x2C
        at = PutNum(mut rec, at + 1, n)
        rec[at] = 0x3A
        at += 1
        for i in 0..n {
            rec[at + i] = buf[i]
        }
        out.Bytes(rec[0..at + n]) else {
            err.Str("fail write\n") else {}
            return
        }
        k += 1
        if k % 16 == 0 {
            err.Str(".") else {}
        }
    }
}

export Main(sys: &System): ParseError!void {
    tasks := int(try sys.args[1].ParseInt())
    size := int(try sys.args[2].ParseInt())
    p := proc.RdProc(sys)
    scope s := sys.io.Scope() {
        for tid in 0..tasks - 1 {
            s.go Read(sys.io, p, tid, size)
        }
        Read(sys.io, p, tasks - 1, size)
    }
}
`

// m5rdInput is n records "%07d\n": every record differs, so a piece that
// holds a whole record has one place in the input.
func m5rdInput(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%07d\n", i)
	}
	return b.String()
}

type m5rdPiece struct {
	task int
	data string
}

func m5rdParse(out string) ([]m5rdPiece, error) {
	var ps []m5rdPiece
	for len(out) > 0 {
		c := strings.IndexByte(out, ',')
		k := strings.IndexByte(out, ':')
		if c < 0 || k < c {
			return nil, fmt.Errorf("bad header at %q", m5rdHead(out))
		}
		task, err1 := strconv.Atoi(out[:c])
		n, err2 := strconv.Atoi(out[c+1 : k])
		if err1 != nil || err2 != nil || n <= 0 || k+1+n > len(out) {
			return nil, fmt.Errorf("bad header at %q", m5rdHead(out))
		}
		ps = append(ps, m5rdPiece{task, out[k+1 : k+1+n]})
		out = out[k+1+n:]
	}
	return ps, nil
}

func m5rdHead(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

// m5rdCheck: the pieces hold every input byte once (the same length and
// byte counts); every piece that holds a whole record is the input at that
// record's place, no two such pieces overlap, and one task's pieces come
// in input order.
func m5rdCheck(in string, ps []m5rdPiece) error {
	total := 0
	var count [256]int
	for _, c := range []byte(in) {
		count[c]++
	}
	for _, p := range ps {
		total += len(p.data)
		for _, c := range []byte(p.data) {
			count[c]--
		}
	}
	if total != len(in) {
		return fmt.Errorf("%d bytes read, input has %d", total, len(in))
	}
	for c, n := range count {
		if n != 0 {
			return fmt.Errorf("byte %#x: %d more in the input than read", c, n)
		}
	}
	type span struct{ at, end int }
	var spans []span
	last := map[int]int{}
	placed := 0
	for i, p := range ps {
		j := strings.IndexByte(p.data, '\n')
		if j < 0 || j+9 > len(p.data) {
			continue
		}
		r, err := strconv.Atoi(p.data[j+1 : j+8])
		if err != nil {
			return fmt.Errorf("piece %d (task %d) %q: not a run of the input", i, p.task, m5rdHead(p.data))
		}
		at := r*8 - (j + 1)
		if at < 0 || at+len(p.data) > len(in) || in[at:at+len(p.data)] != p.data {
			return fmt.Errorf("piece %d (task %d) %q is not the input at %d", i, p.task, m5rdHead(p.data), at)
		}
		if l, ok := last[p.task]; ok && at < l {
			return fmt.Errorf("task %d read offset %d after %d", p.task, at, l)
		}
		last[p.task] = at
		spans = append(spans, span{at, at + len(p.data)})
		placed++
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a].at < spans[b].at })
	for i := 1; i < len(spans); i++ {
		if spans[i].at < spans[i-1].end {
			return fmt.Errorf("pieces at %d and %d overlap", spans[i-1].at, spans[i].at)
		}
	}
	if placed == 0 && len(ps) > 0 && len(ps[0].data) >= 15 {
		return fmt.Errorf("no piece could be placed")
	}
	return nil
}

// TestSTDIO_ConcurrentStdinReaders: 8 reader tasks over 1 MiB of input
// (16 refills of the 64 KiB stdin buffer), pieces of at most 7, 64 and 200
// bytes, at 1, 8 and 16 carriers.
func TestSTDIO_ConcurrentStdinReaders(t *testing.T) { ioConcurrentStdinReaders(t, hostIo) }

func ioConcurrentStdinReaders(t *testing.T, h ioHost) {
	exe := h.build(t, map[string]string{"core/io_zrd.mk": m5rdCore, "proc/zrd.mk": m5rdProc}, m5rdProgram)
	in := m5rdInput(1 << 17)
	type c struct {
		carriers int
		size     string
	}
	var cs []c
	for _, k := range []int{1, 8, 16} {
		for _, s := range []string{"7", "64", "200"} {
			cs = append(cs, c{k, s})
		}
	}
	calls := make([]ioCall, len(cs))
	for i, k := range cs {
		calls[i] = ioCall{stdin: in, env: []string{fmt.Sprintf("MK_CARRIERS=%d", k.carriers)}, args: []string{"8", k.size}, watch: 120}
	}
	for i, r := range h.runMany(t, exe, calls) {
		where := fmt.Sprintf("MK_CARRIERS=%d size %s", cs[i].carriers, cs[i].size)
		if r.code != 0 || strings.Contains(r.stderr, "fail") || strings.Trim(r.stderr, ".") != "" {
			t.Errorf("%s: exit %d, stderr %q", where, r.code, m5rdHead(r.stderr))
			continue
		}
		ps, err := m5rdParse(r.stdout)
		if err == nil {
			err = m5rdCheck(in, ps)
		}
		if err != nil {
			t.Errorf("%s: %v", where, err)
			continue
		}
		tasks := map[int]bool{}
		for _, p := range ps {
			tasks[p.task] = true
		}
		t.Logf("%s: %d pieces from %d tasks", where, len(ps), len(tasks))
	}
}
