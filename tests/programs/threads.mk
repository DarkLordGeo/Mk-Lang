// runtime.Dedicated, Thread and Joined.Join:
// what a program sees through the public API.
// - Dedicated: 32 tasks spawned from a scope opened on a dedicated Io run on its one carrier
//   thread, so no two of them are ever in their busy loop at once, while 8 tasks on the shared
//   carriers run beside them.
// - Thread: f runs on a carrier of its own and spawns 8 tasks on the Io it is given; Join returns
//   once f and all 8 have ended.
// - Join in a cancelled scope gives Canceled and the Joined stays the caller's; once the Thread's
//   f is let go, a Join on a NoCancel Io waits for its end.
// The output is the same at any carrier count: 1 carrier never overlaps tasks either, so the
// Dedicated part checks its claim at 16 carriers (linux.sh runs both counts on Linux). Expected
// output: threads.out.
import std.atomic
import std.runtime
import std.time

// In counts the dedicated tasks inside their busy loop; Overlap counts the times one saw another
// there. Ran counts the dedicated tasks that ran.
var In: atomic.Atomic[i64] = atomic.Atomic[i64].New(0)
var Overlap: atomic.Atomic[i64] = atomic.Atomic[i64].New(0)
var Ran: atomic.Atomic[i64] = atomic.Atomic[i64].New(0)

// Busy is a dedicated task: it computes for a while without suspending and counts the times
// another dedicated task was in its loop at once.
Busy(io: Io) {
    if In.FetchAdd(1, atomic.SeqCst) != 0 {
        _ = Overlap.FetchAdd(1, atomic.SeqCst)
    }
    var x: u64 = 1
    for i in 0..20000 {
        x = x *% 6364136223846793005 +% Trunc[u64](i)
        if In.Load(atomic.SeqCst) != 1 {
            _ = Overlap.FetchAdd(1, atomic.SeqCst)
        }
    }
    _ = In.FetchAdd(-1, atomic.SeqCst)
    _ = Ran.FetchAdd(1, atomic.SeqCst)
    if x == 0 {
        _ = Overlap.FetchAdd(1000000, atomic.SeqCst)
    }
    io.Yield() else {}
}

// Shared runs on the shared carriers beside the dedicated tasks.
Shared(io: Io, n: &atomic.Atomic[i64]) {
    for _ in 0..50 {
        io.Yield() else {}
    }
    _ = n.FetchAdd(1, atomic.SeqCst)
}

// Sum is what the Thread's tasks added.
var Sum: atomic.Atomic[i64] = atomic.Atomic[i64].New(0)

// Child is one task of the Thread's f: it yields and sleeps on the Thread's carrier, then adds j.
Child(io: Io, j: int) {
    for _ in 0..8 {
        io.Yield() else {}
    }
    io.Sleep(time.Millis(1)) else {}
    _ = Sum.FetchAdd(Cast[i64](j), atomic.SeqCst)
}

// Body is a Thread's f: it spawns 8 tasks on its Io, which run on its carrier.
Body(io: Io) {
    scope s := io.Scope() {
        for j in 1..9 {
            s.go Child(io, j)
        }
    }
}

// Gate holds Hold until Main opens it.
var Gate: atomic.Atomic[u32] = atomic.Atomic[u32].New(0)

Hold(io: Io) {
    for Gate.Load(atomic.SeqCst) == 0 {
        Gate.Wait(io, 0) else {}
    }
}

// Joins joins j, retrying a cancelled Join with a NoCancel Io; it answers how many Joins were
// Canceled.
Joins(j: runtime.Joined, io: Io): int {
    j.Join(io) else {
        return 1 + Joins(j, io.NoCancel())
    }
    return 0
}

// CancelJoin joins j in a scope cancelled at once: Canceled with the Joined kept. Then it lets
// Hold go and joins again on a NoCancel Io. It answers the Canceled Joins.
CancelJoin(j: runtime.Joined, io: Io): int {
    var canceled := 0
    scope s := io.Scope() {
        s.Cancel()
        j.Join(io) else {
            canceled = 1
            Gate.Store(1, atomic.SeqCst)
            Gate.Wake(true)
            canceled += Joins(j, io.NoCancel())
        }
    }
    return canceled
}

export Main(sys: &System): WriteError!void {
    d := runtime.Dedicated(sys.io)
    shared := atomic.Atomic[i64].New(0)
    scope sh := sys.io.Scope() {
        for _ in 0..8 {
            sh.go Shared(sys.io, &shared)
        }
        scope s := d.Scope() {
            for _ in 0..32 {
                s.go Busy(d)
            }
        }
    }

    t := runtime.Thread(sys.io, Body)
    c1 := Joins(t, sys.io)
    after := Sum.Load(atomic.SeqCst)

    c2 := CancelJoin(runtime.Thread(sys.io, Hold), sys.io)

    var out := sys.Stdout()
    try Print(mut out, "Dedicated: {n} tasks ran on its carrier, {o} times two were in their loop at once; {s} shared tasks ran beside them\n", (n: Ran.Load(atomic.SeqCst), o: Overlap.Load(atomic.SeqCst), s: shared.Load(atomic.SeqCst)))
    try Print(mut out, "Thread: Join returned after f and its 8 tasks ended, which added {a} (want 36); Joins canceled {c}\n", (a: after, c: c1))
    try Print(mut out, "Join in a cancelled scope: Canceled {c} time, the Joined kept, and a NoCancel Join waited for the end\n", (c: c2))
}
