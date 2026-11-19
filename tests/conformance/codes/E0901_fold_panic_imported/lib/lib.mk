// An imported clause whose arithmetic panics at the call's closed const
// arguments, inside a generic with an open type argument.
export At[T, const N: int, const K: int](): int
    requires K * 2 == N && K < 4
{
    return K
}
