// mk test --prop finds a planted hole and shrinks it: Clamp forgets the upper bound, and a
// midpoint overflows i32. The run fails with each contract's shrunk call.
Clamp(x: i32, lo: i32, hi: i32): i32
    requires lo <= hi
    ensures result >= lo && result <= hi
{
    if x < lo {
        return lo
    }
    return x
}

Mid(lo: i32, hi: i32): i32
    requires lo <= hi
{
    return (lo + hi) / 2
}
