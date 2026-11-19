// verdict: reject E1002
// note: an s.go method call whose `&self` receiver is a local value is judged at the type self receives (&Ctr), not at the receiver's own type (Ctr, which is Send). Ctr holds a Cell, so &Ctr is not Send, and accepted, two tasks would race on the Cell in safe code (with MK_CARRIERS=4 the printed total falls short of 4000000). Acceptance means the hole reopened (TestE1002's local value receiver rows)
struct Ctr {
    c: Cell[int]
}

Ctr.BumpPlain(&self) {
    for _ in 0..2000000 {
        self.c.Store(self.c.Load() + 1)
    }
}

export Main(sys: &System): WriteError!void {
    k := Ctr(c: Cell[int].New(0))
    scope s := sys.io.Scope() {
        s.go k.BumpPlain() // <- E1002
        s.go k.BumpPlain() // <- E1002
    }
    try sys.Print("total {n} (4000000 without a race)\n", (n: k.c.Load()))
}
