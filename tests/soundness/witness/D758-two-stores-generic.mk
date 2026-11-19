// verdict: reject E0604
// note: the two-stores cost holds even when the callee is generic. Twice stores the same x into h twice through the generic Put, whose `T: Linker` bound has a method with an `into` clause: the second Put links x into the copy the first one stored, and Link writes x.b into x.c's cell, so x's loan reaches x's own referent. h's node holds x after the first Put, so the second is a store of x into x, which no clause beside `x into h` declares. Outer forwards its own x, so the self-reach check's self filter drops x at Outer's call, and Inner's n dies while Main's cell still refers to it. A narrowing that skipped the walk through what a generic callee stored (its T opaque) would accept Twice. Accepted, `mk run` prints an r other than n's 7 on each run, reading the dead n; mutant M-D694 flips it
interface Linker {
    Link(&self, other: Self into self)
}

struct S {
    c: &Cell[?&i32]
    b: &i32
}

impl Linker for S {
    Link(&self, other: S into self) {
        self.c.Store(some(other.b))
    }
}

struct Holder[T: Plain] {
    first: ?T
}

Put[T: Linker + Plain](h: mut Holder[T], x: T into h) {
    if f := &h.first {
        f.Link(x)
        return
    }
    h.first = some(x)
}

Twice(h: mut Holder[S], x: S into h) {
    Put(h, x)
    Put(h, x) // <- E0604
}

Outer(x: S) {
    var h := Holder[S](first: none)
    Twice(mut h, x)
}

Inner(cl: &Cell[?&i32]) {
    n: i32 = 7
    Outer(S(c: cl, b: &n))
}

Clobber(n: i32): i32 {
    var acc: i32 = n
    var w0: i32 = n + 1001
    var w1: i32 = n + 2002
    for _ in 0..4 {
        acc = acc + w0 + w1
        w0 = w0 + 1
        w1 = w1 + 1
    }
    return acc
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    cl := Cell[?&i32].New(none)
    Inner(&cl)
    k := Clobber(5)
    var v: i32 = -1
    if r := cl.Load() {
        v = *r
    }
    try Print(mut out, "r={v} k={k}\n", (v, k))
}
