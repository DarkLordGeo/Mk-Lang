// verdict: reject E0502
// case: H54.noclause
struct Mix {
    data: [16]u8
    src: []u8
}

Mix.View(&self): []u8 {
    return self.src
}

Caller(m: mut Mix) {
    v := m.View()
    m.data[0] = 1 // <- E0502
    Use(&v)
}
