// Why: under one Sim, tasks run one at a time in an order drawn from the seed, so
// one seed must give one interleaving and one output in every process.
// What: TestSTDSIM_SchedSameSeedSameInterleaving.
// How: runs several tasks under a Sim in separate processes and compares outputs:
// equal for one seed, different across seeds.

package std

import (
	"strings"
	"testing"
)

// simSchedProgram: for each of 24 seeds, main spawns one root task under
// the Sim's Io; the root spawns four workers that send ten values each
// through a channel of capacity 2, sleeping or yielding between sends,
// while the root receives all 40 and logs each with the simulated clock.
// main prints one line per seed: the values in the order they arrived,
// and the simulated time the root took.
const simSchedProgram = `import std.sim
import std.time

Worker(io: Io, ch: &Chan[int], i: int) {
    for j in 0..10 {
        ch.Send(io, i * 100 + j) else {
            return
        }
        if (i + j) % 3 == 0 {
            io.Sleep(time.Millis(Cast[i64](i + 1))) else {
                return
            }
        } else {
            io.Yield() else {
                return
            }
        }
    }
}

Root(io: Io, ch: &Chan[int], log: &Chan[int]) {
    t0 := time.Now(io)
    scope s := io.Scope() {
        for i in 0..4 {
            s.go Worker(io, ch, i)
        }
        for _ in 0..40 {
            v := ch.Recv(io) else {
                break
            }
            log.Send(io, v) else {
                break
            }
        }
    }
    log.Send(io, Cast[int](time.Now(io).Since(t0).Nanos())) else {
    }
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    for seed in 0..24 {
        ch := try Chan[int].New(sys.arena.Alloc(), 2)
        log := Chan[int].New(sys.arena.Alloc(), 64) else {
            ch.Free(sys.arena.Alloc())
            fail OutOfMemory
        }
        var w := sim.Sim.New(sys.arena, Cast[u64](seed)) else {
            ch.Free(sys.arena.Alloc())
            log.Free(sys.arena.Alloc())
            fail OutOfMemory
        }
        io := w.Io()
        scope sc := io.Scope() {
            sc.go Root(io, &ch, &log)
        }
        w.Free()
        log.Close()
        var n := 0
        for {
            v := log.Recv(sys.io) else {
                break
            }
            if n == 40 {
                try Print(mut out, "n={n} t={v}\n", (n: n, v: v))
            } else {
                try Print(mut out, "{v} ", (v))
            }
            n += 1
        }
        ch.Free(sys.arena.Alloc())
        log.Free(sys.arena.Alloc())
    }
}
`

// TestSTDSIM_SchedSameSeedSameInterleaving: the program above, run in
// three processes (dev, then release twice), prints the same 24 lines;
// every line holds all 40 values, and the seeds give at least 6 distinct
// interleavings.
func TestSTDSIM_SchedSameSeedSameInterleaving(t *testing.T) {
	t.Parallel()
	dev := buildNative(t, nil, simSchedProgram)
	rel := buildNative(t, nil, simSchedProgram, "--release")
	runs := []nativeRun{runNative(t, dev, ""), runNative(t, rel, ""), runNative(t, rel, "")}
	for _, r := range runs {
		if r.code != 0 {
			t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
		}
	}
	if runs[0].stdout != runs[1].stdout || runs[1].stdout != runs[2].stdout {
		t.Fatalf("one seed gave two interleavings:\n%s\n---\n%s\n---\n%s", runs[0].stdout, runs[1].stdout, runs[2].stdout)
	}
	lines := strings.Split(strings.TrimSpace(runs[0].stdout), "\n")
	if len(lines) != 24 {
		t.Fatalf("got %d lines, want 24:\n%s", len(lines), runs[0].stdout)
	}
	distinct := map[string]bool{}
	for i, l := range lines {
		if !strings.Contains(l, "n=40 t=") {
			t.Errorf("seed %d: not every value arrived: %s", i, l)
		}
		distinct[l[:strings.Index(l, "n=")]] = true
	}
	if len(distinct) < 6 {
		t.Errorf("24 seeds gave %d interleavings, want at least 6:\n%s", len(distinct), runs[0].stdout)
	}
	t.Logf("24 seeds, %d distinct interleavings; seed 0: %s", len(distinct), lines[0])
}
