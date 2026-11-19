// libc-search, Mk: the C kernel of kernel.c, linked against std.libc's
// strlen and memchr. Usage: main <n> <reps>. Prints the checksum
// (README.md).

import c.kernel

export Main(sys: &System): ParseError|WriteError!void {
    n := try sys.args[1].ParseInt()
    reps := try sys.args[2].ParseInt()
    var sum: i64 = 0
    unsafe "k_run is plain C over std.libc" {
        sum = kernel.k_run(n, reps)
    }
    var out := sys.Stdout()
    try Print(mut out, "{s}\n", (s: sum))
}
