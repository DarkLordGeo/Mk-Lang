// verdict: reject E0604
// case: H68.local
// note: the List's storage lives in the arena, so a store into the list is a store into the arena (it needs `x: &i32 into a`)
Fill(a: &Arena, x: &i32): OutOfMemory!List[&i32] from a, x {
    var out := List[&i32].New(a)
    try out.Push(x) // <- E0604
    return out
}
