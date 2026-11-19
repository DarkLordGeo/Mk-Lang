// verdict: reject E0607
// case: R10.Sink
struct Sink {
    buf: mut []u8
}

Sink.Out(mut self): mut []u8 from self.buf { // <- E0607
    return mut *self.buf
}
