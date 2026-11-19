// verdict: reject E0607
// case: X.First
struct Sink {
    out: mut []u8
}

Sink.First(mut self): mut u8 from self.out { // <- E0607
    return mut self.out[0]
}
