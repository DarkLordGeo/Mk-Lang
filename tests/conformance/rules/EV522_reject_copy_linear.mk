// verdict: reject E0409
// rule: EV522
// note: `for x in xs` copies each element, so `T` must be plain; `Res` is linear (the borrow test `TestE0409_ForCopyNonPlain`)
Each(rs: []Res) {
    for r in rs { // <- E0409
        Use(&r)
    }
}
