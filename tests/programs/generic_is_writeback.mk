// A var local narrowed by `if T is C` is written back after the branch when the branch writes it, a mut-self call with an explicit const argument (x.Inc[5]()) included, so F's write is kept and this prints "6 6".
struct C {
    n: int
}

C.Inc[const K: int](mut self) {
    self.n = self.n + K
}

C.IncPlain(mut self) {
    self.n = self.n + 5
}

F[T: Plain](v: T): int {
    var x := v
    if T is C {
        x.Inc[5]()
    }
    if T is C {
        return x.n
    }
    return -1
}

G[T: Plain](v: T): int {
    var x := v
    if T is C {
        x.IncPlain()
    }
    if T is C {
        return x.n
    }
    return -1
}

export Main(sys: &System): WriteError!void {
    try sys.Print("{a} {b}\n", (a: F[C](C(n: 1)), b: G[C](C(n: 1))))
}
