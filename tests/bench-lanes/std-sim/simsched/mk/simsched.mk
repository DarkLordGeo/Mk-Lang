// simsched, Mk, std.sim: n tasks under one Sim, each sleeping 1 ms of
// simulated time and then sending its index to one channel, r times; a
// collector task receives all n * r values. The Sim's scheduler runs one
// task at a time and its clock jumps over the sleeps, so the run takes r
// ms of simulated time (SIM-R21; Go's testing/synctest bubble is the same
// job). Usage: simsched <tasks> <rounds>. Prints the sum of the values
// and the simulated time in ms.

import std.sim
import std.time

Worker(io: Io, ch: &Chan[int], i: int, r: int) {
    for _ in 0..r {
        io.Sleep(time.Millis(1)) else {
            return
        }
        ch.Send(io, i) else {
            return
        }
    }
}

Collector(io: Io, ch: &Chan[int], total: int, res: &Chan[int]) {
    start := time.Now(io)
    var s := 0
    for _ in 0..total {
        v := ch.Recv(io) else {
            return
        }
        s += v
    }
    ms := Cast[int](time.Now(io).Since(start).Nanos() / 1_000_000)
    res.Send(io, s) else {
    }
    res.Send(io, ms) else {
    }
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    r := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 1)
    res := Chan[int].New(a, 2) else {
        ch.Free(a)
        fail OutOfMemory
    }
    var s := sim.Sim.New(sys.arena, 1) else {
        ch.Free(a)
        res.Free(a)
        fail OutOfMemory
    }
    io := s.Io()
    scope sc := io.Scope() {
        sc.go Collector(io, &ch, n * r, &res)
        for i in 0..n {
            sc.go Worker(io, &ch, i, r)
        }
    }
    s.Free()
    sum := res.Recv(sys.io) else {
        ch.Free(a)
        res.Free(a)
        try Print(mut out, "no result\n", ())
        return
    }
    ms := res.Recv(sys.io) else {
        ch.Free(a)
        res.Free(a)
        try Print(mut out, "no result\n", ())
        return
    }
    ch.Free(a)
    res.Free(a)
    try Print(mut out, "sum {sum} simulated {ms} ms\n", (sum, ms))
}
