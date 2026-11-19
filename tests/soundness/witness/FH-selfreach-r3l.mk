// verdict: reject E0604
// note: self-reach through a forwarding parameter: r3b with a field write between the split and the forward. Accepted, `mk run` prints `r=-1153430736 k=576320` from a dead local; a change of this verdict reopens the hole
struct S {
    a: &Cell[?&i32]
    b: &i32
}

struct H {
    s: S
}

F(h: mut H, x: S into h) {
    v := x.b
    h.s = x
    h.s.a.Store(some(v)) // <- E0604
}

Outer(px: &S) {
    dummy: i32 = 0
    dc := Cell[?&i32].New(none)
    var h := H(s: S(a: &dc, b: &dummy))
    F(mut h, *px)
}

Inner(cl: &Cell[?&i32]) {
    m: i32 = 1
    n: i32 = 7
    var s := S(a: cl, b: &m)
    s.b = &n
    Outer(&s)
}

Smash(n: i32): i32 {
    var a: [64]i32 = [0; 64]
    for i in 0..64 {
        a[i] = n + 9000
    }
    var s: i32 = 0
    for i in 0..64 {
        s = s + a[i]
    }
    return s
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    cl := Cell[?&i32].New(none)
    Inner(&cl)
    k := Smash(5)
    if r := cl.Load() {
        v := *r
        try Print(mut out, "r={v} k={k}\n", (v, k))
    }
}
