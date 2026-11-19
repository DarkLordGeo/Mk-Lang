// verdict: reject E0604
// rule: D-454
// note: the allocator-field store through an element of a mut []Deep; *xs into xs clears it (D454_accept_allocator_field_shapes_declared)
struct Deep {
    a: &i32
    b: mut &i32
    l: List[i32]
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

Mix(xs: mut []Deep) {
    Put(mut xs[0].a, mut xs[0].b) // <- E0604
}

export Main(sys: &System): void {
}
