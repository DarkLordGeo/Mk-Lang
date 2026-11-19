// verdict: ok
// rule: D-424
// note: Pool.Iter, Sweep and GetPair, and an 8-byte plain Handle with Eq, wrapped in a one-field struct to keep kinds apart
import std.mem

struct NodeId {
    h: mem.Handle
}

Collect(a: &Arena): OutOfMemory!int {
    var p := mem.Pool[int].New(a)
    h := try p.Add(1)
    k := try p.Add(2)
    id := NodeId(h: h)
    _ = id.h.Eq(&k)
    var total := 0
    for (handle, value) in p.Iter() {
        _ = handle
        total += *value
    }
    if q := p.GetPair(h, k) {
        *q.a += *q.b
    }
    dropped := p.Sweep(fn(x: mem.Handle, v: &int): bool {
        _ = x
        return *v > 1
    })
    return total + dropped + SizeOf[mem.Handle]()
}
