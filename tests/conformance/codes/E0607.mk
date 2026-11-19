// verdict: reject E0607
// note: from p.f where f is stored inline
struct Buf {
    data: [4]u8
}

Head(b: &Buf): []u8 from b.data { // <- E0607
    return b.data[0..1]
}
