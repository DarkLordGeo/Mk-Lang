// verdict: reject E0609
// rule: D-252
// note: a capture is read through &, so it is not passed to a mut self method
Grow(a: &Arena, xs: mut []i32): OutOfMemory!int {
    var l := List[i32].New(a)
    xs.Sort(fn(p: &i32, q: &i32): bool {
        l.Push(*p) else { // <- E0609
        }
        return *p < *q
    })
    return l.Len()
}
