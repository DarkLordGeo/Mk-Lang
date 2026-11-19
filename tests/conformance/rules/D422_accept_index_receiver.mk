// verdict: ok
// rule: D-422
// note: a mut self call on an indexed element borrows the place xs[i] mut, through a mut slice, a var array, a List and nested indices
struct Counter {
    n: int
}

Counter.Bump(mut self) {
    self.n += 1
}

struct Row {
    cs: [2]Counter
}

BumpAt(cs: mut []Counter, i: int) {
    cs[i].Bump()
}

Local(): int {
    var cs := [Counter(n: 0), Counter(n: 5)]
    cs[1].Bump()
    var rows := [Row(cs: [Counter(n: 0), Counter(n: 0)])]
    rows[0].cs[1].Bump()
    return cs[1].n + rows[0].cs[1].n
}

InList(a: &Arena): OutOfMemory!int {
    var xs := List[Counter].New(a)
    try xs.Push(Counter(n: 1))
    xs[0].Bump()
    return xs[0].n
}
