// verdict: ok
// case: X.ClauseRow2
// note: R9: a clause on any carrier result
Peek[T: Plain](c: &Cell[T]): T from c {
    return c.Load()
}
