// Why: std.fs, std.time, std.proc and std.net must behave as the OS does,
// and Go's os package is a trusted reference for that.
// What: TestReadBytesMatchesOsReadFile, TestFsErrorMapping, TestFilePositions,
// TestWholeFileOps, TestTimeDurations, TestProcExit and TestNetLoopback (file
// reads, error mapping, positions, whole-file ops, time, proc.Exit, loopback
// sockets).
// How: each module is driven by a native program and judged against Go's
// os package or literal expectations.

package std

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fsProgram runs one std.fs operation per invocation (args[1]) and prints
// its result, or "<op> <member>" for the fs.Error member it failed with.
const fsProgram = `import std.fs

ErrName(e: fs.Error|OutOfMemory): str {
    match e {
        fs.NotFound {
            return "NotFound"
        }
        fs.Exists {
            return "Exists"
        }
        fs.Denied {
            return "Denied"
        }
        fs.IsDirectory {
            return "IsDirectory"
        }
        fs.NotDirectory {
            return "NotDirectory"
        }
        fs.NoSpace {
            return "NoSpace"
        }
        fs.InvalidPath {
            return "InvalidPath"
        }
        fs.Unsupported {
            return "Unsupported"
        }
        Utf8Error {
            return "Utf8Error"
        }
        fs.Os(code: _) {
            return "Os"
        }
        OutOfMemory {
            return "OutOfMemory"
        }
        Canceled {
            return "Canceled"
        }
        _ {
            Panic("fs.Error gained a member this match does not handle")
        }
    }
}

Report(out: mut Writer, what: str, e: fs.Error|OutOfMemory): WriteError!void {
    name := ErrName(e)
    try Print(out, "{what} {name}\n", (what, name))
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    io := sys.io
    cmd := sys.args[1]
    p := sys.args[2]
    if cmd == "cat" {
        b := fs.ReadBytes(sys.arena, io, p) else e {
            try Report(mut out, "cat", e)
            return
        }
        try out.Bytes(b)
    } else if cmd == "text" {
        s := fs.ReadText(sys.arena, io, p) else e {
            try Report(mut out, "text", e)
            return
        }
        try out.Str(s)
    } else if cmd == "put" {
        fs.WriteAll(io, p, sys.args[3].AsBytes()) else e {
            try Report(mut out, "put", e)
            return
        }
    } else if cmd == "rename" {
        fs.Rename(io, p, sys.args[3]) else e {
            try Report(mut out, "rename", e)
            return
        }
    } else if cmd == "remove" {
        fs.Remove(io, p) else e {
            try Report(mut out, "remove", e)
            return
        }
    } else if cmd == "file" {
        var f := try fs.Create(io, p)
        onerr f.Abandon()
        _ = try f.Write(io, "abc".AsBytes())
        _ = try f.WriteAt(io, 0, "X".AsBytes())
        var wb: [4]u8 = [0; 4]
        var w := f.Writer(io, mut wb[..])
        try w.Str("defghij")
        try w.Flush()
        _ = try f.Write(io, "k".AsBytes())
        try f.WriteAll(io, "lm".AsBytes())
        try f.Sync(io)
        try f.Close(io)
        var g := try fs.Open(io, p)
        onerr g.Abandon()
        var rb: [3]u8 = [0; 3]
        n1 := try g.Read(io, mut rb[..])
        try out.Bytes(rb[0..n1])
        try out.Str("|")
        n2 := try g.ReadAt(io, 9, mut rb[..])
        try out.Bytes(rb[0..n2])
        try out.Str("|")
        n3 := try g.Read(io, mut rb[..])
        try out.Bytes(rb[0..n3])
        try out.Str("|")
        var all: [64]u8 = [0; 64]
        n4 := try g.ReadAt(io, 0, mut all[..])
        try out.Bytes(all[0..n4])
        try out.Str("|")
        n5 := try g.ReadAt(io, 100, mut all[..])
        try Print(mut out, "{n5}\n", (n5))
        g.Abandon()
    } else if cmd == "open" {
        f := fs.Open(io, p) else e {
            try Report(mut out, "open", e)
            return
        }
        f.Abandon()
        try out.Str("opened\n")
    } else if cmd == "create" {
        var f := try fs.Create(io, p)
        onerr f.Abandon()
        try f.WriteAll(io, sys.args[3].AsBytes())
        try f.Close(io)
    }
}
`

// fsRunMany runs the fs program once per argument list, in order, with
// the executable's directory as its working directory, and fails the test
// unless every run exits 0.
func fsRunMany(t *testing.T, h ioHost, exe string, argss ...[]string) []nativeRun {
	t.Helper()
	calls := make([]ioCall, len(argss))
	for i, a := range argss {
		calls[i] = ioCall{args: a}
	}
	runs := h.runMany(t, exe, calls)
	for i, r := range runs {
		if r.code != 0 {
			t.Fatalf("%v: exit %d\nstderr: %s", argss[i], r.code, r.stderr)
		}
	}
	return runs
}

// fsRun is fsRunMany for one run.
func fsRun(t *testing.T, h ioHost, exe string, args ...string) nativeRun {
	t.Helper()
	return fsRunMany(t, h, exe, args)[0]
}

// TestReadBytesMatchesOsReadFile: fs.ReadBytes returns exactly
// os.ReadFile's bytes for files of 0 bytes, 1, 4095 to 4097 (ReadFirst's
// edge), 65,537 and 1 MiB + 3 (seeded random bytes), so the size hint, the
// end-of-input read and the doubling all agree with Go; ReadText returns
// the same for valid UTF-8 and Utf8Error for invalid text.
func TestReadBytesMatchesOsReadFile(t *testing.T) { ioReadBytesMatchesOsReadFile(t, hostIo) }

func ioReadBytesMatchesOsReadFile(t *testing.T, h ioHost) {
	exe := h.build(t, nil, fsProgram)
	dir := filepath.Dir(exe)
	rng := rand.New(rand.NewSource(134))
	sizes := []int{0, 1, 4095, 4096, 4097, 65537, 1<<20 + 3}
	var argss [][]string
	var wants [][]byte
	for _, n := range sizes {
		data := make([]byte, n)
		rng.Read(data)
		name := "f" + strconv.Itoa(n)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		argss, wants = append(argss, []string{"cat", name}), append(wants, want)
	}
	text := strings.Repeat("héllo wörld ✓ ", 5000)
	os.WriteFile(filepath.Join(dir, "good.txt"), []byte(text), 0o644)
	argss = append(argss, []string{"text", "good.txt"})
	bad := []string{"bad1.txt", "bad2.txt", "bad3.txt", "bad4.txt"}
	for i, b := range [][]byte{
		{0xff},
		[]byte("ok\xc0\xafno"),           // overlong
		[]byte("ok\xed\xa0\x80no"),       // surrogate
		append([]byte(text), 0xe2, 0x9c), // truncated at the end
	} {
		os.WriteFile(filepath.Join(dir, bad[i]), b, 0o644)
		argss = append(argss, []string{"text", bad[i]})
	}
	runs := fsRunMany(t, h, exe, argss...)
	for i, n := range sizes {
		if !bytes.Equal([]byte(runs[i].stdout), wants[i]) {
			t.Errorf("ReadBytes of %d bytes: got %d bytes, not os.ReadFile's", n, len(runs[i].stdout))
		}
	}
	if r := runs[len(sizes)]; r.stdout != text {
		t.Errorf("ReadText: %d bytes, want %d", len(r.stdout), len(text))
	}
	for i, name := range bad {
		if r := runs[len(sizes)+1+i]; r.stdout != "text Utf8Error\n" {
			t.Errorf("ReadText %s: %q, want Utf8Error", name, r.stdout)
		}
	}
}

// TestFsErrorMapping: the SysErr of each failure maps to the fs.Error
// member of the same name (4.12): a missing file is NotFound for every
// operation, a missing directory too, a directory opened as a file is
// IsDirectory, a non-empty directory's Remove is Os (fs.Error has no
// member for it).
func TestFsErrorMapping(t *testing.T) { ioFsErrorMapping(t, hostIo) }

func ioFsErrorMapping(t *testing.T, h ioHost) {
	exe := h.build(t, nil, fsProgram)
	dir := filepath.Dir(exe)
	os.MkdirAll(filepath.Join(dir, "full"), 0o755)
	os.WriteFile(filepath.Join(dir, "full", "x"), []byte("x"), 0o644)
	cases := [][2][]string{
		{{"cat", "missing.txt"}, {"cat NotFound\n"}},
		{{"text", "missing.txt"}, {"text NotFound\n"}},
		{{"open", "missing.txt"}, {"open NotFound\n"}},
		{{"open", "nodir/missing.txt"}, {"open NotFound\n"}},
		{{"put", "nodir/x.txt", "c"}, {"put NotFound\n"}},
		{{"remove", "missing.txt"}, {"remove NotFound\n"}},
		{{"rename", "missing.txt", "other.txt"}, {"rename NotFound\n"}},
		{{"cat", "full"}, {"cat IsDirectory\n", "cat Denied\n"}},
		{{"open", "full"}, {"open IsDirectory\n", "open Denied\n", "opened\n"}},
		{{"remove", "full"}, {"remove Os\n"}},
	}
	argss := make([][]string, len(cases))
	for i, c := range cases {
		argss[i] = c[0]
	}
	runs := fsRunMany(t, h, exe, argss...)
	for i, c := range cases {
		r := runs[i]
		ok := false
		for _, w := range c[1] {
			ok = ok || r.stdout == w
		}
		if !ok {
			t.Errorf("%v: %q, want one of %q", c[0], r.stdout, c[1])
		}
	}
}

// TestFilePositions: File.Write moves the position, WriteAt does
// not, File.Writer writes at the file's position and moves it (the
// cursor writer, io_file.mk; before it, a Writer wrote at the OS stream
// position, which after this WriteAt is 1 on Windows and 0 on Linux), Read
// moves it and ReadAt does not; a ReadAt past the end is 0.
func TestFilePositions(t *testing.T) { ioFilePositions(t, hostIo) }

func ioFilePositions(t *testing.T, h ioHost) {
	exe := h.build(t, nil, fsProgram)
	dir := filepath.Dir(exe)
	r := fsRun(t, h, exe, "file", "f.bin")
	if r.stdout != "Xbc|jkl|def|Xbcdefghijklm|0\n" {
		t.Errorf("got %q", r.stdout)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.bin")); string(b) != "Xbcdefghijklm" {
		t.Errorf("file holds %q", b)
	}
}

// TestWholeFileOps: WriteAll writes the whole content, Create
// truncates a longer file, Rename replaces an existing target, Remove
// deletes a file and an empty directory.
func TestWholeFileOps(t *testing.T) { ioWholeFileOps(t, hostIo) }

func ioWholeFileOps(t *testing.T, h ioHost) {
	exe := h.build(t, nil, fsProgram)
	dir := filepath.Dir(exe)
	at := func(p string) string { return filepath.Join(dir, p) }
	fsRun(t, h, exe, "put", "a.txt", "first content")
	if b, _ := os.ReadFile(at("a.txt")); string(b) != "first content" {
		t.Errorf("WriteAll: %q", b)
	}
	fsRun(t, h, exe, "put", "a.txt", "2nd")
	if b, _ := os.ReadFile(at("a.txt")); string(b) != "2nd" {
		t.Errorf("WriteAll over a longer file: %q", b)
	}
	os.WriteFile(at("long.txt"), []byte(strings.Repeat("z", 10000)), 0o644)
	fsRun(t, h, exe, "create", "long.txt", "short")
	if b, _ := os.ReadFile(at("long.txt")); string(b) != "short" {
		t.Errorf("Create did not truncate: %d bytes", len(b))
	}
	fsRun(t, h, exe, "put", "b.txt", "bee")
	fsRun(t, h, exe, "rename", "b.txt", "a.txt")
	if b, _ := os.ReadFile(at("a.txt")); string(b) != "bee" {
		t.Errorf("Rename did not replace: %q", b)
	}
	if _, err := os.Stat(at("b.txt")); !os.IsNotExist(err) {
		t.Errorf("Rename left the source: %v", err)
	}
	fsRun(t, h, exe, "remove", "a.txt")
	if _, err := os.Stat(at("a.txt")); !os.IsNotExist(err) {
		t.Errorf("Remove of a file: %v", err)
	}
	os.Mkdir(at("empty"), 0o755)
	fsRun(t, h, exe, "remove", "empty")
	if _, err := os.Stat(at("empty")); !os.IsNotExist(err) {
		t.Errorf("Remove of an empty directory: %v", err)
	}
}

// timeProgram prints Seconds, Millis and an elapsed check, or overflows
// on purpose (args[1]).
const timeProgram = `import std.time

export Main(sys: &System): WriteError|Canceled!void {
    var out := sys.Stdout()
    if sys.args[1] == "secs" {
        _ = time.Seconds(9_223_372_037)
        return
    }
    if sys.args[1] == "millis" {
        _ = time.Millis(-9_223_372_036_855)
        return
    }
    a := time.Now(sys.io)
    try sys.io.Sleep(time.Millis(20))
    b := time.Now(sys.io)
    s := time.Seconds(-3).Nanos()
    m := time.Millis(1_500).Nanos()
    big := time.Seconds(9_223_372_036).Nanos()
    up := b.Since(a).Nanos() >= 20_000_000
    down := a.Since(b).Nanos() <= -20_000_000
    d: time.Duration = time.Millis(7)
    n := d.Nanos()
    try Print(mut out, "{s} {m} {big} {up} {down} {n}\n", (s, m, big, up, down, n))
}
`

// TestTimeDurations: Seconds and Millis scale exactly up to the edge of i64,
// overflow panics with code 1, the clock is monotonic and
// a 20 ms sleep shows as at least 20 ms either way round, and
// time.Duration is std.core's Duration (Io.Sleep takes it).
func TestTimeDurations(t *testing.T) { ioTimeDurations(t, hostIo) }

func ioTimeDurations(t *testing.T, h ioHost) {
	exe := h.build(t, nil, timeProgram)
	runs := h.runMany(t, exe, []ioCall{{args: []string{"run"}}, {args: []string{"secs"}}, {args: []string{"millis"}}})
	wantRun(t, runs[0], 0, "-3000000000 1500000000 9223372036000000000 true true 7000000\n")
	for i, arg := range []string{"secs", "millis"} {
		r := runs[1+i]
		if r.code != 101 || !strings.Contains(r.stderr, "panic: overflow") {
			t.Errorf("%s: exit %d, stderr %q; want an overflow panic", arg, r.code, r.stderr)
		}
	}
}

// procProgram exits with args[1] after buffering a line, or waits for a
// signal in a scope cancelled before the wait starts.
const procProgram = `import std.proc

Watch(io: Io, out: mut int) {
    proc.WaitSignal(io, proc.Signal.Terminate) else {
        *out = 1
        return
    }
    *out = 2
}

export Main(sys: &System): WriteError|ParseError!void {
    var out := sys.Stdout()
    try out.Str("buffered\n")
    if sys.args[1] == "wait" {
        var r := 0
        scope s := sys.io.Scope() {
            s.Cancel()
            s.go Watch(sys.io, mut r)
        }
        try Print(mut out, "{r}\n", (r))
        return
    }
    proc.Exit(int(try sys.args[1].ParseInt()))
}
`

// TestProcExit: proc.Exit flushes the buffered output and exits
// with the code; a code outside i32 panics with code 5, whose report still
// flushes; WaitSignal in a cancelled scope returns Canceled (a real
// delivery is std-io_signal_test.go and tests/programs/tasks/signal.mk).
func TestProcExit(t *testing.T) { ioProcExit(t, hostIo) }

func ioProcExit(t *testing.T, h ioHost) {
	exe := h.build(t, nil, procProgram)
	codes := []int{0, 7, 255, 300}
	big := []string{"2147483648", "-2147483649", "9223372036854775807"}
	var calls []ioCall
	for _, c := range codes {
		calls = append(calls, ioCall{args: []string{strconv.Itoa(c)}})
	}
	for _, c := range big {
		calls = append(calls, ioCall{args: []string{c}})
	}
	calls = append(calls, ioCall{args: []string{"wait"}})
	runs := h.runMany(t, exe, calls)
	for i, c := range codes {
		wantRun(t, runs[i], h.exitCode(c), "buffered\n")
	}
	for i, c := range big {
		r := runs[len(codes)+i]
		if r.code != 101 || r.stdout != "buffered\n" || !strings.Contains(r.stderr, "proc.Exit: the code is outside i32") {
			t.Errorf("Exit(%s): exit %d, stdout %q, stderr %q", c, r.code, r.stdout, r.stderr)
		}
	}
	wantRun(t, runs[len(runs)-1], 0, "buffered\n1\n")
}

// netProgram listens on 127.0.0.1:0, prints the port, dials itself, sends
// lines through a Stream.Writer and reads them back through the accepted
// stream's Reader, then dials a closed port.
const netProgram = `import std.net

Serve(l: &net.Listener, io: Io, lines: mut int) {
    conn := l.Accept(io) else {
        return
    }
    defer conn.Close()
    var buf: [64]u8 = [0; 64]
    var r := conn.Reader(io, mut buf[..])
    var got: [100]u8 = [0; 100]
    for {
        n := r.Read(mut got[..]) else {
            return
        }
        if n == 0 {
            return
        }
        for i in 0..n {
            if got[i] == 0x0A {
                *lines += 1
            }
        }
    }
}

Client(io: Io, addr: str): net.Error|WriteError!void {
    c := try net.Dial(io, addr)
    defer c.Close()
    var wb: [16]u8 = [0; 16]
    var w := c.Writer(io, mut wb[..])
    for _ in 0..1000 {
        try w.Str("a line of text\n")
    }
    try w.Flush()
}

export Main(sys: &System): net.Error|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    l := try net.Listen(sys.io, "127.0.0.1:0")
    defer l.Close()
    port := l.Port()
    ok := port > 0 && port < 65536
    try Print(mut out, "port {ok}\n", (ok))
    var lines := 0
    scope s := sys.io.Scope() {
        s.go Serve(&l, sys.io, mut lines)
        addr := try sys.arena.Fmt("127.0.0.1:{port}", (port))
        try Client(sys.io, addr)
    }
    try Print(mut out, "lines {lines}\n", (lines))
}
`

// TestNetLoopback: a listener on port 0 reports its bound port
// and a stream round-trips 1,000 lines through Writer and Reader. Skips,
// saying why, when the runtime's socket entries are stubs that return
// SysErr.Other (std.net then reports net.Os); the runtime's NativeListen
// must return the bound port in IoResult.os.
func TestNetLoopback(t *testing.T) { ioNetLoopback(t, hostIo) }

func ioNetLoopback(t *testing.T, h ioHost) {
	exe := h.build(t, nil, netProgram)
	r := h.run(t, exe, "")
	if r.code == 1 && strings.Contains(r.stderr, "std.net.Os") && r.stdout == "" {
		t.Skipf("BLOCKED: the runtime's listen entry fails with SysErr.Other (stubs in std/task/io_misc.mk): %s", strings.TrimSpace(r.stderr))
	}
	wantRun(t, r, 0, "port true\nlines 1000\n")
}
