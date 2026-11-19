// verdict: reject E0604
// rule: D-453
// note: Swap keeps R11's reading of `*a into b`, a store at any depth below b, so a swap of two view fields of self needs `*self into self`
struct Buffers {
    front: HeapText
    back: HeapText
}

Buffers.Flip(mut self) {
    Swap(mut self.front, mut self.back) // <- E0604
}
