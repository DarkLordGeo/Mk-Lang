// verdict: ok
// note: H09's shape at the boundary on a view. F's per-field clause gives w.a only x's loan, so G returns w.a while buf, which only w.b and the rest hold, ends. With a flat `from x, y`, w.a would hold buf's loan and the return would be E0603
view struct W {
    a: []u8
    b: []u8
}

F(x: []u8, y: []u8): W from (a: x, b: y) {
    return W(a: x, b: y)
}

G(x: []u8): []u8 from x {
    buf: [4]u8 = [0; 4]
    w := F(x, buf[0..2])
    return w.a
}
