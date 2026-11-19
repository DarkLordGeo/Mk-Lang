// selidle, Mk: one producer sends 0..n-1 on a Chan[i64] of capacity 64,
// which it then closes, and one consumer sums what it receives (most Sends
// buffer: the path that notifies Selects), while k other tasks sit parked
// in sync.Select over two channels nobody uses until the end (README.md).
// It measures what a parked Select costs channel traffic that has nothing
// to do with it (G-MISS-R3, C-STDMISS-CONC-2). After the stream, quit is closed, which releases
// every Select. Usage: selidle <n> <k>. Prints sum + k.

import std.atomic
import std.sync
import std.time

type ChanError = Canceled | Closed

Produce(io: Io, c: &Chan[i64], n: i64): ChanError!void {
    for i in 0..n {
        try c.Send(io, i)
    }
    c.Close()
}

Consume(io: Io, c: &Chan[i64], sum: mut i64): ChanError!void {
    for {
        x := c.Recv(io) else e {
            match e {
                Closed {
                    return
                }
                Canceled {
                    fail Canceled
                }
            }
        }
        *sum += x
    }
}

// quit is field 1 in byte order (idle, quit).
Idle(io: Io, idle: &Chan[i64], quit: &Chan[i64], got: &atomic.AtomicInt) {
    i := sync.Select(io, (idle: idle, quit: quit)) else {
        return
    }
    _ = got.Add(i64(i))
}

export Main(sys: &System): ChanError|OutOfMemory|ParseError|WriteError!void {
    n := try sys.args[1].ParseInt()
    k := try sys.args[2].ParseInt()
    al := sys.arena.Alloc()
    var c := try Chan[i64].New(al, 64)
    var idle := try Chan[i64].New(al, 0)
    var quit := try Chan[i64].New(al, 0)
    got := atomic.AtomicInt.New(0)
    var sum: i64 = 0
    scope w := sys.io.Scope() {
        for _ in 0..k {
            w.go Idle(sys.io, &idle, &quit, &got)
        }
        // Let the k tasks reach their wait (Go's column sleeps the same).
        sys.io.Sleep(time.Millis(20)) else {}
        try scope s := sys.io.ScopeOf[ChanError]() {
            s.go Produce(sys.io, &c, n)
            s.go Consume(sys.io, &c, mut sum)
        }
        quit.Close()
    }
    c.Free(al)
    idle.Free(al)
    quit.Free(al)
    var out := sys.Stdout()
    try Print(mut out, "{t}\n", (t: sum + got.Load()))
}
