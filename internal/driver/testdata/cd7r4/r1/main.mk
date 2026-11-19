struct G[T] {
    v: T
    next: ?&(a: G[?T], b: int)
}

F(g: &G[int]): int {
    return g.v
}

Bump(g: mut G[int]) {
    g.v += 1
}

export Main(sys: &System) {
    var g := G[int](v: 3, next: none)
    Bump(mut g)
    r := F(&g)
    sys.Print("{r}\n", (r)) else {}
}
