view struct V[T] {
    p: &T
    next: ?&V[?T]
}

F(v: &V[int]): int {
    return *v.p
}

export Main(sys: &System) {
    x := 4
    v := V[int](p: &x, next: none)
    n := F(&v)
    sys.Print("{n}\n", (n)) else {}
}
