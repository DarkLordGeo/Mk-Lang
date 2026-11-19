// Why: The commands take positional arguments and options; parsing them is kept
// apart from the commands.
//
// What: The command line: a command, its positional arguments, and `--name
// value` options, which may also be written `--name=value`.
//
// How: Parse fills Options, each field holding its default when its option is
// missing; a bad argument is a Usage error naming its position and the Problem.

import std.math
import std.sim
import std.time

/// What is wrong with an argument.
enum Problem {
    UnknownOption
    MissingValue
    BadValue
    UnknownCommand
    WrongCount
    NoCommand
}

/// Argument `arg` (1 for the first after the program's name) is wrong.
error Usage(arg: int, problem: Problem)

/// The parsed command line. Defaults are the values a missing option gives.
struct Options {
    command: str
    positional: List[str]
    help: bool
    ascii: bool
    wide: bool
    single: bool
    double: bool
    band: int
    max_time: ?time.Duration
    mem_limit: ?int
    seed: ?u64
    fault: ?sim.Fault
    samples: ?int
    width: ?int
    height: ?int
    from: f64
    to: f64
    low: ?f64
    high: ?f64
}

Problem.Text(self): str {
    return match self {
        .UnknownOption { "unknown option" }
        .MissingValue { "option needs a value" }
        .BadValue { "bad value" }
        .UnknownCommand { "unknown command" }
        .WrongCount { "wrong number of arguments for the command" }
        .NoCommand { "no command given" }
    }
}

// The options that take a value.
const Valued: []str = ["--band", "--max-time", "--mem-limit", "--seed", "--fault", "--samples", "--size", "--from", "--to", "--ymin", "--ymax"]

Int(text: str, arg: int, lo: i64, hi: i64): Usage!int {
    n := text.ParseInt() else { fail Usage(arg: arg, problem: Problem.BadValue) }
    if n < lo || n > hi {
        fail Usage(arg: arg, problem: Problem.BadValue)
    }
    return Cast[int](n)
}

Real(text: str, arg: int): Usage!f64 {
    x := text.ParseFloat() else { fail Usage(arg: arg, problem: Problem.BadValue) }
    if !math.IsFinite(x) {
        fail Usage(arg: arg, problem: Problem.BadValue)
    }
    return x
}

/// Parses `args`, the arguments after the program's name. Fails with the
/// first wrong argument and what is wrong with it. O(n); allocates the
/// positional list in `a`.
Parse(a: &Arena, args: []str): Usage|OutOfMemory!Options from a, args *args into a {
    var o := Options(command: "", positional: List[str].New(a), help: false, ascii: false, wide: false, single: false, double: false, band: 4, max_time: none, mem_limit: none, seed: none, fault: none, samples: none, width: none, height: none, from: -5.0, to: 5.0, low: none, high: none)
    var i := 0
    for i < args.Len() {
        arg := args[i]
        n := i + 1
        i += 1
        if !arg.StartsWith("--") || arg == "--" {
            if o.command.Len() == 0 {
                o.command = arg
            } else {
                try o.positional.Push(arg)
            }
            continue
        }
        // Split `--name=value`; otherwise a valued option takes the next
        // argument.
        var name := arg
        var value := ""
        if eq := arg.Find("=") {
            name = arg[..eq]
            value = arg[eq + 1..]
        } else if Valued.Has(&arg) {
            if i == args.Len() {
                fail Usage(arg: n, problem: Problem.MissingValue)
            }
            value = args[i]
            i += 1
        }
        match name {
            "--help" { o.help = true }
            "--ascii" { o.ascii = true }
            "--16" { o.wide = true }
            "--f32" { o.single = true }
            "--f64" { o.double = true }
            "--band" { o.band = try Int(value, n, 1, 4096) }
            "--max-time" { o.max_time = some(time.Millis(Cast[i64](try Int(value, n, 0, 1 << 40)))) }
            "--mem-limit" { o.mem_limit = some(try Int(value, n, 1, 1 << 32)) }
            "--seed" { o.seed = some(Cast[u64](try Int(value, n, 0, 1 << 62))) }
            "--fault" {
                o.fault = some(match value {
                    "nospace" { sim.Fault.NoSpace }
                    "denied" { sim.Fault.Denied }
                    _ { fail Usage(arg: n, problem: Problem.BadValue) }
                })
            }
            "--samples" { o.samples = some(try Int(value, n, 1, 4096)) }
            "--size" {
                x := value.Find("x") else { fail Usage(arg: n, problem: Problem.BadValue) }
                o.width = some(try Int(value[..x], n, 1, 4096))
                o.height = some(try Int(value[x + 1..], n, 1, 4096))
            }
            "--from" { o.from = try Real(value, n) }
            "--to" { o.to = try Real(value, n) }
            "--ymin" { o.low = some(try Real(value, n)) }
            "--ymax" { o.high = some(try Real(value, n)) }
            _ { fail Usage(arg: n, problem: Problem.UnknownOption) }
        }
    }
    return o
}

test "options take their value after a space or an equals sign" (sys: &System) {
    args := ["plot", "--from", "-2", "sin(x)", "--to=3", "out.ppm", "--f32"]
    o := try Parse(sys.arena, args[..])
    Assert(o.command == "plot" && o.positional.Len() == 2)
    Assert(o.from == -2.0 && o.to == 3.0 && o.single)
}

test "a valued option at the end is MissingValue" (sys: &System) {
    args := ["render", "--band"]
    _ = Parse(sys.arena, args[..]) else e {
        match e {
            Usage(arg, problem) {
                Assert(arg == 2 && problem == Problem.MissingValue)
                return
            }
            _ {
                Assert(false)
            }
        }
    }
    Assert(false)
}
