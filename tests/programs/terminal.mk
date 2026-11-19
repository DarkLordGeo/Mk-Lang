// std.term on a pseudo-terminal: the program
// opens a proc.Pty, spawns itself on it as its controlling terminal, and types keys into it. The
// child opens the terminal, puts it in raw mode, reads its size, announces itself, decodes the
// keys into events until q, restores the mode, and writes what it saw to a file the parent
// prints, since a Linux terminal and a Windows pseudo-console draw the same output with different
// bytes. A task drains the terminal's output meanwhile. The parent also checks that its own
// standard output is no terminal, and the bytes of a style, a cursor move and a sanitized string.
import std.atomic
import std.fs
import std.proc
import std.term
import std.time

// Report is the file the child writes what it saw to, in the run directory.
const Report = "terminal-report.txt"

// Describe names an event the way the report prints it.
Describe(a: &Arena, e: &term.Event): OutOfMemory!str from a {
    return match *e {
        term.Event.Key(key: k, ch: c, mods: m) {
            name := match k {
                term.Key.Char { try a.Fmt("char {c}", (c: c)) }
                term.Key.Enter { "enter" }
                term.Key.Escape { "escape" }
                term.Key.Backspace { "backspace" }
                term.Key.Tab { "tab" }
                term.Key.Up { "up" }
                term.Key.Down { "down" }
                term.Key.Left { "left" }
                term.Key.Right { "right" }
                _ { "another key" }
            }
            try a.Fmt("key {name}, ctrl {c}, alt {l}", (name: name, c: m.ctrl, l: m.alt))
        }
        term.Event.Paste(text: t) { try a.Fmt("paste {t}", (t: t)) }
        term.Event.Resize(cols: c, rows: r) { try a.Fmt("resize {c}x{r}", (c: c, r: r)) }
        _ { "another event" }
    }
}

// IsQuit reports whether e is the key q with no modifier.
IsQuit(e: &term.Event): bool {
    return match *e {
        term.Event.Key(key: k, ch: c, mods: m) {
            match k {
                term.Key.Char { c == 'q' && !m.ctrl && !m.alt }
                _ { false }
            }
        }
        _ { false }
    }
}

// Child is the "term" mode: it reads keys on its controlling terminal until q and writes what it
// saw to Report.
Child(sys: &System): term.Error|fs.Error|WriteError!void {
    a := sys.arena
    io := sys.io
    var lines := Text.New(a)
    var t := try term.Open(io)
    onerr t.Close(io)
    try t.Raw(io)
    (cols, rows) := try t.Size(io)
    try lines.Push(try a.Fmt("stdin is a terminal {s}, size {cols}x{rows}\n", (s: term.IsTerminal(io, term.Std.Stdin), cols: cols, rows: rows)))
    var buf: [64]u8 = [0; 64]
    var w := t.Writer(io, mut buf[..])
    try w.Str("READY\r\n")
    try w.Flush()
    for _ in 0..32 {
        e := try t.Read(a, io)
        if IsQuit(&e) {
            break
        }
        try lines.Push(try Describe(a, &e))
        try lines.Push("\n")
    }
    try t.Restore(io)
    t.Close(io)
    try fs.WriteAll(io, Report, lines.Str().AsBytes())
}

// Drain reads the terminal's output until its end, and sets ready once it has seen READY.
Drain(io: Io, pty: &proc.Pty, ready: &atomic.AtomicBool) {
    var buf: [4_096]u8 = [0; 4_096]
    var tail: [8]u8 = [0; 8]
    var kept := 0
    for {
        n := pty.Read(io, mut buf[..]) else {
            return
        }
        if n == 0 {
            return
        }
        for i in 0..n {
            if kept == tail.Len() {
                for j in 1..tail.Len() {
                    tail[j - 1] = tail[j]
                }
                kept -= 1
            }
            tail[kept] = buf[i]
            kept += 1
            if kept >= 5 {
                seen := str.From(tail[kept - 5..kept]) else {
                    ""
                }
                if seen == "READY" {
                    ready.Store(true)
                }
            }
        }
    }
}

// Session spawns the "term" child on pty, waits for it to be ready, types the keys, and waits
// for it to end.
Session(sys: &System, a: &Arena, exe: str, pty: &proc.Pty, ready: &atomic.AtomicBool): proc.Error|Canceled!str from a {
    io := sys.io
    args := [exe, "term"]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.terminal = some(pty)
    (child, stdin, stdout, stderr) := try proc.Spawn(a, io, &c)
    if x := stdin {
        x.Close()
    }
    if x := stdout {
        x.Close()
    }
    if x := stderr {
        x.Close()
    }
    for _ in 0..1_000 {
        if ready.Load() {
            break
        }
        io.Sleep(time.Millis(10)) else {
            break
        }
    }
    keys := ["a", "B", "\x01", "\t", "\x1b[A", "\x1b[D", "\r", "q"]
    for k in keys {
        pty.WriteAll(io, k.AsBytes()) else {
            break
        }
        io.Sleep(time.Millis(30)) else {
            break
        }
    }
    status := child.Wait(io) else e {
        child.Detach()
        fail e
    }
    code := status.code else {
        return "a signal"
    }
    return if code == 0 { "code 0" } else { "another code" }
}

export Main(sys: &System): WriteError!void {
    a := sys.arena
    io := sys.io
    if sys.args.Len() > 1 && sys.args[1] == "term" {
        Child(sys) else e {
            why := a.Fmt("child: {e}\n", (e: e)) else {
                "child failed\n"
            }
            fs.WriteAll(io, Report, why.AsBytes()) else {
            }
            proc.Exit(1)
        }
        return
    }
    try sys.Print("parent's stdout is a terminal {t}, uses color {c}\n", (t: term.IsTerminal(io, term.Std.Stdout), c: term.UseColor(io, term.Std.Stdout)))
    var text := Text.New(a)
    var w := text.Writer()
    style := term.Style(fg: some(term.Color.Ansi(n: 2)), bg: none, bold: true, underline: false, reverse: false)
    try style.Format(mut w)
    try term.MoveTo(mut w, 2, 4)
    try term.Sanitize(mut w, "bell\x07 and escape\x1b[2J kept\tline\n")
    try w.Flush()
    var shown := Text.New(a)
    for c in text.Str().Scalars() {
        if c == '\x1b' {
            shown.Push("ESC") else {
            }
        } else {
            shown.PushChar(c) else {
            }
        }
    }
    try sys.Print("styled bytes: {s}\n", (s: shown.Str().Trim()))
    fs.Remove(io, Report) else {
    }
    exe := fs.Absolute(a, io, sys.args[0]) else e {
        try sys.Print("absolute: {e}\n", (e: e))
        return
    }
    pty := proc.Pty.Open(io, 80, 24) else e {
        try sys.Print("pty: {e}\n", (e: e))
        return
    }
    ready := atomic.AtomicBool.New(false)
    var ended := ""
    scope s := io.Scope() {
        s.go Drain(io, &pty, &ready)
        ended = Session(sys, a, exe, &pty, &ready) else e {
            a.Fmt("session: {e}", (e: e)) else {
                "session failed"
            }
        }
    }
    pty.Close()
    try sys.Print("child ended with {ended}\n", (ended: ended))
    report := fs.ReadText(a, io, Report) else e {
        try sys.Print("report: {e}\n", (e: e))
        return
    }
    try sys.Print("{report}", (report: report))
}
