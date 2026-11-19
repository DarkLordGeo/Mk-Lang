// Source: MIN / -1, reported with the divide-by-zero code.
export Main(sys: &System): int {
    min := -9223372036854775807 - sys.args.Len()
    return min / -sys.args.Len()
}
