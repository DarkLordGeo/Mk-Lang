// verdict: reject E0505
// rule: D-654
// rule: R11
// note: a `Cell` behind a shared reference field is a store target of `into h`, so the caller counts `*outer`; were it not, `Sneak` would leave the caller's cell holding `&short` after `short` ends (a callee's store through a cell picked from a shared field, `Pick`, is indistinguishable from any other)
struct H {
    c: &Cell[&i32]
    n: int
}

Pick(h: &H): &Cell[&i32] from h {
    return h.c
}

Sneak(h: mut H, x: &i32 into h) {
    c := Pick(&*h)
    c.Store(x)
}

Caller(outer: &Cell[&i32]) {
    var hh := H(c: outer, n: 0)
    short: i32 = 2
    Sneak(mut hh, &short) // <- E0505
}
