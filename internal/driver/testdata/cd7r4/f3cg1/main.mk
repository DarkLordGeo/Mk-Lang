struct G[T] {
    v: T
    next: ?&G[?T]
}

var GG: ?&G[int] = none

export Main(sys: &System) {
    if g := GG {
        _ = g.v
        sys.Print("some\n") else {}
    } else {
        sys.Print("none\n") else {}
    }
}
