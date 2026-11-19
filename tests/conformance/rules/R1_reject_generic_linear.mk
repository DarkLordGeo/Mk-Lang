// verdict: reject E0401
// rule: R1
// note: Pair[Res] is linear with no keyword: a struct's kind is the worst of its instantiated fields
struct Pair[T] {
    a: T
    b: T
}

Twice() {
    p := Pair[Res](a: Acquire("a"), b: Acquire("b"))
    q := p
    Use(&p) // <- E0401
    Pair(a, b) := q
    a.Close()
    b.Close()
}
