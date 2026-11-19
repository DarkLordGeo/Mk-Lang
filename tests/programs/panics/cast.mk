// Source: Cast[T](x) that does not fit (types.PanicCast).
export Main(sys: &System): int {
    b := Cast[u8](sys.args.Len() + 299)
    return int(b)
}
