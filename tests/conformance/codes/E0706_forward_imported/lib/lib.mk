// An enum const generic beside a lane count, and clauses whose
// right side the left side decides.
export enum Mode {
    A
    B
}

export At[const K: int, const D: Mode](): int
    requires K >= 0 && K < 4 && D == Mode.A
{
    return K
}

export F[const N: int](): int
    requires N == 0 || 8 / N > 1
{
    return N
}

export G[const N: int](): int
    requires N != 0 && 8 / N > 1
{
    return N
}

Lane[const K: int](): int
    requires K >= 0 && K < 4
{
    return K
}

// M's set is implied by N's: Lane's clause holds over it.
export Implied[const N: int, const M: int](): int
    requires N == 2
    requires M == N + 1
{
    return Lane[M]()
}

export Listed[const N: int, const M: int](): int
    requires N == 2
    requires M == 3
{
    return Lane[M]()
}

export Skip[const M: int](): int
    requires M == 3 || M == 24
    requires M < 4
{
    return Lane[M]()
}

export const MA: Mode = Mode.A

export AtK[T, const D: Mode](const k: int): int
    requires k >= 0 && k < 4 && D == Mode.A
{
    return k
}
