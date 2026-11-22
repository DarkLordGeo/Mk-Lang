// verdict: reject E0505
// rule: R11
// note: at the caller, `self into c` keeps the receiver's loans alive as long as c, so a local that dies first is E0505
struct Ref {
    p: &i32
}

Ref.Put(self into c, c: &Cell[?&i32]) {
    c.Store(some(self.p))
}

export Main(sys: &System): WriteError!void {
    cell := Cell[?&i32].New(none)
    if true {
        x: i32 = 4
        Ref(p: &x).Put(&cell) // <- E0505
    }
    if q := cell.Load() {
        try sys.Print("{v}\n", (v: *q))
    }
}
