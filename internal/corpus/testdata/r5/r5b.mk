// verdict: reject E0501 E0604
struct Countdown {
    n: int
}

NextC(c: mut Countdown): ?int {
    if c.n == 0 {
        return none
    }
    c.n -= 1
    return some(c.n)
}

CollectF[T: Plain, I](it: mut I, next: fn(c: mut I): ?T from c, out: mut List[T]): OutOfMemory!void
    *it into it, *it into out {
    for x := next(mut *it) { // <- E0501
        try out.Push(x) // <- E0604
    }
}

struct Peek[T: Plain, I] {
    inner: I
    next: fn(c: mut I): ?T from c
    head: ?T
}

Peek[T, I].Fill(mut self) {
    g := self.next
    self.head = g(mut self.inner) // <- E0604
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var l := List[int].New(sys.arena)
    var c := Countdown(n: 5)
    try CollectF[int, Countdown](mut c, NextC, mut l)
    var p := Peek[int, Countdown](inner: Countdown(n: 3), next: NextC, head: none)
    p.Fill()
    n := l.Len()
    try sys.Print("len {n}\n", (n))
}
