struct G[T] {
    v: T
    next: ?&G[?T]
}

Next(g: &G[int]): ?&G[?int] {
    return g.next
}

export Main(sys: &System) {
    g := G[int](v: 3, next: none)
    n := Next(&g)
    if m := n {
        _ = m
        sys.Print("some\n") else {}
    } else {
        sys.Print("none\n") else {}
    }
}
