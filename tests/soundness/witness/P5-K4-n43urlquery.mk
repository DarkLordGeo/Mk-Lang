// verdict: reject E0503
// note: url.ParseQuery of a literal decodes %XX into ua; the value read out of the list
// holds a loan of ua: E0503 at ua.Free. If accepted, a run prints result 48 (freed memory). Mutant M-D791-keep
// accepts it
// The callee half, reached in std with a literal: url.ParseQuery(a, s): List[(name, value)]
// from a decodes %XX into a; the value is read out of the list through Done, ua freed, h
// refilled with X. Expect E0503 at ua.Free.
import std.url

Run(sys: &System): int {
    h := sys.heap
    var ua := Arena.New(h) else {
        return -1
    }
    q := url.ParseQuery(&ua, "k=hello%20arena%21%20this%20value%20lives%20in%20the%20first%20arena") else {
        ua.Free()
        return -2
    }
    ps := q.Done()
    var v := ""
    if ps.Len() > 0 {
        v = ps[0].value
    }
    ua.Free() // <- E0503
    return Refill(h, v.AsBytes())
}

// CountX counts the bytes of b that are 'X' (88).
CountX(b: []u8): int {
    var n := 0
    for i in 0..b.Len() {
        if b[i] == 88 {
            n += 1
        }
    }
    return n
}

// Refill takes a second arena from h, fills 32 blocks of 64 bytes with 'X', and returns
// CountX(e) while it lives: a program reading freed memory through e answers with the X count.
Refill(h: &Heap, e: []u8): int {
    var two := Arena.New(h) else {
        return -40
    }
    for _ in 0..32 {
        fill := two.Slice[u8](64) else {
            two.Free()
            return -50
        }
        for i in 0..fill.Len() {
            fill[i] = 88
        }
    }
    n := CountX(e)
    two.Free()
    return n
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}
