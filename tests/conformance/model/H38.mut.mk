// verdict: reject E0604
// case: H38.mut
// note: H38 through `Mut` instead of `Store`
Bad(m: &Mutex[?&i32], v: &i32, io: Io): Canceled!void {
    var g := try m.Lock(io)
    defer g.Unlock()
    r := g.Mut()
    *r = some(v) // <- E0604
}
