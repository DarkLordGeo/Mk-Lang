// verdict: ok
// rule: D-423
// note: a map of heap strings is overwritten with Set and freed with one deep Free; containers of Free elements, and an open T bounded by Free or Plain, take Free
Store(a: &Alloc into m, m: mut TextMap[HeapText], key: str, value: str): OutOfMemory!void {
    var v := HeapText.New(a)
    onerr v.Free(a)
    try v.Push(a, value)
    try m.Set(a, key, v)
}

StoreAt(a: &Alloc into m, m: mut HeapMap[int, HeapText], key: int, value: str): OutOfMemory!void {
    var v := HeapText.New(a)
    onerr v.Free(a)
    try v.Push(a, value)
    try m.Set(a, key, v)
}

Release(a: &Alloc, m: HeapMap[int, HeapText]) {
    m.Free(a)
}

Nested(a: &Alloc, xs: HeapList[HeapList[HeapText]], t: TextMap[HeapText], b: Box[HeapText]) {
    xs.Free(a)
    t.Free(a)
    b.Free(a)
}

FreeAny[T: Free](a: &Alloc, xs: HeapList[T]) {
    xs.Free(a)
}

FreePlain[T: Plain](a: &Alloc, xs: HeapList[T]) {
    xs.Free(a)
}

struct Conn {
    buf: HeapText
}

impl Free for Conn {
    Free(self, a: &Alloc) {
        Conn(buf) := self
        buf.Free(a)
    }
}

Close(a: &Alloc, conns: HeapList[Conn]) {
    conns.Free(a)
}
