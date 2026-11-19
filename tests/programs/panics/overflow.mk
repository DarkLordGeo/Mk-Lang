// Source: integer overflow of `+` (types.PanicOverflow).
export Main(sys: &System): int {
    big := 9223372036854775807
    return big + sys.args.Len()
}
