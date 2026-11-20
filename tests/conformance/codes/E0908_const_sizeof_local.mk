// verdict: reject E0908
// note: a const whose initializer reads the layout of a same-module struct, used as an array length in a type
struct In {
    a: int
}

const S = SizeOf[In]() + 1

struct Buf {
    data: [S]u8 // <- E0908
}
