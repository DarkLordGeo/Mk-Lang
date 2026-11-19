// verdict: reject E0505
// note: `&t` of a type holding Cell is writable by type, so t is whole after it and y's loan reaches every field: E0505 at y's borrow. MixC's cross-field Cell store needs no clause (R11's first-level exemption), so this collapse alone rejects the program (mutant M-RV2-a6-writable)
struct TwoC {
    a: Cell[&i32]
    b: Cell[&i32]
}

MixC(t: &TwoC) {
    v := t.b.Load()
    t.a.Store(v)
}

Caller() {
    x: i32 = 1
    var t := TwoC(a: Cell.New(&x), b: Cell.New(&x))
    if Cond() {
        y: i32 = 2
        t.b = Cell.New(&y) // <- E0505
        MixC(&t)
    }
    Use(t.a.Load())
}
