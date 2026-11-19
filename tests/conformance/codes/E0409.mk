// verdict: reject E0409
// prelude: yes
struct Wrap {
    r: Res
}

Inner(w: &Wrap): Res {
    return w.r // <- E0409
}
