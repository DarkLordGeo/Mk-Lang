struct G[T] {
    v: T
    a: ?&G[?T]
    b: ?&G[[2]T]
}

var GG: ?&G[int] = none

Next(g: &G[int]): ?&G[?int] {
    return g.a
}

export Main(sys: &System) {
    if g := GG {
        _ = Next(g)
        sys.Print("some\n") else {}
    } else {
        sys.Print("none\n") else {}
    }
}
