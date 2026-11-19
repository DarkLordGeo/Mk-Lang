// Source: .Must() on none (types.PanicMust).
export Main(sys: &System): int {
    s := sys.args.Get(5).Must()
    return s.Len()
}
