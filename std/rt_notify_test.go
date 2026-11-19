// Why: a Notify.Wait cut by a deadline must take nothing, so a later Wait
// still sees the next NotifyOne; the test needs both std.sim and std.sync.
// What: TestRt_NotifyWaitSim, a program importing both (std.sync imports
// std.sim, so a block in either cannot).
// How: in a Sim, a 1 ms deadline ends the Wait Canceled with Expired true; the
// next Wait returns at a NotifyOne at 5 ms; 20 seeds, dev and release.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"fmt"
	"strings"
	"testing"
)

const rt8NotifySrc = `import std.atomic
import std.sim
import std.sync
import std.time

// Waiter waits under a 1 ms deadline, then waits again with none; out is 1
// when the first wait was Canceled by the deadline and the second returned
// at the notify, else the first check that failed (2 to 5).
Waiter(io: Io, n: &sync.Notify, out: &atomic.AtomicInt) {
    t0 := time.Now(io)
    var cut := false
    var exp := false
    scope d := io.Scope() {
        d.CancelAfter(time.Millis(1))
        n.Wait(io) else {
            cut = true
        }
        exp = d.Expired()
    }
    if !cut {
        out.Store(2)
        return
    }
    if !exp {
        out.Store(3)
        return
    }
    n.Wait(io) else {
        out.Store(4)
        return
    }
    at := time.Now(io).Since(t0).Nanos()
    if at < 5_000_000 || at > 10_000_000 {
        out.Store(5)
        return
    }
    out.Store(1)
}

Notifier(io: Io, n: &sync.Notify) {
    io.Sleep(time.Millis(5)) else {}
    n.NotifyOne()
}

export Main(sys: &System): OutOfMemory!void {
    for seed in 0..20 {
        var w := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := w.Io()
        n := sync.Notify.New()
        out := atomic.AtomicInt.New(0)
        scope s := io.Scope() {
            s.go Waiter(io, &n, &out)
            s.go Notifier(io, &n)
        }
        w.Free()
        sys.Print("seed {seed} {v}\n", (seed: seed, v: out.Load())) else {}
    }
}
`

func TestRt_NotifyWaitSim(t *testing.T) {
	for _, flags := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, rt8NotifySrc, flags...)
		r := runNative(t, exe, "")
		if r.code != 0 {
			t.Fatalf("%v: exit %d\n%s%s", flags, r.code, r.stdout, r.stderr)
		}
		for seed := 0; seed < 20; seed++ {
			if want := fmt.Sprintf("seed %d 1\n", seed); !strings.Contains(strings.ReplaceAll(r.stdout, "\r\n", "\n"), want) {
				t.Errorf("%v: no %q in\n%s", flags, want, r.stdout)
			}
		}
	}
}
