// verdict: reject E0901
// note: a same-module compile-time requires whose arithmetic panics at a concrete call or fn value is E0901 there, as an imported clause's evaluation is at a call; a side of && or || that its left side decides is not reached, so its panic is none
At[const N: int, const K: int](): int
    requires K * 2 == N && K < 4
{
    return K
}

F[const N: int](): int
    requires N == 0 || 8 / N > 1
{
    return N
}

Calls(): int {
    a := At[16, -9223372036854775800]() // <- E0901
    return a + At[4, 2]() + F[0]() + F[2]()
}

Values(): int {
    f: fn(): int = At[16, -9223372036854775800] // <- E0901
    return f()
}
