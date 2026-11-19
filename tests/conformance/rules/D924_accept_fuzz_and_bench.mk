// verdict: ok
// rule: D-924
// note: std.testing registers a fuzz target and a benchmark from test blocks with plain calls, no new syntax
import std.testing

Parse(b: []u8) {
    Assert(b.Len() >= 0)
}

Loop(n: int) {
    var s := 0
    for i in 0..n {
        s += i
    }
    Assert(s >= 0)
}

test "parse" {
    testing.Fuzz(Parse)
}

test "loop" {
    testing.Bench(Loop)
}
