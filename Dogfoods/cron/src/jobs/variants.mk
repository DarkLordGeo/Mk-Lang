// Why: besides a plain command, a job can be a two-stage pipeline or a
// detached job that may outlive the daemon.
//
// What: Pump, Pipeline, Detachable and Settle.
//
// How: Pump logs output a line at a time as `name| line`. Each pipeline
// stage leads its own group, joined by a pipe; a detached job writes
// straight to the log file and is left running at shutdown.

import std.fs
import std.proc
import src.crontab

/// Reads the job's output a line at a time into the log, as `name| line`,
/// until the job closes it. Output that is not lines of text (a line over
/// 4 KiB, bytes that are not UTF-8) is counted from there on, not logged.
Pump(h: &Heap, io: Io, r: &proc.PipeReader, j: &Journal, name: str) {
    var a := Arena.New(h) else {
        return
    }
    defer a.Free()
    var buf: [4096]u8 = [0; 4096]
    var reader := r.Reader(io, mut buf[..])
    for {
        line := reader.Line(&a) else {
            break
        }
        text := line else {
            return
        }
        j.Info(io, "{name}| {text}", (name, text))
        a.Reset()
    }
    // the reader failed: drain the rest raw, so the job never blocks on a
    // full pipe, and say how much was not logged
    var rest: [4096]u8 = [0; 4096]
    var dropped: i64 = 0
    for {
        n := r.Read(io, mut rest[..]) else {
            // a cancelled read: the job is being stopped, which is logged
            return
        }
        if n == 0 {
            break
        }
        dropped += i64(n)
    }
    j.Warn(io, "{name}: output is not lines of text; the rest is not logged", (name))
    j.FileOnly(io, "{name}: {dropped} bytes of output not logged", (name, dropped))
}

/// A detached job writes straight to the log file, so it needs nothing of
/// the daemon's once it runs; at shutdown it is left running.
Detachable(a: &Arena, io: Io, set: &Setting, e: &crontab.Entry, base: proc.Command): Outcome {
    var f := fs.OpenWith(io, set.log_path, fs.Append) else err {
        set.journal.Warn(io, "{e.name}: not started: the log: {err}", (e, err))
        return Outcome.NotStarted
    }
    var c := base
    c.stdout = proc.Stdio.File(f: &f)
    c.stderr = proc.Stdio.JoinStdout
    (child, stdin, stdout, stderr) := proc.Spawn(a, io, &c) else err {
        set.journal.Warn(io, "{e.name}: not started: {err}", (e, err))
        f.Close(io) else {
            f.Abandon()
        }
        return Outcome.NotStarted
    }
    // the child holds its own copy of the handle
    f.Close(io) else {
        f.Abandon()
    }
    Started(io, set, e, &child)
    CloseReader(stdout)
    CloseReader(stderr)
    var outcome := Outcome.NotStarted
    scope s := io.Scope() {
        if d := e.timeout {
            s.CancelAfter(d)
        }
        if w := stdin {
            s.go Feed(io, w, e.input else { "" })
        }
        outcome = Settle(io, child, &s)
    }
    Ended(io, set, e, outcome)
    return outcome
}

/// `first | second`: each stage leads a group of its own, the first's
/// output goes to the second through a pipe, and the second's is logged.
/// The run's outcome is the second stage's, unless the first failed.
Pipeline(h: &Heap, a: &Arena, io: Io, set: &Setting, e: &crontab.Entry): Outcome *e into a {
    (r, w) := proc.Pipe(io) else err {
        set.journal.Warn(io, "{e.name}: not started: {err}", (e, err))
        return Outcome.NotStarted
    }
    first := Program(a, e.argv) else {
        r.Close()
        w.Close()
        return Outcome.NotStarted
    }
    second := Program(a, e.then) else {
        r.Close()
        w.Close()
        return Outcome.NotStarted
    }
    var c1 := proc.DefaultCommand
    c1.args = first
    c1.env = proc.Env.Extend(vars: set.env)
    c1.group = true
    c1.stdin = if e.input.IsSome() { proc.Stdio.Pipe } else { proc.Stdio.Null }
    c1.stdout = proc.Stdio.Writer(w: &w)
    c1.stderr = proc.Stdio.Null
    (child: p1, stdin: i1, stdout: o1, stderr: e1) := proc.Spawn(a, io, &c1) else err {
        set.journal.Warn(io, "{e.name}: not started: {err}", (e, err))
        r.Close()
        w.Close()
        return Outcome.NotStarted
    }
    w.Close()
    CloseReader(o1)
    CloseReader(e1)
    var c2 := proc.DefaultCommand
    c2.args = second
    c2.env = proc.Env.Extend(vars: set.env)
    c2.group = true
    c2.stdin = proc.Stdio.Reader(r: &r)
    c2.stdout = proc.Stdio.Pipe
    c2.stderr = proc.Stdio.JoinStdout
    (child: p2, stdin: i2, stdout: o2, stderr: e2) := proc.Spawn(a, io, &c2) else err {
        r.Close()
        set.journal.Warn(io, "{e.name}: second stage not started: {err}", (e, err))
        if w1 := i1 {
            w1.Close()
        }
        _ = Reap(io, p1)
        return Outcome.NotStarted
    }
    r.Close()
    Started(io, set, e, &p1)
    if w2 := i2 {
        w2.Close()
    }
    CloseReader(e2)
    out := o2.Must() // c2 asked for a pipe on stdout
    var last := Outcome.NotStarted
    var before := Outcome.NotStarted
    // the pump reads through a masked Io, as in Supervise
    q := io.NoCancel()
    scope s := io.Scope() {
        if d := e.timeout {
            s.CancelAfter(d)
        }
        if w1 := i1 {
            s.go Feed(io, w1, e.input else { "" })
        }
        s.go Pump(h, q, &out, &set.journal, e.name)
        last = Await(io, set, e, p2, &s)
        before = Await(io, set, e, p1, &s)
    }
    out.Close()
    outcome := match last {
        .Exited(code) {
            if code == 0 { before } else { last }
        }
        _ { last }
    }
    Ended(io, set, e, outcome)
    return outcome
}

/// Waits for a detached job: on its timeout its group is killed; at the
/// daemon's shutdown it is left running.
Settle(io: Io, child: proc.Child, s: &Scope): Outcome {
    st := child.Wait(io) else {
        if s.Expired() {
            _ = Reap(io, child)
            return Outcome.TimedOut
        }
        child.Detach()
        return Outcome.Detached
    }
    return FromStatus(&st)
}
