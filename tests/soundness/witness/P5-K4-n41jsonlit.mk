// verdict: reject E0503
// note: json.Parse of a literal whose key holds an escape unescapes the key into ja; the
// key read out of members.Done() holds a loan of ja: E0503 at ja.Free. If accepted, a run prints result 46 (freed memory).
// Mutant M-D791-keep accepts it
// The callee half, reached in std with a literal: json.Parse of a literal text whose key holds
// an escape, so Parse unescapes the key into ja (a view of ja's own memory). The key is read
// straight out of the members list through Done, ja is freed and h refilled with X. Expect
// E0503 at ja.Free; an accept that answers above 0 read freed arena memory.
import std.json

Run(sys: &System): int {
    h := sys.heap
    var ja := Arena.New(h) else {
        return -1
    }
    v := json.Parse(&ja, "{\"hello arena\\u0021 this key lives in the first arena\": 1}") else {
        ja.Free()
        return -2
    }
    var k := ""
    match v {
        .Object(members) {
            ms := members.Done()
            if ms.Len() > 0 {
                k = ms[0].key
            }
        }
        _ {}
    }
    ja.Free() // <- E0503
    return Refill(h, k.AsBytes())
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
