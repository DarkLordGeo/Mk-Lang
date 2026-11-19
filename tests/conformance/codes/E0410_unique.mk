// verdict: reject E0410
// note: the private `n` makes `Counter` unique (a `Cell` is unique), so an exported declaration must say `view`
export struct Counter { // <- E0410
    n: Cell[int]
}
