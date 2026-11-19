// A clause over a forwarded const generic K and an enum value D, which no
// clause of a caller in another module can spell.
export enum Mode {
    A
    B
}

export const MA: Mode = Mode.A

export const MB: Mode = Mode.B

export At[const K: int, const D: Mode](t: &[4]int): int
    requires K >= 0 && K < 4 && D == Mode.A
{
    return t[K]
}
