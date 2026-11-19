// verdict: reject E0706
// note: an imported clause over a forwarded const, or over a type generic bound to the caller's own type parameter, is restated or judged over the caller's finite set, as a same-module one; beside an enum value no clause can spell, a forwarded const parameter cannot be judged here, since its closed conjuncts may be false whatever the value, and a forwarded const generic is judged over the caller's finite set or cannot be judged
import lib

Generic[const k: int](t: &[4]int): int {
    return lib.F(k, t) // <- E0706
}

GenericListed[const k: int](t: &[4]int): int
    requires k == 0 || k == 2
{
    return lib.F(k, t)
}

Spoof(const k: int, t: &[4]int): int
    requires k >= 0 && k < 4
{
    return lib.At[lib.MB](k, t) // <- E0706
}

Honest(const k: int, t: &[4]int): int
    requires k >= 0 && k < 4
{
    return lib.At[lib.MA](k, t) // <- E0706
}

InGeneric[T](const k: int, t: &[4]int): int
    requires k >= 0 && k < 4
{
    return lib.AtT[T, lib.MB](t, k) // <- E0706
}

ListedSpoof[const k: int](t: &[4]int): int
    requires k == 0 || k == 1
{
    return lib.At[lib.MB](k, t) // <- E0706
}

ListedHonest[const k: int](t: &[4]int): int
    requires k == 0 || k == 1
{
    return lib.At[lib.MA](k, t)
}

TypeNotStated[U](t: &[4]int): int {
    return lib.Small[U](t) // <- E0706
}

TypeStated[U](t: &[4]int): int
    requires SizeOf[U]() < 4
{
    return lib.Small[U](t)
}

struct Big {
    b: [24]u8
}

Calls(t: &[4]int): int {
    a := GenericListed[2](t) + ListedHonest[1](t) + lib.At[lib.MA](2, t) + TypeStated[u16](t)
    b := TypeStated[Big](t) // <- E0706
    return a + b
}
