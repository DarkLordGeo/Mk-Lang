// Source: stack limit on a spawned task (types.PanicStackLimit).
// The first task spawned is task 2 (Main is task 1), so the message is "stack limit in task 2".
// Deep is stack_main.mk's: a live 256-byte local across a non-tail call, read afterwards.
Deep(n: int): int {
    if n < 0 {
        return 0
    }
    var pad: [256]u8 = [0; 256]
    pad[n % 256] = Trunc[u8](n)
    r := Deep(n + 1)
    return r + int(pad[(n + 1) % 256])
}

Run(n: int, out: mut int) {
    *out = Deep(n)
}

export Main(sys: &System): int {
    var r := 0
    scope s := sys.io.Scope() {
        s.go Run(sys.args.Len(), mut r)
    }
    return r
}
