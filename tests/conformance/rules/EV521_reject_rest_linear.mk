// verdict: reject E0408
// rule: EV521
// rule: R4
// note: a `..` rest in a destructuring must cover only plain fields; skipping the linear `b` is E0408 from check, never E0402
struct Two {
    a: Res
    b: Res
}

First(t: Two) {
    Two(a, ..) := t // <- E0408
    a.Close()
}
