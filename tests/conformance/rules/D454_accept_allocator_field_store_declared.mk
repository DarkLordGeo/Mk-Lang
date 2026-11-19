// verdict: ok
// rule: D-454
// note: an allocator field (List[i32] holds one) gets a node of its own, and a method that stores a loan of self.a into self.b and states *self into self checks clean
struct Deep {
    a: &i32
    b: mut &i32
    l: List[i32]
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

Deep.Mix(mut self) *self into self {
    Put(mut self.a, mut self.b)
}

export Main(sys: &System): void {
}
