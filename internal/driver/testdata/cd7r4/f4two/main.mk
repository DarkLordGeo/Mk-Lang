struct G[T] {
    v: T
    a: ?&G[?T]
    b: ?&G[[2]T]
}

var GI: G[int] = G[int](v: 5, a: none, b: none)

Pick(x: &i32): &G[int] from x {
    return &GI
}

export Main(sys: &System) {
    n: i32 = 7
    g := Pick(&n)
    w := g.v
    sys.Print("{w}\n", (w)) else {}
}
