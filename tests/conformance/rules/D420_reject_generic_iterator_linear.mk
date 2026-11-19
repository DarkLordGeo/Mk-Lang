// verdict: reject E0402
// rule: D-420
// note: the hidden var of a generic iterator that may be linear is consumed by nobody, so the loop's source is not consumed
interface Counter {
    Next(mut self): ?int
}

Sum[I: Counter](it: I): int {
    var s := 0
    for x in it { // <- E0402
        s += x
    }
    return s
}
