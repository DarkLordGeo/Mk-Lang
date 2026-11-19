// verdict: reject E0604
// rule: D-454
// note: the allocator reaches the parameter through a generic instance's argument, G[Ctx] with Ctx holding an &Arena; *g into g clears it (D454_accept_allocator_field_shapes_declared)
struct Ctx {
    ar: &Arena
    name: str
}

struct G[T] {
    x: T
    a: &i32
    b: mut &i32
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

UseCtx(g: mut G[Ctx]) {
    Put(mut g.a, mut g.b) // <- E0604
}

export Main(sys: &System): void {
}
