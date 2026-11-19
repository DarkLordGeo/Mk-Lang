// Why: fm runs as a terminal UI or as one command; both share one parser.
// What: ParseArgs, Args, the command names and SplitLine for batch lines.
// How: global flags come first, then a command and its own options; errors
// name the argument's index so the message can quote it.

/// An option fm does not know, or a value that does not read as its type.
export error BadArg(index: int)
/// An option that takes a value came last.
export error MissingValue(index: int)

type ArgError = BadArg | MissingValue | OutOfMemory

// The command line, read: the command (empty for the interactive file
// manager), its words in order, and every option. An option is
// `--name value` or `--name=value`, anywhere on the line; `--` ends them.
struct Args {
    command: str
    words: List[str]
    help: bool
    times: bool
    yes: bool
    bytes: bool
    depth: int
    skip: ?str
    width: int
    mem: ?int
    timeout: ?int
    seed: int
    zone: str
    transcript: ?str
    root: ?str
}

const Commands: [13]str = ["ls", "view", "cp", "mv", "rm", "mkdir", "link", "stat", "touch", "du", "pwd", "batch", "selftest"]

// Reads `argv` (without the program's name). O(n); allocates the word
// list in `a`.
ParseArgs(a: &Arena, argv: []str): ArgError!Args from a, argv *argv into a {
    var r := Args(
        command: "",
        words: List[str].New(a),
        help: false,
        times: false,
        yes: false,
        bytes: false,
        depth: -1,
        skip: none,
        width: 80,
        mem: none,
        timeout: none,
        seed: 0,
        zone: "local",
        transcript: none,
        root: none,
    )
    var i := 0
    var options := true
    for i < argv.Len() {
        arg := argv[i]
        at := i
        i += 1
        if !options || !arg.StartsWith("--") {
            if r.command.Len() == 0 && r.words.Len() == 0 && Commands[..].Has(&arg) {
                r.command = arg
            } else {
                try r.words.Push(arg)
            }
            continue
        }
        if arg == "--" {
            options = false
            continue
        }
        var name := arg[2..]
        var value: ?str = none
        if eq := name.Find("=") {
            value = some(name[eq + 1..])
            name = name[..eq]
        }
        match name {
            "help" { r.help = true }
            "times" { r.times = true }
            "yes" { r.yes = true }
            "bytes" { r.bytes = true }
            _ {
                if value.IsNone() {
                    if i == argv.Len() {
                        fail MissingValue(index: at)
                    }
                    value = some(argv[i])
                    i += 1
                }
                // value was set on both paths above, so it holds a value here
                try SetValue(mut r, name, value.Must(), at)
            }
        }
    }
    return r
}

SetValue(r: mut Args, name: str, v: str into r, at: int): BadArg!void {
    match name {
        "depth" { r.depth = try Count(v, at) }
        "width" { r.width = try Count(v, at) }
        "mem" { r.mem = some(try Count(v, at)) }
        "timeout" { r.timeout = some(try Count(v, at)) }
        "seed" { r.seed = try Count(v, at) }
        "skip" { r.skip = some(v) }
        "zone" { r.zone = v }
        "transcript" { r.transcript = some(v) }
        "root" { r.root = some(v) }
        _ { fail BadArg(index: at) }
    }
}

// A whole number from 0 to 2^31 - 1.
Count(v: str, at: int): BadArg!int {
    n := v.ParseInt() else {
        fail BadArg(index: at)
    }
    if n < 0 || n > 2147483647 {
        fail BadArg(index: at)
    }
    return Trunc[int](n)
}

/// Splits one line of a batch script into words: runs of characters
/// between spaces or tabs, or a double-quoted run that may hold them
/// (`""` inside quotes is one quote). O(n); allocates the words in `a`.
SplitLine(a: &Arena, line: str into a): OutOfMemory!List[str] from a, line {
    var out := List[str].New(a)
    var i := 0
    for i < line.Len() {
        b := line.Byte(i)
        if b == b' ' || b == b'\t' {
            i += 1
            continue
        }
        if b != b'"' {
            start := i
            for i < line.Len() && line.Byte(i) != b' ' && line.Byte(i) != b'\t' {
                i += 1
            }
            try out.Push(line[start..i])
            continue
        }
        var t := Text.New(a)
        i += 1
        var start := i
        for i < line.Len() {
            if line.Byte(i) != b'"' {
                i += 1
                continue
            }
            try t.Push(line[start..i])
            if i + 1 < line.Len() && line.Byte(i + 1) == b'"' {
                try t.Push("\"")
                i += 2
                start = i
                continue
            }
            break
        }
        if i >= line.Len() {
            try t.Push(line[start..])
        }
        i += 1
        try out.Push(t.Done())
    }
    return out
}
