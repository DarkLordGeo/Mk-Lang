// Why: a heap container that owns resources must release each element with
// its own Free, and a heap map must release the key and value it replaces.
// What: test blocks of deep Free on heap containers whose element types are
// not linear or are Free, and of Set, Ensure and Replace on heap maps.
// How: the counting heap fails a block that leaves heap memory live (exit
// 102), so each test proves every element's own memory came back. Helper
// names start with Df, because every file of std/core is one module.

// DfRes owns a heap text, as a resource a program defines would.
linear struct DfRes {
    name: HeapText
}

impl Free for DfRes {
    Free(self, a: &Alloc) {
        DfRes(name) := self
        name.Free(a)
    }
}

// DfKey is a map key that owns memory: equal and hashed by id, and Free.
linear struct DfKey {
    id: int
    name: HeapText
}

impl Eq for DfKey {
    Eq(&self, other: &DfKey): bool {
        return self.id == other.id
    }
}

impl Hash for DfKey {
    Hash(&self, h: mut Hasher) {
        h.U64(Cast[u64](self.id))
    }
}

impl Free for DfKey {
    Free(self, a: &Alloc) {
        DfKey(id: _, name) := self
        name.Free(a)
    }
}

DfText(a: &Alloc, s: str): OutOfMemory!HeapText from a {
    var t := HeapText.New(a)
    onerr t.Free(a)
    try t.Set(a, s)
    return t
}

DfNewRes(a: &Alloc, s: str): OutOfMemory!DfRes from a {
    return DfRes(name: try DfText(a, s))
}

DfNewKey(a: &Alloc, id: int): OutOfMemory!DfKey from a {
    return DfKey(id: id, name: try DfText(a, "a key that owns memory"))
}

DfPushText(a: &Alloc into xs, xs: mut HeapList[HeapText], s: str): OutOfMemory!void {
    var t := try DfText(a, s)
    onerr t.Free(a)
    try xs.Push(a, t)
}

DfSet(a: &Alloc into m, m: mut HeapMap[DfKey, HeapText], id: int, s: str): OutOfMemory!void {
    var k := try DfNewKey(a, id)
    onerr k.Free(a)
    var v := try DfText(a, s)
    onerr v.Free(a)
    try m.Set(a, k, v)
}

test "HeapList.Free of heap texts frees every text, then the buffer" (sys: &System) {
    a := sys.heap.Alloc()
    var xs := HeapList[HeapText].New(a)
    onerr xs.Free(a)
    for i in 0..100 {
        try DfPushText(a, mut xs, "a text long enough to own memory")
        Assert(xs.Len() == i + 1)
    }
    xs.Free(a)
}

test "Free is deep at every level: a list of lists of texts" (sys: &System) {
    a := sys.heap.Alloc()
    var outer := HeapList[HeapList[HeapText]].New(a)
    onerr outer.Free(a)
    for i in 0..10 {
        var inner := HeapList[HeapText].New(a)
        onerr inner.Free(a)
        for _ in 0..i {
            try DfPushText(a, mut inner, "inner text")
        }
        try outer.Push(a, inner)
    }
    Assert(outer.Len() == 10)
    outer.Free(a)
}

test "HeapMap.Set of Free keys and values frees the old value and the surplus key" (sys: &System) {
    a := sys.heap.Alloc()
    var m := HeapMap[DfKey, HeapText].New(a)
    onerr m.Free(a)
    for i in 0..60 {
        try DfSet(a, mut m, i % 10, "a value of some length")
    }
    Assert(m.Len() == 10)
    m.Free(a)
}

test "HeapMap.Ensure frees the surplus key and value; Replace hands the old value back" (sys: &System) {
    a := sys.heap.Alloc()
    var m := HeapMap[DfKey, HeapText].New(a)
    onerr m.Free(a)
    try DfSet(a, mut m, 1, "first")
    var k := try DfNewKey(a, 1)
    onerr k.Free(a)
    var v := try DfText(a, "second")
    onerr v.Free(a)
    got := try m.Ensure(a, k, v)
    Assert(got.Str() == "first")
    var k2 := try DfNewKey(a, 1)
    onerr k2.Free(a)
    var v2 := try DfText(a, "third")
    onerr v2.Free(a)
    old := try m.Replace(a, k2, v2)
    if o := old {
        Assert(o.Str() == "first")
        o.Free(a)
    } else {
        Assert(false)
    }
    Assert(m.Len() == 1)
    m.Free(a)
}

test "TextMap.Set of a Free value frees the old one, and Free frees every value and key" (sys: &System) {
    a := sys.heap.Alloc()
    var m := TextMap[DfRes].New(a)
    onerr m.Free(a)
    keys := "abcdefghij"
    for i in 0..50 {
        k := keys[i % 10..i % 10 + 1]
        var r := try DfNewRes(a, "a resource name")
        onerr r.Free(a)
        try m.Set(a, k, r)
    }
    Assert(m.Len() == 10)
    m.Free(a)
}

test "Box.Free of a Free value frees the value, then the box" (sys: &System) {
    a := sys.heap.Alloc()
    var r := try DfNewRes(a, "boxed")
    onerr r.Free(a)
    b := try Box[DfRes].New(a, r)
    Assert(b.Ref().name.Str() == "boxed")
    b.Free(a)
    var inner := HeapList[HeapText].New(a)
    onerr inner.Free(a)
    var d := try Box[HeapList[HeapText]].New(a, inner)
    onerr d.Free(a)
    try DfPushText(a, d.Mut(), "in a list in a box")
    d.Free(a)
}

test "Chan.Free frees every buffered Free value" (sys: &System) {
    a := sys.heap.Alloc()
    c := try Chan[HeapText].New(a, 8)
    for _ in 0..5 {
        var t := DfText(a, "buffered") else {
            c.Free(a)
            Assert(false)
            return
        }
        c.Send(sys.io, t) else {
            t.Free(a)
            c.Free(a)
            Assert(false)
            return
        }
    }
    c.Free(a)
}

test "a list of a program's own Free type is freed through its impl" (sys: &System) {
    a := sys.heap.Alloc()
    var xs := HeapList[DfRes].New(a)
    onerr xs.Free(a)
    for _ in 0..30 {
        var r := try DfNewRes(a, "a resource")
        onerr r.Free(a)
        try xs.Push(a, r)
    }
    xs.Free(a)
}

test "a Free on plain elements walks nothing and frees the storage" (sys: &System) {
    a := sys.heap.Alloc()
    var xs := HeapList[int].New(a)
    onerr xs.Free(a)
    for i in 0..1000 {
        try xs.Push(a, i)
    }
    xs.Free(a)
    var m := HeapMap[int, int].New(a)
    onerr m.Free(a)
    for i in 0..1000 {
        try m.Set(a, i % 100, i)
    }
    Assert(m.Len() == 100)
    m.Free(a)
}

// DfTok is linear and not Free: check refuses every release of it, so only
// a direct call of the helper reaches ReleaseValue with it.
linear struct DfTok {
    n: int
}

test "ReleaseValue of a linear value that is not Free panics, and the build has no Release for it [exit 101: release of a linear value that is not Free]" (sys: &System) {
    ReleaseValue(DfTok(n: 1), sys.heap.Alloc())
}

test "Chan.Free over a wrapped ring frees every buffered value" (sys: &System) {
    a := sys.heap.Alloc()
    c := try Chan[HeapText].New(a, 4)
    onerr c.Free(a)
    // Two rounds of three sends and three receives leave the head at 2, so
    // the last three values sit in slots 2, 3 and 0 and the walk wraps.
    for _ in 0..2 {
        for _ in 0..3 {
            var t := try DfText(a, "a text around the ring")
            onerr t.Free(a)
            c.Send(sys.io, t).Must()
        }
        for _ in 0..3 {
            c.Recv(sys.io).Must().Free(a)
        }
    }
    for _ in 0..3 {
        var t := try DfText(a, "a text across the wrap")
        onerr t.Free(a)
        c.Send(sys.io, t).Must()
    }
    c.Free(a)
}

test "HeapMap.Free after Removes frees every value left, past the tombstones" (sys: &System) {
    a := sys.heap.Alloc()
    var m := HeapMap[int, HeapText].New(a)
    onerr m.Free(a)
    for i in 0..200 {
        var v := try DfText(a, "a value that owns memory")
        onerr v.Free(a)
        try m.Set(a, i, v)
    }
    for i in 0..150 {
        k := i * 4 / 3
        if v := m.Remove(&k) {
            v.Free(a)
        }
    }
    Assert(m.Len() == 50)
    m.Free(a)
}
