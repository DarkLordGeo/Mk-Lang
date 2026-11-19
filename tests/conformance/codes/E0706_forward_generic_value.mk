// verdict: reject E0706
// note: a caller's own const generic passed as a callee's const value argument is forwarded, as a const parameter is: the caller states the clause, which is then judged at each concrete call, unless its finite set bounds the generic to values where the clause holds; an interface impl method, which may state no such clause, cannot pass one on
F(const n: int, t: &[4]int): int
    requires n >= 0 && n < 4
{
    return t[n]
}

NotRestated[const k: int](t: &[4]int): int {
    return F(k, t) // <- E0706
}

Restated[const k: int](t: &[4]int): int
    requires k >= 0 && k < 4
{
    return F(k, t)
}

Forward(const k: int, t: &[4]int): int {
    return F(k, t) // <- E0706
}

Listed[const k: int](t: &[4]int): int
    requires k == 1 || k == 3
{
    return F(k, t)
}

ListedBad[const k: int](t: &[4]int): int
    requires k == 1 || k == 24
{
    return F(k, t) // <- E0706
}

struct S {
    a: int
}

S.G(&self, const n: int, t: &[4]int): int
    requires n >= 0 && n < 4
{
    return t[n]
}

Method[const k: int](s: &S, t: &[4]int): int {
    return s.G(k, t) // <- E0706
}

interface Getter {
    Get(&self, t: &[4]int): int
}

struct V[const n: int] {
    x: int
}

impl[const n: int] Getter for V[n] {
    Get(&self, t: &[4]int): int {
        return F(n, t) // <- E0706
    }
}

Calls(t: &[4]int): int {
    a := Restated[2](t) + Listed[3](t)
    b := Restated[24](t) // <- E0706
    return a + b
}
