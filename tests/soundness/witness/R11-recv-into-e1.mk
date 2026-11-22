// verdict: reject E0503
// note: a receiver's `into` is a parameter's: after `&self into out`, out holds the loans of n, so freeing n's map while out is read later is refused. Accepted, it prints a key whose bytes were freed
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
    defer ar.Free()
    a := sys.heap.Alloc()
    var m := TextMap[int].New(a)
    m.Set(a, "x", 1) else {}
    n := Names(map: m)
    var out := List[str].New(&ar)
    n.Fill(mut out) else {}
    Names(map) := n // <- E0503
    map.Free(a)
    try sys.Print("{k}\n", (k: out[0]))
}
