// verdict: reject E0501
// case: R10.Buf
struct Buf {
    data: [16]u8
    pos: int
}

Buf.Peek(mut self): []u8 {
    return self.data[self.pos..]
}

Two(b: mut Buf) {
    x := b.Peek()
    y := b.Peek() // <- E0501
    Use(&x)
    Use(&y)
}
