// verdict: reject E1002
// note: an s.go method call whose `&self` receiver is a field place through a reference parameter (h.ctr with h: &Holder) is judged at the type self receives (&Ctr), not at the field's type (Ctr, which is Send). Ctr holds a Cell, so &Ctr is not Send, and accepted, two tasks would race on the Cell in safe code (with MK_CARRIERS=4 the printed total falls short of 4000000). Acceptance means the hole reopened (TestE1002's field of a reference parameter rows)
struct Ctr {
    c: Cell[int]
}

struct Holder {
    ctr: Ctr
}

Ctr.BumpPlain(&self) {
    for _ in 0..2000000 {
        self.c.Store(self.c.Load() + 1)
    }
}

Race(io: Io, h: &Holder) {
    scope s := io.Scope() {
        s.go h.ctr.BumpPlain() // <- E1002
        s.go h.ctr.BumpPlain() // <- E1002
    }
}

export Main(sys: &System): WriteError!void {
    h := Holder(ctr: Ctr(c: Cell[int].New(0)))
    Race(sys.io, &h)
    try sys.Print("total {n} (4000000 without a race)\n", (n: h.ctr.c.Load()))
}
