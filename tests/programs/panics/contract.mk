// Source: a false `requires` in dev and test builds (types.PanicContract); a release build runs past it and exits 0.
Half(n: int): int
    requires n > 1
{
    return n / 2
}

export Main(sys: &System) {
    h := Half(sys.args.Len())
    _ = h
}
