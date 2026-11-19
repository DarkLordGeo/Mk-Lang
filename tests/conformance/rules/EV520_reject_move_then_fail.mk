// verdict: reject E0411
// rule: EV520
// rule: R2
// note: moving the parameter into a local is a change; a `fail` after it is an error exit
error Full

Keep(r: Res, ok: bool): Full!Res {
    s := r // <- E0411
    if !ok {
        s.Close()
        fail Full
    }
    return s
}
