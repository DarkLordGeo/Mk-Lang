// A regex filter, run on both targets.
// args[1] names a file whose first line is an RE2 pattern; patterns are read from files so
// that no shell between the runner and the program (wsl.exe) sees them. Without args[2], it
// prints each line of standard input that holds a match, as "<line>: <text>", then the line's
// matches as "  [lo, hi) <match>" and group 1 when it took part; with args[2], a file whose
// first line is a Replace template, it prints every line through Replace. Then "<n> lines",
// the count of matching lines. A pattern that does not compile prints "bad pattern at offset
// <n>" on stderr and exits 2.
import std.fs
import std.regex
import std.proc

FirstLine(a: &Arena, io: Io, p: str): fs.Error|OutOfMemory!str from a {
    text := try fs.ReadText(a, io, p)
    var lines := text.Lines()
    if l := lines.Next() {
        return l
    }
    return ""
}

export Main(sys: &System): fs.Error|ReadError|WriteError|Utf8Error|OutOfMemory!void {
    var out := sys.Stdout()
    var errw := sys.Stderr()
    if sys.args.Len() < 2 {
        try Print(mut errw, "usage: regexfilter <pattern file> [<template file>]\n")
        try errw.Flush()
        proc.Exit(2)
    }
    pattern := try FirstLine(sys.arena, sys.io, sys.args[1])
    var template := ""
    if sys.args.Len() > 2 {
        template = try FirstLine(sys.arena, sys.io, sys.args[2])
    }
    re := regex.Compile(sys.arena, pattern) else e {
        match e {
            regex.Syntax(offset) {
                try Print(mut errw, "bad pattern at offset {offset}\n", (offset))
            }
            regex.TooLarge {
                try Print(mut errw, "pattern too large\n")
            }
            OutOfMemory {
                try Print(mut errw, "out of memory\n")
            }
            _ {
                Panic("regex.Error gained a member this match does not handle")
            }
        }
        try errw.Flush()
        proc.Exit(2)
    }
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](1 << 20)
    n := try inp.ReadAll(buf)
    text := try str.From(buf[0..n])
    var lines := text.Lines()
    var no := 0
    var kept := 0
    for line := lines.Next() {
        no += 1
        if re.IsMatch(line) {
            kept += 1
        }
        if sys.args.Len() > 2 {
            try Print(mut out, "{no}: {r}\n", (no: no, r: try re.Replace(sys.arena, line, template)))
            continue
        }
        if !re.IsMatch(line) {
            continue
        }
        try Print(mut out, "{no}: {line}\n", (no, line))
        for m in try re.FindAll(sys.arena, line) {
            w := line[m.lo..m.hi]
            try Print(mut out, "  [{lo}, {hi}) {w}\n", (lo: m.lo, hi: m.hi, w))
            if g := re.Group(line, m, 1) {
                try Print(mut out, "    1: {g}\n", (g))
            }
        }
    }
    try Print(mut out, "{kept} lines\n", (kept))
}
