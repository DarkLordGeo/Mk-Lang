struct G[T] {
    v: T
    a: ?&G[?T]
    b: ?&G[[2]T]
}

F(g: &G[int]): int {
    n := g.a else {
        return g.v
    }
    w := n.v else {
        return 0
    }
    return g.v + w
}

Bump(g: mut G[int]) {
    g.v += 1
}

export Main(sys: &System) {
    inner := G[?int](v: some(5), a: none, b: none)
    var g := G[int](v: 3, a: some(&inner), b: none)
    Bump(mut g)
    r := F(&g)
    sys.Print("{r}\n", (r)) else {}
}
