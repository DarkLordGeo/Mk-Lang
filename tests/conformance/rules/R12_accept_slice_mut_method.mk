// verdict: ok
// rule: R12
// rule: D-218
// note: the twin of R12_reject_slice_mut_method: a `mut []i32` and a `var` array take a `mut self` `Slice[T]` method, and a `&self` one works on `[]i32`
Good(xs: mut []i32, ys: []i32): int {
    xs.Swap(0, 1)
    var arr := [3, 1, 2]
    arr.Swap(0, 1)
    return ys.Len()
}
