// verdict: reject E0604
// case: H38
Bad(m: &Mutex[?&i32], v: &i32, io: Io): Canceled!void {
    var g := try m.Lock(io)
    defer g.Unlock()
    g.Store(some(v)) // <- E0604
}
