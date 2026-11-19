// verdict: reject E0604
// case: H68.into
// note: the List's storage lives in the arena, so a store into the list is a store into the arena (Caller needs `x: &i32 into a`)
Add(out: mut List[&i32], x: &i32 into out): OutOfMemory!void {
    try out.Push(x)
}

Caller(a: &Arena, x: &i32): OutOfMemory!void {
    var out := List[&i32].New(a)
    try Add(mut out, x) // <- E0604
}
