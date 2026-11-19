// verdict: reject E0604
// rule: D-454
// note: the allocator sits in an enum payload beside the mut &i32 field; the clause clears it (D454_accept_allocator_field_shapes_declared)
enum Kind {
    One(l: List[i32])
    Two
}

struct Deep {
    a: &i32
    b: mut &i32
    k: Kind
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

Deep.Mix(mut self) {
    Put(mut self.a, mut self.b) // <- E0604
}

export Main(sys: &System): void {
}
