// verdict: ok
// rule: EV520
// rule: R2
// note: the parameter is unchanged at the `fail`; the caller still owns `r` on the error edge and its `onerr` closes it
error Full

Keep(r: Res, ok: bool): Full!Res {
    if !ok {
        fail Full
    }
    return r
}

Caller(): Full!void {
    r := Acquire("a")
    onerr r.Close()
    k := try Keep(r, Cond())
    k.Close()
}
