// Why: the store's byte count and digest are what the server reports.
// What: tests of Store's byte count and of Digest.
// How: the digest must not depend on the hash key or insertion order.

import std.hash

test "the byte count follows sets, appends, swaps and deletes" (sys: &System) {
    a := sys.heap.Alloc()
    var s := Store.NewFixed(a)
    defer s.Free(a)
    try s.Set(a, "k", "v1")
    try s.Set(a, "k", "v22")
    _ = try s.Append(a, "j", "x")
    old := try s.Swap(a, "k", "new")
    if v := old {
        Assert(v.Str() == "v22")
        v.Free(a)
    }
    Assert(s.Bytes() == 1 + 3 + 1 + 1)
    Assert(s.Del(a, "j"))
    Assert(!s.Del(a, "j"))
    Assert(s.Bytes() == 4 && s.Len() == 1)
}

test "the digest does not depend on the map's hash key or insertion order" (sys: &System) {
    a := sys.heap.Alloc()
    var x := Store.New(a, HashKey.Of(1, 2))
    defer x.Free(a)
    var y := Store.New(a, HashKey.Of(3, 4))
    defer y.Free(a)
    try x.Set(a, "a", "1")
    try x.Set(a, "b", "2")
    try y.Set(a, "b", "2")
    try y.Set(a, "a", "1")
    Assert(x.Digest() == y.Digest())
    try y.Set(a, "a", "3")
    Assert(x.Digest() != y.Digest())
}

test "an entry's digest is Xx64 of its value under its key's hash, in any split" (sys: &System) {
    a := sys.heap.Alloc()
    var x := Store.New(a, HashKey.Of(1, 2))
    defer x.Free(a)
    try x.Set(a, "key", "a value long enough to cross a 32-byte stripe of xxHash")
    k := hash.Xx64("key".AsBytes(), 0x6B7664)
    whole := hash.Xx64("a value long enough to cross a 32-byte stripe of xxHash".AsBytes(), k)
    Assert(x.Digest() == whole +% 1)
    var split := hash.Xx64State.New(k)
    split.Bytes("a value long enough".AsBytes())
    split.Bytes(" to cross a 32-byte stripe of xxHash".AsBytes())
    Assert(split.Sum() == whole)
}
