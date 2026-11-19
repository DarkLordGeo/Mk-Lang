// Why: std.testing must behave in a plain test run and parse its inputs.
// What: test blocks of Fuzz, Bench, the input file's records and Mutate,
// run by `mk test --std=std.testing`.
// How: mk test clears the MK_TESTING_ variables, so Fuzz calls nothing and
// Bench calls f(1); internal/driver/fuzz_test.go runs both modes.

import std.rt

NeverCalled(b: []u8) {
    _ = b
    Panic("a fuzz target ran with no corpus")
}

OnlyOne(n: int) {
    Assert(n == 1)
}

test "Testing Fuzz with no corpus calls nothing, Bench calls f(1)" {
    Fuzz(NeverCalled)
    Bench(OnlyOne)
}

test "Testing Decimal and PutInt" {
    d := Decimal("12 345".AsBytes(), 0)
    Assert(d.n == 12 && d.end == 2)
    Assert(Decimal("12 345".AsBytes(), 3).n == 345)
    Assert(Decimal("x".AsBytes(), 0).n == 0)
    var line: [32]u8 = [0; 32]
    w := PutInt(mut line[..], 0, 9_007_199_254_740_993)
    Assert(str.From(line[0..w]).Must() == "9007199254740993")
    Assert(PutInt(mut line[..], 0, 0) == 1 && line[0] == 48)
}

test "Testing the input file's records" {
    // 's' "ab" [1, 2, 3], then 'd' "" [9].
    b := [b's', 2, 0, 0, 0, b'a', b'b', 3, 0, 0, 0, 1, 2, 3, b'd', 0, 0, 0, 0, 1, 0, 0, 0, 9]
    Assert(DataAt(b[..], 0) == 11 && RecordEnd(b[..], 0) == 14)
    Assert(Nth(b[..], b'd', 0) == 14 && Nth(b[..], b's', 1) == -1)
    var buf: [8]u8 = [0; 8]
    Assert(CopySeed(b[..], 0, mut buf[..]) == 3 && buf[2] == 3)
}

test "Testing Mutate keeps the input within MaxLen and its bounds" {
    b := [b's', 0, 0, 0, 0, 2, 0, 0, 0, 7, 8, b'd', 0, 0, 0, 0, 3, 0, 0, 0, 1, 2, 3]
    var a := Arena.New(rt.ProcessHeap()).Must()
    buf := a.Slice[u8](MaxLen).Must()
    var n := 0
    var r: u64 = 1
    for _ in 0..20_000 {
        r = Step(r)
        Assert(r != 0)
        n = Mutate(r, buf, n, b[..], 1, 1)
        Assert(n >= 0 && n <= MaxLen)
    }
    a.Free()
}
