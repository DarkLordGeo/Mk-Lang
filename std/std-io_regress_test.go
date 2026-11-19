// Why: six defects of the process streams and std.fs each keep the case
// that failed before the fix.
// What: the TestSTDIO_ regression tests: the streams under cancellation, ReadAt
// and WriteAt at a negative position, File I/O on a handle with no position,
// fs.ReadBytes's arena use at an unknown size, handle release on error paths.
// How: native programs and the scripted Io of std-io_fault_test.go; each test
// has a Linux run in TestStdIo_LinuxRuns (std-io_wsl_test.go).

package std

import (
	"fmt"
	"math/rand"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// reviewStreams: args[1] a mode. "out", "err": write "hello\n" to stdout
// and stderr, then 9,000 bytes to stdout (or stderr) inside a cancelled
// scope body, then "bye\n" to both. "flush": Flush both streams in the
// cancelled body instead. "task": a task spawned into the cancelled scope
// writes the 9,000 bytes to stdout through its own Writer (the io_zmn
// fixture). "prompt": write prompts to stdout and stderr, read stdin once
// in a cancelled body, then read it normally and echo it. Every mode ends
// with "|failed F seen S" on stderr: F 1 when the write, flush or read in
// the cancelled body failed, S 1 when Io.Sleep(0) there was Canceled (so
// the body really was cancelled).
const reviewStreams = `import std.proc
import std.time

Fill(b: mut [9000]u8) {
    for i in 0..9000 {
        b[i] = Trunc[u8](0x61 + i % 26)
    }
}

Child(io: Io, p: uint, failed: mut int) {
    var w := proc.MnStream(io, p, 0)
    var b: [9000]u8 = [0; 9000]
    Fill(mut b)
    w.Bytes(b[..]) else {
        *failed = 1
    }
}

export Main(sys: &System): WriteError|ReadError!void {
    var out := sys.Stdout()
    var e := sys.Stderr()
    mode := sys.args[1]
    var big: [9000]u8 = [0; 9000]
    Fill(mut big)
    var failed := 0
    var seen := 0
    if mode == "prompt" {
        try out.Str("prompt: ")
        try e.Str("eprompt: ")
        var inp := sys.Stdin()
        var buf: [64]u8 = [0; 64]
        scope s := sys.io.Scope() {
            s.Cancel()
            _ = inp.Read(mut buf[..]) else {
                failed = 1
            }
            sys.io.Sleep(time.Millis(0)) else {
                seen = 1
            }
        }
        n := try inp.Read(mut buf[..])
        try out.Bytes(buf[0..n])
    } else {
        try out.Str("hello\n")
        try e.Str("hello\n")
        p := proc.MnProc(sys)
        scope s := sys.io.Scope() {
            s.Cancel()
            if mode == "out" {
                out.Bytes(big[..]) else {
                    failed = 1
                }
            } else if mode == "err" {
                e.Bytes(big[..]) else {
                    failed = 1
                }
            } else if mode == "flush" {
                out.Flush() else {
                    failed = 1
                }
                e.Flush() else {
                    failed = 1
                }
            } else if mode == "task" {
                s.go Child(sys.io, p, mut failed)
            }
            sys.io.Sleep(time.Millis(0)) else {
                seen = 1
            }
        }
        try out.Str("bye\n")
        try e.Str("bye\n")
    }
    try Print(mut e, "|failed {failed} seen {seen}\n", (failed, seen))
}
`

func reviewBig() string {
	var b strings.Builder
	for i := 0; i < 9000; i++ {
		b.WriteByte(byte(0x61 + i%26))
	}
	return b.String()
}

// TestSTDIO_CancelledWriteKeepsBufferedBytes: a stream write, a Flush
// or a spawned task's write inside a cancelled scope must not throw away
// bytes other writes had already buffered (they were told the bytes were
// written). A stream write is not a cancellation point (io_stream.mk
// StreamPut writes through io.NoCancel()), so the cancelled writer's own
// bytes are written too, in order. Before the fix stdout was empty in
// mode "out" (the 8 KiB buffer, "hello\n" included, was lost).
//
// TestSTDIO_CancelledStdinReadKeepsPrompt: a cancelled stdin read fails
// (ReadError) at its own read, but the prompts it flushed first reach
// stdout and stderr; the next read gets the input. Before the fix
// "prompt: " was lost.
func TestSTDIO_CancelledWriteKeepsBufferedBytes(t *testing.T) { ioCancelledWrite(t, hostIo) }

func TestSTDIO_CancelledStdinReadKeepsPrompt(t *testing.T) { ioCancelledStdinRead(t, hostIo) }

func ioCancelledWrites(t *testing.T, h ioHost) {
	t.Run("R1", func(t *testing.T) { ioCancelledWrite(t, h) })
	t.Run("R2", func(t *testing.T) { ioCancelledStdinRead(t, h) })
}

func buildReviewStreams(t *testing.T, h ioHost) string {
	return h.build(t, map[string]string{"core/io_zmn.mk": mnCore, "proc/zmn.mk": mnProc}, reviewStreams)
}

func ioCancelledWrite(t *testing.T, h ioHost) {
	exe := buildReviewStreams(t, h)
	big := reviewBig()
	modes := []string{"out", "err", "flush", "task"}
	var calls []ioCall
	for _, m := range modes {
		for k := 0; k < 3; k++ {
			calls = append(calls, ioCall{args: []string{m}, watch: 60})
		}
	}
	for i, r := range h.runMany(t, exe, calls) {
		m := calls[i].args[0]
		wantOut, wantErr := "hello\nbye\n", "hello\nbye\n"
		switch m {
		case "out", "task":
			wantOut = "hello\n" + big + "bye\n"
		case "err":
			wantErr = "hello\n" + big + "bye\n"
		}
		wantErr += "|failed 0 seen 1\n"
		if r.code != 0 || r.stdout != wantOut || r.stderr != wantErr {
			t.Errorf("%s: exit %d, stdout %d bytes (want %d, starts %q), stderr %d bytes (want %d), tail %q",
				m, r.code, len(r.stdout), len(wantOut), stdioHead(r.stdout), len(r.stderr), len(wantErr), stdioTail(r.stderr))
		}
	}
}

func ioCancelledStdinRead(t *testing.T, h ioHost) {
	exe := buildReviewStreams(t, h)
	for _, r := range h.runMany(t, exe, []ioCall{{stdin: "xyz", args: []string{"prompt"}, watch: 60}}) {
		if r.code != 0 || r.stdout != "prompt: xyz" || r.stderr != "eprompt: |failed 1 seen 1\n" {
			t.Errorf("prompt: exit %d, stdout %q (want %q), stderr %q", r.code, r.stdout, "prompt: xyz", r.stderr)
		}
	}
}

func stdioHead(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

// reviewFs drives real files. "neg": File.ReadAt and WriteAt at negative
// positions, then the file's own Read and Write. "cat path": File.Read
// to the end of path, echoed to stdout. "put path": stdin copied to path
// through File.Writer, then File.WriteAll of "|end\n" (on Linux the
// paths are /dev/stdin and /dev/stdout over pipes).
const reviewFs = `import std.fs

Name(e: fs.Error|OutOfMemory): str {
    match e {
        fs.Os(code: c) {
            if c == 0 {
                return "Os0"
            }
            return "Os"
        }
        _ {
            return "other"
        }
    }
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError|ReadError!void {
    var out := sys.Stdout()
    io := sys.io
    cmd := sys.args[1]
    if cmd == "neg" {
        try fs.WriteAll(io, "t.txt", "abcdefgh".AsBytes())
        var f := try fs.Open(io, "t.txt")
        onerr f.Abandon()
        var b: [3]u8 = [0; 3]
        var r1 := "ok"
        _ = f.ReadAt(io, -1, mut b[..]) else e {
            r1 = Name(e)
        }
        var r2 := "ok"
        _ = f.ReadAt(io, -7, mut b[..]) else e {
            r2 = Name(e)
        }
        var r3 := "ok"
        _ = f.ReadAt(io, -9_223_372_036_854_775_807 - 1, mut b[..]) else e {
            r3 = Name(e)
        }
        n := try f.Read(io, mut b[..])
        try Print(mut out, "{r1} {r2} {r3} ", (r1, r2, r3))
        try out.Bytes(b[0..n])
        f.Abandon()
        var g := try fs.Create(io, "w.txt")
        onerr g.Abandon()
        var r4 := "ok"
        _ = g.WriteAt(io, -1, "Z".AsBytes()) else e {
            r4 = Name(e)
        }
        try g.WriteAll(io, "abc".AsBytes())
        try g.Close(io)
        w := try fs.ReadBytes(sys.arena, io, "w.txt")
        try Print(mut out, " {r4} ", (r4))
        try out.Bytes(w)
        try out.Str("\n")
    } else if cmd == "cat" {
        var f := try fs.Open(io, sys.args[2])
        onerr f.Abandon()
        var buf: [65536]u8 = [0; 65536]
        for {
            n := try f.Read(io, mut buf[..])
            if n == 0 {
                break
            }
            try out.Bytes(buf[0..n])
        }
        try f.Close(io)
    } else if cmd == "put" {
        var g := try fs.Create(io, sys.args[2])
        onerr g.Abandon()
        var wb: [4096]u8 = [0; 4096]
        var w := g.Writer(io, mut wb[..])
        var inp := sys.Stdin()
        var buf: [65536]u8 = [0; 65536]
        for {
            n := try inp.Read(mut buf[..])
            if n == 0 {
                break
            }
            try w.Bytes(buf[0..n])
        }
        try w.Flush()
        try g.WriteAll(io, "|end\n".AsBytes())
        try g.Close(io)
    }
}
`

// TestSTDIO_NegativePositionFails: File.ReadAt and WriteAt at a
// negative position are Os(code: 0) with no call; they used to read and
// write at the handle's hidden stream position ("abc", then "def"), and
// the file's own Read and Write are unaffected.
func TestSTDIO_NegativePositionFails(t *testing.T) { ioNegativePosition(t, hostIo) }

func ioNegativePosition(t *testing.T, h ioHost) {
	exe := h.build(t, nil, reviewFs)
	r := h.runMany(t, exe, []ioCall{{args: []string{"neg"}}})[0]
	wantRun(t, r, 0, "Os0 Os0 Os0 abc Os0 abc\n")
}

// TestSTDIO_FileOnNoPositionHandle: File.Read, File.Writer and
// File.WriteAll on a path work whatever the handle is. On Linux the paths
// are /dev/stdin and /dev/stdout over pipes, where a positioned
// pread/pwrite is ESPIPE: File falls back to the stream position
// (core.CursorRead, CursorPut). Windows has no such path for a pipe, so
// there the same commands run over regular files (no regression).
func TestSTDIO_FileOnNoPositionHandle(t *testing.T) { ioFileOnNoPositionHandle(t, hostIo) }

func ioFileOnNoPositionHandle(t *testing.T, h ioHost) {
	exe := h.build(t, nil, reviewFs)
	rng := rand.New(rand.NewSource(4))
	var in strings.Builder
	for in.Len() < 100_000 {
		in.WriteByte(byte(0x20 + rng.Intn(95)))
	}
	text := in.String()
	if !h.linux {
		r := h.runMany(t, exe, []ioCall{{args: []string{"put", "p.txt"}, stdin: text}, {args: []string{"cat", "p.txt"}}})
		wantRun(t, r[0], 0, "")
		wantRun(t, r[1], 0, text+"|end\n")
		return
	}
	// Both backends: the default one (io_uring where the kernel has it,
	// whose READ and WRITE ignore the offset of a pipe), and MK_IO=epoll,
	// where file I/O is pread64/pwrite64 on the blocking pool and a pipe
	// is ESPIPE: the old File failed there with fs.Os(29) (checked on WSL,
	// 2026-11-05); the default backend never showed it.
	var calls []ioCall
	for _, env := range [][]string{nil, {"MK_IO=epoll"}} {
		calls = append(calls,
			ioCall{args: []string{"cat", "/dev/stdin"}, stdin: text, pipe: true, env: env},
			ioCall{args: []string{"put", "/dev/stdout"}, stdin: text, pipe: true, env: env})
	}
	for i, r := range h.runMany(t, exe, calls) {
		want := text
		if calls[i].args[0] == "put" {
			want += "|end\n"
		}
		if r.code != 0 || r.stdout != want {
			t.Errorf("%v %v over a pipe: exit %d, %d bytes of %d\nstdout starts %q\nstderr: %s", calls[i].env, calls[i].args, r.code, len(r.stdout), len(want), stdioHead(r.stdout), r.stderr)
		}
	}
}

// reviewFault drives std.fs over the scripted Io of std-io_fault_test.go.
// args[1]: "neg" (ReadAt and WriteAt at -1 make no table call),
// "espipe op err os" (mode 12, positioned calls fail with
// SysErr[err] and os), "pipe n" and "sized n" (ReadBytes of n bytes,
// size 0 or n, into a fresh sub-arena; the bytes are checked against the
// fixture's pattern and the sub-arena's taken bytes printed), "bench n
// reps" (ReadBytes's benchmark: mode 11, nothing filled), "leak rounds" (real
// handles, every failing path, the OS handle count before and after).
const reviewFault = `import std.fs

Name(e: fs.Error|OutOfMemory|WriteError): str {
    match e {
        fs.NotFound {
            return "NotFound"
        }
        fs.Denied {
            return "Denied"
        }
        fs.Os(code: c) {
            if c == 0 {
                return "Os 0"
            }
            if c == 29 {
                return "Os 29"
            }
            return "Os ?"
        }
        WriteError {
            return "WriteError"
        }
        OutOfMemory {
            return "OutOfMemory"
        }
        _ {
            return "other"
        }
    }
}

Espipe(io: Io, op: str): fs.Error|OutOfMemory|WriteError!int {
    data := "0123456789".AsBytes()
    var f := try fs.Create(io, "x")
    onerr f.Abandon()
    var buf: [8]u8 = [0; 8]
    var n := 0
    if op == "read" {
        n = try f.Read(io, mut buf[..])
        n += try f.Read(io, mut buf[..])
    } else if op == "write" {
        n = try f.Write(io, data)
        n += try f.Write(io, data)
    } else if op == "writer" {
        var wb: [4]u8 = [0; 4]
        var w := f.Writer(io, mut wb[..])
        try w.Bytes(data)
        try w.Bytes(data)
        try w.Flush()
    } else if op == "fwriteall" {
        try f.WriteAll(io, data)
    } else if op == "readat" {
        n = try f.ReadAt(io, 0, mut buf[..])
    } else if op == "readwrite" {
        n = try f.Read(io, mut buf[..])
        n += try f.Write(io, data)
    }
    try f.Close(io)
    return n
}

Pattern(b: []u8): int {
    var bad := 0
    for i in 0..b.Len() {
        var want: u8 = 0x61
        if i % 5 == 4 {
            want = 0x0A
        }
        if b[i] != want {
            bad += 1
        }
    }
    return bad
}

CloseFails(io: Io): fs.Error!void {
    var f := try fs.Open(io, "leak.bin")
    onerr f.Abandon()
    try f.Close(io)
}

WriterCloseFails(io: Io): fs.Error|WriteError!void {
    var f := try fs.Create(io, "leak.bin")
    onerr f.Abandon()
    var wb: [4]u8 = [0; 4]
    var w := f.Writer(io, mut wb[..])
    try w.Str("0123456789")
    try w.Flush()
    try f.Close(io)
}

// Round runs every failing path of fs that holds a handle, and the
// succeeding ones, once each over real handles.
Round(sys: &System) {
    data := "0123456789".AsBytes()
    p := "leak.bin"
    fs.WriteAll(fs.FaultIo(0, 0, 0, 0), p, data) else {}
    rb: [5]u32 = [2, 3, 4, 5, 0]
    for i in 0..5 {
        _ = fs.ReadBytes(sys.arena, fs.FaultIo(rb[i], 3, 0, 10), p) else {}
    }
    wa: [6]u32 = [3, 7, 4, 5, 8, 6]
    for i in 0..6 {
        fs.WriteAll(fs.FaultIo(wa[i], 6, 0, 0), p, data) else {}
    }
    CloseFails(fs.FaultIo(8, 3, 0, 0)) else {}
    WriterCloseFails(fs.FaultIo(8, 3, 0, 0)) else {}
    WriterCloseFails(fs.FaultIo(3, 6, 0, 0)) else {}
    CloseFails(fs.FaultIo(0, 0, 0, 0)) else {}
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError|ParseError!void {
    var out := sys.Stdout()
    op := sys.args[1]
    if op == "neg" {
        io := fs.FaultIo(0, 0, 0, 10)
        var f := try fs.Create(io, "x")
        onerr f.Abandon()
        var buf: [8]u8 = [0; 8]
        var r1 := "ok"
        _ = f.ReadAt(io, -1, mut buf[..]) else e {
            r1 = Name(e)
        }
        var r2 := "ok"
        _ = f.WriteAt(io, -1, "Z".AsBytes()) else e {
            r2 = Name(e)
        }
        c1 := fs.FaultCalls()
        n := try f.Read(io, mut buf[..])
        try f.Close(io)
        c2 := fs.FaultCalls()
        try Print(mut out, "readat {r1} writeat {r2} calls {c1} read {n} calls {c2}\n", (r1, r2, c1, n, c2))
        return
    }
    if op == "espipe" {
        err := Trunc[u32](try sys.args[3].ParseInt())
        os := try sys.args[4].ParseInt()
        io := fs.FaultIo(12, err, os, 10)
        n := Espipe(io, sys.args[2]) else e {
            name := Name(e)
            calls := fs.FaultCalls()
            try Print(mut out, "{name} calls {calls}\n", (name, calls))
            return
        }
        calls := fs.FaultCalls()
        try Print(mut out, "ok {n} calls {calls}\n", (n, calls))
        return
    }
    if op == "pipe" || op == "sized" || op == "bench" {
        n := try sys.args[2].ParseInt()
        var mode: u32 = 10
        if op == "sized" {
            mode = 0
        } else if op == "bench" {
            mode = 11
        }
        var reps := 1
        if op == "bench" {
            reps = int(try sys.args[3].ParseInt())
        }
        var total := 0
        for _ in 0..reps {
            var sub := try sys.arena.Sub()
            b := fs.ReadBytes(&sub, fs.FaultIo(mode, 0, 0, n), "x") else e {
                sub.Free()
                name := Name(e)
                try Print(mut out, "{name}\n", (name))
                return
            }
            len := b.Len()
            total += len
            var bad := 0
            if op != "bench" {
                bad = Pattern(b)
            }
            taken := fs.ArenaTaken(&sub)
            calls := fs.FaultCalls()
            sub.Free()
            if op != "bench" {
                try Print(mut out, "ok {len} bad {bad} taken {taken} calls {calls}\n", (len, bad, taken, calls))
            }
        }
        if op == "bench" {
            try Print(mut out, "{total}\n", (total))
        }
        return
    }
    if op == "leak" {
        rounds := int(try sys.args[2].ParseInt())
        fs.FaultReal(sys.io)
        Round(sys)
        Round(sys)
        h0 := fs.Handles()
        for _ in 0..rounds {
            Round(sys)
        }
        h1 := fs.Handles()
        try Print(mut out, "handles {h0} {h1}\n", (h0, h1))
        return
    }
}
`

func buildReviewFault(t *testing.T, h ioHost) string {
	return h.build(t, map[string]string{"core/io_zfault.mk": faultCore, "fs/zfault.mk": faultFs, "net/zfault.mk": faultNet}, reviewFault)
}

// TestSTDIO_NegativePositionNoCall is the negative position over the scripted Io: no
// table call is made for a negative position, and the file's own Read
// still reads from 0.
func TestSTDIO_NegativePositionNoCall(t *testing.T) {
	exe := buildReviewFault(t, hostIo)
	r := hostIo.run(t, exe, "", "neg")
	wantRun(t, r, 0, "readat Os 0 writeat Os 0 calls 0 read 8 calls 1\n")
}

// TestSTDIO_NoSeekFallback is the no-position handle over the scripted Io (mode 12: every
// positioned read and write fails as a pipe's pread/pwrite does, ESPIPE,
// SysErr.Other with os 29). On POSIX targets File.Read, Write, Writer and
// WriteAll make the call again at the stream position once and stay
// there (call counts exact); on Windows, which never reports ESPIPE (its
// pipes ignore the offset), the failure is reported as Os(29). ReadAt has
// no fallback anywhere, and an ESPIPE os code under another SysErr is not
// one.
func TestSTDIO_NoSeekFallback(t *testing.T) { ioNoSeekFallback(t, hostIo) }

func ioNoSeekFallback(t *testing.T, h ioHost) {
	exe := buildReviewFault(t, h)
	type c struct{ op, err, os, posix, windows string }
	cs := []c{
		{"read", "15", "29", "ok 10 calls 3", "Os 29 calls 1"},
		{"write", "15", "29", "ok 20 calls 3", "Os 29 calls 1"},
		{"writer", "15", "29", "ok 0 calls 3", "WriteError calls 1"},
		{"fwriteall", "15", "29", "ok 0 calls 2", "Os 29 calls 1"},
		{"readwrite", "15", "29", "ok 18 calls 3", "Os 29 calls 1"},
		{"readat", "15", "29", "Os 29 calls 1", "Os 29 calls 1"},
		{"read", "3", "29", "Denied calls 1", "Denied calls 1"},
		{"read", "15", "28", "Os ? calls 1", "Os ? calls 1"},
	}
	calls := make([]ioCall, len(cs))
	for i, k := range cs {
		calls[i] = ioCall{args: []string{"espipe", k.op, k.err, k.os}}
	}
	for i, r := range h.runMany(t, exe, calls) {
		want := cs[i].posix
		if !h.linux && runtime.GOOS == "windows" {
			want = cs[i].windows
		}
		if got := strings.TrimRight(r.stdout, "\r\n"); r.code != 0 || got != want {
			t.Errorf("espipe %v: exit %d, %q, want %q%s", cs[i], r.code, got, want, faultTail(r))
		}
	}
}

// reviewReads is a model of fs.ReadBytes over the fixture's
// reads (every read fills as much as it may): how many table calls it
// makes for n bytes from a first buffer of first bytes. The first buffer
// fills (it cannot grow in place in a fresh sub-arena: its chunk is
// exactly twice its size plus the chunk record), then segments of 2, 4,
// 8 ... times first bytes, each one read, until a read returns 0.
func reviewReads(n, first int) int {
	if n == 0 {
		return 1 // the read that returns 0
	}
	if n < first {
		return 2 // n bytes, then 0
	}
	calls := 1 // the first buffer, full
	left := n - first
	size := first
	for {
		size *= 2
		if left >= size {
			calls++ // the segment, full
			left -= size
			continue
		}
		if left > 0 {
			calls++ // the rest
		}
		return calls + 1 // the read that returns 0
	}
}

// reviewOld is the old fs.ReadBytes (a doubling buffer, each one a fresh
// zero-filled Arena.Slice, the old one abandoned): the bytes of the arena
// it takes for n bytes from first bytes.
func reviewOld(n, first int) int {
	taken, cap := first, first
	for got := 0; ; {
		if got == cap {
			taken += cap * 2
			cap *= 2
		}
		k := min(n-got, cap-got)
		if k == 0 {
			return taken
		}
		got += k
	}
}

// TestSTDIO_ReadBytesArenaBound: fs.ReadBytes of n bytes with no size
// (a pipe) returns exactly the n bytes, makes the model's reads, and takes
// at most n + 3 * ReadFirst + 512 bytes of the arena: n for the result,
// at most ReadFirst for the first buffer when the arena can neither grow
// nor free it, and the fresh sub-arena's own geometry (chunk records, the
// first chunk's 4,016 free bytes that a 4 KiB block does not fit, the
// tail of a chunk the result did not fit). The old code took about 4n
// before any geometry (reviewOld; 16,773,120 bytes for a 4 MiB pipe).
// With the size right ("sized") it costs two reads and n + 512 bytes:
// the spare byte goes back.
func TestSTDIO_ReadBytesArenaBound(t *testing.T) { ioReadBytesArenaBound(t, hostIo) }

func ioReadBytesArenaBound(t *testing.T, h ioHost) {
	exe := buildReviewFault(t, h)
	const first = 4096
	ns := []int{0, 10, 4095, 4096, 4097, 8192, 8193, 12288, 65536, 1 << 22, 5_000_000}
	var calls []ioCall
	for _, n := range ns {
		calls = append(calls, ioCall{args: []string{"pipe", strconv.Itoa(n)}})
	}
	for _, n := range []int{1, 4096, 1 << 20} {
		calls = append(calls, ioCall{args: []string{"sized", strconv.Itoa(n)}})
	}
	for i, r := range h.runMany(t, exe, calls) {
		var n, bad, taken, reads int
		if _, err := fmt.Sscanf(r.stdout, "ok %d bad %d taken %d calls %d", &n, &bad, &taken, &reads); err != nil || r.code != 0 {
			t.Errorf("%v: exit %d, %q (%v)%s", calls[i].args, r.code, r.stdout, err, faultTail(r))
			continue
		}
		want, _ := strconv.Atoi(calls[i].args[1])
		wantReads, bound := reviewReads(want, first), want+3*first+512
		if calls[i].args[0] == "sized" {
			wantReads, bound = 2, want+512
		}
		if n != want || bad != 0 || reads != wantReads || taken > bound {
			t.Errorf("%v: %d bytes (%d wrong), %d reads (model %d), %d bytes of the arena taken, bound %d (old code: %d)",
				calls[i].args, n, bad, reads, wantReads, taken, bound, reviewOld(want, first))
		}
		t.Logf("%v: %d bytes taken (old model %d)", calls[i].args, taken, reviewOld(want, first))
	}
	if old := reviewOld(1<<22, first); old < 4*(1<<22)-first {
		t.Errorf("model: the old code takes %d bytes for a 4 MiB pipe, the finding says about 4n", old)
	}
}

// TestSTDIO_ErrorPathsReleaseHandles: every fs path that holds a
// handle (ReadBytes: size, read, bad counts; WriteAll: a failed, empty,
// too long or negative write, a failed close; File.Close failing, then
// the caller's Abandon; File.Writer then Close failing) releases it,
// counted against the OS (std.sys HandleCount) over real handles: the
// fixture's open and succeeding close are the runtime's (FaultReal), every
// other entry is scripted, so only rt.Abandon or a real close frees a
// handle. 300 rounds of 16 paths: a path that leaked would leave 300
// handles open.
func TestSTDIO_ErrorPathsReleaseHandles(t *testing.T) { ioErrorPathsReleaseHandles(t, hostIo) }

func ioErrorPathsReleaseHandles(t *testing.T, h ioHost) {
	exe := buildReviewFault(t, h)
	// The default carriers, then one carrier: every fs call
	// goes through the blocking pool, and with one carrier each one parks
	// the only carrier's task while the pool thread runs it.
	calls := []ioCall{
		{args: []string{"leak", "300"}, watch: 120},
		{args: []string{"leak", "100"}, env: []string{"MK_CARRIERS=1"}, watch: 120},
	}
	for i, r := range h.runMany(t, exe, calls) {
		what := "default carriers"
		if i == 1 {
			what = "MK_CARRIERS=1"
		}
		var h0, h1 int
		if _, err := fmt.Sscanf(r.stdout, "handles %d %d", &h0, &h1); err != nil || r.code != 0 {
			t.Errorf("%s: exit %d, %q (%v)%s", what, r.code, r.stdout, err, faultTail(r))
			continue
		}
		if h0 <= 0 || h1-h0 > 8 || h1 < h0-8 {
			t.Errorf("%s: open handles %d before the rounds, %d after: a path leaks", what, h0, h1)
		}
		t.Logf("%s: open handles: %d before, %d after", what, h0, h1)
	}
}
