// mk test --prop on a file with contracts and a test block: the
// cookbook's Clamp, a struct with an invariant, and a midpoint computed in i64. Every
// contract holds, so the run passes. The test block is not run under --prop.
Clamp(x: i32, lo: i32, hi: i32): i32
    requires lo <= hi
    ensures result >= lo && result <= hi
{
    if x < lo {
        return lo
    }
    if x > hi {
        return hi
    }
    return x
}

struct Span {
    lo: i32
    hi: i32
    invariant self.lo <= self.hi
}

Span.Len(&self): i64
    ensures result >= 0
{
    return i64(self.hi) - i64(self.lo)
}

Mid(lo: i32, hi: i32): i32
    requires lo <= hi
    ensures result >= lo && result <= hi
{
    return Trunc[i32]((i64(lo) + i64(hi)) / 2)
}

test "clamp" {
    Assert(Clamp(5, 0, 3) == 3)
}
