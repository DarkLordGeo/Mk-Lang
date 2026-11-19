// Why: libprobe's commands share one option syntax.
// What: Parse, Options, Problem and the Usage error.
// How: a command, its positional arguments, and `--name` or `--name value`
// options, which may also be written `--name=value`.

/// What is wrong with an argument.
enum Problem {
    UnknownOption
    MissingValue
    BadValue
    UnexpectedValue
}

/// Argument `arg` (1 for the first after the program's name) is wrong.
error Usage(arg: int, problem: Problem)

/// The parsed command line.
struct Options {
    command: str
    positional: List[str]
    help: bool
    search: str
    keep: bool
    system: bool
    ordinals: bool
    summary: bool
    filter: str
}

Problem.Text(self): str {
    return match self {
        .UnknownOption { "unknown option" }
        .MissingValue { "option needs a value" }
        .BadValue { "bad value" }
        .UnexpectedValue { "option takes no value" }
    }
}

// The options that take a value.
const Valued: []str = ["--search", "--filter"]

// The options that take none.
const Flags: []str = ["--help", "--keep", "--system", "--ordinals", "--summary"]

/// Parses `args`, the arguments after the program's name. Fails with the
/// first wrong argument and what is wrong with it. O(n); the positional
/// list is allocated in `a`.
Parse(a: &Arena, args: []str): Usage|OutOfMemory!Options from a, args *args into a {
    var o := Options(command: "", positional: List[str].New(a), help: false, search: "", keep: false, system: false, ordinals: false, summary: false, filter: "")
    var i := 0
    var options := true
    for i < args.Len() {
        arg := args[i]
        n := i + 1
        i += 1
        if !options || !arg.StartsWith("--") {
            if o.command.Len() == 0 {
                o.command = arg
            } else {
                try o.positional.Push(arg)
            }
            continue
        }
        if arg == "--" {
            options = false
            continue
        }
        // Split `--name=value`; otherwise a valued option takes the next
        // argument.
        var name := arg
        var value := ""
        var given := false
        if eq := arg.Find("=") {
            name = arg[..eq]
            value = arg[eq + 1..]
            given = true
        } else if Valued.Has(&arg) {
            if i == args.Len() {
                fail Usage(arg: n, problem: Problem.MissingValue)
            }
            value = args[i]
            given = true
            i += 1
        }
        if given && Flags.Has(&name) {
            fail Usage(arg: n, problem: Problem.UnexpectedValue)
        }
        match name {
            "--help" { o.help = true }
            "--keep" { o.keep = true }
            "--system" { o.system = true }
            "--ordinals" { o.ordinals = true }
            "--summary" { o.summary = true }
            "--search" {
                match value {
                    "system" | "application" | "path" { o.search = value }
                    _ { fail Usage(arg: n, problem: Problem.BadValue) }
                }
            }
            "--filter" { o.filter = value }
            _ { fail Usage(arg: n, problem: Problem.UnknownOption) }
        }
    }
    return o
}
