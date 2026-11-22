// verdict: ok
// note: a receiver's `into` is a parameter's: Fill stores str views of its own map's keys into out, declared by `&self into out`, and the caller reads out and frees the arena before it frees n's map
struct Names {
    map: TextMap[int]
}

Names.Fill(&self into out, out: mut List[str]): OutOfMemory!void {
    for (key, value) in self.map.Iter() {
        _ = value
        try out.Push(key)
    }
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var ar := try Arena.New(sys.heap)
    a := sys.heap.Alloc()
    var m := TextMap[int].New(a)
    m.Set(a, "x", 1) else {}
    n := Names(map: m)
    var out := List[str].New(&ar)
    n.Fill(mut out) else {}
    count := out.Len()
    ar.Free()
    Names(map) := n
    map.Free(a)
    try sys.Print("{n}\n", (n: count))
}
