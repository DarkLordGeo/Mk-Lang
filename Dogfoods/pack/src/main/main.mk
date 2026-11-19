// Why: pack is an archiver that signs with Ed25519 and seals to X25519 keys;
// this module is its entry point.
//
// What: Main, Dispatch over the commands, Ctx (what every command gets), the
// exit statuses, and WithBudget and WithNoHeap.
//
// How: every command runs under a counting allocator, inside a fixed buffer
// when `--mem-limit` is given; commands that hold no file data get an
// allocator that refuses every request, so a stray allocation shows up.

import std.cli
import std.mem

const Usage: str = """
    usage: pack <command> [options] [arguments]

    Commands:
      create       pack a directory into an archive
      list         list what an archive holds
      verify       check an archive's signature and every digest
      extract      unpack an archive into a directory
      sum          print the SHA-256 or SHA-512 of each file in an archive
      keygen       make a key pair for signing and sealing
      fingerprint  print the fingerprint of a key file
      untar        list or extract a tar archive (.tar, .tar.zst, .tar.gz, .tar.zz)
      zlib         compress a file into a zlib stream
      unzlib       decompress a zlib stream into a file

    Run pack <command> --help for a command's options.
    Exit status: 0 success, 1 a damaged, forged or unopenable archive,
    2 a usage error, 3 a file system error, 4 the memory budget exhausted.

    """

/// Exit statuses.
const ExitBad: int = 1
const ExitUsage: int = 2
const ExitFiles: int = 3
const ExitMemory: int = 4

/// What every command is given: the program's capabilities, and the
/// allocator that the memory budget counts.
struct Ctx {
    sys: &System
    arena: &Arena
    io: Io
    alloc: &Alloc
    /// The budget in bytes, or 0 for none.
    limit: int
}

/// A command failed and said why on stderr; the process exits with `code`.
error Failed(code: int)

/// The errors a command returns once it has reported what went wrong.
type Error = Failed | OutOfMemory | WriteError

export Main(sys: &System): WriteError!int {
    if sys.args.Len() < 2 {
        try sys.PrintErr(Usage)
        return ExitUsage
    }
    command := sys.args[1]
    args := sys.args[2..]
    if command == "--help" || command == "-h" || command == "help" {
        try sys.Print(Usage)
        return 0
    }
    var out := sys.Stdout()
    if WantsHelp(args) {
        try sys.Print("usage: pack {command} [options]\n\n", (command))
        match command {
            "create" { try CreateOptions.Usage(mut out) }
            "list" { try ListOptions.Usage(mut out) }
            "verify" { try VerifyOptions.Usage(mut out) }
            "extract" { try ExtractOptions.Usage(mut out) }
            "sum" { try SumOptions.Usage(mut out) }
            "keygen" { try KeygenOptions.Usage(mut out) }
            "fingerprint" { try FingerprintOptions.Usage(mut out) }
            "untar" { try UntarOptions.Usage(mut out) }
            "zlib" { try ZlibOptions.Usage(mut out) }
            "unzlib" { try UnzlibOptions.Usage(mut out) }
            _ {
                try sys.PrintErr("pack: unknown command {command}\n", (command))
                return ExitUsage
            }
        }
        return 0
    }
    Dispatch(sys, command, args) else e {
        return match e {
            Failed(code) { code }
            OutOfMemory {
                try sys.PrintErr("pack: out of memory\n")
                ExitMemory
            }
            WriteError {
                try sys.PrintErr("pack: cannot write the output\n")
                ExitFiles
            }
        }
    }
    return 0
}

/// Parses the options of `command` and runs it.
Dispatch(sys: &System, command: str, args: []str): Error!void {
    match command {
        "create" {
            o := CreateOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithBudget(sys, o.mem_limit, o.stats, fn(c: &Ctx): Error!void { try Create(c, &o) })
        }
        "list" {
            o := ListOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithBudget(sys, o.mem_limit, o.stats, fn(c: &Ctx): Error!void { try ListArchive(c, &o) })
        }
        "verify" {
            o := VerifyOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithBudget(sys, o.mem_limit, o.stats, fn(c: &Ctx): Error!void { try Verify(c, &o) })
        }
        "extract" {
            o := ExtractOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithBudget(sys, o.mem_limit, o.stats, fn(c: &Ctx): Error!void { try Extract(c, &o) })
        }
        "sum" {
            o := SumOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithBudget(sys, o.mem_limit, o.stats, fn(c: &Ctx): Error!void { try Sum(c, &o) })
        }
        "keygen" {
            o := KeygenOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithNoHeap(sys, fn(c: &Ctx): Error!void { try Keygen(c, &o) })
        }
        "fingerprint" {
            o := FingerprintOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithNoHeap(sys, fn(c: &Ctx): Error!void { try Fingerprint(c, &o) })
        }
        "untar" {
            o := UntarOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithNoHeap(sys, fn(c: &Ctx): Error!void { try Untar(c, &o) })
        }
        "zlib" {
            o := ZlibOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithNoHeap(sys, fn(c: &Ctx): Error!void { try Zlib(c, &o) })
        }
        "unzlib" {
            o := UnzlibOptions.Parse(sys.arena, args) else e {
                fail Failed(code: try BadArgs(sys, command, args, e))
            }
            try WithNoHeap(sys, fn(c: &Ctx): Error!void { try Unzlib(c, &o) })
        }
        _ {
            try sys.PrintErr("pack: unknown command {command}\n{Usage}", (command, Usage))
            fail Failed(code: ExitUsage)
        }
    }
}

/// Whether the arguments ask for a command's help.
WantsHelp(args: []str): bool {
    for a in args {
        if a == "--help" || a == "-h" {
            return true
        }
    }
    return false
}

/// Reports an argument the parser refused, and gives the usage status.
BadArgs(sys: &System, command: str, args: []str, e: cli.Error): WriteError!int {
    match e {
        cli.Unknown(arg) {
            try sys.PrintErr("pack {command}: unknown option {a}\n", (command, a: args[arg]))
        }
        cli.Invalid(arg) {
            try sys.PrintErr("pack {command}: bad value {a}\n", (command, a: args[arg]))
        }
        cli.Missing(field) {
            _ = field
            try sys.PrintErr("pack {command}: a required option is missing\n", (command))
        }
        OutOfMemory {
            try sys.PrintErr("pack: out of memory\n")
            return ExitMemory
        }
        _ {
            Panic("cli.Error gained a member this match does not handle")
        }
    }
    try sys.PrintErr("run pack {command} --help for its options\n", (command))
    return ExitUsage
}

/// Runs `run` with an allocator that counts every byte, inside a fixed
/// buffer of `limit` bytes when a limit is given, and reports the counts
/// with `stats`.
WithBudget(sys: &System, limit: ?int, stats: bool, const run: fn(&Ctx): Error!void): Error!void {
    if n := limit {
        if n <= 0 {
            try sys.PrintErr("pack: --mem-limit must be above 0\n")
            fail Failed(code: ExitUsage)
        }
        buf := try sys.arena.Slice[u8](n)
        fixed := mem.Fixed.New(buf)
        counting := mem.Counting.New(fixed.Alloc())
        c := Ctx(sys: sys, arena: sys.arena, io: sys.io, alloc: counting.Alloc(), limit: n)
        run(&c) else e {
            try Report(sys, &counting, stats)
            fail e
        }
        try Report(sys, &counting, stats)
        return
    }
    counting := mem.Counting.New(sys.heap.Alloc())
    c := Ctx(sys: sys, arena: sys.arena, io: sys.io, alloc: counting.Alloc(), limit: 0)
    run(&c) else e {
        try Report(sys, &counting, stats)
        fail e
    }
    try Report(sys, &counting, stats)
}

// Runs a command that holds no file data. Its allocator refuses every
// request, so a heap allocation added to such a command fails its cases
// with OutOfMemory instead of going unnoticed.
WithNoHeap(sys: &System, const run: fn(&Ctx): Error!void): Error!void {
    c := Ctx(sys: sys, arena: sys.arena, io: sys.io, alloc: mem.Null(), limit: 0)
    try run(&c)
}

Report(sys: &System, counting: &mem.Counting, stats: bool): WriteError!void {
    if stats {
        try sys.PrintErr("pack: memory: peak {p} bytes, {b} bytes in {l} blocks still held\n", (p: counting.Peak(), b: counting.Bytes(), l: counting.Live()))
    }
}
