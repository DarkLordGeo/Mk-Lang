// verdict: reject E0604
// case: H07.param
// note: list in a parameter arena. The List's storage lives in the arena, so a store into the list is a store into the arena (it needs `xs: mut []i32 into a`)
Keep(xs: mut []i32, a: &Arena): OutOfMemory!void {
    var out := List[mut i32].New(a)
    for x in mut xs {
        try out.Push(x) // <- E0604
    }
}
