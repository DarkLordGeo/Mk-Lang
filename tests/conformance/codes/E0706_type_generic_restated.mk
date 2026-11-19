// verdict: reject E0706
// note: inside a generic, a callee's clause over a type generic bound to the caller's own type parameter is restated over it (`SizeOf[U]() < 4`), and the restated clause is judged at each concrete call; a compound argument such as W[U] is still left to the entry check
At[const K: int](t: &[4]int): int
    requires K < 4
{
    return t[K]
}

G[T](t: &[4]int): int
    requires SizeOf[T]() < 4
{
    return At[SizeOf[T]()](t)
}

NotStated[U](t: &[4]int): int {
    return G[U](t) // <- E0706
}

Stated[U](t: &[4]int): int
    requires SizeOf[U]() < 4
{
    return G[U](t)
}

struct W[T] {
    a: T
}

Wrapped[U](t: &[4]int): int {
    return G[W[U]](t)
}

struct Big {
    b: [24]u8
}

Calls(t: &[4]int): int {
    a := Stated[u16](t) + Wrapped[u8](t)
    b := Stated[Big](t) // <- E0706
    return a + b
}
