// Why: the entry point: one executable for the server, its client, the
// offline log tools and the simulation.
// What: Main, the help text and the exit codes (failure, usage, corrupt
// data, memory, simulation mismatch).
// How: Main reads the subcommand and hands the rest of the arguments to
// that command's Run function. `kvd --help` lists the commands.

const Help: str = """
    kvd: a key-value server with a write-ahead log

    usage:
      kvd serve [--addr IP:PORT] [--data DIR] [flags]     run a server until SHUTDOWN or a signal
      kvd client [--addr HOST:PORT] [COMMAND...]          send one command, or one per line of stdin
      kvd admin --admin PATH COMMAND...                   send one command over the admin channel
      kvd demo [--clients N] [--hold]                     a server and N clients in one process
      kvd bench [--clients N] [--requests N]              load an in-process server, report the replies
      kvd sim [--seeds A..B] [--clients N] [--steps N]    crash, fault and recover under std.sim
      kvd record LOG [COMMAND...]                         a client session, recorded to LOG
      kvd replay LOG [COMMAND...]                         the same session replayed from LOG
      kvd wal dump FILE                                   print a log's records
      kvd wal check FILE                                  verify a log and summarise it
      kvd wal compact FILE [--out FILE] [--max-memory N]  rewrite a log as one record per key
      kvd snap dump FILE                                  print a snapshot's header and entries
      kvd snap get FILE KEY                               look one key up in a snapshot

    serve flags:
      --max-key N --max-value N --compact-after N --max-clients N --slow-ms N
      --log-level debug|info|warn|error  --log stderr|stdout  --log-time
      --port-file FILE  --lock FILE  --admin PATH  --sync always|never|N
      --idle-ms N  --request-ms N

    a data directory holds kvd.wal (the log), kvd.snap (the snapshot the log
    starts from) and kvd.lock (held by the one server that serves it)

    commands: PING GET SET DEL APPEND INCR GETSET KEYS DBSIZE HASH WAIT STATS
              NAME CLIENTS COMPACT QUIT SHUTDOWN

    exit codes: 0 ok, 1 failure, 2 usage, 3 corrupt log, 4 out of memory, 5 simulation check failed
    """

/// The exit codes.
const ExitFailure: int = 1
const ExitUsage: int = 2
const ExitCorrupt: int = 3
const ExitMemory: int = 4
const ExitSim: int = 5

export Main(sys: &System): int {
    if sys.args.Len() < 2 {
        sys.PrintErr("{h}", (h: Help)) else {}
        return ExitUsage
    }
    command := sys.args[1]
    rest := sys.args[2..]
    if command == "--help" || command == "-h" || command == "help" {
        sys.Print("{h}", (h: Help)) else {}
        return 0
    }
    code := match command {
        "serve" { RunServe(sys, rest) }
        "client" { RunClient(sys, rest) }
        "admin" { RunAdmin(sys, rest) }
        "snap" { RunSnap(sys, rest) }
        "demo" { RunDemo(sys, rest) }
        "bench" { RunBench(sys, rest) }
        "sim" | "--sim" { RunSim(sys, rest) }
        "record" { RunRecord(sys, rest) }
        "replay" { RunReplay(sys, rest) }
        "wal" { RunWal(sys, rest) }
        _ {
            sys.PrintErr("kvd: unknown command {command}; see kvd --help\n", (command)) else {}
            ExitUsage
        }
    }
    return code
}

/// Reports a bad command line and returns the usage exit code.
UsageError(sys: &System, e: ArgError | OutOfMemory): int {
    sys.PrintErr("kvd: bad arguments: {e}; see kvd --help\n", (e)) else {}
    return ExitUsage
}
