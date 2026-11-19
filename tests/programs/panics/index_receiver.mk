// Source: a mut method through an index past the end (the index of an index
// receiver is evaluated and checked once, and out of range it panics like any index).
// The index is the argument count plus 8, so no constant folds it.
struct Counter {
    n: int
}

Counter.Bump(mut self) {
    self.n += 1
}

export Main(sys: &System): int {
    var cs := [Counter(n: 0), Counter(n: 0), Counter(n: 0)]
    cs[sys.args.Len() + 8].Bump()
    return cs[0].n
}
