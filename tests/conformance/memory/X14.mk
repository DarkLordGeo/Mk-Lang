// verdict: reject E0409
// note: no partial moves, with or without a reference in the way; destructure the whole value
struct Both {
    a: Res
    b: Res
}

Steal(p: &Both): Res {
    return p.a // <- E0409
}
