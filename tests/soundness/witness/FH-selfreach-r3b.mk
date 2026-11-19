// verdict: reject E0604
// note: r3 with Outer(px: &S) forwarding *px and the write through a Cell: F's write `h.s.a.Store(some(v))` reaches x's own node through what h's node holds after `h.s = x`, a store of x into x that F does not declare. Accepted, `mk run` prints a varying r from a dead local; a change of this verdict reopens the hole
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
    n: i32 = 7
    s := S(a: cl, b: &n)
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
