// verdict: reject E0505
// case: H66.local
// note: the store is a direct write through the guard in the same function
Caller(io: Io): Canceled!void {
    long: i32 = 1
    var p := &long
    m := Mutex.New(mut p)
    if Cond() {
        short: i32 = 2
        var g := try m.Lock(io)
        r := g.Mut()
        **r = &short // <- E0505
        g.Unlock()
    }
    Use(p)
}
