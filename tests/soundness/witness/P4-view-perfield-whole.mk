// verdict: reject E0603
// note: the control of P4-view-perfield-ok: a whole use of the split view (returning w, not w.a) reads its rest, which holds every source of F's clause, buf's loan included
view struct W {
    a: []u8
    b: []u8
}

F(x: []u8, y: []u8): W from (a: x, b: y) {
    return W(a: x, b: y)
}

G(x: []u8): W from x {
    buf: [4]u8 = [0; 4]
    w := F(x, buf[0..2])
    return w // <- E0603
}
