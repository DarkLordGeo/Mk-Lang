// verdict: reject E0604
// case: R11.Mix
// also: H65
struct Deep {
    a: &i32
    b: mut &i32
}

Deep.Mix(mut self) {
    *self.b = self.a // <- E0604
}
