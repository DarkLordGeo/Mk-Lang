// verdict: reject E1002
// note: the receiver of an s.go method call with explicit generic arguments (x.Bump[1](), an IndexExpr callee) crosses to the task as x.Bump()'s does, so a &T whose T is not Share is rejected. A check that judged only an x.M callee would accept the through-bound shape, and two tasks would race on the Cell in safe code (with MK_CARRIERS=4 the printed total falls short of 4000000). Acceptance means the hole reopened (TestE1002's generic-arguments rows)
interface I {
    Bump[const K: int](&self)
}

struct Ctr {
    c: Cell[int]
}

impl I for Ctr {
    Bump[const K: int](&self) {
        for _ in 0..2000000 {
            self.c.Store(self.c.Load() + K)
        }
    }
}

H[T: I](io: Io, x: &T) {
    scope s := io.Scope() {
        s.go x.Bump[1]() // <- E1002
        s.go x.Bump[1]() // <- E1002
    }
}

export Main(sys: &System): WriteError!void {
    k := Ctr(c: Cell[int].New(0))
    H[Ctr](sys.io, &k)
    try sys.Print("total {n} (4000000 without a race)\n", (n: k.c.Load()))
}
