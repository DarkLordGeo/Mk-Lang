// verdict: reject E0301
// note: in a declaration's signature a same-module type's layout is not final, and an error union's layout reads each error's payload, so its size for a u8 const generic is not known there; a reference or a slice is one or two pointers whatever its element
struct Big {
    a: [10]u8
}

error Bad(b: Big)

struct Box[const N: u8] {
    v: int
}

F1(b: Box[SizeOf[Bad!u8]()]): int { // <- E0301
    return b.v
}

F2(b: Box[SizeOf[&Big]()], c: Box[SizeOf[[]Big]()]): int {
    return b.v + c.v
}
