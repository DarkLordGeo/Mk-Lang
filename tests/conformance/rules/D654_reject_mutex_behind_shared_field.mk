// verdict: reject E0505 E0604
// rule: D-654
// rule: R11
// note: a true rejection: `*self into self` reaches the `Mutex` behind `r.cell`, a shared reference field, so `R.Add`'s body may put arena memory into `*cell`; the arena dies first (E0505) and `Loop` does not declare `cell into cell` (E0604). Keep a value that must not receive the stores out of `self`: std.http passes the connection as a handler parameter of its own (`link: &http.Link`)
view struct R {
    arena: &Arena
    items: List[str]
    cell: &Mutex[?str]
}

R.Add(mut self, v: str): OutOfMemory!void *self into self {
    s := try self.arena.Dup(v)
    try self.items.Push(s)
}

Loop(h: &Heap, cell: &Mutex[?str]) {
    var a := Arena.New(h) else {
        return
    }
    defer a.Free()
    var r := R(arena: &a, items: List[str].New(&a), cell: cell)
    r.Add("x") else { // <- E0505 E0604
        return
    }
}
