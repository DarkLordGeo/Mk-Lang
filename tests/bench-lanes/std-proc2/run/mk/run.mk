// run, Mk, std.proc.Run: n runs of this program with the argument "child",
// which writes 64 bytes to standard output and exits 0; standard output
// and standard error captured, standard input the null device (an empty
// input), each child waited for. Prints n, the bytes captured and the
// failed runs: a run fails when Run fails or the child's exit code is not
// 0, as in every baseline (G-PROC2-R6). Usage: run <n>.
//
// The program is sys.args[0], as the baselines' resolved own path:
// tools/bench starts every column by the path of its built executable,
// which has a separator, and rt-io2's spawn searches PATH only for a name
// with none (std/sys/windows/proc.mk HasSeparator, std/sys/linux/proc.mk),
// so no iteration searches PATH.

import std.proc

export Main(sys: &System): ParseError|WriteError!void {
    a1 := sys.args[1]
    var out := sys.Stdout()
    if a1.Len() == 5 && a1.AsBytes()[0] == 99 {
        try out.Str("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n")
        return
    }
    n := Cast[int](try a1.ParseInt())
    args := [sys.args[0], "child"]
    var bytes := 0
    var fails := 0
    for _ in 0..n {
        r := proc.Run(sys.arena, sys.io, args[0..], b"", 1_048_576) else {
            fails += 1
            continue
        }
        if r.code != 0 {
            fails += 1
            continue
        }
        bytes += r.stdout.Len() + r.stderr.Len()
    }
    try Print(mut out, "{n} {bytes} {fails}\n", (n, bytes, fails))
}
