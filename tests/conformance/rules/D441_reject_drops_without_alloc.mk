// verdict: reject E0701
// rule: D-441
// note: a call that would drop a linear value and has no allocator to free it with is refused: an arena Map's Set and Ensure, and Pool.Sweep
import std.mem

Put(m: mut Map[int, Res], r: Res, q: Res): OutOfMemory!void {
    try m.Set(1, r)        // <- E0701
    _ = try m.Ensure(2, q) // <- E0701
}

Sweep(p: mut mem.Pool[Res]): int {
    return p.Sweep(fn(h: mem.Handle, r: &Res): bool { // <- E0701
        _ = h
        return r.id > 0
    })
}
