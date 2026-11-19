// verdict: reject E0506
// rule: D-420
// note: only the iterated value lives for the loop; a temporary it borrows ends before the first iteration
MakeMap(a: &Arena): Map[str, int] {
    return Map[str, int].New(a)
}

Loop(a: &Arena): int {
    var s := 0
    for (key: k, value: v) in MakeMap(a).Iter() { // <- E0506
        _ = k
        s += *v
    }
    return s
}
