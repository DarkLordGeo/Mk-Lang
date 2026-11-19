// Task switch (a target of at most 50 ns per switch): two tasks
// alternate through two channels of capacity 0 for n round trips. A round trip is four switches
// (ping to pong and back, each a rendezvous that parks one task and readies the other), so the
// figure is time / (4 x n). Usage: pingpong <n> [time]. Without "time" it prints only
// `pingpong <n> sum <s>`, the sum of the values pong returned (n * (n + 1) / 2 + n), so the
// .cases file can check it; with "time" it also prints `ns <total> per-switch <total / (4n)>`
// for a timing measurement at n = 10,000,000 (a nondeterministic line, never in a case).
// Run with MK_CARRIERS=1 for the single-carrier switch the budget names.
import std.time

Pong(io: Io, ping: &Chan[int], pong: &Chan[int], n: int) {
    for _ in 0..n {
        x := ping.Recv(io) else {
            return
        }
        pong.Send(io, x + 1) else {
            return
        }
    }
}

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    timed := sys.args.Len() > 2 && sys.args[2] == "time"
    a := sys.heap.Alloc()
    ping := try Chan[int].New(a, 0)
    pong := try Chan[int].New(a, 0)
    var sum := 0
    t0 := time.Now(sys.io)
    scope s := sys.io.Scope() {
        s.go Pong(sys.io, &ping, &pong, n)
        for i in 0..n {
            ping.Send(sys.io, i + 1) else {
                break
            }
            y := pong.Recv(sys.io) else {
                break
            }
            sum += y
        }
    }
    t1 := time.Now(sys.io)
    ping.Free(a)
    pong.Free(a)
    var out := sys.Stdout()
    try Print(mut out, "pingpong {n} sum {sum}\n", (n, sum))
    if timed {
        ns := t1.Since(t0).Nanos()
        try Print(mut out, "ns {ns} per-switch {per}\n", (ns, per: ns / i64(4 * n)))
    }
}
