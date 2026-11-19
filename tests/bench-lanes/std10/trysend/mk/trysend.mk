// trysend, Mk: Chan[i64].TrySend on one task, no waiter (README.md): reps
// rounds, each sending values without waiting into a channel of capacity
// 1,024 until it is full and then 16 more, each of which TrySend gives back,
// then draining the 1,024 with Recv. The checksum adds every value received
// and every value given back, with wrapping adds.
// Usage: trysend <reps>.

type ChanError = Canceled | Closed

export Main(sys: &System): ChanError|OutOfMemory|ParseError|WriteError!void {
    reps := try sys.args[1].ParseInt()
    var c := try Chan[i64].New(sys.arena.Alloc(), 1024)
    var acc: i64 = 0
    var v: i64 = 0
    for _ in 0..reps {
        for _ in 0..1040 {
            v = v +% 1
            r := try c.TrySend(v)
            if x := r {
                acc = acc +% x
            }
        }
        for _ in 0..1024 {
            acc = acc +% try c.Recv(sys.io)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{acc}\n", (acc))
}
