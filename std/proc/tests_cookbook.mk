// Why: the cookbook's child-process recipe must keep compiling and working.
// What: as a test block, the recipe
// "Talk to a running child, build a pipeline, and kill on timeout".
// How: the recipe as written, its names prefixed with Cb, using SpCode from
// tests_spawn.mk; run by std/std-proc_spawnblocks_test.go and TestStdIo_TestBlocks.

import std.time

// The cookbook's recipe
// "Talk to a running child, build a pipeline, and kill on timeout"
// (doc/cookbook.md), as written there, renamed only so its
// names do not meet this module's.

CbCloseEnds(i: ?PipeWriter, o: ?PipeReader, e: ?PipeReader) {
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

CbFeed(io: Io, w: PipeWriter, input: []u8) {
    w.WriteAll(io, input) else {
        w.Close()
        return
    }
    w.Close()
}

CbForward(io: Io, child: &Child) {
    for {
        WaitSignal(io, Signal.Interrupt) else {
            return
        }
        child.Signal(Signal.Interrupt) else {
            return
        }
    }
}

CbLive(sys: &System, args: []str, input: []u8): Error|WriteError!Status {
    io := sys.io
    var c := DefaultCommand
    c.args = args
    c.stdin = Stdio.Pipe
    c.stdout = Stdio.Pipe
    c.group = true
    (child, stdin, stdout, stderr) := try Spawn(sys.arena, io, &c)
    onerr child.Detach()
    CbCloseEnds(none, none, stderr)
    r := stdout.Must()
    onerr r.Close()
    var out := sys.Stdout()
    var buf: [4096]u8 = [0; 4096]
    scope s := io.Scope() {
        s.go CbFeed(io, stdin.Must(), input)
        s.go CbForward(io, &child)
        for {
            n := try r.Read(io, mut buf[..])
            if n == 0 {
                break
            }
            try out.Bytes(buf[..n])
            try out.Flush()
        }
        s.Cancel()
    }
    r.Close()
    return try child.Wait(io)
}

CbPipeline(a: &Arena, io: Io, first: []str, second: []str): Error!Status {
    (r, w) := try Pipe(io)
    onerr r.Close()
    onerr w.Close()
    var c1 := DefaultCommand
    c1.args = first
    c1.stdout = Stdio.Writer(w: &w)
    (child: p1, stdin: i1, stdout: o1, stderr: e1) := try Spawn(a, io, &c1)
    onerr p1.Detach()
    CbCloseEnds(i1, o1, e1)
    w.Close()
    var c2 := DefaultCommand
    c2.args = second
    c2.stdin = Stdio.Reader(r: &r)
    (child: p2, stdin: i2, stdout: o2, stderr: e2) := try Spawn(a, io, &c2)
    onerr p2.Detach()
    CbCloseEnds(i2, o2, e2)
    r.Close()
    _ = try p1.Wait(io)
    return try p2.Wait(io)
}

CbReap(io: Io, child: Child): ?Status {
    child.Kill() else {
        child.Detach()
        return none
    }
    q := io.NoCancel()
    scope s := q.Scope() {
        s.CancelAfter(time.Seconds(5))
        st := child.Wait(q) else {
            child.Detach()
            return none
        }
        return some(st)
    }
}

CbRunFor(a: &Arena, io: Io, args: []str, limit: time.Duration): Error|time.TimedOut!Status {
    var c := DefaultCommand
    c.args = args
    c.group = true
    (child, stdin, stdout, stderr) := try Spawn(a, io, &c)
    CbCloseEnds(stdin, stdout, stderr)
    scope s := io.Scope() {
        s.CancelAfter(limit)
        st := child.Wait(io) else e {
            _ = CbReap(io, child)
            if s.Expired() {
                fail time.TimedOut
            }
            fail e
        }
        return st
    }
}

test "Cookbook: the live child, the pipeline and kill on timeout run as written" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var cat: [3]str = [sys.args[0], sys.args[1], "cat"]
    st := CbLive(sys, cat[..], "live\n".AsBytes()).Must()
    Assert(SpCode(st) == 0)
    var g5: [6]str = [sys.args[0], sys.args[1], "gen", "5", "0", "0"]
    var g9: [6]str = [sys.args[0], sys.args[1], "gen", "0", "0", "9"]
    Assert(SpCode(CbPipeline(sys.arena, io, g5[..], g9[..]).Must()) == 9)
    var slp: [3]str = [sys.args[0], sys.args[1], "sleep"]
    var timed := false
    _ = CbRunFor(sys.arena, io, slp[..], time.Millis(200)) else e {
        timed = match e {
            time.TimedOut { true }
            _ { false }
        }
    }
    Assert(timed)
    Assert(SpCode(CbRunFor(sys.arena, io, g9[..], time.Seconds(30)).Must()) == 9)
}
