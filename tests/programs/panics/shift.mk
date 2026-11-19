// Source: a shift amount of at least the bit width (types.PanicShiftAmount).
export Main(sys: &System): int {
    return 1 << (sys.args.Len() + 63)
}
