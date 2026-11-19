// Half's M is implied by N, so Lane's clause is judged over
// M in {8, 16}; Pair's combinations are those its clauses allow, so (4, 4)
// and (8, 2) are skipped and Rel's clause is judged at (4, 2) and (8, 4).
// Rel spells the relation otherwise than Pair (`A == 2 * B`), so Pair's
// clause is no restatement of it and only the skip accepts Pair's body.
Lane[const K: int](): int
    requires K == 8 || K == 16
{
    return K
}

Rel[const A: int, const B: int](): int
    requires A == 2 * B
{
    return B
}

export Half[const N: int, const M: int](): int
    requires N == 16 || N == 32
    requires M == N / 2
{
    return Lane[M]()
}

export Pair[const N: int, const M: int](): int
    requires N == 4 || N == 8
    requires M == 2 || M == 4
    requires M * 2 == N
{
    return Rel[N, M]()
}
