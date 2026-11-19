// verdict: reject E0502
// rule: R10
// note: the bytes are stored inline, so a view of them borrows the struct
struct Line {
    bytes: [8]u8
}

Line.Head(&self): []u8 {
    return self.bytes[0..2]
}

Edit(l: mut Line) {
    h := l.Head()
    l.bytes[0] = 1 // <- E0502
    Use(&h)
}
