// verdict: reject E1002
// note: a const's data is emitted read-only, so a const whose evaluated value relocates a pointee whose type can hold a Cell (or another interior-mutable value or a raw pointer, in any variant) is E1002 at its name, judged on the value, not the declared type
MakeC(a: &Arena): &Cell[int] from a {
    c := a.Put(Cell[int].New(1)) else {
        return MakeC(a)
    }
    return &*c
}

const GC: &Cell[int] = MakeC(EvalArena()) // <- E1002

const NoCell: ?&Cell[int] = none

Get(): int {
    if c := NoCell {
        return c.Load()
    }
    return GC.Load()
}
