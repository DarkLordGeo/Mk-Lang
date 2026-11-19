// verdict: reject E0706
// note: inside a generic, an enum value passed to a same-module callee is spelled, so a clause that reads it is judged over the caller's finite sets; a clause that reads an argument no clause can spell, which an implied set or a skipped combination may rely on, rejects the call or value rather than being left to the entry check, once per call; only the callee's sole clause over the one const generic it names is still left to the entry check
enum Mode {
    A
    B
}

const MA: Mode = Mode.A

struct W[T] {
    a: T
    b: T
    c: T
}

At[const K: int, const D: Mode](): int
    requires K >= 0 && K < 4 && D == Mode.A
{
    return K
}

Pick[const N: int](): int
    requires N == 2 || N == 24
{
    a := At[N, Mode.A]() // <- E0706
    return a + At[N, MA]() // <- E0706
}

Fine[const N: int](): int
    requires N == 2 || N == 3
{
    return At[N, Mode.A]()
}

Lane[const K: int](): int
    requires K >= 0 && K < 4
{
    return K
}

Implied[const N: int, const M: int](): int
    requires N == 2
    requires M == N + 1
{
    return Lane[M]()
}

Listed[const N: int, const M: int](): int
    requires N == 2
    requires M == 3
{
    return Lane[M]()
}

Fwd[T](): int {
    a := Implied[2, SizeOf[W[T]]()]() // <- E0706
    return a + Listed[2, SizeOf[W[T]]()]() + Implied[2, 3]() + Fine[2]()
}

// Two clauses over one generic: M = 24 is skipped by M < 4, so Lane[M] is
// judged at M = 3 only.
Skip[const M: int](): int
    requires M == 3 || M == 24
    requires M < 4
{
    return Lane[M]()
}

FwdSkip[T](): int {
    a := Skip[SizeOf[W[T]]()]() // <- E0706
    f := Skip[SizeOf[W[T]]()] // <- E0706
    return a + f() + Skip[3]()
}
