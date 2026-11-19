// verdict: ok
// rule: D-455
// note: Replace gives a B-tree's old value back for the caller to consume, so a tree of linear values that are not Free can be filled; a HeapBTree of Free values takes Set
import std.coll

HeapPut(a: &Alloc, t: mut coll.HeapBTree[int, Res], r: Res): OutOfMemory!void {
    if old := try t.Replace(a, 1, r) {
        old.Close()
    }
}

ArenaPut(t: mut coll.BTree[int, Res], r: Res): OutOfMemory!void {
    if old := try t.Replace(1, r) {
        old.Close()
    }
}

TextPut(a: &Alloc into t, t: mut coll.HeapBTree[int, HeapText], s: str): OutOfMemory!void {
    var v := HeapText.New(a)
    onerr v.Free(a)
    try v.Push(a, s)
    try t.Set(a, 1, v)
}
