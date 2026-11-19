// verdict: reject E0705
// note: a call is no argument of a const parameter (a const argument is a literal, a named const, an enum variant, a struct literal of such values, or operators, conversions and builtins such as SizeOf over them), so such a call is not evaluated
Id(const x: int): int {
    return x
}

Two(): int {
    return 2
}

Lane(const i: int): int {
    return i * 10
}

struct P {
    n: int
}

LaneP(const p: P): int {
    return p.n
}

Calls(): int {
    a := Lane(Id(3)) // <- E0705
    b := Lane(Two()) // <- E0705
    c := LaneP(P(n: Two())) // <- E0705
    return a + b + c + Lane(5) + LaneP(P(n: 2)) + Lane(SizeOf[u32]())
}
