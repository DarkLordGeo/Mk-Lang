// Why: a Procfile can be tried without writing child programs, and procman's
// own cases need children that behave the same on every OS.
//
// What: `procman demo <name>`: Run and the Programs it offers (Number, Flood,
// IgnoreSignals, Count and others).
//
// How: Procfiles name them as `$PROCMAN demo ...`, so procman re-runs itself
// as the child.

import std.fs
import std.proc
import std.time

/// What `procman demo` can be, with a one-line description each.
export const Programs: []str = [
    "echo WORD...           print the words on one line",
    "exit CODE [MESSAGE]    print MESSAGE to stderr and exit with CODE",
    "flood BYTES            write BYTES bytes of text to stdout, or forever when 0",
    "sleep MS               wait MS milliseconds, then exit 0",
    "ignore-signals MS      ignore Ctrl-C and termination for MS milliseconds",
    "count FILE GOAL        add one to the count in FILE; exit 1 until it reaches GOAL",
    "binary                 write bytes that are not UTF-8",
    "panic                  panic",
]

/// Runs the demo program `args[0]` with the rest of `args`, and returns its
/// exit code: 2 for an unknown program or bad arguments.
export Run(sys: &System, args: []str): WriteError!int {
    if args.Len() == 0 {
        try sys.PrintErr("demo: name a program\n")
        return 2
    }
    name := args[0]
    rest := args[1..]
    if name == "echo" {
        var out := sys.Stdout()
        for i, w in rest {
            if i > 0 {
                try out.Str(" ")
            }
            try out.Str(w)
        }
        try out.Str("\n")
        return 0
    }
    if name == "exit" && rest.Len() >= 1 {
        code := Number(rest[0]) else {
            return try Usage(sys, name)
        }
        if rest.Len() >= 2 {
            try sys.PrintErr("{m}\n", (m: rest[1]))
        }
        return Trunc[int](code)
    }
    if name == "flood" && rest.Len() == 1 {
        bytes := Number(rest[0]) else {
            return try Usage(sys, name)
        }
        try Flood(sys, bytes)
        return 0
    }
    if name == "sleep" && rest.Len() == 1 {
        ms := Number(rest[0]) else {
            return try Usage(sys, name)
        }
        sys.io.Sleep(time.Millis(ms)) else {}
        return 0
    }
    if name == "ignore-signals" && rest.Len() == 1 {
        ms := Number(rest[0]) else {
            return try Usage(sys, name)
        }
        try IgnoreSignals(sys, ms)
        return 0
    }
    if name == "count" && rest.Len() == 2 {
        goal := Number(rest[1]) else {
            return try Usage(sys, name)
        }
        return try Count(sys, rest[0], goal)
    }
    if name == "binary" {
        var out := sys.Stdout()
        try out.Bytes(b"\xff\xfe binary\n")
        return 0
    }
    if name == "panic" {
        Panic("demo panic")
    }
    return try Usage(sys, name)
}

// A non-negative decimal number.
Number(s: str): ?i64 {
    v := s.ParseInt() else {
        return none
    }
    if v < 0 {
        return none
    }
    return some(v)
}

Usage(sys: &System, name: str): WriteError!int {
    for p in Programs {
        if p.StartsWith(name) && p.Len() > name.Len() && p.Byte(name.Len()) == b' ' {
            synopsis := p[..p.Find("  ") else { p.Len() }]
            try sys.PrintErr("usage: procman demo {synopsis}\n", (synopsis))
            return 2
        }
    }
    try sys.PrintErr("demo: no program `{name}`; try `procman demo help`\n", (name))
    return 2
}

// Lines of 64 bytes, the last one cut to make `bytes` exactly.
Flood(sys: &System, bytes: i64): WriteError!void {
    line := "procman demo flood: the same line of text, again and again ....\n"
    var out := sys.Stdout()
    var left := bytes
    for bytes == 0 || left > 0 {
        n := if bytes == 0 || left >= 64 { 64 } else { Trunc[int](left) }
        try out.Str(line[..n])
        left -= i64(n)
    }
}

// Watches both stop signals, so neither has its default action, and reports
// each that arrives while it waits out `ms`.
IgnoreSignals(sys: &System, ms: i64): WriteError!void {
    var interrupts := 0
    var terminations := 0
    scope s := sys.io.Scope() {
        s.go Ignore(sys.io, proc.Signal.Interrupt, mut interrupts)
        s.go Ignore(sys.io, proc.Signal.Terminate, mut terminations)
        sys.io.Sleep(time.Millis(ms)) else {}
        s.Cancel()
    }
    try sys.Print("ignored {interrupts} interrupts and {terminations} terminations\n", (interrupts, terminations))
}

Ignore(io: Io, sig: proc.Signal, count: mut int) {
    for {
        proc.WaitSignal(io, sig) else {
            return
        }
        *count += 1
    }
}

// The count lives in `file` as decimal text; a missing file is 0.
Count(sys: &System, file: str, goal: i64): WriteError!int {
    text := fs.ReadText(sys.arena, sys.io, file) else e {
        match e {
            fs.NotFound { "0" }
            _ {
                try sys.PrintErr("count: {file}: {e}\n", (file, e))
                return 2
            }
        }
    }
    n := text.Trim().ParseInt() else {
        try sys.PrintErr("count: {file} holds no number\n", (file))
        return 2
    }
    next := n + 1
    data := sys.arena.Fmt("{next}\n", (next)) else {
        return 2
    }
    fs.WriteAll(sys.io, file, data.AsBytes()) else e {
        try sys.PrintErr("count: {file}: {e}\n", (file, e))
        return 2
    }
    try sys.Print("attempt {next} of {goal}\n", (next, goal))
    return if next < goal { 1 } else { 0 }
}
