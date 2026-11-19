// verdict: ok
// rule: D-393
// note: Pool.Free inside a generic whose T is known not linear from its bounds
import std.mem

Drop[T: Plain](p: mut mem.Pool[T], h: mem.Handle) {
    _ = p.Free(h)
}
