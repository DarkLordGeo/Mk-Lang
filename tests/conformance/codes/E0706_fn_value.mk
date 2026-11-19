// verdict: reject E0706
// note: a function named as a fn value meets its compile-time requires where it is named, as a call with the same type arguments does
Lanes[const N: int](): int
    requires N == 2 || N == 4 || N == 8
{
    return N
}

Apply(f: fn(): int): int {
    return f()
}

Three(): int {
    return Apply(Lanes[3]) // <- E0706
}
