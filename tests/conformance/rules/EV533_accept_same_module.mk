// verdict: ok
// rule: EV533
// rule: R4
// note: the declaring module may destructure a type with private fields
struct Pair {
    a: i32
    b: i32
}

Sum(p: Pair): i32 {
    Pair(a, b) := p
    return a + b
}
