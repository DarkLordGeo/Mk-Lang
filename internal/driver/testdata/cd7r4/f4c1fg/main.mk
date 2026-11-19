struct G[T] {
    v: T
    next: ?&G[?T]
}

var GI: G[?int] = G[?int](v: none, next: none)

Pick(x: &i32): &G[?int] from x {
    return &GI
}

export Main(sys: &System) {
    n: i32 = 7
    g := Pick(&n)
    if w := g.v {
        sys.Print("{w}\n", (w)) else {}
    } else {
        sys.Print("none\n") else {}
    }
}
