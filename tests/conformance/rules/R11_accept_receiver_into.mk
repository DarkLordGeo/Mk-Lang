// verdict: ok
// rule: R11
// note: a receiver takes `into` as a parameter does: `&self into arena`, `mut self into p` and `self into p` declare the receiver's store
struct Names {
    map: TextMap[int]
}

Names.Keys(&self into arena, arena: &Arena): OutOfMemory!List[str] from arena, self {
    var out := List[str].New(arena)
    for (key, value) in self.map.Iter() {
        _ = value
        try out.Push(key)
    }
    return out
}

struct Ref {
    p: &i32
}

Ref.Put(self into c, c: &Cell[?&i32]) {
    c.Store(some(self.p))
}

Ref.PutMut(mut self into c, c: &Cell[?&i32]) {
    c.Store(some(self.p))
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    x: i32 = 4
    cell := Cell[?&i32].New(none)
    Ref(p: &x).Put(&cell)
    var r := Ref(p: &x)
    r.PutMut(&cell)
    if q := cell.Load() {
        try sys.Print("{v}\n", (v: *q))
    }
}
