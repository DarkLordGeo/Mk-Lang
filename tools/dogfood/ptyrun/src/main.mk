// Why: tools/dogfood's terminal cases (Dogfoods/README.md, "Terminal
// cases") need a program run on a real pseudo-terminal, typed at by a
// script, with one result line the harness can read.
//
// What: Main, ParseArgs and Options for the command line, Drain for the
// terminal's output, and the step loop with its Outcome, Why and Fed:
//
//     ptyrun [--limit MS] [--raw FILE] [--transcript FILE] --script FILE -- PROGRAM [ARGS...]
//
// How: it opens a proc.Pty of the script's size, starts the program on it
// with Command.terminal, types the script's keys, pastes and resizes, waits
// where it says, and then waits for the program to exit, within --limit.
// Its standard output is one line, which the harness reads: `exit N`,
// `signal NAME`, or `error WHY` when ptyrun failed (bad usage or script, no
// terminal, the program would not start, an `expect` or the program
// outlasted the limit, the program ended before an `expect` was met, or a
// signal proc.Status does not name ended it); its exit status is 0 for
// the first two and 2 for an error. The terminal's own bytes are never
// judged: a Windows pseudo-console writes its rendering of the screen, not
// the program's bytes; --raw keeps them for a person reading a failure. A
// case judges the transcript, which the program writes, and the exit code.
// The --transcript file is removed before the program starts, so an
// `expect` counts only the lines this run's program writes.

import std.fs
import std.proc
import std.time

/// The time ptyrun gives the script and the program when --limit is absent.
const DefaultLimit: i64 = 20000

/// How often an `expect` reads the transcript, in milliseconds.
const PollMillis: i64 = 10

export Main(sys: &System): WriteError!int {
    return try Run(sys)
}

/// What ptyrun's command line says.
struct Options {
    limit_ms: i64
    script: str
    raw: str
    transcript: str
    program: []str
}

/// Prints ptyrun's one error line and gives its exit status.
Fail(sys: &System, why: str): WriteError!int {
    try sys.Print("error {why}\n", (why))
    return 2
}

Run(sys: &System): WriteError!int {
    io := sys.io
    o := ParseArgs(sys.args) else {
        return try Fail(sys, "usage: ptyrun [--limit MS] [--raw FILE] [--transcript FILE] --script FILE -- PROGRAM [ARGS...]")
    }
    text := fs.ReadText(sys.arena, io, o.script) else e {
        try sys.Print("error cannot read the script {p}: {e}\n", (p: o.script, e))
        return 2
    }
    script := Parse(sys.arena, text) else e {
        try sys.Print("error the script {p} does not parse: {e}\n", (p: o.script, e))
        return 2
    }
    // A transcript left by an earlier run would meet each `expect` at once,
    // so the keys would reach the program before it is ready: it is removed
    // before the program starts.
    fs.Remove(io, o.transcript) else e {
        match e {
            fs.NotFound {}
            _ {
                try sys.Print("error cannot remove the old transcript {p}: {e}\n", (p: o.transcript, e))
                return 2
            }
        }
    }
    start := time.Now(io)
    limit_ns := o.limit_ms * 1000000
    p := proc.Pty.Open(io, script.cols, script.rows) else e {
        try sys.Print("error cannot open a pseudo-terminal: {e}\n", (e))
        return 2
    }
    var c := proc.DefaultCommand
    c.args = o.program
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else e {
        p.Close()
        try sys.Print("error cannot start {prog}: {e}\n", (prog: o.program[0], e))
        return 2
    }
    CloseEnds(stdin, stdout, stderr)
    var fed := Fed(out: Done(), end: End.Lost)
    scope s := io.Scope() {
        s.go Drain(io, &p, o.raw)
        fed = Feed(io, &p, script.steps, start, limit_ns, o.transcript, child)
        s.Cancel()
    }
    p.Close()
    out := fed.out
    if out.failed {
        why := WhyText(out.why)
        if out.have < 0 {
            try sys.Print("error script line {line}: {why}\n", (line: out.line, why))
            return 2
        }
        match fed.end {
            .Ended(st) {
                how := EndText(sys.arena, st)
                try sys.Print("error script line {line}: {why} ({how}); the transcript has {have} lines\n", (line: out.line, why, how, have: out.have))
            }
            _ { try sys.Print("error script line {line}: {why}; the transcript has {have} lines\n", (line: out.line, why, have: out.have)) }
        }
        return 2
    }
    match fed.end {
        .Ended(st) {
            if code := st.code {
                try sys.Print("exit {code}\n", (code))
                return 0
            }
            if sig := st.signal {
                try sys.Print("signal {n}\n", (n: SignalName(sig)))
                return 0
            }
            // proc.Status names three signals; a Linux child ended by any
            // other (SIGKILL, SIGSEGV, SIGABRT, ...) has neither field.
            return try Fail(sys, "the program was ended by a signal other than Hangup, Interrupt or Terminate, which proc.Status does not name")
        }
        .Killed { return try Fail(sys, "the program did not exit within --limit; it was killed") }
        .Lost { return try Fail(sys, "the program could not be waited for or killed; it may still run") }
    }
}

/// The command line, or none when it is not ptyrun's usage.
ParseArgs(args: []str): ?Options from args {
    var o := Options(limit_ms: DefaultLimit, script: "", raw: "", transcript: "transcript", program: args[0..0])
    var i := 1
    for i < args.Len() {
        a := args[i]
        if a == "--" {
            o.program = args[i + 1..]
            break
        }
        if i + 1 >= args.Len() {
            return none
        }
        v := args[i + 1]
        i += 2
        match a {
            "--limit" {
                n := v.ParseInt() else {
                    return none
                }
                if n < 1 {
                    return none
                }
                o.limit_ms = n
            }
            "--script" { o.script = v }
            "--raw" { o.raw = v }
            "--transcript" { o.transcript = v }
            _ { return none }
        }
    }
    if o.script.Len() == 0 || o.program.Len() == 0 {
        return none
    }
    return some(o)
}

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

/// Reads what the program writes to its terminal until the end or a
/// cancel, so it never blocks on a full terminal, and keeps the bytes in
/// raw when raw names a file.
Drain(io: Io, p: &proc.Pty, raw: str) {
    var buf: [8192]u8 = [0; 8192]
    if raw.Len() == 0 {
        for {
            n := p.Read(io, mut buf[..]) else {
                return
            }
            if n == 0 {
                return
            }
        }
    }
    var f := fs.Create(io, raw) else {
        return
    }
    for {
        n := p.Read(io, mut buf[..]) else {
            break
        }
        if n == 0 {
            break
        }
        f.WriteAll(io, buf[..n]) else {}
    }
    f.Close(io) else {
        f.Abandon()
    }
}

/// How feeding the script ended: every step done, or the step that failed,
/// why, and for an `expect` the lines the transcript had (-1 otherwise).
struct Outcome {
    failed: bool
    line: int
    why: Why
    have: int
}

/// Why a step failed.
enum Why {
    Write
    Resize
    ExpectTime
    SleepTime
    /// The program ended while an `expect` waited, and the transcript did
    /// not hold the lines.
    ExpectEnded
}

WhyText(w: Why): str {
    return match w {
        .Write { "writing to the terminal failed" }
        .Resize { "resizing the terminal failed" }
        .ExpectTime { "expect: the time ran out" }
        .SleepTime { "sleep: the time ran out" }
        .ExpectEnded { "expect: the program ended first" }
    }
}

Failed(line: int, why: Why, have: int): Outcome {
    return Outcome(failed: true, line: line, why: why, have: have)
}

/// Every step done.
Done(): Outcome {
    return Outcome(failed: false, line: 0, why: Why.Write, have: -1)
}

/// How the script went and how the program ended.
struct Fed {
    out: Outcome
    end: End
}

/// Runs the steps in order against the terminal, each within what is left
/// of limit_ns since start, then waits for the child within what is left
/// (Finish). A failed step kills the child. An `expect` waits on the child
/// as it polls the transcript, so a program that ends before the lines
/// come ends the wait at once (Ended), not at the limit.
Feed(io: Io, p: &proc.Pty, steps: []Step, start: time.Instant, limit_ns: i64, transcript: str, child: proc.Child): Fed {
    for k in 0..steps.Len() {
        line := steps[k].line
        match steps[k].act {
            .Input(bytes) {
                p.WriteAll(io, bytes) else {
                    return Fed(out: Failed(line, Why.Write, -1), end: Kill(io, child))
                }
            }
            .Resize(cols, rows) {
                p.Resize(io, cols, rows) else {
                    return Fed(out: Failed(line, Why.Resize, -1), end: Kill(io, child))
                }
            }
            .Expect(lines) {
                for {
                    have := TranscriptLines(io, transcript)
                    if have >= lines {
                        break
                    }
                    if time.Now(io).Since(start).Nanos() >= limit_ns {
                        return Fed(out: Failed(line, Why.ExpectTime, have), end: Kill(io, child))
                    }
                    // The poll's pause is a wait on the child, cut at
                    // PollMillis; a cut Wait leaves the child running and
                    // ours, and Wait can be called again.
                    scope w := io.Scope() {
                        w.CancelAfter(time.Millis(PollMillis))
                        st := child.Wait(io) else {
                            // Canceled at the cut, or another error: the
                            // sleep ends at the same cut, so the poll never
                            // spins.
                            io.Sleep(time.Millis(PollMillis)) else {}
                            continue
                        }
                        return Ended(io, steps, k, transcript, st)
                    }
                }
            }
            .Sleep(ms) {
                left := limit_ns - time.Now(io).Since(start).Nanos()
                if i64(ms) * 1000000 >= left {
                    return Fed(out: Failed(line, Why.SleepTime, -1), end: Kill(io, child))
                }
                io.Sleep(time.Millis(i64(ms))) else {}
            }
        }
    }
    return Fed(out: Done(), end: Finish(io, child, limit_ns - time.Now(io).Since(start).Nanos()))
}

/// The rest of the script from step k, an `expect`, once the program has
/// ended with st: the transcript is final, so each `expect` left is judged
/// on it at once (the first again, since the program may have written its
/// last line just before it ended), and the steps that type, resize or
/// sleep are skipped, with no program left to see them.
Ended(io: Io, steps: []Step, k: int, transcript: str, st: proc.Status): Fed {
    for i in k..steps.Len() {
        match steps[i].act {
            .Expect(lines) {
                have := TranscriptLines(io, transcript)
                if have < lines {
                    return Fed(out: Failed(steps[i].line, Why.ExpectEnded, have), end: End.Ended(st: st))
                }
            }
            _ {}
        }
    }
    return Fed(out: Done(), end: End.Ended(st: st))
}

/// How a program ended, as the error line of a failed `expect` says it.
EndText(a: &Arena, st: proc.Status): str from a {
    if code := st.code {
        t := a.Fmt("exit {code}", (code)) else {
            "an exit"
        }
        return t
    }
    if sig := st.signal {
        t := a.Fmt("signal {n}", (n: SignalName(sig))) else {
            "a signal"
        }
        return t
    }
    return "a signal proc.Status does not name"
}

/// The complete lines of the transcript file: 0 while it does not exist.
TranscriptLines(io: Io, path: str): int {
    var f := fs.Open(io, path) else {
        return 0
    }
    var buf: [16384]u8 = [0; 16384]
    var lines := 0
    for {
        n := f.Read(io, mut buf[..]) else {
            break
        }
        if n == 0 {
            break
        }
        for b in buf[..n] {
            if b == b'\n' {
                lines += 1
            }
        }
    }
    f.Close(io) else {
        f.Abandon()
    }
    return lines
}

/// How the program ended.
enum End {
    /// By itself, with this status.
    Ended(st: proc.Status)
    /// Killed by ptyrun: the script failed or the time ran out.
    Killed
    /// Neither waited for nor killed; left running.
    Lost
}

/// Waits for the child for left_ns, or kills it at once when the time is
/// spent.
Finish(io: Io, child: proc.Child, left_ns: i64): End {
    if left_ns <= 0 {
        return Kill(io, child)
    }
    scope s := io.Scope() {
        s.CancelAfter(time.Nanos(left_ns))
        st := child.Wait(io) else {
            return Kill(io, child)
        }
        return End.Ended(st: st)
    }
}

/// Kills the child and waits up to 5 seconds for it, under a mask so the
/// caller's cancel does not cut the wait short.
Kill(io: Io, child: proc.Child): End {
    child.Kill() else {
        child.Detach()
        return End.Lost
    }
    q := io.NoCancel()
    scope s := q.Scope() {
        s.CancelAfter(time.Seconds(5))
        _ = child.Wait(q) else {
            child.Detach()
            return End.Lost
        }
    }
    return End.Killed
}

/// A signal's name as proc.Signal spells it.
SignalName(s: proc.Signal): str {
    return match s {
        .Interrupt { "Interrupt" }
        .Terminate { "Terminate" }
        .Hangup { "Hangup" }
        _ {
            Panic("proc.Signal gained a member this match does not handle")
        }
    }
}
