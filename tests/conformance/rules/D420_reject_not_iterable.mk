// verdict: reject E0301
// rule: D-420
// note: a value with no Next is not iterable
struct Bag {
    n: int
}

Loop(b: Bag) {
    for x in b { // <- E0301
        _ = x
    }
}
