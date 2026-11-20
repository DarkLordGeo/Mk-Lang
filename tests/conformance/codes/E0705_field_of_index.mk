// verdict: reject E0705
// note: a field of an element of a named const, as an array length, is no more constant than a field of a named const
struct Pt {
    a: int
}

const S = [Pt(a: 4)]

const P = Pt(a: 4)

struct Buf {
    data: [S[0].a]u8 // <- E0705
    more: [P.a]u8 // <- E0705
}

Get(): int {
    var b: [S[0].a]u8 // <- E0705
    return b.Len()
}
