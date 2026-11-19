struct Holder {
    c: Cell[int]
}

MakeH(a: &Arena): ?&Holder from a {
    h := a.Put(Holder(c: Cell[int].New(1))) else {
        return none
    }
    return some(&*h)
}

const GH: ?&Holder = MakeH(EvalArena())

export Main(sys: &System) {
    if h := GH {
        h.c.Store(9)
        n := h.c.Load()
        sys.Print("{n}\n", (n)) else {}
    }
}
