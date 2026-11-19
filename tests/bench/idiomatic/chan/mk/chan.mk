// chan, Mk, idiomatic std: the task of ../README.md with Mk's tasks and two
// Chan[i64] of capacity 1: Ping sends 0..n-1 on a and waits for each to come
// back on b; Pong receives on a, adds the value to its sum and sends it back
// on b. Both run in one scope on MK_CARRIERS carriers (tools/bench sets it
// to the row's thread count). Usage: chan <n>. Prints the checksum.

type ChanError = Canceled | Closed

Ping(io: Io, a: &Chan[i64], b: &Chan[i64], n: i64, sum: mut i64): ChanError!void {
    for i in 0..n {
        try a.Send(io, i)
        *sum += try b.Recv(io)
    }
}

Pong(io: Io, a: &Chan[i64], b: &Chan[i64], n: i64): ChanError!void {
    for _ in 0..n {
        x := try a.Recv(io)
        try b.Send(io, x)
    }
}

export Main(sys: &System): ChanError|OutOfMemory|ParseError|WriteError!void {
    n := try sys.args[1].ParseInt()
    var a := try Chan[i64].New(sys.arena.Alloc(), 1)
    var b := try Chan[i64].New(sys.arena.Alloc(), 1)
    var sum: i64 = 0
    try scope s := sys.io.ScopeOf[ChanError]() {
        s.go Ping(sys.io, &a, &b, n, mut sum)
        s.go Pong(sys.io, &a, &b, n)
    }
    a.Free(sys.arena.Alloc())
    b.Free(sys.arena.Alloc())
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
