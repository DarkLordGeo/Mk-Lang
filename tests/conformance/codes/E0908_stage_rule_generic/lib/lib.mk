// An imported clause that reads the size of its type argument P, called
// inside a generic with P a type of the caller's module.
export At[T, const N: int, P](x: &T): int
    requires SizeOf[P]() > N
{
    return N
}
