// verdict: reject E0409
// case: R4.Leak
struct Both {
    a: Res
    b: Res
}

Leak(p: Both): Res {
    return p.a // <- E0409
}
