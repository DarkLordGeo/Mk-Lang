// verdict: reject E0503
// note: the row allows E0503 or E0603; this is the move form. A value holding a loan on itself cannot move (R18)
struct Ring {
    me: Cell[?&Ring]
}

Moves() {
    r := Ring(me: Cell.New(none))
    r.me.Store(some(&r))
    Take(r) // <- E0503
}
