// verdict: reject E0504
// rule: D-424
// note: the pair of GetPair holds the pool's loan, so the pool cannot be read while it is live
import std.mem

Touch(p: mut mem.Pool[int], h: mem.Handle, k: mem.Handle): int {
    var n := 0
    if q := p.GetPair(h, k) {
        n = p.Len() // <- E0504
        *q.a += 1
    }
    return n
}
