// verdict: reject E0301
// rule: D-924
// note: a fuzz target takes the input bytes, fn([]u8); a function of another type is no target
import std.testing

Parse(s: str) {
    Assert(s.Len() >= 0)
}

test "parse" {
    testing.Fuzz(Parse) // <- E0301
}
