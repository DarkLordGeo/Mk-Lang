// verdict: reject E0604
// rule: D-453
// note: why no rule keyed on places and roots exempts Swap: Put has Swap's shape at the call, two disjoint fields of one root, and really stores a loan through the mut &i32 field into what the caller's reference reaches
struct Deep {
    a: &i32
    b: mut &i32
}

Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

Deep.Mix(mut self) {
    Put(mut self.a, mut self.b) // <- E0604
}
