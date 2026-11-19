// Atomic[T].Wait and Wake, and runtime.Waker, with Wait and Wake on the
// operating system's native word wait. Each part prints only
// what holds at any carrier count and any interleaving, so the output is the same at
// MK_CARRIERS=1 and 16, in dev and release (linux.sh runs both counts on Linux):
// - a ping-pong of 1,000 rounds on one u32 word, each side waiting for its turn;
// - Wake(true) releasing 8 tasks that wait on one word;
// - a Wait on a word nobody changes, ended by its scope's cancel with Canceled;
// - a Waker's Wake given before its Wait, kept, so the Wait returns at once;
// - an i32 word holding -1, which compares bits.
// Expected output: wakeword.out.
import std.atomic
import std.runtime

// Side plays one side of the ping-pong: it waits until the word holds me, counts a round, and
// hands the turn over, n times. Wait may return early, so it rereads the word in a loop.
Side(io: Io, turn: &atomic.Atomic[u32], me: u32, n: int, rounds: &atomic.Atomic[i64]) {
    for _ in 0..n {
        for turn.Load(atomic.Acquire) != me {
            turn.Wait(io, 1 - me) else {
                return
            }
        }
        _ = rounds.FetchAdd(1, atomic.Relaxed)
        turn.Store(1 - me, atomic.Release)
        turn.Wake(false)
    }
}

// WaitFor parks until the word is not 0, then counts itself into seen.
WaitFor(io: Io, a: &atomic.Atomic[u32], parked: &atomic.Atomic[i64], seen: &atomic.Atomic[i64]) {
    _ = parked.FetchAdd(1, atomic.Relaxed)
    for a.Load(atomic.Acquire) == 0 {
        a.Wait(io, 0) else {
            return
        }
    }
    _ = seen.FetchAdd(1, atomic.Relaxed)
}

// WaitCanceled waits on a word nobody changes, so only its scope's cancel ends the wait.
WaitCanceled(io: Io, a: &atomic.Atomic[u32], started: &atomic.Atomic[i64], out: &atomic.Atomic[i64]) {
    _ = started.FetchAdd(1, atomic.Relaxed)
    for {
        a.Wait(io, 0) else {
            _ = out.FetchAdd(1, atomic.Relaxed)
            return
        }
    }
}

// WaitNeg parks while the i32 word holds -1 (0xFFFFFFFF), then counts itself into seen.
WaitNeg(io: Io, a: &atomic.Atomic[i32], seen: &atomic.Atomic[i64]) {
    for a.Load(atomic.Acquire) == -1 {
        a.Wait(io, -1) else {
            return
        }
    }
    _ = seen.FetchAdd(1, atomic.Relaxed)
}

// Settle yields until n tasks have counted themselves into c, then a while longer, so they are
// parked in Wait or about to be; the result does not depend on whether they are.
Settle(io: Io, c: &atomic.Atomic[i64], n: i64) {
    for c.Load(atomic.Acquire) < n {
        io.Yield() else {}
    }
    for _ in 0..64 {
        io.Yield() else {}
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()

    turn := atomic.Atomic[u32].New(0)
    rounds := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go Side(sys.io, &turn, 0, 1000, &rounds)
        s.go Side(sys.io, &turn, 1, 1000, &rounds)
    }
    try Print(mut out, "ping-pong: {r} turns, word ends at {w}\n", (r: rounds.Load(atomic.SeqCst), w: turn.Load(atomic.SeqCst)))

    a := atomic.Atomic[u32].New(0)
    parked := atomic.Atomic[i64].New(0)
    seen := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go WaitFor(sys.io, &a, &parked, &seen)
        }
        Settle(sys.io, &parked, 8)
        a.Store(1, atomic.Release)
        a.Wake(true)
    }
    try Print(mut out, "Wake(true): {n} of 8 waiters released\n", (n: seen.Load(atomic.SeqCst)))

    b := atomic.Atomic[u32].New(0)
    started := atomic.Atomic[i64].New(0)
    canceled := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go WaitCanceled(sys.io, &b, &started, &canceled)
        s.go WaitCanceled(sys.io, &b, &started, &canceled)
        Settle(sys.io, &started, 2)
        s.Cancel()
    }
    try Print(mut out, "cancel: {n} of 2 waits ended with Canceled, word still {w}\n", (n: canceled.Load(atomic.SeqCst), w: b.Load(atomic.SeqCst)))

    k := runtime.Waker.New()
    k.Wake()
    var kept := "returned at once"
    k.Wait(sys.io) else {
        kept = "canceled"
    }
    try Print(mut out, "Waker: a Wake before the Wait is kept; the Wait {kept}\n", (kept: kept))

    m := atomic.Atomic[i32].New(-1)
    negs := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go WaitNeg(sys.io, &m, &negs)
        s.go WaitNeg(sys.io, &m, &negs)
        for _ in 0..64 {
            sys.io.Yield() else {}
        }
        m.Store(5, atomic.Release)
        m.Wake(true)
    }
    try Print(mut out, "i32 word: {n} of 2 waiters on -1 released after a store of {v}\n", (n: negs.Load(atomic.SeqCst), v: m.Load(atomic.SeqCst)))
}
