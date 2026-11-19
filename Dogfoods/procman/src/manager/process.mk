// Why: each process needs its own supervisor: run it, report it, restart it.
//
// What: RunProcess, Backoff, WantsRestart, MaxBackoffMs and StableSeconds.
//
// How: one task per process; restarts follow its policy with an exponential
// back-off that starts over after a stable run. The last task to end closes
// the event channel and cancels the scope.

import std.proc
import std.time
import src.procfile
import src.rundir

/// The longest procman waits between two runs, whatever the back-off doubled to.
export const MaxBackoffMs: i64 = 30_000

/// The wait before restart number `restarts + 1`: `first` doubled once per
/// restart already made, at most `MaxBackoffMs`.
export Backoff(first: i64, restarts: int): i64 {
    var d := first
    for _ in 0..restarts {
        if d >= MaxBackoffMs / 2 {
            return MaxBackoffMs
        }
        d *= 2
    }
    return if d > MaxBackoffMs { MaxBackoffMs } else { d }
}

/// Whether a run that ended with `code` (or failed to run, `failed`) is run
/// again under `policy`, before counting retries.
export WantsRestart(policy: procfile.Restart, code: int, failed: bool): bool {
    return match policy {
        .Never { false }
        .OnFailure { failed || code != 0 }
        .Always { true }
    }
}

/// A run that lasts this long counts as a good start: the back-off and the
/// count of retries start over after it, as supervisord's `startsecs` does.
export const StableSeconds: i64 = 10

/// Runs the process `e`, number `index` in the Procfile, until it ends for
/// good or `s` is cancelled, sending each step on `events`. The last task to
/// end closes `events` and cancels `s`, so the manager's wait ends with it.
export RunProcess(s: &Scope, h: &Heap, io: Io, dir: rundir.RunDir, e: &procfile.Entry, index: int, events: &Chan[Event], c: &Control) {
    a := h.Alloc()
    var arena := Arena.New(h) else {
        Post(a, io, events, Event.New(a, index, Phase.GaveUp, 0))
        Finish(s, e, events, c)
        return
    }
    defer arena.Free()
    stable := time.Seconds(StableSeconds)
    var restarts := 0
    var streak := 0
    var run := 0
    for {
        run += 1
        Post(a, io, events, Event.New(a, index, Phase.Started, run))
        WriteState(&arena, io, dir, e, "running", run, restarts, "-", a, events, index)
        started := time.Now(io)
        var failed := false
        var code := 0
        r := proc.Run(&arena, io, e.argv, b"", e.limit) else err {
            if IsCanceled(err) {
                WriteState(&arena, io.NoCancel(), dir, e, "stopped", run, restarts, "killed", a, events, index)
                Post(a, io.NoCancel(), events, Event.New(a, index, Phase.Stopped, run))
                break
            }
            var ev := Event.New(a, index, Phase.Failed, run)
            ev.failure = some(err)
            Post(a, io, events, ev)
            failed = true
            proc.Output(code: -1, stdout: b"", stderr: b"")
        }
        if time.Now(io).Since(started).Nanos() >= stable.Nanos() {
            streak = 0
        }
        if !failed {
            code = r.code
            SaveOutput(&arena, io, dir, e, r.stdout, r.stderr, a, events, index)
            // with no room for the output, the exit still goes, without it
            ev := ExitEvent(a, index, run, &r) else { Event.New(a, index, Phase.Exited, run) }
            Post(a, io, events, ev)
        }
        last := if failed { "failed" } else { arena.Fmt("exit {code}", (code)) else { "exit" } }
        if !WantsRestart(e.restart, code, failed) {
            WriteState(&arena, io, dir, e, "ended", run, restarts, last, a, events, index)
            break
        }
        if streak >= e.retries {
            WriteState(&arena, io, dir, e, "gave-up", run, restarts, last, a, events, index)
            Post(a, io, events, Event.New(a, index, Phase.GaveUp, run))
            break
        }
        delay := Backoff(e.backoff_ms, streak)
        streak += 1
        restarts += 1
        var ev := Event.New(a, index, Phase.Restarting, run)
        ev.delay_ms = delay
        ev.attempt = streak
        Post(a, io, events, ev)
        WriteState(&arena, io, dir, e, "backoff", run, restarts, last, a, events, index)
        arena.Reset()
        io.Sleep(time.Millis(delay)) else {
            Post(a, io.NoCancel(), events, Event.New(a, index, Phase.Stopped, run))
            break
        }
    }
    Finish(s, e, events, c)
}

Finish(s: &Scope, e: &procfile.Entry, events: &Chan[Event], c: &Control) {
    if e.main {
        c.Stop(s, StopReason.MainEnded)
    }
    if c.Ended() {
        events.Close()
        c.Stop(s, StopReason.Finished)
    }
}

// An Exited event carrying the run's output. Output that is not UTF-8 is
// replaced by a note, since the console shows text. Fails with OutOfMemory
// when the heap has no room for the output.
ExitEvent(a: &Alloc, index: int, run: int, r: &proc.Output): OutOfMemory!Event from a {
    var ev := Event.New(a, index, Phase.Exited, run)
    onerr ev.Free(a)
    ev.code = r.code
    try PushOutput(a, mut ev.stdout, r.stdout)
    try PushOutput(a, mut ev.stderr, r.stderr)
    return ev
}

PushOutput(a: &Alloc, t: mut HeapText, bytes: []u8): OutOfMemory!void {
    text := str.From(bytes) else {
        try t.Push(a, "<output that is not UTF-8>\n")
        return
    }
    try t.Push(a, text)
}

IsCanceled(e: proc.Error): bool {
    return match e {
        Canceled { true }
        _ { false }
    }
}

// Sends `ev`, or frees it when the manager is gone or procman is stopping.
Post(a: &Alloc, io: Io, events: &Chan[Event], ev: Event into events) {
    events.Send(io, ev) else {
        ev.Free(a)
    }
}

WriteState(arena: &Arena, io: Io, dir: rundir.RunDir, e: &procfile.Entry, state: str, run: int, restarts: int, last: str, a: &Alloc into events, events: &Chan[Event], index: int) {
    line := arena.Fmt("{name:<12} {state:<8} runs={run} restarts={restarts} last={last}\n", (name: e.name, state, run, restarts, last)) else {
        return
    }
    file := arena.Fmt("{name}.state", (name: e.name)) else {
        return
    }
    dir.Replace(arena, io, file, line.AsBytes()) else err {
        var ev := Event.New(a, index, Phase.DirFailed, run)
        ev.dir_failure = some(err)
        Post(a, io, events, ev)
    }
}

SaveOutput(arena: &Arena, io: Io, dir: rundir.RunDir, e: &procfile.Entry, stdout: []u8, stderr: []u8, a: &Alloc into events, events: &Chan[Event], index: int) {
    var both := arena.Slice[u8](stdout.Len() + stderr.Len()) else {
        return
    }
    (mut both[..stdout.Len()]).CopyFrom(stdout)
    (mut both[stdout.Len()..]).CopyFrom(stderr)
    dir.SaveOutput(arena, io, e.name, both[..]) else err {
        var ev := Event.New(a, index, Phase.DirFailed, 0)
        ev.dir_failure = some(err)
        Post(a, io, events, ev)
    }
}
