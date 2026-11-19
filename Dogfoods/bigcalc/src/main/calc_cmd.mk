// Why: the calculator runs from arguments or from a script, under memory
// and time limits.
//
// What: Calc (`calc`), Run (`run`), the exit statuses and ExitFor.
//
// How: each line runs in an arena reset after it; `--mem-limit` puts the
// kept state in a fixed buffer and `--max-time` sets a scope deadline.

import std.fs
import std.mem
import std.time
import src.calc

const CalcValued: []str = ["--base", "--max-digits", "--max-bits", "--mem-limit", "--max-time"]
const CalcSwitches: []str = ["--keep-going"]

// The exit codes: an evaluation error, a memory limit and a time limit each
// have their own, and a check that fails (a signature, a tag, a chain) too.
const ExitError: int = 1
const ExitUsage: int = 2
const ExitMemory: int = 3
const ExitTime: int = 4
const ExitInvalid: int = 5

// Reads the options both commands share.
Settings(args: &Args): BadOptionValue!calc.Settings {
    d := calc.DefaultSettings()
    return calc.Settings(
        base: try args.Int("--base", d.base, 2, 36),
        max_digits: try args.Int("--max-digits", d.max_digits, 1, 100_000_000),
        max_bits: try args.Int("--max-bits", d.max_bits, 64, 1_000_000_000),
    )
}

// What a failed line exits with.
ExitFor(e: calc.Error): int {
    return match e {
        OutOfMemory { ExitMemory }
        Canceled { ExitTime }
        _ { ExitError }
    }
}

// The bytes --mem-limit gives the stack and the variables, 0 for no limit,
// or -1 after reporting a bad value.
MemLimit(sys: &System, args: &Args): int {
    return args.Int("--mem-limit", 0, 0, 1 << 40) else {
        sys.PrintErr("bigcalc: --mem-limit takes a byte count\n") else {}
        -1
    }
}

/// Calc evaluates each argument as one line and prints the top of the stack
/// at the end.
Calc(sys: &System, argv: []str): int *argv into sys {
    args := ParseArgs(sys.arena, argv, CalcValued, CalcSwitches) else e {
        return Usage(sys, argv, e)
    }
    settings := Settings(&args) else {
        sys.PrintErr("bigcalc: a number option is out of range\n") else {}
        return ExitUsage
    }
    limit := MemLimit(sys, &args)
    if limit < 0 {
        return ExitUsage
    }
    buf := sys.arena.Slice[u8](limit) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    var fixed := mem.Fixed.New(buf)
    a := if limit > 0 { fixed.Alloc() } else { sys.heap.Alloc() }
    return CalcIn(sys, a, settings, args.positional[..])
}

// Runs `lines` on a machine whose numbers live in `a`. A function of its own,
// so the machine's stores reach the allocator `a` and stop there, whatever
// buffer a budget's allocator was made over.
CalcIn(sys: &System, a: &Alloc, settings: calc.Settings, lines: []str): int {
    var m := calc.Machine.New(a, settings)
    code := CalcLines(sys, a, mut m, lines)
    m.Free(a)
    return code
}

CalcLines(sys: &System, a: &Alloc into m, m: mut calc.Machine, lines: []str): int {
    var arena := Arena.New(sys.heap) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    defer arena.Free()
    var out := sys.Stdout()
    for i, line in lines {
        m.Line(a, &arena, sys.io, mut out, line) else e {
            sys.PrintErr("bigcalc: argument {n}: {e}\n", (n: i + 1, e)) else {}
            return ExitFor(e)
        }
        arena.Reset()
    }
    if m.Depth() > 0 {
        m.Line(a, &arena, sys.io, mut out, "p") else e {
            sys.PrintErr("bigcalc: {e}\n", (e)) else {}
            return ExitFor(e)
        }
    }
    return 0
}

/// Run evaluates a script, one line at a time, from a file or `-` for
/// standard input. --keep-going reports a failed line and goes on;
/// --max-time stops the script after that many milliseconds.
Run(sys: &System, argv: []str): int *argv into sys {
    args := ParseArgs(sys.arena, argv, CalcValued, CalcSwitches) else e {
        return Usage(sys, argv, e)
    }
    if args.positional.Len() != 1 {
        sys.PrintErr("bigcalc: run takes one script, or - for standard input\n") else {}
        return ExitUsage
    }
    settings := Settings(&args) else {
        sys.PrintErr("bigcalc: a number option is out of range\n") else {}
        return ExitUsage
    }
    max_time := args.Int("--max-time", 0, 0, 86_400_000) else {
        sys.PrintErr("bigcalc: --max-time takes milliseconds\n") else {}
        return ExitUsage
    }
    limit := MemLimit(sys, &args)
    if limit < 0 {
        return ExitUsage
    }
    path := args.positional[0]
    buf := sys.arena.Slice[u8](65_536) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    budget := sys.arena.Slice[u8](limit) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    var fixed := mem.Fixed.New(budget)
    a := if limit > 0 { fixed.Alloc() } else { sys.heap.Alloc() }
    job := Job(name: path, keep_going: args.Has("--keep-going"), max_time: max_time)
    return RunIn(sys, a, settings, buf, &job)
}

// Runs the script `job` names on a machine whose numbers live in `a`, reading
// it through `buf`. A function of its own, as `CalcIn` is.
RunIn(sys: &System, a: &Alloc, settings: calc.Settings, buf: mut []u8, job: &Job): int {
    path := job.name
    var m := calc.Machine.New(a, settings)
    defer m.Free(a)
    if path == "-" {
        var input := sys.Stdin()
        return Timed(sys, a, mut m, mut input, job)
    }
    var f := fs.Open(sys.io, path) else e {
        sys.PrintErr("bigcalc: {path}: {e}\n", (path, e)) else {}
        return ExitError
    }
    var input := f.Reader(sys.io, buf)
    code := Timed(sys, a, mut m, mut input, job)
    f.Close(sys.io) else {
        f.Abandon()
    }
    return code
}

// How a script runs: its name for messages, whether a failed line stops it,
// and its time limit in milliseconds, 0 for none.
struct Job {
    export name: str
    export keep_going: bool
    export max_time: int
}

// Runs the script under a deadline when the job has a time limit.
Timed(sys: &System, a: &Alloc into m, m: mut calc.Machine, input: mut Reader, job: &Job): int {
    if job.max_time == 0 {
        return Script(sys, a, m, input, job)
    }
    var code := 0
    scope s := sys.io.Scope() {
        s.CancelAfter(time.Millis(Cast[i64](job.max_time)))
        code = Script(sys, a, m, input, job)
        if s.Expired() {
            sys.PrintErr("bigcalc: {job.name}: stopped after --max-time {job.max_time} ms\n", (job)) else {}
        }
    }
    return code
}

// Evaluates every line of input; the line arenas are reset after each.
Script(sys: &System, a: &Alloc into m, m: mut calc.Machine, input: mut Reader, job: &Job): int {
    var arena := Arena.New(sys.heap) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    defer arena.Free()
    var lines := Arena.New(sys.heap) else {
        return Stop(sys, "out of memory", ExitMemory)
    }
    defer lines.Free()
    var out := sys.Stdout()
    var n := 0
    var code := 0
    for {
        line := input.Line(&lines) else e {
            sys.PrintErr("bigcalc: {job.name}: line {n}: {e}\n", (job, n: n + 1, e)) else {}
            return ExitError
        }
        text := line else {
            break
        }
        n += 1
        m.Line(a, &arena, sys.io, mut out, text) else e {
            sys.PrintErr("bigcalc: {job.name}:{n}: {e}\n", (job, n, e)) else {}
            code = ExitFor(e)
            if !job.keep_going || code != ExitError {
                return code
            }
        }
        arena.Reset()
        lines.Reset()
    }
    return code
}
