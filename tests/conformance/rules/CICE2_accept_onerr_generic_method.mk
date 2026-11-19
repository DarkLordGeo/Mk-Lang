// verdict: ok
// rule: R21
// note: an onerr whose call has explicit generic arguments, onerr t.Close[0](), consumes t as onerr t.Close() does, so it gets its guard and the later move of t is not E0406
error Oops

linear struct Token {
    id: int
}

Token.Close[const K: int](self) {
    Token(id: _) := self
}

F(bad: bool): Oops!int {
    t := Token(id: 2)
    onerr t.Close[0]()
    id := t.id
    t.Close[1]()
    if bad {
        fail Oops
    }
    return id
}
