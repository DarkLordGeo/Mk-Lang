// Why: the command line of raft: simulate seeds, replay a recording, check a
// history file.
// What: Main, Options, Usage and the exit codes.
// How: parses the arguments, then hands off to the simulator or the checker.

import std.cli
import std.proc

/// raft: a five-node Raft cluster over UDP under a deterministic simulator,
/// with a linearizability checker over its clients' history.
struct Options {
    /// Print this help and exit
    export help: bool
    /// The one seed to run (default 1)
    export seed: ?int
    /// The seeds to run, A..B inclusive
    export seeds: ?str
    /// Nodes in the cluster, 1 to 5 (default 5)
    export nodes: ?int
    /// Clients, 1 to 8 (default 3)
    export clients: ?int
    /// Operations each client invokes (default 12)
    export ops: ?int
    /// Registers the clients use, 1 to 8 (default 4)
    export keys: ?int
    /// Percent of datagrams lost (default 0)
    export loss: ?int
    /// Percent of datagrams held back one step (default 0)
    export delay: ?int
    /// Percent of datagrams sent twice (default 0)
    export duplicate: ?int
    /// Percent of datagrams with one byte changed (default 0)
    export corrupt: ?int
    /// Messages an endpoint keeps per step, 1 to 256 (default 256)
    export inbox: ?int
    /// Partition the network every 300 ms of simulated time
    export partitions: bool
    /// Fail a share of socket operations in the simulator
    export faults: bool
    /// Keep node state on the simulated disk; crash and restart the cluster once
    export crash: bool
    /// Simulated milliseconds the clients run for (default 3000)
    export duration: ?int
    /// Run every node in a task of its own
    export tasks: bool
    /// The nodes' addresses, host:port separated by commas
    export peers: ?str
    /// Write the last seed's client history to this file
    export history: ?str
    /// Write the last seed's recording to this file
    export record: ?str
    /// Heap budget in bytes for the network's queues and sockets
    export memory: ?int
    /// Print every seed's history
    export verbose: bool
    /// Build the nodes with a bug: reads answered from local state
    export stale: bool
    /// The command: sim, check FILE or replay FILE
    export args: []str
}

gen cli.Args(Options)

/// The exit codes: what the checker or the command found.
const ExitOk: int = 0
const ExitNotLinearizable: int = 1
const ExitUsage: int = 2
const ExitInput: int = 3
const ExitMemory: int = 4
const ExitReplay: int = 5
const ExitNetwork: int = 6

Usage(sys: &System): WriteError!void {
    try sys.Print("""
        usage: raft sim [options]       simulate a cluster for each seed and check its history
               raft check FILE          check a history file for linearizability
               raft replay FILE         replay a recording that sim --record wrote

        exit status: 0 linearizable, 1 not linearizable, 2 usage, 3 bad input,
        4 out of memory, 5 replay differs, 6 network error

        options:

        """)
    var out := sys.Stdout()
    try Options.Usage(mut out)
}

// what, then the argument Parse stopped at, counted after the program name.
Quote(sys: &System, what: str, arg: int): str from sys, what {
    word := sys.args.Get(arg + 1) else {
        return what
    }
    return sys.arena.Fmt("{what} {w}", (what, w: *word)) else { what }
}

// Reports a usage error and exits; a failed report changes nothing, since
// the exit status says it already.
UsageError(sys: &System, what: str): never {
    sys.PrintErr("raft: {what}\nrun raft --help for usage\n", (what)) else {}
    proc.Exit(ExitUsage)
}

export Main(sys: &System): WriteError!void {
    o := Options.Parse(sys.arena, sys.args[1..]) else e {
        what := match e {
            cli.Unknown(arg) { Quote(sys, "unknown option", arg) }
            cli.Invalid(arg) { Quote(sys, "invalid argument", arg) }
            _ { "cannot read the options" }
        }
        UsageError(sys, what)
    }
    if o.help {
        try Usage(sys)
        proc.Exit(ExitOk)
    }
    if o.args.Len() == 0 {
        UsageError(sys, "no command")
    }
    command := o.args[0]
    code := match command {
        "sim" {
            if o.args.Len() != 1 {
                UsageError(sys, "sim takes no file")
            }
            try Simulate(sys, &o)
        }
        "check" {
            if o.args.Len() != 2 {
                UsageError(sys, "check takes one file")
            }
            try CheckFile(sys, o.args[1])
        }
        "replay" {
            if o.args.Len() != 2 {
                UsageError(sys, "replay takes one file")
            }
            try ReplayFile(sys, &o, o.args[1])
        }
        _ {
            UsageError(sys, "unknown command")
        }
    }
    proc.Exit(code)
}
