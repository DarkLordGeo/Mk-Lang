// runtime.Blocking cancelled while its job waits in the pool's queue (the rule units
// are tests/conformance/rules/D934_*): 64 Blocking jobs hold
// the pool's 64 threads (std/task/pool.mk's MaxPoolThreads), each spinning on a lock Main holds,
// so a 65th job queues. Cancelling the 65th caller's scope gives Canceled and the caller still
// owns its state (a linear list of 3, freed on the error edge), and the 65th job never runs. Then
// Main lets the 64 go, they finish, and a new Blocking call runs and consumes its state. Main does
// no I/O while the pool is held (the pool is shared with std's own blocking work), so it prints
// at the end. The output is the same at any carrier count. Expected output: blockingcancel.out.
import std.atomic
import std.runtime
import std.time

// Gate is the lock the 64 holding jobs spin on while Main holds it. Started counts the holding
// jobs that are in their call; Ran counts the runs of the 65th job's function.
var Gate: atomic.Spin[int] = atomic.Spin[int].New(0)
var Started: atomic.AtomicInt = atomic.AtomicInt.New(0)
var Ran: atomic.AtomicInt = atomic.AtomicInt.New(0)

// Hold runs on a pool thread and holds it until Main releases Gate.
Hold(x: int): int {
    _ = Started.Add(1)
    g := Gate.Lock()
    g.Unlock()
    return x + 1
}

// Holder is one task whose Blocking call holds a pool thread; done counts the calls that returned.
Holder(io: Io, done: &atomic.AtomicInt) {
    r := runtime.Blocking(io, 1, Hold) else {
        return
    }
    _ = done.Add(Cast[i64](r - 1))
}

// Grow is the 65th job's function, and the one after the release: it counts its run and gives the
// list back.
Grow(xs: HeapList[int]): HeapList[int] {
    _ = Ran.Add(1)
    return xs
}

// Queued makes a list of n and runs Grow on it through Blocking. It answers the list's length
// when the job ran and consumed it, and 1000 plus the length of the list it still owns when the
// call was Canceled.
Queued(io: Io, h: &Heap, n: int, out: &atomic.AtomicInt) {
    var xs := HeapList[int].New(h.Alloc())
    for i in 0..n {
        xs.Push(h.Alloc(), i) else {}
    }
    ys := runtime.Blocking(io, xs, Grow) else {
        out.Store(Cast[i64](1000 + xs.Len()))
        xs.Free(h.Alloc())
        return
    }
    out.Store(Cast[i64](ys.Len()))
    ys.Free(h.Alloc())
}

export Main(sys: &System): WriteError!void {
    done := atomic.AtomicInt.New(0)
    queued := atomic.AtomicInt.New(-1)
    after := atomic.AtomicInt.New(-1)
    var started: i64 = 0
    var ran_while_queued: i64 = -1
    g := Gate.Lock()
    scope s := sys.io.Scope() {
        for _ in 0..64 {
            s.go Holder(sys.io, &done)
        }
        for Started.Load() < 64 {
            sys.io.Sleep(time.Millis(1)) else {}
        }
        started = Started.Load()
        scope c := sys.io.Scope() {
            c.go Queued(sys.io, sys.heap, 3, &queued)
            sys.io.Sleep(time.Millis(20)) else {}
            c.Cancel()
        }
        ran_while_queued = Ran.Load()
        g.Unlock()
    }
    scope s := sys.io.Scope() {
        s.go Queued(sys.io, sys.heap, 4, &after)
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} Blocking jobs held the pool's 64 threads\n", (n: started))
    q := queued.Load()
    if q >= 1000 {
        try Print(mut out, "the 65th job, queued, was cancelled: Canceled, and its caller still owns its list of {k}\n", (k: q - 1000))
    } else {
        try Print(mut out, "the 65th job was not cancelled: it ran on a list of {k}\n", (k: q))
    }
    try Print(mut out, "the 65th job's function ran {r} times while it was queued\n", (r: ran_while_queued))
    try Print(mut out, "after the release {d} held jobs finished\n", (d: done.Load()))
    try Print(mut out, "a new Blocking call ran and consumed its list of {k}\n", (k: after.Load()))
}
