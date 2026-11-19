// Why: the entry point: it picks fetch's mode from the command line.
// What: Main, Dispatch and BadUsage.
// How: `url` and `cert` are subcommands; anything else is parsed as
// transfer options and URLs, and a bad command line exits 2.

import std.proc

/// Runs fetch: a client in the manner of curl, with the subcommands `url`
/// and `cert`.
export Main(sys: &System) {
    code := Dispatch(sys)
    if code != 0 {
        proc.Exit(code)
    }
}

// Dispatch picks the subcommand and returns the exit status.
Dispatch(sys: &System): int {
    args := sys.args[1..]
    if args.Len() > 0 {
        match args[0] {
            "url" { return UrlCommand(sys, args[1..]) }
            "cert" { return CertCommand(sys, args[1..]) }
            _ {}
        }
    }
    o := ParseOptions(sys.arena, args) else e {
        return BadUsage(sys, args, e)
    }
    if o.help {
        for line in UsageLines {
            sys.Print("{line}\n", (line)) else {}
        }
        return 0
    }
    return Client(sys, &o)
}

// BadUsage reports a command line that cannot be read, naming the argument.
BadUsage(sys: &System, args: []str, e: OptionError): int {
    text := match e {
        Usage(arg) { Quote(sys.arena, "unknown option", args[arg]) }
        MissingValue(arg) { Quote(sys.arena, "a value is needed after", args[arg]) }
        BadValue(arg) { Quote(sys.arena, "bad value in", args[arg]) }
        NoUrl { "no URL given" }
        OutOfMemory { "out of memory" }
    }
    sys.PrintErr("fetch: {text}\nTry 'fetch --help'.\n", (text)) else {}
    return 2
}

// Quote is "what 'arg'", for a message about one argument.
Quote(a: &Arena, what: str, arg: str): str from a, what {
    return a.Fmt("{what} '{arg}'", (what, arg)) else { what }
}
