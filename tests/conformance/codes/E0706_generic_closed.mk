// verdict: reject E0706
// note: inside a generic, a clause that reads only arguments known there is judged as at a call outside one
Pick[T, const N: int](x: &T): int
    requires N > 0
{
    return N
}

Wrap[T](x: &T): int {
    return Pick[T, 0](x) // <- E0706
}
