// Source: SplitAt past the end (SplitAt(i) panics unless
// 0 <= i <= Len()). SplitAt(Len()) is allowed and gives an empty right part; the
// index past it is the length plus the argument count, so no constant folds it.
// Site: std's raw.Fail call in Slice.SplitAt, code 2 (bounds); the .err moves when
// std/core/mem_views.mk does.
Sizes(xs: mut []int, i: int): (l: int, r: int) {
    (left, right) := xs.SplitAt(i)
    return (l: left.Len(), r: right.Len())
}

export Main(sys: &System): WriteError!int {
    var xs := [1, 2, 3]
    at_end := Sizes(mut xs[0..], 3)
    try sys.Print("split {l} {r}\n", at_end)
    past := Sizes(mut xs[0..], 3 + sys.args.Len())
    return past.l + past.r
}
