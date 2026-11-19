// verdict: reject E0604
// rule: D-454
// note: a by-value parameter whose allocator sits beside the mut &i32 field, in each form here: a nested struct, ?&Arena (also read through if), [2]&Alloc, an enum payload, a view struct, a List, a Box[Arena]. The clause the diagnostic names, s: D into s, clears each (D454_accept_allocator_field_shapes_declared)
Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

struct In {
    a: &Arena
}

struct D1 {
    a: &i32
    b: mut &i32
    inn: In
}

F1(var s: D1) {
    Put(mut s.a, mut s.b) // <- E0604
}

struct D2 {
    a: &i32
    b: mut &i32
    oa: ?&Arena
}

F2(var s: D2) {
    Put(mut s.a, mut s.b) // <- E0604
}

G2(var s: D2) {
    if a := s.oa {
        _ = a
        Put(mut s.a, mut s.b) // <- E0604
    }
}

struct D3 {
    a: &i32
    b: mut &i32
    as: [2]&Alloc
}

F3(var s: D3) {
    x := s.as[0]
    _ = x
    Put(mut s.a, mut s.b) // <- E0604
}

enum E4 {
    A(ar: &Arena)
    B(n: i32)
}

struct D4 {
    a: &i32
    b: mut &i32
    e: E4
}

F4(var s: D4) {
    Put(mut s.a, mut s.b) // <- E0604
}

view struct D5 {
    a: &i32
    b: mut &i32
    ar: &Arena
}

F5(var s: D5) {
    Put(mut s.a, mut s.b) // <- E0604
}

struct D7 {
    a: &i32
    b: mut &i32
    l: List[i32]
}

F7(var s: D7) {
    Put(mut s.a, mut s.b) // <- E0604
}

struct D8 {
    a: &i32
    b: mut &i32
    bx: Box[Arena]
}

F8(var s: D8, h: &Heap) {
    Put(mut s.a, mut s.b) // <- E0604
    D8(a: _, b: _, bx: bx) := s
    ar := bx.Take(h.Alloc())
    ar.Free()
}

export Main(sys: &System): void {
}
