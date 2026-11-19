struct B[T] {
    p: &T
}

struct Deep {
    d: ?&B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[B[int]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]
}

var GD: Deep = Deep(d: none)

Pick(x: &i32): &Deep from x {
    return &GD
}

export Main(sys: &System) {
    n: i32 = 7
    w := Pick(&n)
    if x := w.d {
        _ = x
        sys.Print("some\n") else {}
    } else {
        sys.Print("none\n") else {}
    }
}
