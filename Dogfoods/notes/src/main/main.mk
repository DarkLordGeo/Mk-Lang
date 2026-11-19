// Why: The `notes` command: an HTTP JSON notes service over SQLite, and its
// client.
//
// What: Options (parsed by `gen cli.Args`), the exit codes, the help text, and
// Main, which dispatches to the command.
//
// How: Exit codes: 0 success, 1 failure, 2 bad usage, 3 a locked database; a
// refused client request exits 4.

import std.cli
import std.log
import src.client

/// The command line. Every option applies to the commands that name it.
struct Options {
    /// Print this help and exit
    export help: bool
    /// The database file (serve, replay, import, export, backup, and client commands with --embed)
    export db: ?str
    /// The address to serve on, or the server to reach, as host:port (default 127.0.0.1:8080)
    export addr: ?str
    /// The largest request body in bytes (default 1048576)
    export limit: ?int
    /// Write the request log to this file, replacing it, instead of printing it at exit
    export log: ?str
    /// The least request log level: debug, info, warn or error (default info)
    export level: ?str
    /// Print no request log
    export quiet: bool
    /// Concurrent clients (load, default 4)
    export clients: ?int
    /// New notes each client sends (load, default 25)
    export requests: ?int
    /// The database to copy into (backup)
    export to: ?str
    /// Serve --db on --addr inside this process for the client commands, batch and load
    export embed: bool
    /// The command and its arguments
    export args: []str
}

gen cli.Args(Options)

// The exit codes, beside 0 for success.
const Failed: int = 1
const BadUsage: int = 2
const Locked: int = 3

const Help: str = """
    notes: a notes service over SQLite, and its client

    usage: notes <command> [arguments] [options]

    server commands:
      serve                      serve the HTTP JSON API on --addr until Ctrl-C
      replay                     answer raw HTTP/1.1 requests from standard input on standard output
      import FILE                add the notes of a JSON array file in one transaction
      export                     print every note as a JSON array
      backup                     copy every note of --db into --to
      version                    print the program's and sqlite's versions

    client commands, sent to the server at --addr:
    """

const After: str = """
      batch                      run the client commands of standard input, one a line
      load                       post --requests notes from each of --clients tasks at once

    """

export Main(sys: &System): WriteError!int {
    opts := Options.Parse(sys.arena, sys.args[1..]) else e {
        m := CliMessage(sys.arena, sys.args[1..], e) else { "out of memory" }
        try sys.PrintErr("notes: {m}; see notes --help\n", (m))
        return BadUsage
    }
    if opts.help {
        var out := sys.Stdout()
        try out.Str(Help)
        try out.Str(client.Commands)
        try out.Str(After)
        try Options.Usage(mut out)
        return 0
    }
    if opts.args.Len() == 0 {
        try sys.PrintErr("notes: no command; see notes --help\n")
        return BadUsage
    }
    level := ParseLevel(opts.level else { "info" }) else {
        try sys.PrintErr("notes: --level is debug, info, warn or error\n")
        return BadUsage
    }
    return match opts.args[0] {
        "serve" { try Serve(sys, &opts, level) }
        "replay" { try Replay(sys, &opts, level) }
        "import" { try Import(sys, &opts, level) }
        "export" { try Export(sys, &opts) }
        "backup" { try Backup(sys, &opts) }
        "version" { try Version(sys) }
        "batch" { try Batch(sys, &opts, level) }
        "load" { try LoadTest(sys, &opts, level) }
        _ { try Client(sys, &opts, level) }
    }
}

// A sentence for a command-line error, naming the argument it is about.
CliMessage(a: &Arena, args: []str, e: cli.Error): OutOfMemory!str from a, args {
    return match e {
        cli.Unknown(arg) { try a.Fmt("unknown option {o}", (o: args[arg])) }
        cli.Invalid(arg) { try a.Fmt("bad value for {o}", (o: args[arg])) }
        cli.Missing { "a required option is missing" }
        OutOfMemory { "out of memory" }
        _ {
            Panic("cli.Error gained a member this match does not handle")
        }
    }
}

// The log level a --level word names.
ParseLevel(word: str): ?log.Level {
    return match word {
        "debug" { some(log.Level.Debug) }
        "info" { some(log.Level.Info) }
        "warn" { some(log.Level.Warn) }
        "error" { some(log.Level.Error) }
        _ { none }
    }
}
