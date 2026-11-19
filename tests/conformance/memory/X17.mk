// verdict: reject E0411
// rule: R2
// note: a non-plain by-value argument moved, then a later check fails; the rewrite is to check first and move last
error Late

Hand(r: Res, ok: bool): Late!void {
    Take(r) // <- E0411
    if !ok {
        fail Late
    }
}
