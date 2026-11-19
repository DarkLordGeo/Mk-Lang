MakeC(a: &Arena): &Cell[int] from a {
    c := a.Put(Cell[int].New(1)) else {
        return MakeC(a)
    }
    return &*c
}

const GC: &Cell[int] = MakeC(EvalArena())

export Main(sys: &System) {
    GC.Store(7)
    n := GC.Load()
    sys.Print("{n}\n", (n)) else {}
}
