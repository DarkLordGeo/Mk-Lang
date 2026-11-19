// verdict: reject E0505
// case: H71
Bad(h: &Heap): OutOfMemory!void {
    a := try Arena.New(h)
    onerr a.Free()
    long: i32 = 1
    c := try a.Put(Cell.New(&long))
    if Cond() {
        short: i32 = 2
        c.Store(&short) // <- E0505
    }
    r := c.Load()
    Use(r)
    a.Free()
}
