// verdict: reject E0602
// case: H44
struct Mix {
    data: [16]u8
    src: []u8
}

Mix.Bad(&self): []u8 from self.src {
    return self.data[0..] // <- E0602
}
