// verdict: reject E0604
// rule: D-454
// note: the allocator-field store of D454_reject_allocator_field_store made by a spawned task; the clause clears it (D454_accept_allocator_field_shapes_declared)
struct Deep {
    a: &i32
    b: mut &i32
    l: List[i32]
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

Deep.Mix(mut self, io: Io) {
    scope s := io.Scope() {
        s.go Put(mut self.a, mut self.b) // <- E0604
    }
}

export Main(sys: &System): void {
}
