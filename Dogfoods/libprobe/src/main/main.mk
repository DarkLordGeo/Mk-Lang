// Why: libprobe answers what a machine's libraries load and export, as ldd
// and dumpbin /exports do.
// What: Main, the help text and the exit statuses.
// How: opens libraries at run time as a program with plug-ins does
// (std.dl), checks a list of required libraries and symbols, reads a PE
// file's export table from its bytes, and on Windows reads file versions
// through version.dll.

// Exit statuses: 1 a library, symbol or version is missing, 2 a bad
// command line, 3 this system cannot answer, 4 a file is unreadable or
// malformed.
const ExitMissing: int = 1
const ExitUsage: int = 2
const ExitUnsupported: int = 3
const ExitFile: int = 4

const Help: str = """
    usage: libprobe <command> [options] [arguments]

    commands:
      open LIB [SYMBOL...]      load LIB, look up each SYMBOL, release it
      scan LIB...               try each LIB under every search
      check FILE                check a requirements file: one library a
                                line, its search, then its symbols
      call LIB SYMBOL [TEXT]    call SYMBOL as fn(): u32, or with TEXT as
                                fn(*u8): i32 given TEXT NUL-terminated
      exports FILE              list the names a PE file exports
      version FILE...           print the file version of each FILE
                                (Windows)
      help                      print this text

    options:
      --search S                open and call: system (the system's own
                                libraries), application (beside this
                                program, then the system's) or path (an
                                absolute path); default: path for a name
                                with a separator, else system
      --keep                    open: keep LIB loaded instead of closing it
      --system                  exports and version: FILE names a file in
                                the system's library directory
      --filter PREFIX           exports: only names starting with PREFIX
      --ordinals                exports: print each ordinal
      --summary                 exports: print the counts first; version:
                                say only whether FILE has a version
      --help                    print this text

    The current directory and PATH are never searched.

    exit status: 0 done, 1 a library, symbol or version is missing, 2 bad
    command line, 3 not possible on this system, 4 unreadable or malformed
    file.
    """

export Main(sys: &System): WriteError!int {
    o := Parse(sys.arena, sys.args[1..]) else e {
        match e {
            Usage(arg, problem) {
                try sys.PrintErr("libprobe: argument {arg}: {p}\n", (arg, p: problem.Text()))
            }
            OutOfMemory {
                try sys.PrintErr("libprobe: out of memory\n")
                return ExitFile
            }
        }
        try sys.PrintErr("run libprobe --help for usage\n")
        return ExitUsage
    }
    if o.help || o.command == "help" {
        try sys.Print(Help)
        return 0
    }
    // The fewest and most positional arguments each command takes; -1 is
    // no limit.
    var least := 1
    var most := -1
    match o.command {
        "open" | "scan" | "version" {
        }
        "check" | "exports" { most = 1 }
        "call" {
            least = 2
            most = 3
        }
        "" {
            try sys.PrintErr("libprobe: no command given\nrun libprobe --help for usage\n")
            return ExitUsage
        }
        _ {
            try sys.PrintErr("libprobe: unknown command {c}\nrun libprobe --help for usage\n", (c: o.command))
            return ExitUsage
        }
    }
    count := o.positional.Len()
    if count < least || (most >= 0 && count > most) {
        try sys.PrintErr("libprobe: wrong number of arguments for {c}\nrun libprobe --help for usage\n", (c: o.command))
        return ExitUsage
    }
    return match o.command {
        "open" { try OpenCommand(sys, &o) }
        "scan" { try ScanCommand(sys, &o) }
        "check" { try CheckCommand(sys, &o) }
        "call" { try CallCommand(sys, &o) }
        "exports" { try ExportsCommand(sys, &o) }
        _ { try VersionCommand(sys, &o) }
    }
}
