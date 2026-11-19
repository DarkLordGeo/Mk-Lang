// verdict: reject E0503
// case: R18.Tie
// also: H10
struct Tie {
    a: Cell[?&Tie]
}

Moves() {
    t := Tie(a: Cell.New(none))
    t.a.Store(some(&t))
    Take(t) // <- E0503
}
