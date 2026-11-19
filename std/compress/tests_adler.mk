// Why: Adler32State must give the one-shot Adler32 however the input is cut.
// What: test blocks of known sums, the zero-filled state and every cut point.
// How: AdlerPieces feeds the input in pieces; run by
// mk test --std=std.compress, and std/std-compress_adler_test.go checks
// against Go's hash/adler32.

// AdlerPieces absorbs data in pieces of k bytes, with an empty Bytes after
// each, and returns the Sum.
AdlerPieces(data: []u8, k: int): u32 {
    var s := Adler32State.New()
    var i := 0
    for i < data.Len() {
        e := if i + k > data.Len() { data.Len() } else { i + k }
        s.Bytes(data[i..e])
        s.Bytes(data[e..e])
        i = e
    }
    return s.Sum()
}

test "Adler32State: the empty message, abc and Wikipedia" {
    var s := Adler32State.New()
    Assert(s.Sum() == 1)
    s.Bytes("abc".AsBytes())
    Assert(s.Sum() == 0x024d0127)
    var w := Adler32State.New()
    w.Bytes("Wiki".AsBytes())
    w.Bytes("pedia".AsBytes())
    Assert(w.Sum() == 0x11e60398)
}

test "Adler32State: a zero-filled state is the empty message's" (sys: &System) {
    zs := sys.arena.Slice[Adler32State](1).Must()
    Assert(zs[0].Sum() == 1)
    zs[0].Bytes("abc".AsBytes())
    Assert(zs[0].Sum() == 0x024d0127)
}

test "Adler32State: the input whose checksum is 0" (sys: &System) {
    // 63,473 zero bytes, 256 of 0xFF and one of 240 (Go's hash/adler32
    // gives 0).
    n := 63473 + 256 + 1
    buf := sys.arena.Slice[u8](n).Must()
    for i in 63473..63729 {
        buf[i] = 0xFF
    }
    buf[n - 1] = 240
    data := buf[..]
    Assert(Adler32(data) == 0)
    Assert(AdlerPieces(data, 5552) == 0)
    Assert(AdlerPieces(data, 32769) == 0)
}

test "Adler32State: every 997th cut and twelve piece sizes equal Adler32" (sys: &System) {
    // Past three 32 KiB reduction blocks, all bytes 0xFF (the largest
    // sums) for half, a pattern for the rest.
    n := 3 * 32768 + 77
    buf := sys.arena.Slice[u8](n).Must()
    for i in 0..n {
        buf[i] = if i < n / 2 { 0xFF } else { Trunc[u8](i * 7 + i / 251) }
    }
    data := buf[..]
    want := Adler32(data)
    var c := 0
    for c <= n {
        var s := Adler32State.New()
        s.Bytes(data[0..c])
        Assert(s.Sum() == Adler32(data[0..c]))
        s.Bytes(data[c..n])
        Assert(s.Sum() == want)
        c += 997
    }
    ks: [12]int = [1, 7, 8, 9, 31, 32, 33, 1000, 5552, 32768, 32769, 100000]
    for j in 0..12 {
        Assert(AdlerPieces(data, ks[j]) == want)
    }
}
