// chan8, Mk: 8 sender tasks each send 0..n-1 on one Chan[i64] of capacity
// c, and 8 receiver tasks sum what they receive until it is closed; prints
// the total (8 * n * (n - 1) / 2). The contended channel of
// std/std-sync_stress_test.go (syncBenchContended's two Chan programs) as a
// tools/bench row: run at 8 carriers, capacity 64 (row chan8) and 0 (row
// chan8x0). Usage: chan8 <n> <c>.

import std.atomic

type ChanError = Canceled | Closed

Sender(io: Io, c: &Chan[i64], n: i64) {
    for i in 0..n {
        c.Send(io, i) else {
            return
        }
    }
}

Receiver(io: Io, c: &Chan[i64], total: &atomic.AtomicInt) {
    var k: i64 = 0
    for {
        x := c.Recv(io) else {
            break
        }
        k += x
    }
    _ = total.Add(k)
}

export Main(sys: &System): ChanError|OutOfMemory|ParseError|WriteError!void {
    n := try sys.args[1].ParseInt()
    cap := try sys.args[2].ParseInt()
    c := try Chan[i64].New(sys.arena.Alloc(), Trunc[int](cap))
    total := atomic.AtomicInt.New(0)
    scope r := sys.io.Scope() {
        for _ in 0..8 {
            r.go Receiver(sys.io, &c, &total)
        }
        scope s := sys.io.Scope() {
            for _ in 0..8 {
                s.go Sender(sys.io, &c, n)
            }
        }
        c.Close()
    }
    var out := sys.Stdout()
    try Print(mut out, "{t}\n", (t: total.Load()))
}
