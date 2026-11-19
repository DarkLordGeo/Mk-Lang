// Source: Assert(false), in every build (types.PanicAssert).
export Main(sys: &System) {
    Assert(sys.args.Len() == 2)
}
