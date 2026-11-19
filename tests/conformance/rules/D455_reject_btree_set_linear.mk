// verdict: reject E0701
// rule: D-455
// note: a B-tree Set that finds its key would drop a linear value: HeapBTree needs each of K and V not linear or Free, the arena BTree needs them not linear; Replace is the alternative
import std.coll

HeapPut(a: &Alloc, t: mut coll.HeapBTree[int, Res], r: Res): OutOfMemory!void {
    try t.Set(a, 1, r) // <- E0701
}

ArenaPut(t: mut coll.BTree[int, Res], r: Res): OutOfMemory!void {
    try t.Set(1, r) // <- E0701
}
