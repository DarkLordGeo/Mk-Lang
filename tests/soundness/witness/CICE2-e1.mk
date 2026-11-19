// verdict: reject E1002
// note: an s.go method call whose `&self` receiver is a reference (k: mut Ctr) reborrows it, so &Ctr crosses to the task, and it is judged at &Ctr, not at the receiver's own type (mut Ctr, which is Send). Ctr holds a Cell, so &Ctr is not Send, and accepted, two tasks would race on the Cell in safe code (with MK_CARRIERS=4 the printed total falls short of 4000000). A mutant that judges only a Deref 0 receiver at the type self receives accepts it. Acceptance means the hole reopened (TestE1002's mut reference receiver rows)
struct Ctr {
    c: Cell[int]
}

Ctr.BumpPlain(&self) {
    for _ in 0..2000000 {
        self.c.Store(self.c.Load() + 1)
    }
}

Race(io: Io, k: mut Ctr) {
    scope s := io.Scope() {
        s.go k.BumpPlain() // <- E1002
        s.go k.BumpPlain() // <- E1002
    }
}

export Main(sys: &System): WriteError!void {
    var k := Ctr(c: Cell[int].New(0))
    Race(sys.io, mut k)
    try sys.Print("total {n} (4000000 without a race)\n", (n: k.c.Load()))
}
