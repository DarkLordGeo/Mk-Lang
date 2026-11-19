// verdict: reject E0701
// rule: D-423
// note: Free of every heap container whose element type is linear and not Free is refused at the call
import std.coll

Release(a: &Alloc, c: Chan[Res], b: Box[Res], d: coll.HeapDeque[Res], t: coll.HeapBTree[int, Res], m: TextMap[Res]) {
    c.Free(a) // <- E0701
    b.Free(a) // <- E0701
    d.Free(a) // <- E0701
    t.Free(a) // <- E0701
    m.Free(a) // <- E0701
}
