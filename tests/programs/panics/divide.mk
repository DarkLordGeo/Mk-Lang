// Source: division by zero (types.PanicDivideByZero).
export Main(sys: &System): int {
    return 10 / (sys.args.Len() - 1)
}
