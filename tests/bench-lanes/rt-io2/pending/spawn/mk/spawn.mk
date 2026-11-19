// spawn, Mk, the runtime's spawn and procwait entries (core.IoSpawn,
// core.IoProcWait): n runs of this program
// again (an empty args[0] is the program itself) with the argument
// "child", every stream null; the child exits 0 at once, and each exit
// code is waited for. Prints n, the sum of the exit codes and the failed
// spawns. Usage: spawn <n>.

import std.core

export Main(sys: &System): ParseError|WriteError!void {
    a1 := sys.args[1]
    if a1.Len() == 5 && a1.AsBytes()[0] == 99 {
        return
    }
    n := Cast[int](try a1.ParseInt())
    var argv: [2]str = ["", "child"]
    var sum: i64 = 0
    var fails := 0
    for _ in 0..n {
        var c := core.IoChild(stdio: [core.IoStdNull, core.IoStdNull, core.IoStdNull], inherit: 0, pipes: [0; 3], pid: 0)
        r := core.IoSpawn(sys.io, argv[..], mut c)
        if r.err != core.SysErr.None {
            fails += 1
        } else {
            h := Trunc[uint](r.value)
            w := core.IoProcWait(sys.io, h)
            sum += w.value
            _ = core.IoClose(sys.io, h)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
