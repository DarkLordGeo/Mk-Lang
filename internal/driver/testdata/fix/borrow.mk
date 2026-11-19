view struct Pager {
    text: []u8
    at: int
}

Pager.Peek(mut self): []u8 {
    self.at += 1
    return self.text
}

Both(t: []u8): int {
    var b := Pager(text: t, at: 0)
    x := b.Peek()
    y := b.Peek()
    return x.Len() + y.Len()
}
