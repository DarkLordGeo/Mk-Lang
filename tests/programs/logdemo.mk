// std.log's levels: one line per call, the level first; --quiet keeps
// warnings and errors only. The flags come from gen cli.Args.
import std.cli
import std.log

struct Flags {
    /// Log warnings and errors only.
    export quiet: bool
    /// How many events to log (3 when not given).
    export events: ?int
}

gen cli.Args(Flags)

export Main(sys: &System): cli.Error|WriteError!void {
    f := try Flags.Parse(sys.arena, sys.args[1..])
    var out := sys.Stdout()
    var level := log.Level.Debug
    if f.quiet {
        level = log.Level.Warn
    }
    var l := log.Logger.New(mut out, level)
    n := f.events else { 3 }
    for k in 0..n {
        try l.Debug("event {k} starts", (k: k))
        try l.Info("event {k} has {size} bytes", (k: k, size: k * 100))
        if k % 2 == 1 {
            try l.Warn("event {k} is odd", (k: k))
        }
    }
    try l.Error("{n} events, {{done}}", (n: n))
}
