// Source: a str slice off a scalar boundary (types.PanicStrBoundary); "é" is two bytes.
export Main(sys: &System): int {
    s := "é"
    t := s[0..sys.args.Len()]
    return t.Len()
}
