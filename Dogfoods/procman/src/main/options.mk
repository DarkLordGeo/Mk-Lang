// Why: flags and environment variables set the same things, and must be
// checked once.
//
// What: Options, Config, Configure and the Bad... errors.
//
// How: the command line and the environment are merged into one Config.

import std.cli
import std.log

/// procman's command line. Every option has an environment variable that
/// gives its default, named in its help line.
struct Options {
    /// Show this help
    export help: bool
    /// The Procfile to read (default $PROCMAN_PROCFILE, else Procfile)
    export procfile: ?str
    /// The run directory (default $PROCMAN_RUN_DIR, else .procman)
    export run_dir: ?str
    /// debug, info, warn or error (default $PROCMAN_LOG, else info)
    export log_level: ?str
    /// Stop every process after this many milliseconds
    export stop_after: ?int
    /// Serve the status port at this address, such as 127.0.0.1:0 (default $PROCMAN_STATUS)
    export status: ?str
    /// Status workers to run (default 2)
    export workers: ?int
    /// Let a request for /panic stop a status worker
    export status_debug: bool
    /// Print a table of every process when procman stops
    export summary: bool
    /// Bytes of output kept in memory for the summary (default 65536)
    export tail_memory: ?int
    /// Remove the run directory when start ends
    export clean: bool
    /// The command and its arguments
    export command: []str
}

gen cli.Args(Options)

/// What every command reads, with the defaults filled in.
struct Config {
    procfile: str
    run_dir: str
    level: log.Level
    stop_after: ?int
    status: ?str
    workers: int
    status_debug: bool
    summary: bool
    tail_memory: int
    clean: bool
    command: str
    args: []str
}

error BadLevel
error BadWorkers
error BadTailMemory
error BadStopAfter

/// The usage text's first lines; the options follow from `Options.Usage`.
const Synopsis: str = """
    usage: procman [options] COMMAND [ARGS...]

    Commands:
      start [NAME...]   run the Procfile's processes (or the ones named) until they end or a signal comes
      run TASK [ARG...] run one task or process once, with ARGs added, and exit with its code
      check             read the Procfile and list its entries
      status [PATH...]  ask the status port of a running procman (paths: health, status)
      ps                print the state of each process from the run directory
      logs NAME         print the output of NAME's last run
      clean             remove the run directory
      demo PROGRAM ...  run a small demo program; `procman demo help` lists them
      help              show this help

    """

/// Builds the configuration from `opts` and the environment. A value given
/// on the command line wins over the environment, which wins over the default.
Configure(sys: &System, opts: &Options): BadLevel|BadWorkers|BadTailMemory|BadStopAfter!Config from sys, opts {
    level_name := opts.log_level else {
        sys.Env("PROCMAN_LOG") else { "info" }
    }
    level := match level_name {
        "debug" { log.Level.Debug }
        "info" { log.Level.Info }
        "warn" { log.Level.Warn }
        "error" { log.Level.Error }
        _ { fail BadLevel }
    }
    workers := opts.workers else { 2 }
    if workers < 1 || workers > 64 {
        fail BadWorkers
    }
    tail := opts.tail_memory else { 65536 }
    if tail < 0 || tail > (64 << 20) {
        fail BadTailMemory
    }
    if stop := opts.stop_after {
        if stop < 0 {
            fail BadStopAfter
        }
    }
    status: ?str = if s := opts.status { some(s) } else { sys.Env("PROCMAN_STATUS") }
    command := if opts.command.Len() > 0 { opts.command[0] } else { "help" }
    rest := if opts.command.Len() > 0 { opts.command[1..] } else { opts.command }
    return Config(
        procfile: opts.procfile else { sys.Env("PROCMAN_PROCFILE") else { "Procfile" } },
        run_dir: opts.run_dir else { sys.Env("PROCMAN_RUN_DIR") else { ".procman" } },
        level: level,
        stop_after: opts.stop_after,
        status: status,
        workers: workers,
        status_debug: opts.status_debug,
        summary: opts.summary,
        tail_memory: tail,
        clean: opts.clean,
        command: command,
        args: rest,
    )
}
