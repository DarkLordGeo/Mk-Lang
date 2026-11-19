// verdict: reject E0604
// note: direct write rule: the walk from `out` stops at the arena; `v into out` covers the store in Put. The List's storage lives in the arena, so a store into the list is a store into the arena; Caller would need `v: &i32 into a`
Put(out: mut List[&i32], v: &i32 into out) {
    out[0] = v
}

Caller(a: &Arena, v: &i32): OutOfMemory!void {
    var out := List[&i32].New(a)
    try out.Push(v) // <- E0604
    Put(mut out, v) // <- E0604
}
