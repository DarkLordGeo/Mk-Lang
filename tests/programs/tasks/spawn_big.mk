// A big spawn block: s.go F(big) with a 16 KiB argument filled
// with a pattern; the task sees every byte. On Windows the child slot's committed top grows
// before the parent writes the block.
Check(big: [16384]u8, bad: mut int) {
    var n := 0
    for i in 0..16384 {
        if big[i] != Trunc[u8](i *% 7 +% 3) {
            n += 1
        }
    }
    *bad = n
}

export Main(sys: &System): WriteError!void {
    var big: [16384]u8 = [0; 16384]
    for i in 0..16384 {
        big[i] = Trunc[u8](i *% 7 +% 3)
    }
    var bad := -1
    scope s := sys.io.Scope() {
        s.go Check(big, mut bad)
    }
    var out := sys.Stdout()
    try Print(mut out, "bad {bad}\n", (bad))
}
