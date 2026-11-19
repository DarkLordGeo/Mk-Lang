// verdict: reject E0502
// case: X.Peek
// note: shared slice of a `mut []T`: no row, the model uses `&*r`
struct Sink {
    out: mut []u8
}

Sink.Peek(&self): []u8 {
    return self.out[0..]
}

Caller(buf: mut []u8) {
    var sink := Sink(out: buf)
    x := sink.Peek()
    sink.out[0] = 1 // <- E0502
    Use(&x)
}
