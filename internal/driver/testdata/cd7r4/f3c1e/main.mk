enum E[T] {
    Leaf(v: T)
    Node(next: &E[?T])
}

Inner(e: &E[int]): ?&E[?int] {
    match *e {
        .Node(next) { return some(next) }
        .Leaf(v) {
            _ = v
            return none
        }
    }
}

export Main(sys: &System) {
    e := E[int].Leaf(v: 3)
    n := Inner(&e)
    if m := n {
        _ = m
        sys.Print("some\n") else {}
    } else {
        sys.Print("none\n") else {}
    }
}
