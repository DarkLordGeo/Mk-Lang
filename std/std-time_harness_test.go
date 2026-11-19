// Why: the oracle and fuzz tests compare std.time with Go's time package
// over millions of inputs, which cannot afford a build per input.
// What: time2Prog, a long-running Mk server, and time2StartProg and time2Ask.
// How: the server answers one request a line; programs build against a copy of
// MK_TIME_BASE (a whole std snapshot) with this tree's std/time, or this tree's std.

package std

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// time2Prog answers one request a line: an op, then its arguments,
// text in hex. Each answer is one line per item:
//
//	I zone t...      the Date of each instant in the zone (tzdata.Database's
//	                 bytes through FromTzif, never a system database):
//	                 "y m d h mi s ns wd yd off"
//	F zone y m d h mi s ns  the FromDate instant, or "E <error>"
//	Z hex t...       FromTzif of the bytes, "ok" or "E <error>", then the
//	                 offset at each instant ("-" after an error)
//	L hex            Load of the name from tzdata: "ok" or "E <error>"
//	U s ns           FromUnix's nanoseconds
//	A t d            Add's nanoseconds; S t u Sub's
//	P3 hex, PH hex, PD hex   ParseRfc3339, ParseHttpDate, ParseDuration:
//	                 the nanoseconds or "E"
//	FT t, FH t       Time.Format and FormatHttpDate
const time2Prog = `import std.time
import std.time.tzdata

Nib(c: u8): int {
    return if c <= 57 { int(c) - 48 } else { int(c) - 87 }
}

Unhex(a: &Arena, s: str): OutOfMemory![]u8 from a {
    b := s.AsBytes()
    out := try a.Slice[u8](b.Len() / 2)
    for i in 0..out.Len() {
        out[i] = Trunc[u8](Nib(b[2 * i]) * 16 + Nib(b[2 * i + 1]))
    }
    return out[..]
}

Num(s: str): i64 {
    return s.ParseInt() else { 0 }
}

Day(out: mut Writer, d: &time.Date): WriteError!void {
    try Print(out, "{y} {m} {d} {h} {mi} {s} {ns} {wd} {yd} {off}\n", (y: d.year, m: d.month, d: d.day, h: d.hour, mi: d.minute, s: d.second, ns: d.nanos, wd: d.weekday, yd: d.year_day, off: d.offset_seconds))
}

Serve(h: &Heap, io: Io, out: mut Writer, inp: mut Reader): WriteError|ReadError|OutOfMemory!bool {
    var a := try Arena.New(h)
    defer a.Free()
    line := try inp.Line(&a) else {
        return false
    }
    var ws := line.Words()
    op := ws.Next() else {
        return true
    }
    arg := ws.Next() else { "" }
    if op == "I" || op == "F" {
        // Hermetic: the pinned zones' bytes, never a system database.
        db := tzdata.Database
        data := db.find(arg) else {
            try out.Str("E NotFound\n")
            return true
        }
        z := time.Zone.FromTzif(&a, arg, data) else e {
            try Print(out, "E {e}\n", (e))
            return true
        }
        if op == "I" {
            for w in ws {
                d := time.FromUnix(0, Num(w)).In(&z)
                try Day(out, &d)
            }
            return true
        }
        var v: [8]i64 = [0; 8]
        var n := 0
        for w in ws {
            if n < 8 {
                v[n] = Num(w)
                n += 1
            }
        }
        r := time.FromDate(Trunc[i32](v[0]), Cast[int](v[1]), Cast[int](v[2]), Cast[int](v[3]), Cast[int](v[4]), Cast[int](v[5]), Cast[int](v[6]), &z) else e {
            try Print(out, "E {e}\n", (e))
            return true
        }
        try Print(out, "{t}\n", (t: r.UnixNanos()))
        return true
    }
    if op == "Z" {
        data := try Unhex(&a, arg)
        z := time.Zone.FromTzif(&a, "z", data) else e {
            try Print(out, "E {e}\n", (e))
            for w in ws {
                _ = w
                try out.Str("-\n")
            }
            return true
        }
        try out.Str("ok\n")
        for w in ws {
            d := time.FromUnix(0, Num(w)).In(&z)
            try Print(out, "{o}\n", (o: d.offset_seconds))
        }
        return true
    }
    if op == "L" {
        name := str.From(try Unhex(&a, arg)) else {
            try out.Str("E utf8\n")
            return true
        }
        _ = time.Zone.Load(&a, io, name, some(tzdata.Database)) else e {
            try Print(out, "E {e}\n", (e))
            return true
        }
        try out.Str("ok\n")
        return true
    }
    second := ws.Next() else { "0" }
    if op == "U" {
        try Print(out, "{t}\n", (t: time.FromUnix(Num(arg), Num(second)).UnixNanos()))
        return true
    }
    if op == "A" {
        try Print(out, "{t}\n", (t: time.FromUnix(0, Num(arg)).Add(time.Nanos(Num(second))).UnixNanos()))
        return true
    }
    if op == "S" {
        try Print(out, "{t}\n", (t: time.FromUnix(0, Num(arg)).Sub(time.FromUnix(0, Num(second))).Nanos()))
        return true
    }
    if op == "FT" {
        t := time.FromUnix(0, Num(arg))
        try Print(out, "{t}\n", (t))
        return true
    }
    if op == "FH" {
        s := try time.FormatHttpDate(&a, time.FromUnix(0, Num(arg)))
        try Print(out, "{s}\n", (s))
        return true
    }
    raw := try Unhex(&a, arg)
    text := str.From(raw) else { "\u{FFFD}bad" }
    if op == "P3" {
        t := time.ParseRfc3339(text) else {
            try out.Str("E\n")
            return true
        }
        try Print(out, "{t}\n", (t: t.UnixNanos()))
        return true
    }
    if op == "PH" {
        t := time.ParseHttpDate(text) else {
            try out.Str("E\n")
            return true
        }
        try Print(out, "{t}\n", (t: t.UnixNanos()))
        return true
    }
    if op == "PD" {
        d := time.ParseDuration(text) else {
            try out.Str("E\n")
            return true
        }
        try Print(out, "{d}\n", (d: d.Nanos()))
        return true
    }
    try out.Str("E op\n")
    return true
}

export Main(sys: &System): WriteError|ReadError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    for try Serve(sys.heap, sys.io, mut out, mut inp) {
        try out.Flush()
    }
}
`

// time2Std returns the std directory the time programs build against.
func time2Std(t *testing.T, root string, fixtures map[string]string) string {
	t.Helper()
	base := os.Getenv("MK_TIME_BASE")
	if base == "" {
		return copyStd(t, root, fixtures)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "time")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "time"), os.DirFS("time")); err != nil {
		t.Fatal(err)
	}
	for p, text := range fixtures {
		dst := filepath.Join(stdDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}

// time2Build builds src as a one-file package against time2Std and
// returns the executable; a missing toolchain skips the test.
func time2Build(t *testing.T, fixtures map[string]string, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := time2Std(t, root, fixtures)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// time2Server is a running oracle program with its pipes. Its directory
// outlives the test that started it (os.MkdirTemp), since later tests
// reuse it.
type time2Server struct {
	exe  string
	mu   sync.Mutex
	cmd  *exec.Cmd
	in   *bufio.Writer
	out  *bufio.Reader
	errb *bytes.Buffer
}

// time2Key names a server: its program and its build.
type time2Key struct {
	prog    string
	release bool
}

var time2Srv struct {
	sync.Mutex
	m map[time2Key]*time2Server
}

// time2Progs are the server programs by name, with their fixtures.
var time2Progs = map[string]struct {
	src      string
	fixtures map[string]string
}{
	"": {time2Prog, nil},
}

// time2Start returns the server of time2Prog in a dev or release build.
func time2Start(t testing.TB, release bool) *time2Server {
	return time2StartProg(t, "", release)
}

// time2StartProg returns the server of the named program of time2Progs
// in a dev or release build, starting it.
func time2StartProg(t testing.TB, prog string, release bool) *time2Server {
	t.Helper()
	time2Srv.Lock()
	defer time2Srv.Unlock()
	key := time2Key{prog, release}
	if s, ok := time2Srv.m[key]; ok {
		return s
	}
	tt, ok := t.(*testing.T)
	if !ok {
		t.Skip("the oracle server is started by a test, not a fuzz worker")
	}
	var flags []string
	if release {
		flags = append(flags, "--release")
	}
	p := time2Progs[prog]
	// MK_TIME2_LINUX_ORACLE=1 (with MK_LINUX=1, under the WSL lock, -p 1
	// and -parallel 1): the server is the x86_64-linux release build run
	// through wsl.exe, so the fuzz targets run on the Linux target.
	linux := os.Getenv("MK_TIME2_LINUX_ORACLE") == "1"
	var exe string
	if linux {
		needWSL(tt)
		if len(p.fixtures) > 0 {
			t.Skip("the Linux oracle builds time2Prog only")
		}
		exe = time2BuildLinux(tt, p.src)
	} else {
		exe = time2Build(tt, p.fixtures, p.src, flags...)
	}
	dir, err := os.MkdirTemp(os.Getenv("TMP"), "time2srv")
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, filepath.Base(exe))
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(keep)
	if linux {
		wslGuard()
		cmd = exec.Command("wsl.exe", "--cd", wslPath(dir), "--", "./"+filepath.Base(keep))
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &time2Server{exe: keep, cmd: cmd, in: bufio.NewWriterSize(in, 1<<16), out: bufio.NewReaderSize(out, 1<<16), errb: &errb}
	if time2Srv.m == nil {
		time2Srv.m = map[time2Key]*time2Server{}
	}
	time2Srv.m[key] = s
	return s
}

// time2Ask sends one request line to time2Prog and reads n answer lines.
func time2Ask(t testing.TB, release bool, line string, n int) []string {
	t.Helper()
	return time2AskProg(t, "", release, line, n)
}

// time2AskProg sends one request line to the named program and reads n
// answer lines.
func time2AskProg(t testing.TB, prog string, release bool, line string, n int) []string {
	t.Helper()
	s := time2StartProg(t, prog, release)
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func(err error) {
		time2Srv.Lock()
		delete(time2Srv.m, time2Key{prog, release})
		time2Srv.Unlock()
		s.cmd.Process.Kill()
		s.cmd.Wait()
		t.Fatalf("oracle (release %v) on %.200q: %v\nstderr:\n%s", release, line, err, s.errb.String())
	}
	if len(line) >= 64<<10 {
		t.Fatalf("request of %d bytes is over the 64 KiB line limit", len(line))
	}
	if _, err := s.in.WriteString(line + "\n"); err != nil {
		fail(err)
	}
	if err := s.in.Flush(); err != nil {
		fail(err)
	}
	out := make([]string, n)
	for i := range out {
		l, err := s.out.ReadString('\n')
		if err != nil {
			fail(err)
		}
		out[i] = strings.TrimRight(l, "\r\n")
	}
	return out
}

// time2Hex is s in the request's hex form ("-" for empty text).
func time2Hex(s string) string {
	if s == "" {
		return "-"
	}
	return hex.EncodeToString([]byte(s))
}

// time2Ints joins vs for a request.
func time2Ints(vs []int64) string {
	var sb strings.Builder
	for i, v := range vs {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprint(&sb, v)
	}
	return sb.String()
}
