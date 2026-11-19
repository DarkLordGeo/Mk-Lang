// Why: log.Sink promises whole records in one write, a working std.sim tape,
// and no lost record on cancel or error; each promise once failed.
// What: TestStdLog_OneWritePerRecord, TapeStream, SimOrderUnderContention,
// CancelWhileParked, CancelWhileStreamParked and WriteErrorPaths.
// How: each builds an Mk program over the tree's std and judges its output.

// The regression tests of log.Sink.
// Each test is named after the rule it holds:
//   - OneWritePerRecord: a stream record leaves the process in
//     one write even behind a full System.Stdout buffer (x86_64-linux
//     only: a packet-mode pipe keeps the write boundaries);
//   - TapeStream: a std.sim Recorder's and Replayer's Io reach
//     the process's streams;
//   - SimOrderUnderContention: std.sim's stream order holds
//     while native tasks, or a second Sim, contend for the stream;
//   - CancelWhileParked, CancelWhileStreamParked: a cancel or
//     a deadline that fires while a sink call waits on the file's mutex or
//     in a stream write loses no record;
//   - WriteErrorPaths: the documented causes of WriteError,
//     the failed lock, a failing field Format, a read-only file and a
//     broken pipe, each fails the call and leaves the sink usable.
//
// MK_STDLOG_FIXTURES names a directory of std files (paths relative to
// std) that replace the tree's in every program these tests and
// std-log_sink_test.go build: how a proposed change to another std file
// is measured here without editing it.

package std

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stdLogFixtures is the replaced std files MK_STDLOG_FIXTURES names, or
// none.
func stdLogFixtures(t *testing.T) map[string]string {
	t.Helper()
	dir := os.Getenv("MK_STDLOG_FIXTURES")
	if dir == "" {
		return nil
	}
	fx := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fx[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MK_STDLOG_FIXTURES: %d replaced std files", len(fx))
	return fx
}

// stdLogScript writes a bash script beside exe (the Linux runs' batch
// runs ./<name> in exe's directory) and returns its path.
func stdLogScript(t *testing.T, exe, name, body string) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(exe), name)
	if err := os.WriteFile(p, []byte("#!/bin/bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// stdLogPktPipe is a Linux helper: it runs its arguments as a command
// whose standard output is a packet-mode pipe (pipe2 with O_DIRECT, Linux
// 3.4), so each read returns what one write wrote (a write over PIPE_BUF,
// 4096 bytes, is cut into packets of 4096). It prints each packet as its
// length, first byte and last byte.
const stdLogPktPipe = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_DIRECT|syscall.O_CLOEXEC); err != nil {
		fmt.Fprintln(os.Stderr, "pipe2:", err)
		os.Exit(2)
	}
	r, w := os.NewFile(uintptr(p[0]), "r"), os.NewFile(uintptr(p[1]), "w")
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdout, cmd.Stderr = w, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(2)
	}
	w.Close()
	buf := make([]byte, 1<<16)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			fmt.Printf("%d %d %d\n", n, buf[0], buf[n-1])
		}
		if err != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, "wait:", err)
		os.Exit(3)
	}
}
`

// stdLogBuildPktPipe builds stdLogPktPipe for x86_64-linux into dir.
func stdLogBuildPktPipe(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "pktpipe.go")
	if err := os.WriteFile(src, []byte(stdLogPktPipe), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "pktpipe")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build pktpipe: %v\n%s", err, b)
	}
	return out
}

// stdLogOneWrite buffers 5,000 bytes on a System.Stdout Writer (cap 8192),
// then logs one 4,006-byte record to log.Stdout: the record does not fit
// the buffer's rest, so the drain must write the buffered bytes and then
// the record whole.
var stdLogOneWrite = `import std.log

export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    try o.Str("` + strings.Repeat("a", 5000) + `")
    try log.Stdout(log.Level.Info).Info(sys.io, "{p}", (p: "` + strings.Repeat("y", 4000) + `"))
}
`

// TestStdLog_OneWritePerRecord (a stream sink writes each record with one
// write): behind 5,000 buffered bytes, a log.Stdout record of 4,006 bytes
// is one packet of a packet-mode pipe, so one write; another process
// sharing the pipe cannot land inside it (PIPE_BUF). StreamPut's drain
// topped the 8 KiB buffer up with the record's first 3,192 bytes and wrote
// the rest in a second write: packets 4096, 4096 and 814. x86_64-linux
// only: no Windows pipe this test can make keeps write boundaries.
func TestStdLog_OneWritePerRecord(t *testing.T) {
	linuxGate(t)
	t.Parallel()
	exe := stdLogBuild(t, linuxIo, stdLogOneWrite)
	helper := stdLogBuildPktPipe(t, filepath.Dir(exe))
	r := linuxIo.runMany(t, helper, []ioCall{{args: []string{"./" + filepath.Base(exe)}, watch: 60}})[0]
	if r.code != 0 {
		t.Fatalf("exit %d\nstderr %.500s", r.code, r.stderr)
	}
	total, whole := 0, false
	var packets []string
	for _, l := range stdLogLines(r.stdout) {
		var n, first, last int
		if c, _ := fmt.Sscanf(l, "%d %d %d", &n, &first, &last); c != 3 {
			t.Fatalf("helper line %q", l)
		}
		total += n
		packets = append(packets, fmt.Sprint(n))
		if n == 5+4000+1 && first == 'I' && last == '\n' {
			whole = true
		}
	}
	if total != 5000+4006 || !whole {
		t.Fatalf("the record is not one write: packets %v (%d bytes); want the 5,000 buffered bytes, then one 4006-byte packet", packets, total)
	}
}

// stdLogTape logs through a std.sim Recorder's Io over the process's Io,
// then through a Replayer of the Recorder's log.
const stdLogTape = `import std.log
import std.sim

export Main(sys: &System): OutOfMemory!void {
    sink := log.Stderr(log.Level.Info)
    var r := try sim.Recorder.New(sys.io)
    var rec := "ok"
    sink.Info(r.Io(), "rec {n}", (n: 1)) else {
        rec = "WriteError"
    }
    tape := r.Log(sys.arena) else {
        r.Free()
        return
    }
    r.Free()
    var p := try sim.Replayer.New(tape)
    var rep := "ok"
    sink.Info(p.Io(), "rep {n}", (n: 2)) else {
        rep = "WriteError"
    }
    p.Free()
    sys.Print("recorder {rec} replayer {rep}\n", (rec, rep)) else {}
}
`

// TestStdLog_TapeStream (std.sim's stdio entry answers the real process's
// streams): a sink on a Recorder's Io over
// the process's Io, and on a Replayer's, writes its record to standard
// error. The tape's table answered core.InertStdio, so both calls failed
// with WriteError and wrote nothing.
func TestStdLog_TapeStream(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			r := h.runMany(t, stdLogBuild(t, h, stdLogTape), []ioCall{{watch: 60}})[0]
			out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			errs := strings.ReplaceAll(r.stderr, "\r\n", "\n")
			if r.code != 0 || out != "recorder ok replayer ok\n" || errs != "INFO rec 1\nINFO rep 2\n" {
				t.Errorf("exit %d\nstdout %q\n  want %q\nstderr %q\n  want %q", r.code, out, "recorder ok replayer ok\n", errs, "INFO rec 1\nINFO rep 2\n")
			}
		})
	}
}

// stdLogContend: four native tasks report to log.Stderr while seeded
// Sims, each of four tasks, report to the same stream. Each sim record
// carries its seed and a per-Sim counter taken in the task's turn just
// before the call. With an argument, two native tasks run Sims at once
// (seeds 1 to 20 and 21 to 40), so two Sims contend for the stream too.
const stdLogContend = `import std.atomic
import std.log
import std.sim

Noise(io: Io, sink: &log.Sink, k: int) {
    for i in 0..3000 {
        sink.Info(io, "native {k} {i}", (k, i)) else {
            return
        }
    }
}

SimTask(io: Io, sink: &log.Sink, seq: &atomic.AtomicInt, seed: int, k: int) {
    // the first yield puts the task under the simulation's schedule
    io.Yield() else {}
    for i in 0..10 {
        n := seq.Add(1)
        sink.Info(io, "sim {seed} {k} {i} {n}", (seed, k, i, n)) else {
            return
        }
        io.Yield() else {}
    }
}

// Sims runs the Sims of seeds first to last - 1, one after another, on an
// arena of its own.
Sims(io: Io, h: &Heap, sink: &log.Sink, first: int, last: int) {
    var ar := Arena.New(h) else {
        return
    }
    defer ar.Free()
    for seed in first..last {
        var w := sim.Sim.New(&ar, Cast[u64](seed)) else {
            return
        }
        sio := w.Io()
        seq := atomic.AtomicInt.New(0)
        scope t := sio.Scope() {
            for k in 0..4 {
                t.go SimTask(sio, sink, &seq, seed, k)
            }
        }
        w.Free()
    }
}

export Main(sys: &System) {
    sink := log.Stderr(log.Level.Info)
    two := sys.args.Len() > 1
    scope s := sys.io.Scope() {
        for k in 0..4 {
            s.go Noise(sys.io, &sink, k)
        }
        if two {
            s.go Sims(sys.io, sys.heap, &sink, 1, 21)
            s.go Sims(sys.io, sys.heap, &sink, 21, 41)
        } else {
            s.go Sims(sys.io, sys.heap, &sink, 1, 41)
        }
    }
}
`

// stdLogContendRun checks one run of stdLogContend: every line a whole
// record, each native and each simulated task's records in order and
// complete. It returns each seed's sim records in stream order, joined,
// and the first place where a seed's counters do not rise 1 to 40 along
// the stream (the simulation's order), or "".
func stdLogContendRun(t *testing.T, run int, text string) ([40]string, string) {
	t.Helper()
	var seeds [40][]string
	var native [4]int
	var simNext [40][4]int
	fault := ""
	for _, l := range stdLogLines(strings.ReplaceAll(text, "\r\n", "\n")) {
		var k, i, seed, n int
		if c, _ := fmt.Sscanf(l, "INFO native %d %d", &k, &i); c == 2 && l == fmt.Sprintf("INFO native %d %d", k, i) && k >= 0 && k < 4 {
			if i != native[k] {
				t.Fatalf("run %d: native task %d wrote record %d after %d", run, k, i, native[k]-1)
			}
			native[k]++
			continue
		}
		if c, _ := fmt.Sscanf(l, "INFO sim %d %d %d %d", &seed, &k, &i, &n); c != 4 || l != fmt.Sprintf("INFO sim %d %d %d %d", seed, k, i, n) || seed < 1 || seed > 40 || k < 0 || k > 3 {
			t.Fatalf("run %d: torn or foreign line %q", run, l)
		}
		if i != simNext[seed-1][k] {
			t.Fatalf("run %d, seed %d: sim task %d wrote record %d after %d", run, seed, k, i, simNext[seed-1][k]-1)
		}
		simNext[seed-1][k]++
		s := &seeds[seed-1]
		if n != len(*s)+1 && fault == "" {
			fault = fmt.Sprintf("seed %d: stream position %d holds counter %d (task %d record %d)", seed, len(*s)+1, n, k, i)
		}
		*s = append(*s, fmt.Sprintf("%d.%d.%d", k, i, n))
	}
	if native != [4]int{3000, 3000, 3000, 3000} {
		t.Fatalf("run %d: native records %v, want 3000 each", run, native)
	}
	var out [40]string
	for i, s := range seeds {
		if len(s) != 40 {
			t.Fatalf("run %d: seed %d wrote %d sim records, want 40", run, i+1, len(s))
		}
		out[i] = strings.Join(s, " ")
	}
	return out, fault
}

// TestStdLog_SimOrderUnderContention: std.sim's stream order holds when
// nothing outside the simulation
// writes the stream (SimStreamOrder, SimOrderRepeats). Here native tasks
// write the same stream, and in the second form a second Sim on another
// native task does too: a simulated write that finds the stream held
// waits natively and hands the Sim's turn on by real time, so the order
// follows real time. The test holds what is still promised: no run
// hangs (1 and 16 carriers, alternating, under a 60 s watchdog), every
// record is whole, each task's records are in order and none is lost. It
// logs whether each run kept the simulation's order and whether runs
// agree; on both targets every run broke it when this was measured.
func TestStdLog_SimOrderUnderContention(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		for _, form := range []struct {
			name string
			args []string
		}{{"native", nil}, {"two-sims", []string{"two"}}} {
			t.Run(h.name()+"/"+form.name, func(t *testing.T) {
				t.Parallel()
				exe := stdLogBuild(t, h, stdLogContend, "--release")
				var calls []ioCall
				for r := range 6 {
					calls = append(calls, ioCall{env: []string{fmt.Sprintf("MK_CARRIERS=%d", []int{1, 16}[r%2])}, args: form.args, watch: 60})
				}
				var first [40]string
				ordered, agree := 0, 0
				for r, run := range h.runMany(t, exe, calls) {
					if run.code != 0 {
						t.Fatalf("run %d: exit %d (124: the watchdog killed it)\n%.300s", r, run.code, run.stderr)
					}
					seeds, fault := stdLogContendRun(t, r, run.stderr)
					if fault == "" {
						ordered++
					} else {
						t.Logf("run %d (%s), out of the simulation's order, which is allowed: %s", r, calls[r].env[0], fault)
					}
					if r == 0 {
						first = seeds
						continue
					}
					if seeds == first {
						agree++
					}
				}
				t.Logf("record: %d of 6 runs kept the simulation's order; %d of 5 later runs repeated run 0", ordered, agree)
			})
		}
	}
}

// stdLogParked: one task holds the file's mutex (under NoCancel) until a
// third task has acted, and 20 ms more; a reporter's Warn to the file
// parks on it; the third task, 50 ms after the mutex is held, cancels the
// scope ("cancel"), or arms the scope's deadline at 20 ms and, at 100 ms,
// records whether it expired ("deadline"). So the cancel or the deadline
// fires while Warn is parked in Mutex.Lock(io.NoCancel()). The reporter's result: 1 written, + 2
// the cancel or deadline had fired, + 4 the holder had released, + 8 its
// own scope reads canceled; 15 is right. With --sim the rounds run on a
// std.sim Io for seeds 1 to 50, the file on the simulated disk.
const stdLogParked = `import std.atomic
import std.fs
import std.log
import std.sim
import std.time

// Holder holds the mutex until the canceller has fired (or found the
// deadline not expired), so the reporter's wait spans the cancel however
// slowly the machine runs.
Holder(io: Io, m: &Mutex[fs.File], held: &atomic.AtomicBool, decided: &atomic.AtomicBool, released: &atomic.AtomicBool) {
    q := io.NoCancel()
    var g := m.Lock(q) else {
        return
    }
    held.Store(true)
    for !decided.Load() {
        q.Sleep(time.Millis(5)) else {}
    }
    q.Sleep(time.Millis(20)) else {}
    released.Store(true)
    g.Unlock()
}

Reporter(io: Io, sink: &log.Sink, held: &atomic.AtomicBool, fired: &atomic.AtomicBool, released: &atomic.AtomicBool, res: &atomic.AtomicInt) {
    for !held.Load() {
        io.Yield() else {}
    }
    sink.Warn(io, "parked report") else {
        res.Store(-1)
        return
    }
    var r: i64 = 1
    if fired.Load() {
        r += 2
    }
    if released.Load() {
        r += 4
    }
    io.Sleep(time.Millis(0)) else {
        r += 8
    }
    res.Store(r)
}

Canceller(io: Io, s: &Scope, held: &atomic.AtomicBool, fired: &atomic.AtomicBool, decided: &atomic.AtomicBool, deadline: bool) {
    for !held.Load() {
        io.Yield() else {}
    }
    q := io.NoCancel()
    if deadline {
        s.CancelAfter(time.Millis(20))
        q.Sleep(time.Millis(100)) else {}
        fired.Store(s.Expired())
        decided.Store(true)
        return
    }
    q.Sleep(time.Millis(50)) else {}
    s.Cancel()
    fired.Store(true)
    decided.Store(true)
}

// Round runs the three tasks on io with a log file at path opened through
// io, prints the file, and returns the reporter's result.
Round(sys: &System, io: Io, path: str, deadline: bool): fs.Error|OutOfMemory!i64 {
    f := try fs.Create(io, path)
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    held := atomic.AtomicBool.New(false)
    fired := atomic.AtomicBool.New(false)
    decided := atomic.AtomicBool.New(false)
    released := atomic.AtomicBool.New(false)
    res := atomic.AtomicInt.New(0)
    scope s := io.Scope() {
        s.go Holder(io, &m, &held, &decided, &released)
        s.go Reporter(io, &sink, &held, &fired, &released, &res)
        s.go Canceller(io, &s, &held, &fired, &decided, deadline)
    }
    var g := m.Into()
    onerr g.Abandon()
    try g.Close(io)
    text := try fs.ReadText(sys.arena, io, path)
    try fs.Remove(io, path)
    sys.Print("==file==\n{text}", (text)) else {}
    return res.Load()
}

export Main(sys: &System): fs.Error|OutOfMemory!void {
    path := sys.args[1]
    deadline := sys.args[2] == "deadline"
    if sys.args.Len() < 4 {
        r := try Round(sys, sys.io, path, deadline)
        sys.PrintErr("rounds 1 good {g} first {r}\n", (g: if r == 15 { 1 } else { 0 }, r)) else {}
        return
    }
    var good := 0
    var bad: i64 = 15
    for seed in 1..51 {
        var w := try sim.Sim.New(sys.arena, Cast[u64](seed))
        r := Round(sys, w.Io(), path, deadline) else {
            w.Free()
            sys.PrintErr("seed {seed} failed\n", (seed)) else {}
            return
        }
        w.Free()
        if r == 15 {
            good += 1
        } else if bad == 15 {
            bad = r
        }
    }
    sys.PrintErr("rounds 50 good {good} first {bad}\n", (good, bad)) else {}
}
`

// TestStdLog_CancelWhileParked (a cancel may land at every await point):
// a cancel, or the scope's deadline, that
// fires while Sink.Warn waits on the file's mutex (taken through
// io.NoCancel()) neither fails the call nor loses the record: Warn
// returns after the holder releases, with the cancel already fired, and
// the reporter's own scope reads canceled afterwards. At 1 and 16
// carriers, and on std.sim over 50 seeds, for both Cancel and CancelAfter.
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestStdLog_CancelWhileParked(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			exe := stdLogBuild(t, h, stdLogParked)
			var calls []ioCall
			for _, how := range []string{"cancel", "deadline"} {
				calls = append(calls,
					ioCall{env: []string{"MK_CARRIERS=1"}, args: []string{stdLogFile(h, "parked-1-"+how+".log"), how}, watch: 60},
					ioCall{env: []string{"MK_CARRIERS=16"}, args: []string{stdLogFile(h, "parked-16-"+how+".log"), how}, watch: 60},
					ioCall{env: []string{"MK_CARRIERS=16"}, args: []string{"parked-sim.log", how, "--sim"}, watch: 60})
			}
			for i, r := range h.runMany(t, exe, calls) {
				rounds := 1
				if len(calls[i].args) == 3 {
					rounds = 50
				}
				out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
				errs := strings.ReplaceAll(r.stderr, "\r\n", "\n")
				wantOut := strings.Repeat("==file==\nWARN parked report\n", rounds)
				wantErr := fmt.Sprintf("rounds %d good %d first 15\n", rounds, rounds)
				if r.code != 0 || out != wantOut || errs != wantErr {
					t.Errorf("%v %v: exit %d\nstdout %.200q\n  want %.200q\nstderr %q\n  want %q (15: written, fired, released, canceled after)", calls[i].env, calls[i].args[1:], r.code, out, wantOut, errs, wantErr)
				}
			}
		})
	}
}

// stdLogStreamParked: a writer task logs 64 records of about 4 KiB to
// log.Stdout, more than a pipe holds, while its reader has not started;
// 50 ms in, another task cancels the scope. The writer's result: records
// written, whether the cancel had fired when the last returned, and
// whether its scope reads canceled then.
const stdLogStreamParked = `import std.atomic
import std.log
import std.time

Writer(io: Io, sink: &log.Sink, body: str, fired: &atomic.AtomicBool, res: &atomic.AtomicInt) {
    var n: i64 = 0
    for i in 0..64 {
        sink.Info(io, "w {i} {b}", (i, b: body)) else {
            break
        }
        n += 1
    }
    var r := n * 4
    if fired.Load() {
        r += 1
    }
    io.Sleep(time.Millis(0)) else {
        r += 2
    }
    res.Store(r)
}

Canceller(io: Io, s: &Scope, fired: &atomic.AtomicBool) {
    io.NoCancel().Sleep(time.Millis(50)) else {}
    s.Cancel()
    fired.Store(true)
}

export Main(sys: &System): OutOfMemory!void {
    var t := Text.New(sys.arena)
    for _ in 0..4000 {
        try t.Push("x")
    }
    body := t.Done()
    sink := log.Stdout(log.Level.Info)
    fired := atomic.AtomicBool.New(false)
    res := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        s.go Writer(sys.io, &sink, body, &fired, &res)
        s.go Canceller(sys.io, &s, &fired)
    }
    r := res.Load()
    sys.PrintErr("wrote {w} fired {f} canceled {c}\n", (w: r / 4, f: r % 2, c: (r / 2) % 2)) else {}
}
`

// stdLogLateReader runs exe with its standard output on a pipe that is
// read only after delay, under a watchdog.
func stdLogLateReader(t *testing.T, exe string, delay time.Duration, args ...string) nativeRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	var e strings.Builder
	cmd.Stderr = &e
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(delay)
	b, _ := io.ReadAll(out)
	err = cmd.Wait()
	r := nativeRun{stdout: string(b), stderr: e.String()}
	if ctx.Err() != nil {
		r.code = 124
	} else if ee, ok := err.(*exec.ExitError); ok {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestStdLog_CancelWhileStreamParked (the stream case): a
// cancel that fires while a log.Stdout call is blocked in its write (the
// pipe full, its reader not started for a second) loses no record: all
// 64 are written whole and in order, and the cancel had fired before the
// last returned.
func TestStdLog_CancelWhileStreamParked(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 4000)
	check := func(t *testing.T, r nativeRun) {
		t.Helper()
		errs := strings.ReplaceAll(r.stderr, "\r\n", "\n")
		if r.code != 0 || errs != "wrote 64 fired 1 canceled 1\n" {
			t.Fatalf("exit %d, stderr %q, want %q", r.code, errs, "wrote 64 fired 1 canceled 1\n")
		}
		lines := stdLogLines(strings.ReplaceAll(r.stdout, "\r\n", "\n"))
		if len(lines) != 64 {
			t.Fatalf("%d lines on standard output, want 64", len(lines))
		}
		for i, l := range lines {
			if want := fmt.Sprintf("INFO w %d %s", i, body); l != want {
				t.Fatalf("line %d is not record %d whole: %d bytes, starting %.30q", i, i, len(l), l)
			}
		}
	}
	t.Run("host", func(t *testing.T) {
		t.Parallel()
		exe := stdLogBuild(t, hostIo, stdLogStreamParked)
		check(t, stdLogLateReader(t, exe, time.Second))
	})
	if linuxRun() {
		t.Run("linux", func(t *testing.T) {
			t.Parallel()
			exe := stdLogBuild(t, linuxIo, stdLogStreamParked)
			sh := stdLogScript(t, exe, "late.sh", "./"+filepath.Base(exe)+" | (sleep 1; cat)\nexit ${PIPESTATUS[0]}\n")
			check(t, linuxIo.runMany(t, sh, []ioCall{{watch: 60}})[0])
		})
	}
}

// stdLogErrors drives each documented cause of WriteError, by its first
// argument:
//   - freed: a ToFile sink on a freed Sim's Io, which answers Canceled to
//     every call, the mutex free (the write fails) and then held by the
//     caller (Mutex.Lock itself fails: on this Io a held lock returns no
//     other way), and then on the process's Io;
//   - readonly: a ToFile sink over a file opened for reading, twice, and
//     then a lock of its mutex, which must not have been left held;
//   - format: a field whose Format writes and then fails, to each sink;
//   - pipe: two log.Stdout calls with standard output a broken pipe.
const stdLogErrors = `import std.fs
import std.log
import std.sim

// FailingField's Format writes part of its text and then fails.
struct FailingField {
    n: int
}

impl Format for FailingField {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("partial")
        fail WriteError
    }
}

// Done closes the file m guards; a failed close abandons it.
Done(sys: &System, m: Mutex[fs.File]) {
    var f := m.Into()
    f.Close(sys.io) else {
        f.Abandon()
    }
}

// Show prints the file at path after a marker and removes it.
Show(sys: &System, path: str): fs.Error|OutOfMemory!void {
    text := try fs.ReadText(sys.arena, sys.io, path)
    try fs.Remove(sys.io, path)
    sys.Print("==file==\n{text}", (text)) else {}
}

Freed(sys: &System, path: str): fs.Error|OutOfMemory!void {
    var w := try sim.Sim.New(sys.arena, 1)
    dead := w.Io()
    w.Free()
    f := try fs.Create(sys.io, path)
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    var free := "ok"
    sink.Warn(dead, "lost free") else {
        free = "WriteError"
    }
    var held := "ok"
    // the process's Io is not cancelled here, so this Lock returns the
    // guard
    var g := m.Lock(sys.io).Must()
    sink.Warn(dead, "lost held") else {
        held = "WriteError"
    }
    g.Unlock()
    var after := "ok"
    sink.Warn(sys.io, "kept") else {
        after = "WriteError"
    }
    sys.PrintErr("free {free} held {held} after {after}\n", (free, held, after)) else {}
    Done(sys, m)
    try Show(sys, path)
}

ReadOnly(sys: &System, path: str): fs.Error|OutOfMemory!void {
    var c := try fs.Create(sys.io, path)
    c.Close(sys.io) else {
        c.Abandon()
    }
    f := try fs.Open(sys.io, path)
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    var a := "ok"
    sink.Warn(sys.io, "one") else {
        a = "WriteError"
    }
    var b := "ok"
    sink.Warn(sys.io, "two") else {
        b = "WriteError"
    }
    // the failing path released the mutex, or this never returns
    var g := m.Lock(sys.io).Must()
    g.Unlock()
    sys.PrintErr("readonly {a} {b} locked\n", (a, b)) else {}
    Done(sys, m)
    try Show(sys, path)
}

FormatFails(sys: &System, path: str): fs.Error|OutOfMemory!void {
    f := try fs.Create(sys.io, path)
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    var a := "ok"
    log.Stdout(log.Level.Info).Info(sys.io, "a {b} z", (b: FailingField(n: 1))) else {
        a = "WriteError"
    }
    var b := "ok"
    log.Stderr(log.Level.Info).Info(sys.io, "a {b} z", (b: FailingField(n: 2))) else {
        b = "WriteError"
    }
    var c := "ok"
    sink.Info(sys.io, "a {b} z", (b: FailingField(n: 3))) else {
        c = "WriteError"
    }
    var d := "ok"
    sink.Info(sys.io, "kept") else {
        d = "WriteError"
    }
    sys.PrintErr("format {a} {b} {c} after {d}\n", (a, b, c, d)) else {}
    Done(sys, m)
    try Show(sys, path)
}

Pipe(sys: &System) {
    sink := log.Stdout(log.Level.Info)
    var a := "ok"
    sink.Info(sys.io, "one") else {
        a = "WriteError"
    }
    var b := "ok"
    sink.Info(sys.io, "two") else {
        b = "WriteError"
    }
    sys.PrintErr("pipe {a} {b}\n", (a, b)) else {}
}

export Main(sys: &System): fs.Error|OutOfMemory!void {
    mode := sys.args[1]
    if mode == "freed" {
        try Freed(sys, sys.args[2])
    } else if mode == "readonly" {
        try ReadOnly(sys, sys.args[2])
    } else if mode == "format" {
        try FormatFails(sys, sys.args[2])
    } else {
        Pipe(sys)
    }
}
`

// stdLogBrokenPipe runs exe with its standard output a pipe whose reader
// is closed before the program starts, under a watchdog.
func stdLogBrokenPipe(t *testing.T, exe string, args ...string) nativeRun {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	var e strings.Builder
	cmd.Stdout, cmd.Stderr = w, &e
	err = cmd.Run()
	out := nativeRun{stderr: e.String()}
	if ctx.Err() != nil {
		out.code = 124
	} else if ee, ok := err.(*exec.ExitError); ok {
		out.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStdLog_WriteErrorPaths (the sink's documented error set): each
// cause of WriteError fails the call, writes nothing of the
// failed record, and leaves the sink usable.
//   - freed: on a freed Sim's Io the write fails with the mutex free, and
//     Mutex.Lock itself fails with the mutex held (EmitFile's failed-lock
//     branch; on this Io a held lock returns no other way, so a return
//     at all shows the branch ran), and a later call on the process's Io
//     writes.
//   - readonly: a write the OS refuses fails twice, and the mutex was
//     released each time.
//   - format: a field whose Format fails fails each sink's call, and no
//     byte of the record (not "a ", not "partial") reaches any sink.
//   - pipe: standard output a broken pipe, two calls each fail with
//     WriteError and the program ends (SIGPIPE is ignored on Linux).
func TestStdLog_WriteErrorPaths(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			exe := stdLogBuild(t, h, stdLogErrors)
			calls := []ioCall{
				{args: []string{"freed", stdLogFile(h, "err-freed.log")}, watch: 60},
				{args: []string{"readonly", stdLogFile(h, "err-ro.log")}, watch: 60},
				{args: []string{"format", stdLogFile(h, "err-format.log")}, watch: 60},
			}
			want := []struct{ out, err string }{
				{"==file==\nWARN kept\n", "free WriteError held WriteError after ok\n"},
				{"==file==\n", "readonly WriteError WriteError locked\n"},
				{"==file==\nINFO kept\n", "format WriteError WriteError WriteError after ok\n"},
			}
			for i, r := range h.runMany(t, exe, calls) {
				out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
				errs := strings.ReplaceAll(r.stderr, "\r\n", "\n")
				if r.code != 0 || out != want[i].out || errs != want[i].err {
					t.Errorf("%s: exit %d (124: the watchdog killed it)\nstdout %q\n  want %q\nstderr %q\n  want %q", calls[i].args[0], r.code, out, want[i].out, errs, want[i].err)
				}
			}
			var r nativeRun
			if h.linux {
				sh := stdLogScript(t, exe, "broken.sh", "(sleep 0.3; ./"+filepath.Base(exe)+" pipe) | true\nexit ${PIPESTATUS[0]}\n")
				r = h.runMany(t, sh, []ioCall{{watch: 60}})[0]
			} else {
				r = stdLogBrokenPipe(t, exe, "pipe")
			}
			if errs := strings.ReplaceAll(r.stderr, "\r\n", "\n"); r.code == 124 || errs != "pipe WriteError WriteError\n" {
				t.Errorf("pipe: exit %d (124: the watchdog killed it), stderr %q, want %q", r.code, errs, "pipe WriteError WriteError\n")
			}
		})
	}
}
