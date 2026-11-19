// Why: a Sim run is only reproducible if its interleaving depends on the seed
// alone, even when the main task spawns tasks directly (no root task).
// What: TestSTDSIM_MainSpawnsDirect.
// How: with std.task's birth, end and spawner-note facts, one seed must give one
// output in every process, and different seeds different ones.

package std

import (
	"strings"
	"testing"
)

// simDirectProgram: for each of 16 seeds, main spawns a collector and
// four workers directly under the Sim's Io; the workers send ten values
// each through a channel of capacity 2, sleeping or yielding between
// sends, and the collector logs each value it receives. main prints one
// line per seed: the values in arrival order, then the simulated time.
const simDirectProgram = `import std.sim
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

Collect(io: Io, ch: &Chan[int], log: &Chan[int]) {
    t0 := time.Now(io)
    for _ in 0..40 {
        v := ch.Recv(io) else {
            break
        }
        log.Send(io, v) else {
            break
        }
    }
    log.Send(io, Cast[int](time.Now(io).Since(t0).Nanos())) else {
    }
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    for seed in 0..16 {
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
            sc.go Collect(io, &ch, &log)
            for i in 0..4 {
                sc.go Worker(io, &ch, i)
            }
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

// TestSTDSIM_MainSpawnsDirect: the program above, run in three
// processes (dev, then release twice), prints the same 16 lines; every
// line holds all 40 values, and the seeds give at least 4 interleavings.
func TestSTDSIM_MainSpawnsDirect(t *testing.T) {
	t.Parallel()
	dev := buildNative(t, nil, simDirectProgram)
	rel := buildNative(t, nil, simDirectProgram, "--release")
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
	if len(lines) != 16 {
		t.Fatalf("got %d lines, want 16:\n%s", len(lines), runs[0].stdout)
	}
	distinct := map[string]bool{}
	for i, l := range lines {
		k := strings.Index(l, "n=40 t=")
		if k < 0 {
			t.Errorf("seed %d: not every value arrived: %s", i, l)
			continue
		}
		distinct[l[:k]] = true
	}
	if len(distinct) < 4 {
		t.Errorf("16 seeds gave %d interleavings, want at least 4:\n%s", len(distinct), runs[0].stdout)
	}
	t.Logf("16 seeds, %d distinct interleavings; seed 0: %s", len(distinct), lines[0])
}
