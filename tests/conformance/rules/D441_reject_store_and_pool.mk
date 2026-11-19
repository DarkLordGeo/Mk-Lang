// verdict: reject E0701
// rule: D-441
// note: Guard.Store of a linear value would drop the old one (Replace(g.Mut(), x) gives it back), and Pool.Free of a closed linear T drops its value
import std.mem

Put(g: mut Guard[Res], r: Res) {
    g.Store(r) // <- E0701
}

Drop(p: mut mem.Pool[Res], h: mem.Handle): bool {
    return p.Free(h) // <- E0701
}
