// Source: stack limit with an 8 KiB frame (types.PanicStackLimit):
// each frame is larger than a guard page, so the probe (Windows
// __chkstk, Linux inline probes) must fault on the guard and never step past it.
Wide(n: int): int {
    if n < 0 {
        return 0
    }
    var pad: [8192]u8 = [0; 8192]
    pad[n % 8192] = Trunc[u8](n)
    r := Wide(n + 1)
    return r + int(pad[(n * 7) % 8192])
}

export Main(sys: &System): int {
    return Wide(sys.args.Len())
}
