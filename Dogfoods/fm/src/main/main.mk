// Why: the entry point and the help text users see.
// What: Main, One, Batch, Interactive and Report, which maps errors to exit
// codes (0 done, 1 failed, 2 usage, 3 memory budget, 124 timeout, 130
// interrupted).
// How: with no command fm starts the two-pane UI (ui.Run); otherwise it runs
// one command, or a batch of them, one a line.

import std.fs
import std.proc
import std.term
import std.time
import src.clock
import src.ui

const Help: str = """
    fm: a two-pane terminal file manager and pager

    usage:
      fm [--zone Z] [--transcript FILE] [LEFT [RIGHT]]
      fm [--zone Z] COMMAND [options] ARGS...

    With no command, fm runs on the terminal with LEFT and RIGHT (default:
    the current directory) in its two panes. Keys:
      up/down j/k pgup/pgdn home/end   move        enter/right  open
      backspace/left h                 parent       tab          other pane
      c F5 copy, m F6 move, d F8 delete (each asks y/n), n F7 new directory
      l link in the other pane, ! run a shell command, g go to, t temp dir
      = the same directory in both panes, s sizes of both trees, r reload,
      q F10 quit, ctrl-c quit (exit 130)
    A paste goes to the pasted directory; a click selects a row and the
    wheel moves. In the pager: up/down j/k, pgup/pgdn b/space, home/end
    g/G, q or esc closes.

    commands:
      ls [--times] [--depth N] [--skip NAME] DIR   list the tree under DIR
      view [--width N] [--mem BYTES] FILE          wrap FILE to N columns (80)
      cp [--yes] SRC DST                           copy a file, link or tree
      mv SRC DST                                   move or rename
      rm [--yes] PATH                              remove a file or a tree
      mkdir PATH                                   make PATH and its parents
      link TARGET LINK                             make a symbolic link
      stat PATH                                    what PATH is, links unfollowed
      touch [--bytes] TIME PATH                    set PATH's time (RFC 3339)
      du [--timeout MS] DIR...                     tree sizes, one task each
      pwd                                          the current directory
      batch [FILE]                                 run commands, one a line
      selftest --seed N                            a scripted session, simulated

    rm and cp ask on standard input before they remove or overwrite.
    --zone is local (default), utc, or +HH:MM; it sets how times show.
    --transcript FILE records one line per terminal event.

    exit codes: 0 done, 1 failed, 2 bad usage, 3 over the --mem budget,
    124 past --timeout, 130 interrupted.
    """

export Main(sys: &System): int {
    args := ParseArgs(sys.arena, sys.args[1..]) else e {
        what := match e {
            BadArg(index) { sys.args[index + 1] }
            MissingValue(index) { sys.args[index + 1] }
            OutOfMemory { "" }
        }
        sys.PrintErr("fm: bad option {what}: {e}\nfm: see fm --help\n", (what, e)) else {}
        return 2
    }
    if args.help {
        sys.Print("{Help}", (Help)) else {
            return 1
        }
        return 0
    }
    choice := clock.ParseZone(args.zone) else {
        sys.PrintErr("fm: --zone {z}: expected local, utc or +HH:MM\n", (z: args.zone)) else {}
        return 2
    }
    match args.command {
        "" { return Interactive(sys, &args, choice) }
        "batch" { return Batch(sys, &args, choice) }
        "selftest" { return SelfTest(sys, &args) }
        _ { return One(sys, &args, choice) }
    }
}

// Loads the zone times show in; the system's local zone falls back to UTC
// with a warning when it cannot be read.
Zone(sys: &System, choice: clock.ZoneChoice): time.Zone from sys.arena {
    return clock.LoadZone(sys.arena, sys.io, choice) else e {
        sys.PrintErr("fm: local zone: {e}; showing UTC\n", (e)) else {}
        time.Zone.Utc()
    }
}

// One command from the command line; its questions read standard input.
One(sys: &System, args: &Args, choice: clock.ZoneChoice): int {
    zone := Zone(sys, choice)
    env := Env(arena: sys.arena, heap: sys.heap, io: sys.io, zone: &zone, root: args.root)
    var input := sys.Stdin()
    var out := sys.Stdout()
    Exec(&env, args, mut input, mut out) else e {
        return Report(sys, args.command, e)
    }
    return 0
}

// Prints a command's error and gives its exit code.
Report(sys: &System, command: str, e: CmdError): int {
    match e {
        Usage {
            sys.PrintErr("fm: {command}: wrong arguments; see fm --help\n", (command)) else {}
            return 2
        }
        Budget {
            sys.PrintErr("fm: {command}: the --mem budget is too small\n", (command)) else {}
            return 3
        }
        time.TimedOut {
            sys.PrintErr("fm: {command}: timed out\n", (command)) else {}
            return 124
        }
        _ {
            sys.PrintErr("fm: {command}: {e}\n", (command, e)) else {}
            return 1
        }
    }
}

// batch: runs the commands of FILE, or of standard input, one a line;
// blank lines and lines starting with # are skipped, a question reads the
// next line, and the first failure stops the run with its exit code.
Batch(sys: &System, args: &Args, choice: clock.ZoneChoice): int {
    if args.words.Len() > 1 {
        return Report(sys, "batch", Usage)
    }
    zone := Zone(sys, choice)
    env := Env(arena: sys.arena, heap: sys.heap, io: sys.io, zone: &zone, root: args.root)
    var out := sys.Stdout()
    if args.words.Len() == 0 {
        var input := sys.Stdin()
        return RunLines(sys, &env, mut input, mut out)
    }
    var f := fs.Open(sys.io, args.words[0]) else e {
        sys.PrintErr("fm: batch: {p}: {e}\n", (p: args.words[0], e)) else {}
        return 1
    }
    var buf: [65536]u8 = [0; 65536]
    var input := f.Reader(sys.io, mut buf[..])
    code := RunLines(sys, &env, mut input, mut out)
    f.Close(sys.io) else {
        f.Abandon()
    }
    return code
}

RunLines(sys: &System, env: &Env, input: mut Reader, out: mut Writer): int {
    var n := 0
    for {
        var line_arena := Arena.New(sys.heap) else {
            return Report(sys, "batch", OutOfMemory)
        }
        defer line_arena.Free()
        line := input.Line(&line_arena) else e {
            return Report(sys, "batch", e)
        }
        text := line else {
            return 0
        }
        n += 1
        t := text.Trim()
        if t.Len() == 0 || t.StartsWith("#") {
            continue
        }
        words := SplitLine(&line_arena, t) else {
            return Report(sys, "batch", OutOfMemory)
        }
        args := ParseArgs(&line_arena, words[..]) else {
            sys.PrintErr("fm: batch line {n}: bad option\n", (n)) else {}
            return 2
        }
        if args.command.Len() == 0 || args.command == "batch" || args.command == "selftest" {
            sys.PrintErr("fm: batch line {n}: not a command: {t}\n", (n, t)) else {}
            return 2
        }
        Print(out, "> {t}\n", (t)) else {
            return 1
        }
        line_env := Env(arena: &line_arena, heap: env.heap, io: env.io, zone: env.zone, root: env.root)
        Exec(&line_env, &args, input, out) else e {
            out.Flush() else {}
            where := line_arena.Fmt("batch line {n}: {c}", (n, c: args.command)) else {
                args.command
            }
            return Report(sys, where, e)
        }
    }
}

// The file manager on the terminal. Standard input and output must be a
// terminal: a program in a pipe uses the commands.
Interactive(sys: &System, args: &Args, choice: clock.ZoneChoice): int {
    if !term.IsTerminal(sys.io, term.Std.Stdin) || !term.IsTerminal(sys.io, term.Std.Stdout) {
        sys.PrintErr("fm: standard input and output must be a terminal; for a pipe use a command (fm --help)\n") else {}
        return 2
    }
    if args.words.Len() > 2 {
        return Report(sys, "fm", Usage)
    }
    left := if args.words.Len() > 0 { args.words[0] } else { "." }
    right := if args.words.Len() > 1 { args.words[1] } else { left }
    o := ui.Options(left: left, right: right, zone: choice)
    path := args.transcript else {
        if Target.os == Os.Windows { "NUL" } else { "/dev/null" }
    }
    var f := fs.Create(sys.io, path) else e {
        sys.PrintErr("fm: transcript {path}: {e}\n", (path, e)) else {}
        return 1
    }
    var buf: [4096]u8 = [0; 4096]
    var log := f.Writer(sys.io, mut buf[..])
    code := ui.Run(sys.heap, sys.io, &o, mut log) else e {
        f.Abandon()
        match e {
            term.NotTerminal { sys.PrintErr("fm: there is no controlling terminal to open\n") else {} }
            term.Device { sys.PrintErr("fm: the terminal failed: {e}\n", (e)) else {} }
            term.Unsupported { sys.PrintErr("fm: the terminal cannot do what fm needs: {e}\n", (e)) else {} }
            _ { sys.PrintErr("fm: {e}\n", (e)) else {} }
        }
        return 1
    }
    f.Close(sys.io) else e {
        f.Abandon()
        sys.PrintErr("fm: transcript {path}: {e}\n", (path, e)) else {}
        return 1
    }
    if code != 0 {
        proc.Exit(code)
    }
    return 0
}
