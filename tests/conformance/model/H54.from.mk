// verdict: ok
// case: H54.from
struct Mix {
    data: [16]u8
    src: []u8
}

Mix.View(&self): []u8 from self.src {
    return self.src
}

Caller(m: mut Mix) {
    v := m.View()
    m.data[0] = 1
    Use(&v)
}
