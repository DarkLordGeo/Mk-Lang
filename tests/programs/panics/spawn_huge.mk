// Source: an s.go whose argument block exceeds half a Default task slot (a direct call with
// the same arguments would overflow the same stack): "stack limit" at the s.go site, while another task sleeps.
// With one argument the same block is spawned into a StackSize.Large scope instead: it fits,
// the task reads its last byte, and the program exits 0. The neighbour's slot header is checked
// by a runtime test hook, not here.
import std.time

Nap(io: Io) {
    io.Sleep(time.Millis(50)) else {
        return
    }
}

Take(buf: [2_000_000]u8, out: mut int) {
    *out = int(buf[1_999_999])
}

export Main(sys: &System): int {
    var huge: [2_000_000]u8 = [7; 2_000_000]
    huge[0] = Trunc[u8](sys.args.Len())
    var r := 0
    scope s := sys.io.Scope() {
        s.go Nap(sys.io)
        if sys.args.Len() > 1 {
            scope big := sys.io.ScopeWith(Limits(tasks: 1, stack: StackSize.Large)) {
                big.go Take(huge, mut r)
            }
        } else {
            s.go Take(huge, mut r)
        }
    }
    return r - 7
}
