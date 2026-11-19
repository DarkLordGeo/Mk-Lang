// A line filter: prints the lines of standard input that hold --pattern
// (with --invert, those that do not), then their count. The options and
// the help come from gen cli.Args over Options and its /// comments.
import std.cli
import std.log
import std.proc

struct Options {
    /// The text a line must hold.
    export pattern: str
    /// Keep the lines that do not hold it.
    export invert: bool
    /// Stop after this many lines.
    export max: ?int
    /// Log each kept line's number to stderr.
    export verbose: bool
    /// Print this help.
    export help: bool
    /// Words printed before the count.
    export labels: []str
}

gen cli.Args(Options)

// Usage reports a bad command line on stderr, with the help, and exits 2.
Usage(sys: &System, what: str, at: int): WriteError!void {
    var e := sys.Stderr()
    try Print(mut e, "grepc: {what} {at}\n", (what: what, at: at))
    try Options.Usage(mut e)
    try e.Flush()
    proc.Exit(2)
}

Parse(sys: &System): WriteError!Options from sys {
    match Options.Parse(sys.arena, sys.args[1..]) {
        ok(o) {
            return o
        }
        err(cli.Unknown(arg)) {
            try Usage(sys, "unknown argument", arg)
        }
        err(cli.Missing(field)) {
            try Usage(sys, "missing the option of field", field)
        }
        err(cli.Invalid(arg)) {
            try Usage(sys, "bad value at argument", arg)
        }
        err(_) {
            try Usage(sys, "out of memory at argument", 0)
        }
    }
    proc.Exit(2)
}

export Main(sys: &System): ReadError|WriteError|Utf8Error|OutOfMemory!void {
    o := try Parse(sys)
    var out := sys.Stdout()
    if o.help {
        try Options.Usage(mut out)
        return
    }
    var level := log.Level.Warn
    if o.verbose {
        level = log.Level.Info
    }
    var errw := sys.Stderr()
    var lg := log.Logger.New(mut errw, level)
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 20)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    var lines := text.Lines()
    var kept := 0
    var no := 0
    for line := lines.Next() {
        no += 1
        if line.Has(o.pattern) == o.invert {
            continue
        }
        if m := o.max {
            if kept == m {
                break
            }
        }
        try out.Str(line)
        try out.Str("\n")
        kept += 1
        try lg.Info("kept line {no}", (no: no))
    }
    for l in o.labels {
        try out.Str(l)
        try out.Str(" ")
    }
    try Print(mut out, "{kept} lines\n", (kept: kept))
}
