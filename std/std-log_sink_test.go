// Why: log.Sink is shared by many tasks and std.sim; records must stay whole,
// ordered and written under cancellation, and its compile-time facts hold.
// What: TestStdLog_Interleave, StreamOrder, ReportWhileCanceled, the Sim*
// tests, CutMatchesModel, CompileFacts and NoUnsafe.
// How: Mk programs over the tree's std, with stdLogFinish as a Go model.

// Tests of log.Sink. Each test builds a program over today's std and judges
// what it wrote: 16 tasks at 16 carriers logging to the three sinks with
// every record whole and none interleaved; a sink record in order with
// System.Stdout's and System.PrintErr's buffered bytes; reports made while
// the scope is being cancelled, the file's mutex held by another task,
// still written; std.sim's order the same for a seed; the cut and its mark
// against stdLogFinish, a Go model; and the compile-time facts (the sink's
// kinds, the loan ToFile keeps, E0706 at the call). On x86_64-linux too
// when MK_LINUX=1, under the WSL lock with -p 1, one wsl.exe per batch.

package std

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// stdLogMax is RecordMax of std/log/sink.mk: a record's bytes, newline
// included.
const stdLogMax = 4096

// stdLogFinish is the model of what Sink.Log writes for the formatted text
// rec (level word and message): rec and a newline when that fits in
// stdLogMax bytes, else rec cut on a UTF-8 boundary (backing over at most
// three continuation bytes) with room for " [cut N bytes]\n", N counting
// the bytes of rec not written, its room reserved for the digits of
// len(rec).
func stdLogFinish(rec string) string {
	if len(rec)+1 <= stdLogMax {
		return rec + "\n"
	}
	keep := stdLogMax - len(" [cut ") - len(strconv.Itoa(len(rec))) - len(" bytes]\n")
	for low := keep - 3; keep > low && rec[keep]&0xC0 == 0x80; keep-- {
	}
	return rec[:keep] + " [cut " + strconv.Itoa(len(rec)-keep) + " bytes]\n"
}

// stdLogHosts are the targets a test runs on: the host, and x86_64-linux
// when MK_LINUX=1.
func stdLogHosts() []ioHost {
	if linuxRun() {
		return []ioHost{hostIo, linuxIo}
	}
	return []ioHost{hostIo}
}

func (h ioHost) name() string {
	if h.linux {
		return "linux"
	}
	return "host"
}

// stdLogBuild builds src for h with the profile's flags, over the tree's
// std with MK_STDLOG_FIXTURES's files in place (stdLogFixtures).
func stdLogBuild(t *testing.T, h ioHost, src string, flags ...string) string {
	t.Helper()
	if h.linux {
		flags = append(flags, "--target", "x86_64-linux")
	}
	return buildNative(t, stdLogFixtures(t), src, flags...)
}

// stdLogFile is the log file a program writes, by host: beside the
// executable on Windows, under /tmp on Linux, named per test.
func stdLogFile(h ioHost, name string) string {
	if h.linux {
		return fmt.Sprintf("/tmp/mk-stdlog-%d-%s", os.Getpid(), name)
	}
	return name
}

// stdLogLines splits text into its lines, CRLF normalized; a last line
// with no newline is a line.
func stdLogLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// stdLogInterleave: 16 tasks each write 63 records, a third to each sink,
// of 0 to 5,000 payload bytes (past RecordMax for some, so a cut record
// is written whole too). After the join, main writes a marker line and the
// file's text to standard output.
const stdLogInterleave = `import std.fs
import std.log

const Count = 63

// Size is the payload length of record i of task k.
Size(k: int, i: int): int {
    return match (k + i) % 7 {
        0 { 0 }
        1 { 7 }
        2 { 100 }
        3 { 1000 }
        4 { 4060 }
        5 { 4090 }
        _ { 5000 }
    }
}

Task(io: Io, sinks: &[3]log.Sink, k: int, body: str) {
    for i in 0..Count {
        n := Size(k, i)
        sinks[i % 3].Info(io, "t={k} i={i} n={n} {b}", (k, i, n, b: body[..n])) else {
            return
        }
    }
}

Run(sys: &System, path: str): fs.Error|OutOfMemory!void {
    var t := Text.New(sys.arena)
    for _ in 0..5000 {
        try t.Push("x")
    }
    body := t.Done()
    f := try fs.Create(sys.io, path)
    m := Mutex[fs.File].New(f)
    sinks: [3]log.Sink = [log.Stdout(log.Level.Info), log.Stderr(log.Level.Info), log.ToFile(&m, log.Level.Info)]
    scope s := sys.io.Scope() {
        for k in 0..16 {
            s.go Task(sys.io, &sinks, k, body)
        }
    }
    var g := m.Into()
    onerr g.Abandon()
    try g.Close(sys.io)
    text := try fs.ReadText(sys.arena, sys.io, path)
    try fs.Remove(sys.io, path)
    sys.Print("==file==\n{text}", (text)) else {}
}

export Main(sys: &System): fs.Error|OutOfMemory!void {
    try Run(sys, sys.args[1])
}
`

// stdLogRecordRe is one record of stdLogInterleave: level, task, number,
// payload length, the payload, and the mark when it was cut.
var stdLogRecordRe = regexp.MustCompile(`^INFO t=([0-9]+) i=([0-9]+) n=([0-9]+) (x*)( \[cut ([0-9]+) bytes\])?$`)

// stdLogSize is Size of stdLogInterleave.
func stdLogSize(k, i int) int {
	return []int{0, 7, 100, 1000, 4060, 4090, 5000}[(k+i)%7]
}

// stdLogCheckSink fails t unless lines are exactly the records of sink s
// (0 stdout, 1 stderr, 2 file) of stdLogInterleave, each whole and as the
// model writes it, every task's in order.
func stdLogCheckSink(t *testing.T, what string, s int, lines []string) {
	t.Helper()
	next := make([]int, 16) // the next record number per task
	for k := range next {
		next[k] = s
	}
	for _, l := range lines {
		m := stdLogRecordRe.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("%s: a line that is no whole record (interleaved or cut wrong): %.200q", what, l)
		}
		k, _ := strconv.Atoi(m[1])
		i, _ := strconv.Atoi(m[2])
		if k < 0 || k >= 16 || i != next[k] {
			t.Fatalf("%s: record t=%d i=%d out of order or in the wrong sink (want i=%d)", what, k, i, next[k])
		}
		next[k] += 3
		n := stdLogSize(k, i)
		rec := fmt.Sprintf("INFO t=%d i=%d n=%d %s", k, i, n, strings.Repeat("x", n))
		if want := stdLogFinish(rec); l+"\n" != want {
			t.Fatalf("%s: record t=%d i=%d is %d bytes, the model's %d:\n got %.120q\nwant %.120q", what, k, i, len(l)+1, len(want), l[max(0, len(l)-60):], want[max(0, len(want)-61):])
		}
	}
	for k, n := range next {
		if n < 63 {
			t.Errorf("%s: task %d's records stop before %d", what, k, n)
		}
	}
}

// TestStdLog_Interleave: 16 tasks at 16 carriers, dev and release, every
// record of each of the three sinks whole, in its sink, and each task's in
// order; a cut record is whole and marked.
func TestStdLog_Interleave(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		for _, mode := range cliModes {
			t.Run(h.name()+"/"+mode.name, func(t *testing.T) {
				t.Parallel()
				exe := stdLogBuild(t, h, stdLogInterleave, mode.flags...)
				path := stdLogFile(h, "interleave-"+mode.name+".log")
				r := h.runMany(t, exe, []ioCall{{env: []string{"MK_CARRIERS=16"}, args: []string{path}, watch: 120}})[0]
				if r.code != 0 {
					t.Fatalf("exit %d\nstderr:\n%.2000s", r.code, r.stderr)
				}
				out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
				k := strings.Index(out, "==file==\n")
				if k < 0 {
					t.Fatalf("no file marker in standard output (%d bytes)", len(out))
				}
				stdLogCheckSink(t, "stdout", 0, stdLogLines(out[:k]))
				stdLogCheckSink(t, "stderr", 1, stdLogLines(r.stderr))
				stdLogCheckSink(t, "file", 2, stdLogLines(out[k+len("==file==\n"):]))
			})
		}
	}
}

// stdLogOrder writes sink records between buffered System.Stdout bytes and
// System.Print and System.PrintErr calls: each record follows what the
// process stream buffered before it.
const stdLogOrder = `import std.log

export Main(sys: &System): WriteError!void {
    out := log.Stdout(log.Level.Debug)
    err := log.Stderr(log.Level.Debug)
    var o := sys.Stdout()
    try o.Str("a1 ")
    try out.Info(sys.io, "r1")
    try sys.Print("a2 {n}\n", (n: 2))
    try sys.PrintErr("e1 ")
    try err.Warn(sys.io, "r2 {s}", (s: "two"))
    try sys.PrintErr("e2\n")
    try out.Debug(sys.io, "r3")
    try sys.Print("a3")
    try err.Error(sys.io, "r4")
}
`

// TestStdLog_StreamOrder: a record is written after the bytes buffered
// before it, by a Writer from System.Stdout or by System.Print and
// PrintErr, and before the ones written after it.
func TestStdLog_StreamOrder(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			r := h.run(t, stdLogBuild(t, h, stdLogOrder), "")
			wantOut := "a1 INFO r1\na2 2\nDEBUG r3\na3"
			wantErr := "e1 WARN r2 two\ne2\nERROR r4\n"
			got := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			gotErr := strings.ReplaceAll(r.stderr, "\r\n", "\n")
			if r.code != 0 || got != wantOut || gotErr != wantErr {
				t.Errorf("exit %d\nstdout %q\n  want %q\nstderr %q\n  want %q", r.code, got, wantOut, gotErr, wantErr)
			}
		})
	}
}

// stdLogCancel: one task holds the file's mutex (taken under NoCancel)
// for 100 ms; another waits until it is held, cancels the scope, checks
// that its own wait now fails with Canceled, and reports to each sink,
// which still writes; its file report waits for the holder's unlock. Each
// round writes "==file==" and the file's text to standard output after the
// join. With --sim the rounds run on a std.sim Io for seeds 1 to 50, the
// file on the simulated disk (a file's Io is the one it was opened with).
const stdLogCancel = `import std.atomic
import std.fs
import std.log
import std.sim
import std.time

Holder(io: Io, m: &Mutex[fs.File], held: &atomic.AtomicBool) {
    q := io.NoCancel()
    var g := m.Lock(q) else {
        return
    }
    held.Store(true)
    q.Sleep(time.Millis(100)) else {}
    g.Unlock()
}

Reporter(io: Io, s: &Scope, sinks: &[3]log.Sink, held: &atomic.AtomicBool, wrote: &atomic.AtomicInt) {
    for !held.Load() {
        io.Yield() else {}
    }
    s.Cancel()
    var canceled := false
    io.Sleep(time.Millis(1)) else {
        canceled = true
    }
    for k in 0..3 {
        sinks[k].Warn(io, "report {k} canceled={canceled}", (k, canceled)) else {
            continue
        }
        _ = wrote.Add(1)
    }
}

// Round runs the two tasks on io with a log file at path opened through
// io, and returns the records written.
Round(sys: &System, io: Io, path: str): fs.Error|OutOfMemory!int {
    f := try fs.Create(io, path)
    m := Mutex[fs.File].New(f)
    sinks: [3]log.Sink = [log.Stdout(log.Level.Info), log.Stderr(log.Level.Info), log.ToFile(&m, log.Level.Info)]
    held := atomic.AtomicBool.New(false)
    wrote := atomic.AtomicInt.New(0)
    scope s := io.Scope() {
        s.go Holder(io, &m, &held)
        s.go Reporter(io, &s, &sinks, &held, &wrote)
    }
    var g := m.Into()
    onerr g.Abandon()
    try g.Close(io)
    text := try fs.ReadText(sys.arena, io, path)
    try fs.Remove(io, path)
    sys.Print("==file==\n{text}", (text)) else {}
    return Cast[int](wrote.Load())
}

Run(sys: &System, path: str, simulate: bool): fs.Error|OutOfMemory!int {
    if !simulate {
        return try Round(sys, sys.io, path)
    }
    var wrote := 0
    for seed in 1..51 {
        var w := try sim.Sim.New(sys.arena, Cast[u64](seed))
        n := Round(sys, w.Io(), path) else {
            w.Free()
            return -seed
        }
        wrote += n
        w.Free()
    }
    return wrote
}

export Main(sys: &System): fs.Error|OutOfMemory!void {
    wrote := try Run(sys, sys.args[1], sys.args.Len() > 2)
    sys.PrintErr("wrote {wrote}\n", (wrote)) else {}
}
`

// TestStdLog_ReportWhileCanceled: a report made in a scope that is being
// cancelled is written to each sink, natively at 1 and 16 carriers and on
// std.sim over 50 seeds, though the reporter's own waits fail with
// Canceled and the file's mutex is held by another task when it reports.
func TestStdLog_ReportWhileCanceled(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			exe := stdLogBuild(t, h, stdLogCancel)
			calls := []ioCall{
				{env: []string{"MK_CARRIERS=1"}, args: []string{stdLogFile(h, "cancel-1.log")}, watch: 60},
				{env: []string{"MK_CARRIERS=16"}, args: []string{stdLogFile(h, "cancel-16.log")}, watch: 60},
				{env: []string{"MK_CARRIERS=16"}, args: []string{"cancel-sim.log", "--sim"}, watch: 60},
			}
			for i, r := range h.runMany(t, exe, calls) {
				rounds := 1
				if i == 2 {
					rounds = 50
				}
				out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
				errs := strings.ReplaceAll(r.stderr, "\r\n", "\n")
				round := "WARN report 0 canceled=true\n==file==\nWARN report 2 canceled=true\n"
				if wantOut := strings.Repeat(round, rounds); r.code != 0 || out != wantOut {
					t.Errorf("call %d: exit %d\nstdout %.300q\n  want %.300q\nstderr %.300q", i, r.code, out, wantOut, errs)
				}
				if wantErr := strings.Repeat("WARN report 1 canceled=true\n", rounds) + fmt.Sprintf("wrote %d\n", 3*rounds); errs != wantErr {
					t.Errorf("call %d: stderr %.300q\n  want %.300q", i, errs, wantErr)
				}
			}
		})
	}
}

// stdLogSim: for each seed, four tasks on a std.sim Io write three records
// each to standard error, yielding between them, framed by System.PrintErr
// bytes buffered before the run and written after it. Each record carries
// the value a shared counter gave it just before the call, so the
// simulated order of the calls can be read back from the stream.
const stdLogSim = `import std.atomic
import std.log
import std.sim

Task(io: Io, sink: &log.Sink, seq: &atomic.AtomicInt, k: int) {
    // the first yield puts the task under the simulation's schedule
    // before it takes a number
    io.Yield() else {}
    for i in 0..3 {
        n := seq.Add(1)
        sink.Info(io, "seed task {k} record {i} seq {n}", (k, i, n)) else {
            return
        }
        io.Yield() else {}
    }
}

export Main(sys: &System): OutOfMemory!void {
    sink := log.Stderr(log.Level.Info)
    for seed in 1..9 {
        sys.PrintErr("seed {seed}: ", (seed)) else {}
        seq := atomic.AtomicInt.New(0)
        var w := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := w.Io()
        scope s := io.Scope() {
            for k in 0..4 {
                s.go Task(io, &sink, &seq, k)
            }
        }
        w.Free()
        sys.PrintErr("end\n") else {}
    }
}
`

// stdLogSimRun is one run of stdLogSim: per seed, its 12 records in stream
// order, each as (task, record, seq).
type stdLogSimRun [8][12][3]int

// stdLogSimRuns runs stdLogSim n times, alternating 16 carriers and 1, and
// checks each run's records: every record whole between its seed's
// framing bytes, and each task's in order. It returns each run's standard
// error and its parsed records.
func stdLogSimRuns(t *testing.T, h ioHost, n int) ([]string, []stdLogSimRun) {
	t.Helper()
	exe := stdLogBuild(t, h, stdLogSim)
	var calls []ioCall
	for r := range n {
		calls = append(calls, ioCall{env: []string{fmt.Sprintf("MK_CARRIERS=%d", []int{16, 1}[r%2])}, watch: 60})
	}
	rs := h.runMany(t, exe, calls)
	texts := make([]string, n)
	runs := make([]stdLogSimRun, n)
	for r := range rs {
		a := strings.ReplaceAll(rs[r].stderr, "\r\n", "\n")
		if rs[r].code != 0 {
			t.Fatalf("run %d: exit %d\n%s", r, rs[r].code, a)
		}
		lines := stdLogLines(a)
		for seed := 1; seed <= 8; seed++ {
			head := fmt.Sprintf("seed %d: ", seed)
			if len(lines) < 13 || !strings.HasPrefix(lines[0], head) || lines[12] != "end" {
				t.Fatalf("run %d, seed %d: not 12 records between its framing:\n%s", r, seed, a)
			}
			recs := append([]string{strings.TrimPrefix(lines[0], head)}, lines[1:12]...)
			next := make([]int, 4)
			for j, l := range recs {
				var k, i, s int
				if c, _ := fmt.Sscanf(l, "INFO seed task %d record %d seq %d", &k, &i, &s); c != 3 || k < 0 || k > 3 || i != next[k] || l != fmt.Sprintf("INFO seed task %d record %d seq %d", k, i, s) {
					t.Fatalf("run %d, seed %d: bad or out-of-order record %q", r, seed, l)
				}
				next[k]++
				runs[r][seed-1][j] = [3]int{k, i, s}
			}
			lines = lines[13:]
		}
		texts[r] = a
	}
	return texts, runs
}

// TestStdLog_SimRecordsWhole: on a std.sim Io, at 16 carriers and at 1,
// each record of four tasks is written whole, in order with the
// System.PrintErr bytes buffered before the run and written after it, and
// each task's records in order.
func TestStdLog_SimRecordsWhole(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			stdLogSimRuns(t, h, 2)
		})
	}
}

// TestStdLog_SimStreamOrder: under std.sim the stream holds the records
// in the order the simulation made the calls: the counter each record
// carries, taken in the caller's turn just before its call, rises along
// the stream within each seed. Before rt8's core.StdWriteHere, SimStdio
// wrote through the native Io, which parked the task on the pool and gave
// the turn away by real time: the record that took seq 5 landed second in
// every seed.
func TestStdLog_SimStreamOrder(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			_, runs := stdLogSimRuns(t, h, 2)
			for r, run := range runs {
				for seed, recs := range run {
					for j, rec := range recs {
						if rec[2] != j+1 {
							t.Errorf("run %d, seed %d: stream position %d holds seq %d (task %d record %d)", r, seed+1, j+1, rec[2], rec[0], rec[1])
							break
						}
					}
				}
			}
		})
	}
}

// TestStdLog_SimOrderRepeats: under std.sim a seed fixes the order of the
// records, so six runs (16 carriers and 1, alternating) write the same
// bytes, and the seeds do not all give one order. A sink write from a
// simulated task must not hand the turn away (core.StdWriteHere;
// with the native write, 8 runs gave 8 schedules). Two runs agreed by
// chance often enough then that fewer
// would hide it.
func TestStdLog_SimOrderRepeats(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			texts, _ := stdLogSimRuns(t, h, 6)
			for r := 1; r < len(texts); r++ {
				al, bl := stdLogLines(texts[0]), stdLogLines(texts[r])
				for i := range min(len(al), len(bl)) {
					if al[i] != bl[i] {
						t.Fatalf("runs 0 and %d of the same seeds differ at line %d: %q against %q", r, i, al[i], bl[i])
					}
				}
			}
			orders := map[string]bool{}
			al := stdLogLines(texts[0])
			for seed := 0; seed < 8; seed++ {
				orders[strings.Join(al[13*seed:13*seed+12], "|")] = true
			}
			if len(orders) < 2 {
				t.Errorf("8 seeds gave one order: the simulation did not interleave the tasks")
			}
		})
	}
}

// stdLogBeside has four native tasks report to log.Stderr on the process's
// Io while, on another task's turn, forty seeded Sims each run four tasks
// that report to the same stream: the simulated writes take the stream's
// lock without parking (they spin) and must release it so that a native
// task parked on it wakes.
const stdLogBeside = `import std.log
import std.sim

Noise(io: Io, sink: &log.Sink, k: int) {
    for i in 0..3000 {
        sink.Info(io, "native {k} {i}", (k, i)) else {
            return
        }
    }
}

SimTask(io: Io, sink: &log.Sink, k: int) {
    for i in 0..20 {
        sink.Info(io, "sim {k} {i}", (k, i)) else {
            return
        }
        io.Yield() else {}
    }
}

export Main(sys: &System): OutOfMemory!void {
    sink := log.Stderr(log.Level.Info)
    scope s := sys.io.Scope() {
        for k in 0..4 {
            s.go Noise(sys.io, &sink, k)
        }
        for seed in 1..41 {
            var w := sim.Sim.New(sys.arena, Cast[u64](seed)) else {
                return
            }
            io := w.Io()
            scope t := io.Scope() {
                for k in 0..4 {
                    t.go SimTask(io, &sink, k)
                }
            }
            w.Free()
        }
    }
}
`

// TestStdLog_SimBesideNative: simulated and native tasks reporting to one
// process stream at once neither hang nor tear a record: four runs (one
// at 1 carrier, three at 16) under a 60 s watchdog each end, with every
// line a whole record and each task's records in order. With SimStdio's
// write moved to rt.DirectIo alone (whose wake does nothing), 30 of 30
// runs of this program hung at 16 carriers. With core.StdWriteHere's
// first form (the lock taken through DirectIo, which spins), every run at
// 1 carrier hung. Inferred from LockWordSlow and DirectWait, not traced:
// a native writer parked in its pool write held the stream's lock, and
// the simulated task spun on the only carrier, so the writer never
// resumed. StreamPut now takes the lock through rel.
func TestStdLog_SimBesideNative(t *testing.T) {
	t.Parallel()
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			t.Parallel()
			exe := stdLogBuild(t, h, stdLogBeside, "--release")
			var calls []ioCall
			for _, c := range []int{16, 1, 16, 16} {
				calls = append(calls, ioCall{env: []string{fmt.Sprintf("MK_CARRIERS=%d", c)}, watch: 60})
			}
			for r, run := range h.runMany(t, exe, calls) {
				if run.code != 0 {
					t.Fatalf("run %d: exit %d (124: the watchdog killed it)", r, run.code)
				}
				lines := stdLogLines(strings.ReplaceAll(run.stderr, "\r\n", "\n"))
				var native, simd [4]int
				for _, l := range lines {
					var kind string
					var k, i int
					if c, _ := fmt.Sscanf(l, "INFO %s %d %d", &kind, &k, &i); c != 3 || k < 0 || k > 3 || l != fmt.Sprintf("INFO %s %d %d", kind, k, i) {
						t.Fatalf("run %d: torn or foreign line %q", r, l)
					}
					switch kind {
					case "native":
						if i != native[k] {
							t.Fatalf("run %d: native task %d wrote record %d after %d", r, k, i, native[k]-1)
						}
						native[k]++
					case "sim":
						if i != simd[k]%20 {
							t.Fatalf("run %d: sim task %d wrote record %d out of order", r, k, i)
						}
						simd[k]++
					default:
						t.Fatalf("run %d: foreign line %q", r, l)
					}
				}
				if native != [4]int{3000, 3000, 3000, 3000} || simd != [4]int{800, 800, 800, 800} {
					t.Fatalf("run %d: records native %v, sim %v; want 3000 and 800 each", r, native, simd)
				}
			}
		})
	}
}

// TestStdLog_CutMatchesModel: messages of seeded random lengths around
// RecordMax and far past it, of one-, two-, three- and four-byte scalars,
// are written by Sink.Log exactly as stdLogFinish writes them, at dev and
// release.
func TestStdLog_CutMatchesModel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(8))
	pieces := []string{"a", "é", "€", "😀"}
	var msgs []string
	for len(msgs) < 120 {
		target := stdLogMax - 30 + rng.Intn(60)
		if len(msgs)%10 == 9 {
			target = 4000 + rng.Intn(9000)
		}
		var sb strings.Builder
		for sb.Len() < target {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		msgs = append(msgs, sb.String())
	}
	var src strings.Builder
	src.WriteString("import std.log\n\nexport Main(sys: &System): WriteError!void {\n    out := log.Stdout(log.Level.Info)\n")
	for _, m := range msgs {
		fmt.Fprintf(&src, "    try out.Info(sys.io, \"{m}\", (m: \"%s\"))\n", m)
	}
	src.WriteString("}\n")
	var want strings.Builder
	for _, m := range msgs {
		want.WriteString(stdLogFinish("INFO " + m))
	}
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			r := runNative(t, buildNative(t, nil, src.String(), mode.flags...), "")
			got := strings.ReplaceAll(r.stdout, "\r\n", "\n")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s", r.code, r.stderr)
			}
			if got != want.String() {
				gl, wl := strings.SplitAfter(got, "\n"), strings.SplitAfter(want.String(), "\n")
				for i := range min(len(gl), len(wl)) {
					if gl[i] != wl[i] {
						t.Fatalf("record %d: got %d bytes ending %q, want %d ending %q", i, len(gl[i]), gl[i][max(0, len(gl[i])-40):], len(wl[i]), wl[i][max(0, len(wl[i])-40):])
					}
				}
				t.Fatalf("%d records, want %d", len(gl), len(wl))
			}
		})
	}
}

// TestStdLog_StdlogFinishModel pins the model itself at its edges, so the
// differential test cannot pass on a model that drifted with the code.
func TestStdLog_StdlogFinishModel(t *testing.T) {
	for _, c := range []struct {
		rec  string
		want string
	}{
		{"", "\n"},
		{strings.Repeat("a", 4095), strings.Repeat("a", 4095) + "\n"},
		{strings.Repeat("a", 4096), strings.Repeat("a", 4078) + " [cut 18 bytes]\n"},
		{"a" + strings.Repeat("é", 3000), "a" + strings.Repeat("é", 2038) + " [cut 1924 bytes]\n"},
		{"a" + strings.Repeat("😀", 2000), "a" + strings.Repeat("😀", 1019) + " [cut 3924 bytes]\n"},
	} {
		if got := stdLogFinish(c.rec); got != c.want || len(got) > stdLogMax {
			t.Errorf("stdLogFinish of %d bytes: %d bytes ending %q, want %q", len(c.rec), len(got), got[max(0, len(got)-24):], c.want[max(0, len(c.want)-24):])
		}
	}
}

// TestStdLog_CompileFacts: the sink is plain, Send and Share, the
// cookbook's "Report from many tasks" block checks clean as written, a
// sink from ToFile holds the mutex's loan (returned past it, E0603's
// family), and a format that does not match its record is E0706 at the
// sink call, as at a Logger call.
func TestStdLog_CompileFacts(t *testing.T) {
	t.Parallel()
	cookbook := stdLogCookbookBlock(t)
	cases := []struct {
		name, src, code string
	}{
		{"cookbook", cookbook, ""},
		{"kinds", `import std.fs
import std.log

NeedAll[T: Plain + Send + Share](x: T) {}

export Main(sys: &System) {
    NeedAll(log.Stderr(log.Level.Info))
    NeedAll(log.Stdout(log.Level.Debug))
    m := Mutex[fs.File].New(fs.Create(sys.io, "k.log") else { return })
    sink := log.ToFile(&m, log.Level.Warn)
    NeedAll(sink)
    copy := sink
    copy.Warn(sys.io, "short form") else {}
    sink.Error(sys.io, "{n} and {s}", (n: 1, s: "two")) else {}
    var f := m.Into()
    f.Close(sys.io) else {
        f.Abandon()
    }
}
`, ""},
		{"loan", `import std.fs
import std.log

export Main(sys: &System) {
    m := Mutex[fs.File].New(fs.Create(sys.io, "l.log") else { return })
    sink := log.ToFile(&m, log.Level.Info)
    var f := m.Into()
    sink.Info(sys.io, "after the mutex moved") else {}
    f.Close(sys.io) else {
        f.Abandon()
    }
}
`, "E05"},
		{"format", `import std.log

export Main(sys: &System) {
    sink := log.Stderr(log.Level.Info)
    sink.Info(sys.io, "x {n}") else {}
}
`, "E0706"},
		{"unused-field", `import std.log

export Main(sys: &System) {
    sink := log.Stderr(log.Level.Info)
    sink.Log(sys.io, log.Level.Warn, "none", (n: 1)) else {}
}
`, "E0706"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, status := cliCheck(t, c.src)
			if c.code == "" {
				if status != 0 {
					t.Fatalf("mk check: exit %d\n%s", status, out)
				}
				return
			}
			if status == 0 || !strings.Contains(out, c.code) {
				t.Fatalf("mk check: exit %d, want %s\n%s", status, c.code, out)
			}
		})
	}
}

// stdLogCookbookBlock is the "Report from many tasks" block of
// doc/cookbook.md, its first two comment lines dropped.
func stdLogCookbookBlock(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "doc", "cookbook.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	k := strings.Index(text, "## Report from many tasks")
	if k < 0 {
		t.Fatal("the cookbook has no \"Report from many tasks\" recipe")
	}
	text = text[k:]
	start := strings.Index(text, "```mk\n")
	end := strings.Index(text[start+6:], "```")
	if start < 0 || end < 0 {
		t.Fatal("the recipe has no mk block")
	}
	block := text[start+6 : start+6+end]
	if !strings.Contains(block, "log.Stderr") {
		t.Fatalf("the recipe's block does not use log.Stderr:\n%s", block)
	}
	return block
}

// TestStdLog_NoUnsafe: std/log holds no unsafe.
func TestStdLog_NoUnsafe(t *testing.T) {
	for _, f := range mkFiles(t, "log") {
		for i, l := range strings.Split(f.text, "\n") {
			if strings.Contains(l, "unsafe") && !strings.HasPrefix(strings.TrimSpace(l), "//") {
				t.Errorf("%s:%d: %s", f.path, i+1, l)
			}
		}
	}
}
