import std.raw

export Use() {
    var w: [2]u64 = [0, 0]
    var f: [2]f64 = [0.0, 0.0]
    var b: [2][3]u8 = [[0, 0, 0], [0, 0, 0]]
    unsafe "w, f and b are local arrays of two elements" {
        p := raw.PtrCast[[2]u64, u64](raw.OfMut(mut w))
        q := raw.PtrCast[[2]f64, f64](raw.OfMut(mut f))
        r := raw.PtrCast[[2][3]u8, [3]u8](raw.OfMut(mut b))
        raw.AtomicFill[u64](p, 1, 2)
        raw.AtomicCopy[u64](p, raw.Offset(p, 1), 1)
        raw.AtomicFill[f64](q, 1.0, 2)
        raw.AtomicCopy[f64](q, raw.Offset(q, 1), 1)
        raw.AtomicCopy[[3]u8](r, raw.Offset(r, 1), 1) // <- E0701
        raw.AtomicFill[[2]u16](raw.PtrCast[[2][3]u8, [2]u16](raw.OfMut(mut b)), [1, 2], 1) // <- E0701
        g := raw.FnFromAddr[fn(): int](0)
        _ = raw.FnFromAddr[int](0) // <- E0701
        _ = g
    }
}
