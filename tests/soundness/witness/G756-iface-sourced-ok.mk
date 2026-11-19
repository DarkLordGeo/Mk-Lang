// verdict: ok
// note: a precision witness: a generic combinator over an interface whose method names a source (here automatically, by R9: Self mentions every type parameter) forwards or stores the method's bare T. The impl's boundary is judged as a sourced one (implSourced, namesSource), so the caller trusts the source, as it trusts an interface method's &U and []U. TakeN is a Take adapter returning self.inner.Next(); CollectN stores each item into a List the caller passes. Both are accepted and run correctly (sum 24, len 5). With the rule cut (mutant M-D756-iface-sourced), each is refused
interface It[T] {
    Next(mut self): ?T
}

struct Countdown {
    n: int
}

impl It[int] for Countdown {
    Next(mut self): ?int {
        if self.n == 0 {
            return none
        }
        self.n -= 1
        return some(self.n)
    }
}

struct TakeN[T, I: It[T]] {
    inner: I
    n: int
}

TakeN[T, I].Next(mut self): ?T {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return self.inner.Next()
}

CollectN[T: Plain, I: It[T]](it: mut I, out: mut List[T]): OutOfMemory!void
    *it into it, *it into out {
    for x := it.Next() {
        try out.Push(x)
    }
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var sum := 0
    var t := TakeN[int, Countdown](inner: Countdown(n: 10), n: 3)
    for k := t.Next() {
        sum += k
    }
    var l := List[int].New(sys.arena)
    var c := Countdown(n: 5)
    try CollectN[int, Countdown](mut c, mut l)
    n := l.Len()
    try sys.Print("sum {sum} len {n}\n", (sum, n))
}
