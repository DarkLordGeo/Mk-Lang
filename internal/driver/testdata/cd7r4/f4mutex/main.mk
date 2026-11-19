struct H[T] {
    m: ?&Mutex[T]
}

struct G[T] {
    v: T
    h: ?&H[T]
    next: ?&G[?T]
}

var GI: G[int] = G[int](v: 1, h: none, next: none)

Pick(x: &i32): &G[int] from x {
    return &GI
}

export Main(sys: &System) {
    n: i32 = 7
    g := Pick(&n)
    w := g.v
    sys.Print("{w}\n", (w)) else {}
}
