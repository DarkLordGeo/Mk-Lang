// verdict: reject E1102
// rule: R18
// rule: S-G135
// note: a call that ends a field of `self` is no proof of `ends self`, because callers clear all of H(self); so `Clear` is a primitive, and outside an unsafe-permitted module that is E1102 at the signature, in the flow group
struct O {
    arena: Arena
    n: int
}

O.Clear(mut self) ends self { // <- E1102
    self.arena.Reset()
}
