struct S[T] {
    v: T
    kids: []S[?T]
}

Kids(s: &S[int]): []S[?int] {
    return s.kids
}

export Main(sys: &System) {
    empty: [0]S[?int] = []
    none_kids := empty[0..]
    s := S[int](v: 3, kids: none_kids)
    k := Kids(&s)
    n := k.Len()
    sys.Print("{n}\n", (n)) else {}
}
