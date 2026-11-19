// verdict: reject E0706
// note: an impl method may not have a compile-time requires over a type generic, its own or the impl's: a caller through the interface bound (`x.M[Big]()`, or `H[W[Big]]`) judges none of its clauses, while its body may rely on one as the restated `SizeOf[U]()` of a callee's const argument; without the clause the callee's demand to state it stays
At[const K: int](t: &[4]int): int
    requires K < 4
{
    return t[K]
}

interface I {
    M[U](&self, t: &[4]int): int
}

struct S {
    a: int
}

impl I for S {
    M[U](&self, t: &[4]int): int
        requires SizeOf[U]() < 4 // <- E0706
    {
        return At[SizeOf[U]()](t)
    }
}

interface J {
    M(&self, t: &[4]int): int
}

struct W[T] {
    a: int
}

impl[T] J for W[T] {
    M(&self, t: &[4]int): int
        requires SizeOf[T]() < 4 // <- E0706
    {
        return At[SizeOf[T]()](t)
    }
}

interface K {
    M[U](&self, t: &[4]int): int
}

struct R {
    a: int
}

impl K for R {
    M[U](&self, t: &[4]int): int {
        return At[SizeOf[U]()](t) // <- E0706
    }
}

struct Big {
    b: [24]u8
}

H[X: I](x: &X, t: &[4]int): int {
    return x.M[Big](t)
}

HJ[X: J](x: &X, t: &[4]int): int {
    return x.M(t)
}

Calls(t: &[4]int): int {
    s := S(a: 1)
    w := W[Big](a: 1)
    return H[S](&s, t) + HJ[W[Big]](&w, t)
}
