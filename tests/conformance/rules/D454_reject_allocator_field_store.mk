// verdict: reject E0604
// rule: D-454
// note: Deep holds a List (an allocator field) beside b: mut &i32; the allocator field has a node of its own, so the store Put makes through self.b is seen and Mix needs a clause, without which the caller would read a dead slot. A Text field is the same shape. Each states *self into self (D454_accept_allocator_field_store_declared)
struct Deep {
    a: &i32
    b: mut &i32
    l: List[i32]
}

struct DeepText {
    a: &i32
    b: mut &i32
    t: Text
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

Deep.Mix(mut self) {
    Put(mut self.a, mut self.b) // <- E0604
}

DeepText.Mix(mut self) {
    Put(mut self.a, mut self.b) // <- E0604
}

Inner(r: mut &i32, ar: &Arena) {
    z: i32 = 7
    var d := Deep(a: &z, b: r, l: List[i32].New(ar))
    d.Mix()
}

export Main(sys: &System): void {
    keep: i32 = 0
    var r: &i32 = &keep
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    Inner(mut r, &ar)
}
