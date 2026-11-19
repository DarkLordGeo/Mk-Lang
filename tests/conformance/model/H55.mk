// verdict: reject E0607
// case: H55
struct B3 {
    buf: [16]u8
}

B3.Bad(&self): []u8 from self.buf { // <- E0607
    return self.buf[0..]
}
