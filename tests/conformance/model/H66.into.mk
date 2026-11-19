// verdict: reject E0505
// case: H66.into
// note: the store sits in a callee with `into`. The model left E0505 unmarked; the checker puts it where the loan on `short` is made
Store(m: &Mutex[mut &i32], v: &i32 into m, io: Io): Canceled!void {
    var g := try m.Lock(io)
    defer g.Unlock()
    r := g.Mut()
    **r = v
}

Caller(io: Io): Canceled!void {
    long: i32 = 1
    var p := &long
    if Cond() {
        short: i32 = 2
        m := Mutex.New(mut p)
        try Store(&m, &short, io) // <- E0505
    }
    Use(p)
}
