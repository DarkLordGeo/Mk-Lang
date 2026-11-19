// verdict: reject E0505
// case: H72
Caller(io: Io): Canceled!void {
    long: i32 = 1
    m := Mutex.New(&long)
    if Cond() {
        short: i32 = 2
        var g := try m.Lock(io)
        *g.Mut() = &short // <- E0505
        g.Unlock()
    }
    Use(m.Into())
}
