// Why: crontabs and test cases need jobs that behave the same on every
// system, with no shell.
//
// What: jobkit's commands: print, sleep, exit with a code, copy or change
// standard input, leave a child behind, wait for an interrupt.
//
// How: Main dispatches on the first argument to one small function each.

import std.fs
import std.proc
import std.time

const Help = """
    usage: jobkit <job> [args]

      print WORD...        print the words on one line
      lines N WORD         print N numbered lines
      long N               print one line of N bytes
      sleep MS             sleep MS milliseconds
      exit N               exit with status N
      cat                  copy standard input to standard output
      upper                copy standard input in upper case
      env NAME             print an environment variable
      marker PATH MS       after MS milliseconds, write the file PATH
      exists PATH          print whether PATH exists
      hang PATH MS         start `marker PATH MS` as a child, then never end
      trap                 wait for an interrupt, report it and exit 0

    """

export Main(sys: &System): int {
    args := sys.args
    if args.Len() < 2 || args[1] == "--help" {
        sys.Print(Help) else {
            return 1
        }
        return if args.Len() < 2 { 2 } else { 0 }
    }
    rest := args[2..]
    done := match args[1] {
        "print" { Words(sys, rest) }
        "lines" { Lines(sys, rest) }
        "long" { Long(sys, rest) }
        "sleep" { Sleep(sys, rest) }
        "exit" { return Number(rest, 0) else { 2 } }
        "cat" { Copy(sys, false) }
        "upper" { Copy(sys, true) }
        "env" { Env(sys, rest) }
        "marker" { Marker(sys, rest) }
        "exists" { Exists(sys, rest) }
        "hang" { Hang(sys, rest) }
        "trap" { Trap(sys) }
        _ { false }
    }
    if !done {
        sys.PrintErr("jobkit: bad job or arguments; try jobkit --help\n") else {}
        return 2
    }
    return 0
}

/// The integer argument at `i`; none when missing or not a number.
Number(args: []str, i: int): ?int {
    if i >= args.Len() {
        return none
    }
    n := args[i].ParseInt() else {
        return none
    }
    return some(Cast[int](n))
}

Words(sys: &System, words: []str): bool {
    var out := sys.Stdout()
    for i, w in words {
        if i > 0 {
            out.Str(" ") else {
                return false
            }
        }
        out.Str(w) else {
            return false
        }
    }
    out.Str("\n") else {
        return false
    }
    return true
}

Lines(sys: &System, args: []str): bool {
    n := Number(args, 0) else {
        return false
    }
    if args.Len() < 2 {
        return false
    }
    word := args[1]
    for i in 1..n + 1 {
        sys.Print("{word} {i}\n", (word, i)) else {
            return false
        }
    }
    return true
}

Sleep(sys: &System, args: []str): bool {
    ms := Number(args, 0) else {
        return false
    }
    sys.io.Sleep(time.Millis(i64(ms))) else {
        return false
    }
    return true
}

/// Copies standard input to standard output, a line at a time.
Copy(sys: &System, upper: bool): bool {
    var input := sys.Stdin()
    var out := sys.Stdout()
    for {
        line := input.Line(sys.arena) else {
            return false
        }
        text := line else {
            return true
        }
        shown := if upper { text.ToUpper(sys.arena) else { return false } } else { text }
        out.Str(shown) else {
            return false
        }
        out.Str("\n") else {
            return false
        }
        out.Flush() else {
            return false
        }
    }
}

Env(sys: &System, args: []str): bool {
    if args.Len() != 1 {
        return false
    }
    name := args[0]
    value := sys.Env(name) else { "(unset)" }
    sys.Print("{name}={value}\n", (name, value)) else {
        return false
    }
    return true
}

Marker(sys: &System, args: []str): bool {
    ms := Number(args, 1) else {
        return false
    }
    sys.io.Sleep(time.Millis(i64(ms))) else {
        return false
    }
    fs.WriteAll(sys.io, args[0], b"alive\n") else {
        return false
    }
    return true
}

Exists(sys: &System, args: []str): bool {
    if args.Len() != 1 {
        return false
    }
    path := args[0]
    state := match fs.Stat(sys.io, path) {
        ok(_) { "present" }
        err(_) { "absent" }
    }
    sys.Print("{path}: {state}\n", (path, state)) else {
        return false
    }
    return true
}

/// Starts a child in this process's group, then sleeps far longer than
/// any timeout a crontab gives it: a job that hangs and leaves work behind.
Hang(sys: &System, args: []str): bool {
    if args.Len() != 2 {
        return false
    }
    var c := proc.DefaultCommand
    argv := [sys.args[0], "marker", args[0], args[1]]
    c.args = argv[..]
    c.stdin = proc.Stdio.Null
    c.stdout = proc.Stdio.Null
    c.stderr = proc.Stdio.Null
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, sys.io, &c) else e {
        sys.PrintErr("jobkit: {e}\n", (e)) else {}
        return false
    }
    CloseEnds(stdin, stdout, stderr)
    child.Detach()
    sys.Print("hang: started a child, now waiting for ever\n") else {
        return false
    }
    var out := sys.Stdout()
    out.Flush() else {
        return false
    }
    sys.io.Sleep(time.Hours(1)) else {
        return false
    }
    return true
}

Trap(sys: &System): bool {
    sys.Print("trap: waiting for an interrupt\n") else {
        return false
    }
    var out := sys.Stdout()
    out.Flush() else {
        return false
    }
    proc.WaitSignal(sys.io, proc.Signal.Interrupt) else {
        return false
    }
    sys.Print("trap: interrupted, cleaning up\n") else {
        return false
    }
    return true
}

/// Closes the pipe ends a spawn returned; with no stream a pipe, all none.
CloseEnds(i: ?proc.PipeWriter, o: ?proc.PipeReader, e: ?proc.PipeReader) {
    if w := i {
        w.Close()
    }
    if r := o {
        r.Close()
    }
    if r := e {
        r.Close()
    }
}

/// One line of `n` bytes, longer than a line reader's buffer.
Long(sys: &System, args: []str): bool {
    n := Number(args, 0) else {
        return false
    }
    var out := sys.Stdout()
    for _ in 0..n {
        out.Str("x") else {
            return false
        }
    }
    out.Str("\n") else {
        return false
    }
    return true
}
