// verdict: reject E0701
// rule: D-393
// note: Pool.Free inside a generic whose T may be linear
import std.mem

Drop[T](p: mut mem.Pool[T], h: mem.Handle) {
    _ = p.Free(h) // <- E0701
}
