// log, Mk: std.log's Logger.Info into one Text through its Writer, n lines
// of two integer holes per rep, the Text cleared before each rep, reps
// times. Usage: log <n> <reps>. Prints the checksum: the sum over reps of
// the output's byte length (README.md).

import std.log

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var t := Text.New(sys.arena)
    var total: u64 = 0
    for _ in 0..reps {
        t.Clear()
        var w := t.Writer()
        var l := log.Logger.New(mut w, log.Level.Info)
        for k in 0..n {
            try l.Info("event {k} took {d} us", (k: k, d: (k * 7) % 1000))
        }
        total = total +% Cast[u64](t.Str().Len())
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
