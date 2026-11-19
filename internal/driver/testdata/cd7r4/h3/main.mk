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

Hold(g: &G[int], x: &i32 into g) {
}

MakeV(): V {
    return V.Of(&GM)
}

export Main(sys: &System) {
    _ = MakeV()
    sys.Print("ok\n") else {}
}
