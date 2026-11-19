// verdict: ok
// rule: D-453
// note: the clause `*self into self` states the store a swap of two view fields makes; a swap of two plain fields stores no loan and needs none
struct Buffers {
    front: HeapText
    back: HeapText
}

Buffers.Flip(mut self) *self into self {
    Swap(mut self.front, mut self.back)
    self.back.Clear()
}

struct Pair {
    x: int
    y: int
}

Pair.Flip(mut self) {
    Swap(mut self.x, mut self.y)
}
