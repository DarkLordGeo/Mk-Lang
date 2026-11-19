// verdict: reject E0507 E0508
// rule: R12
// rule: D-218
// rule: D-235
// note: a `mut self` `Slice[T]` method needs a `mut []T`, a `var` array, a `List` or a `HeapList`: through `&` (`[]i32`) it is E0507, on a local that is not `var` it is E0508, as any `mut` borrow of such a local
Bad(xs: []i32) {
    xs.Swap(0, 1) // <- E0507
}

Local() {
    arr := [3, 1, 2]
    arr.Swap(0, 1) // <- E0508
}
