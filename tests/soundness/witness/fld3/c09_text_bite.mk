// verdict: reject E0501
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. t.Str()'s loan of t lands shallow in l (Push's x: str), and t.Clear() needs t exclusively while l[0] is still to be read: E0501 (conflicts see every loan)
// before FLD-3: reject E0501 at 12 E0501 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var t := Text.New(&ar)
    t.Push("hello") else { return }
    var l := List[str].New(&ar)
    l.Push(t.Str()) else { return }
    t.Clear() // <- E0501
    v := l[0]
    try Print(mut o, "v={v}\n", (v,))
}
