// verdict: reject E0604
// note: a store into state reached through the one state field of a `&Ctx` names the field (`field: c.st`) and says to pass it beside the `Ctx` with `*c into st` (doc/codebase.md section 3)
struct Shared {
    m: Mutex[TextMap[HeapText]]
}

struct Ctx {
    st: &Shared
    a: &Alloc
    io: Io
}

Put(c: &Ctx, key: str, value: str): Canceled|OutOfMemory!void {
    var g := try c.st.m.Lock(c.io)
    defer g.Unlock()
    var v := HeapText.New(c.a)
    onerr v.Free(c.a)
    try v.Push(c.a, value)
    try g.Mut().Set(c.a, key, v) // <- E0604
}
