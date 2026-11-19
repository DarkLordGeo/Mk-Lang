// Source: an index out of range (types.PanicBounds).
export Main(sys: &System): int {
    xs := [0; 3]
    return xs[sys.args.Len() + 2]
}
