// Source: stack limit on the main task (types.PanicStackLimit).
// Deep keeps a 256-byte local live across a non-tail call and reads it afterwards, so no build can
// turn the recursion into a loop or drop it; the base case is never reached, because n starts at 1.
// The site is Deep's row in the function table (FuncRow): the position of its name.
Deep(n: int): int {
    if n < 0 {
        return 0
    }
    var pad: [256]u8 = [0; 256]
    pad[n % 256] = Trunc[u8](n)
    r := Deep(n + 1)
    return r + int(pad[(n + 1) % 256])
}

export Main(sys: &System): int {
    return Deep(sys.args.Len())
}
