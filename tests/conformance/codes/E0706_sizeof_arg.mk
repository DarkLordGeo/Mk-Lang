// verdict: reject E0706
// note: a SizeOf[T]() const argument of a type parameter is restated by the caller, and the restated clause is folded at its concrete call
Need[const N: int](): int
    requires N > 0
{
    return N
}

Size[T](): int
    requires SizeOf[T]() > 0
{
    return Need[SizeOf[T]()]()
}

struct Empty {
}

Use(): int {
    return Size[Empty]() // <- E0706
}
