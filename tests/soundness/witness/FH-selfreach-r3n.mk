// verdict: reject E0604
// note: r3m with Cells, where K(g: &G, h: H into g, x: S into h) stores x.b through the Cell that g holds, which is x.a. Accepted, `mk run` prints `r=525 k=576320`; a change of this verdict reopens the hole
struct S {
    a: &Cell[?&i32]
    b: &i32
}

struct H {
    s: ?S
}

struct G {
    s: Cell[?S]
}

K(g: &G, h: H into g, x: S into h) {
    v := x.b
    if s := g.s.Load() {
        s.a.Store(some(v))
    }
}

Outer(x: S) {
    gl := G(s: Cell[?S].New(some(x)))
    K(&gl, H(s: none), x) // <- E0604
}

Inner(cl: &Cell[?&i32]) {
    n: i32 = 7
    Outer(S(a: cl, b: &n))
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
