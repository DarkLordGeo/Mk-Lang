// Why: a job must be stoppable by its timeout or by the daemon's shutdown,
// together with every process it started.
//
// What: Execute, Supervise, Await, Stop, Reap and Setting.
//
// How: the job is spawned as the leader of its own process group and its
// output read into the log while it runs. On timeout, a deadline on the
// scope around Wait fires, the group is killed, and Wait runs again under a
// fresh scope from `io.NoCancel()` with its own deadline; if even that
// fails the child is detached, so the daemon never hangs on it.

import std.proc
import std.time
import src.crontab

/// How long a child gets to exit after an interrupt or a kill.
const Grace: i64 = 5

/// What a job needs from the daemon, shared by every job task.
export struct Setting {
    /// `NAME=value` for each crontab variable, added to each job's environment
    export env: []str
    /// The log file, which a detached job writes to directly
    export log_path: str
    export journal: Journal
}

/// Runs `e` once and returns how it ended. `h` gives the run its own
/// arena. Every failure is logged; none is returned.
export Execute(h: &Heap, io: Io, set: &Setting, e: &crontab.Entry): Outcome {
    var a := Arena.New(h) else {
        set.journal.Warn(io, "{e.name}: not started: out of memory", (e))
        return Outcome.NotStarted
    }
    defer a.Free()
    if e.then.Len() > 0 {
        return Pipeline(h, &a, io, set, e)
    }
    var c := proc.DefaultCommand
    c.args = Program(&a, e.argv) else {
        set.journal.Warn(io, "{e.name}: not started: out of memory", (e))
        return Outcome.NotStarted
    }
    c.env = proc.Env.Extend(vars: set.env)
    c.group = true
    c.stdin = if e.input.IsSome() { proc.Stdio.Pipe } else { proc.Stdio.Null }
    if e.detach {
        return Detachable(&a, io, set, e, c)
    }
    c.stdout = proc.Stdio.Pipe
    c.stderr = proc.Stdio.JoinStdout
    (child, stdin, stdout, stderr) := proc.Spawn(&a, io, &c) else err {
        set.journal.Warn(io, "{e.name}: not started: {err}", (e, err))
        return Outcome.NotStarted
    }
    Started(io, set, e, &child)
    CloseReader(stderr)
    return Supervise(h, io, set, e, child, stdin, stdout)
}

/// Logs a start, with the process id in the file only.
Started(io: Io, set: &Setting, e: &crontab.Entry, child: &proc.Child) {
    pid := child.Pid()
    set.journal.Info(io, "{e.name}: started", (e))
    set.journal.FileOnly(io, "{e.name}: process {pid}", (e, pid))
}

/// Feeds the input, pumps the output and waits, under the entry's timeout.
Supervise(h: &Heap, io: Io, set: &Setting, e: &crontab.Entry, child: proc.Child, stdin: ?proc.PipeWriter, stdout: ?proc.PipeReader): Outcome {
    out := stdout.Must() // Execute asked for a pipe on stdout
    var outcome := Outcome.NotStarted
    // the pump reads through a masked Io until the job's output ends, so a
    // job's last words reach the log after a timeout or at shutdown: the
    // group is killed or exits, and the pipe closes
    q := io.NoCancel()
    scope s := io.Scope() {
        if d := e.timeout {
            s.CancelAfter(d)
        }
        if w := stdin {
            s.go Feed(io, w, e.input else { "" })
        }
        s.go Pump(h, q, &out, &set.journal, e.name)
        outcome = Await(io, set, e, child, &s)
    }
    out.Close()
    Ended(io, set, e, outcome)
    return outcome
}

/// Waits for the child; on a timeout or a shutdown, ends its group.
Await(io: Io, set: &Setting, e: &crontab.Entry, child: proc.Child, s: &Scope): Outcome {
    st := child.Wait(io) else {
        if s.Expired() {
            _ = Reap(io, child)
            return Outcome.TimedOut
        }
        return Stop(io, set, e, child)
    }
    return FromStatus(&st)
}

/// The daemon is stopping: forward an interrupt to the job's group and
/// give it Grace seconds, then kill it.
Stop(io: Io, set: &Setting, e: &crontab.Entry, child: proc.Child): Outcome {
    q := io.NoCancel()
    child.Signal(proc.Signal.Interrupt) else err {
        set.journal.Warn(q, "{e.name}: cannot interrupt: {err}; killing it", (e, err))
        _ = Reap(q, child)
        return Outcome.Interrupted
    }
    scope s := q.Scope() {
        s.CancelAfter(time.Seconds(Grace))
        _ = child.Wait(q) else {
            _ = Reap(q, child)
            return Outcome.Interrupted
        }
    }
    return Outcome.Interrupted
}

/// Kills the child's group and reaps the leader within Grace seconds,
/// detaching it when even that fails.
Reap(io: Io, child: proc.Child): ?proc.Status {
    child.Kill() else {
        child.Detach()
        return none
    }
    q := io.NoCancel()
    scope s := q.Scope() {
        s.CancelAfter(time.Seconds(Grace))
        st := child.Wait(q) else {
            child.Detach()
            return none
        }
        return some(st)
    }
}

/// Writes the entry's input to the job, each `\n` in it a line break, and a
/// final newline, then closes its standard input so a job that reads to the
/// end ends. A one-line input goes in one write.
Feed(io: Io, w: proc.PipeWriter, input: str) {
    if !input.Has("\\n") {
        w.WriteAll(io, input.AsBytes()) else {}
        w.WriteAll(io, b"\n") else {}
        w.Close()
        return
    }
    var buf: [256]u8 = [0; 256]
    var out := w.Writer(io, mut buf[..])
    for line in input.Split("\\n") {
        Print(mut out, "{line}\n", (line)) else {}
    }
    out.Flush() else {}
    w.Close()
}

/// Logs how a run ended.
Ended(io: Io, set: &Setting, e: &crontab.Entry, o: Outcome) {
    match o {
        .Exited(code) {
            if code == 0 {
                set.journal.Info(io, "{e.name}: done", (e))
            } else {
                set.journal.Warn(io, "{e.name}: exited with status {code}", (e, code))
            }
        }
        .Signalled { set.journal.Warn(io, "{e.name}: ended by a signal", (e)) }
        .TimedOut {
            d := e.timeout else { time.Seconds(0) }
            set.journal.Warn(io.NoCancel(), "{e.name}: timed out after {d}; its process group was killed", (e, d))
        }
        .Interrupted { set.journal.Info(io.NoCancel(), "{e.name}: interrupted at shutdown", (e)) }
        .Detached { set.journal.Info(io.NoCancel(), "{e.name}: left running at shutdown", (e)) }
        .NotStarted | .Recorded {}
    }
}

/// Closes a pipe end that a spawn returned.
CloseReader(r: ?proc.PipeReader) {
    if p := r {
        p.Close()
    }
}

/// The argument list with the program's path made runnable: on Windows a
/// path with a separator and no extension gets `.exe`, as CreateProcess's
/// own search would add it.
Program(a: &Arena, argv: []str): OutOfMemory![]str from a, argv *argv into a {
    if Target.os != Os.Windows {
        return argv
    }
    first := argv[0]
    var slash := -1
    var dot := -1
    for i in 0..first.Len() {
        c := first.Byte(i)
        if c == b'/' || c == b'\\' {
            slash = i
        } else if c == b'.' {
            dot = i
        }
    }
    if slash < 0 || dot > slash {
        return argv
    }
    var out := List[str].New(a)
    try out.Push(try a.Fmt("{first}.exe", (first)))
    for w in argv[1..] {
        try out.Push(w)
    }
    done := out.Done()
    return done[..]
}
