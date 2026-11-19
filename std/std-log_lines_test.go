// Why: a Logger hands each line to its Writer in one write, so over a process
// stream, which takes each write whole under its lock, no other task's log.Sink
// record lands inside a Logger's line.
// What: TestStdLog_LinesBesideSinkRecords and its program logBesideProgram.
// How: 15 tasks write Sink records while Main logs lines, at 16 carriers in dev
// and release; every output line must be one whole line or record, in order.

package std

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// logBesideProgram: the main task logs 4,000 lines through a Logger over
// System.Stdout while 15 tasks write 1,000 records each to log.Stdout.
const logBesideProgram = `import std.log

const Count = 1000

Records(io: Io, k: int, pad: str) {
    sink := log.Stdout(log.Level.Info)
    for i in 0..Count {
        sink.Info(io, "sink t={k} i={i} {pad}", (k, i, pad)) else {
            return
        }
    }
}

export Main(sys: &System): OutOfMemory!void {
    var t := Text.New(sys.arena)
    for _ in 0..100 {
        try t.Push("x")
    }
    pad := t.Done()
    var out := sys.Stdout()
    var l := log.Logger.New(mut out, log.Level.Info)
    scope s := sys.io.Scope() {
        for k in 1..16 {
            s.go Records(sys.io, k, pad)
        }
        for i in 0..Count * 4 {
            l.Info("logger i={i} a={a} b={b} {pad} end", (i, a: i * 3, b: "word", pad)) else {}
        }
    }
}
`

var logLinesLoggerRe = regexp.MustCompile(`^INFO logger i=([0-9]+) a=([0-9]+) b=word x{100} end$`)
var logLinesSinkRe = regexp.MustCompile(`^INFO sink t=([0-9]+) i=([0-9]+) x{100}$`)

// TestStdLog_LinesBesideSinkRecords: at 16 carriers, dev and release,
// every line of standard output is a whole Logger line or a whole Sink
// record, the Logger's in order and each task's records in order.
func TestStdLog_LinesBesideSinkRecords(t *testing.T) {
	t.Parallel()
	for _, mode := range cliModes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			exe := buildNative(t, nil, logBesideProgram, mode.flags...)
			r := runNativeEnv(t, exe, ioCall{env: []string{"MK_CARRIERS=16"}, watch: 120})
			if r.code != 0 {
				t.Fatalf("exit %d\nstderr:\n%.2000s", r.code, r.stderr)
			}
			lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			next := map[int]int{}
			logged := 0
			for _, l := range lines {
				if m := logLinesLoggerRe.FindStringSubmatch(l); m != nil {
					i, _ := strconv.Atoi(m[1])
					a, _ := strconv.Atoi(m[2])
					if i != logged || a != 3*i {
						t.Fatalf("Logger line %q out of order (want i=%d)", l, logged)
					}
					logged++
					continue
				}
				m := logLinesSinkRe.FindStringSubmatch(l)
				if m == nil {
					t.Fatalf("a line that is neither a whole Logger line nor a whole record: %.200q", l)
				}
				k, _ := strconv.Atoi(m[1])
				i, _ := strconv.Atoi(m[2])
				if i != next[k] {
					t.Fatalf("record t=%d i=%d out of order (want i=%d)", k, i, next[k])
				}
				next[k]++
			}
			if logged != 4000 || len(next) != 15 {
				t.Fatalf("%d Logger lines, records from %d tasks", logged, len(next))
			}
			for k, n := range next {
				if n != 1000 {
					t.Errorf("task %d: %d records", k, n)
				}
			}
		})
	}
}
