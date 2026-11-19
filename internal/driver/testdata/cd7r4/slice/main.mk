struct S[T] {
    v: T
    kids: []S[?T]
}

Count(s: &S[int]): int {
    return s.kids.Len() + s.v
}

Grow(s: mut S[int]) {
    s.v += 1
}

export Main(sys: &System) {
    empty: [0]S[?int] = []
    none_kids := empty[0..]
    var s := S[int](v: 3, kids: none_kids)
    Grow(mut s)
    n := Count(&s)
    sys.Print("{n}\n", (n)) else {}
}
