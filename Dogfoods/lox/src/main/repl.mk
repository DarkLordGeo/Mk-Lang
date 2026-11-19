// Why: Interactive use: each line of standard input is an entry, run in the
// same VM, so globals and functions persist.
//
// What: Repl, and Command for the lines that start with `:`.
//
// How: Reads a line, compiles it as a script function, and runs it; a compile
// or runtime error is reported and the session goes on.

/// Reads entries from `input` until its end, keeping each in the session's arena. Returns
/// 0, or 74 when the input fails.
Repl(s: mut Session, t: mut Term, input: mut Reader): WriteError!int *s into s {
    var n := 0
    for {
        try t.out.Str("> ")
        try t.out.Flush()
        // An entry lives as long as the program: its functions and strings stay reachable.
        line := input.Line(s.a) else e {
            try t.out.Str("\n")
            try Print(mut t.err, "lox: standard input: {e}\n", (e))
            return IoError
        }
        entry := line else {
            try t.out.Str("\n")
            return 0
        }
        n += 1
        if entry.StartsWith(":") {
            try Command(s, t, entry)
            continue
        }
        s.Load(entry, n)
        _ = try s.Run(t)
    }
}

/// Runs a REPL command: `:globals [lo hi]`, `:forget name`, `:help`.
Command(s: mut Session, t: mut Term, entry: str): WriteError!void {
    var words := entry.Words()
    cmd := words.Next() else { ":" }
    match cmd {
        ":globals" {
            lo := words.Next() else { "" }
            hi := words.Next() else { "~" }
            s.vm.ListGlobals(mut t.out, lo, hi) else e {
                try Print(mut t.err, "lox: {e}\n", (e))
            }
        }
        ":forget" {
            name := words.Next() else {
                try t.err.Str(":forget needs a name\n")
                return
            }
            if !s.vm.Forget(name) {
                try Print(mut t.err, "no global '{name}'\n", (name))
            }
        }
        ":help" { try t.out.Str(":globals [lo hi]  list globals, or those in [lo, hi)\n:forget name      remove a global\n") }
        _ { try Print(mut t.err, "unknown command {cmd}; try :help\n", (cmd)) }
    }
}
