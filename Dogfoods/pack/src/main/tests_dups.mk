// Why: `pack list --dups` is only right if equal contents meet in one key.
//
// What: tests of the digest table behind it.
//
// How: builds Content keys from equal and almost-equal digests and counts
// the table's keys.

test "Content keys with the same digest are one table key" (sys: &System) {
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var groups := Map[Content, int].NewKeyed(&a, HashKey.Of(1, 2))
    var digest: [32]u8 = [0; 32]
    digest[31] = 9
    try groups.Set(Content(digest: digest), 1)
    try groups.Set(Content(digest: digest), 2)
    Assert(groups.Len() == 1)
    Assert(*groups.Get(&Content(digest: digest)).Must() == 2)
}

test "Content keys that differ in one byte are two table keys" (sys: &System) {
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var groups := Map[Content, int].NewKeyed(&a, HashKey.Of(1, 2))
    var digest: [32]u8 = [0; 32]
    try groups.Set(Content(digest: digest), 1)
    digest[31] = 1
    try groups.Set(Content(digest: digest), 2)
    Assert(groups.Len() == 2)
}
