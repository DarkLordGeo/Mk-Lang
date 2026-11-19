// A clause over a const value parameter, and one that also reads an enum
// const generic, which an importer's clause
// cannot spell.
export enum Mode {
    A
    B
}

export const MA: Mode = Mode.A

export const MB: Mode = Mode.B

export F(const n: int, t: &[4]int): int
    requires n >= 0 && n < 4
{
    return t[n]
}

export At[const D: Mode](const k: int, t: &[4]int): int
    requires k >= 0 && k < 4 && D == Mode.A
{
    return t[k]
}

export AtT[T, const D: Mode](t: &[4]int, const k: int): int
    requires k >= 0 && k < 4 && D == Mode.A
{
    return t[k]
}

export Small[T](t: &[4]int): int
    requires SizeOf[T]() < 4
{
    return t[SizeOf[T]()]
}
