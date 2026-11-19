var GM: Mutex[?&i32] = Mutex[?&i32].New(none)

view struct V {
    p: &Mutex[?&i32]
}

V.Of(m: &Mutex[?&i32]): V from m {
    return V(p: m)
}

struct G[T] {
    v: T
    next: ?&G[?T]
}

F(g: &G[int]): int {
    return g.v
}

MakeV(): V {
    return V.Of(&GM)
}

export Main(sys: &System) {
    _ = MakeV()
    g := G[int](v: 3, next: none)
    n := F(&g)
    sys.Print("{n}\n", (n)) else {}
}
