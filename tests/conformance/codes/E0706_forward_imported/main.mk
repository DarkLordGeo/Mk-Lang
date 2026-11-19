// verdict: reject E0706
// note: inside a generic, an imported callee's clause that reads an enum value beside the caller's const generic is judged over the caller's finite set, the value bound as it is; also at a closed site with a const value parameter, which binds the call's argument; a clause that reads an argument no clause can spell rejects the call unless it is the callee's sole clause over its one const generic; and a side of && or || that its left side decides is not reached, as in a same-module fold
import lib

Pick[const N: int](): int
    requires N == 2 || N == 24
{
    return lib.At[N, lib.Mode.A]() // <- E0706
}

Fine[const N: int](): int
    requires N == 2 || N == 3
{
    return lib.At[N, lib.Mode.A]()
}

Closed[T](): int {
    return lib.At[2, lib.Mode.B]() // <- E0706
}

struct W[T] {
    a: T
    b: T
    c: T
}

// The imported twin of E0706_forward_unjudged.mk's Fwd: the implied clause reads an argument no clause can
// spell, so the call is rejected; the listed twin is left to the entry check.
Fwd[T](): int {
    a := lib.Implied[2, SizeOf[W[T]]()]() // <- E0706
    return a + lib.Listed[2, SizeOf[W[T]]()]() + lib.Implied[2, 3]()
}

// q09b: the skip rests on two clauses over M.
FwdSkip[T](): int {
    return lib.Skip[SizeOf[W[T]]()]() // <- E0706
}

// q20b: the enum value is bound as it is and k as the call passes it.
ValueParam[T](): int {
    a := lib.AtK[T, lib.MA](24) // <- E0706
    return a + lib.AtK[T, lib.MA](3)
}

Calls(): int {
    a := lib.F[0]() + lib.F[2]() + lib.G[2]()
    b := lib.F[16]() // <- E0706
    c := lib.G[0]() // <- E0706
    return a + b + c + Pick[2]() + Fine[3]() + Closed[int]() + Fwd[u64]() + FwdSkip[u64]() + ValueParam[int]()
}
