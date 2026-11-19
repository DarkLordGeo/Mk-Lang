// Why: std.proc under a simulated Io must start no real process and must
// replay the same way from a seed.
// What: test blocks of Spawn, Run, Pipe, Pty and WaitSignal under a Sim, and
// of a Recorder, which answers Unsupported.
// How: under a Sim a child is cat, copying stdin to stdout, and "missing" is
// NotFound; blocks cut each wait by deadlines on several seeds and replay them.

import std.core
import std.raw
import std.sim
import std.time

// SmMark sets bit k of bad when e is not want. Each case marks its own
// bit at most once, so Add is an OR.
SmMark(bad: &raw.Atomic[u32], k: u32, e: Error, want: str) {
    if !PtSame(PtName(e), want) {
        // Edge: the scope's join (or, for the Recorder, program order)
        // orders this add before the block's Load.
        _ = bad.Add(u32(1) << k)
    }
}

// SmSpawn is the error Spawn of args on io fails with; a child it starts
// is killed and detached, and is Os(1) here so the case fails.
SmSpawn(a: &Arena, io: Io, args: []str): Error {
    var c := DefaultCommand
    c.args = args
    (child, stdin, stdout, stderr) := Spawn(a, io, &c) else e {
        return e
    }
    SpCloseEnds(stdin, stdout, stderr)
    child.Kill() else {}
    child.Detach()
    return Os(code: 1)
}

// SmPipe is the error Pipe on io fails with, else Os(1).
SmPipe(io: Io): Error {
    (r, w) := Pipe(io) else e {
        return e
    }
    r.Close()
    w.Close()
    return Os(code: 1)
}

// SmPty is the error Pty.Open on io fails with, else Os(1).
SmPty(io: Io): Error {
    p := Pty.Open(io, 80, 24) else e {
        return e
    }
    p.Close()
    return Os(code: 1)
}

// SmRun is the error Run of args on io fails with, else Os(1).
SmRun(a: &Arena, io: Io, args: []str, input: []u8): Error {
    _ = Run(a, io, args, input, 100) else e {
        return e
    }
    return Os(code: 1)
}

// SmAll runs every case on io, an Io with no processes, and sets bit k
// of bad for case k when it did not answer as it must: 0 Spawn by name,
// 1 Spawn by path, 2 Pipe, 3 Pty.Open, 4 Run by name, 5 Run by path with
// input (all Unsupported), 6 Spawn of no args (NotFound, checked before
// any call); 7 no arena.
SmAll(io: Io, heap: &Heap, exe: str, name: str, bad: &raw.Atomic[u32]) {
    var a := Arena.New(heap) else {
        _ = bad.Add(u32(1) << 7)
        return
    }
    var by_name: [2]str = [name, "x"]
    var by_path: [2]str = [exe, "x"]
    var empty: [0]str = []
    SmMark(bad, 0, SmSpawn(&a, io, by_name[..]), "Unsupported")
    SmMark(bad, 1, SmSpawn(&a, io, by_path[..]), "Unsupported")
    SmMark(bad, 2, SmPipe(io), "Unsupported")
    SmMark(bad, 3, SmPty(io), "Unsupported")
    SmMark(bad, 4, SmRun(&a, io, by_name[..], b""), "Unsupported")
    SmMark(bad, 5, SmRun(&a, io, by_path[..], b"in"), "Unsupported")
    SmMark(bad, 6, SmSpawn(&a, io, empty[..]), "NotFound")
    a.Free()
}

// SmName is a program found in PATH on this target, by name.
SmName(): str {
    return if Target.os == core.Os.Windows { "cmd" } else { "sh" }
}

// SmOk asserts that no case of SmAll failed, one bit at a time, so the
// failing line names the case.
SmOk(b: u32) {
    Assert((b & 1) == 0)
    Assert((b & 2) == 0)
    Assert((b & 4) == 0)
    Assert((b & 8) == 0)
    Assert((b & 16) == 0)
    Assert((b & 32) == 0)
    Assert((b & 64) == 0)
    Assert((b & 128) == 0)
}

// SmNew is a Sim of seed over a, or a panic.
SmNew(a: &Arena, seed: int): sim.Sim {
    return sim.Sim.New(a, Cast[u64](seed)) else {
        Panic("sim.Sim.New failed")
    }
}

// SmIn is a spawn's stdin end, which the test asked for.
SmIn(o: ?PipeWriter): PipeWriter {
    return o else {
        Panic("no stdin end")
    }
}

// SmOut is a spawn's stdout or stderr end, which the test asked for.
SmOut(o: ?PipeReader): PipeReader {
    return o else {
        Panic("no stdout end")
    }
}

// SmCat spawns a simulated cat by args on io with stdin, stdout and
// stderr as given.
SmCat(a: &Arena, io: Io, args: []str, i: Stdio, o: Stdio, e: Stdio): (child: Child, stdin: ?PipeWriter, stdout: ?PipeReader, stderr: ?PipeReader) {
    var c := DefaultCommand
    c.args = args
    c.stdin = i
    c.stdout = o
    c.stderr = e
    return Spawn(a, io, &c).Must()
}

// SmKilled: st is how this target reports a child ended by Kill (the
// Sim reports it as its target does: code 1 on Windows, SIGKILL on Linux).
SmKilled(st: Status): bool {
    return if SpWin() { SpCode(st) == 1 } else { st.code.IsNone() && st.signal.IsNone() }
}

// SmReadN reads exactly n bytes of r into b from the start, or fewer when
// the stream ends; the count.
SmReadN(io: Io, r: &PipeReader, b: mut []u8, n: int): int {
    var got := 0
    for got < n {
        k := r.Read(io, mut b[got..n]).Must()
        if k == 0 {
            break
        }
        got += k
    }
    return got
}

// SmPtyReadN is SmReadN for a Pty.
SmPtyReadN(io: Io, p: &Pty, b: mut []u8, n: int): int {
    var got := 0
    for got < n {
        k := p.Read(io, mut b[got..n]).Must()
        if k == 0 {
            break
        }
        got += k
    }
    return got
}

// SmIdle reads r until a read waits 5 ms of the run's clock with nothing
// to read (cut by its deadline) or the stream ends; the bytes, into b
// from off.
SmIdle(io: Io, r: &PipeReader, b: mut []u8, off: int): int {
    var n := off
    for n < b.Len() {
        k := SpCutRead(io, r, mut b[n..], 5)
        if k == 0 {
            return n
        }
        n += k
    }
    return n
}

// SmPtyIdle is SmIdle for a Pty.
SmPtyIdle(io: Io, p: &Pty, b: mut []u8, off: int): int {
    var n := off
    for n < b.Len() {
        var k := 0
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(5))
            k = p.Read(io, mut b[n..]) else e {
                Assert(PtSame(PtName(e), "Canceled"))
                0
            }
        }
        if k == 0 {
            return n
        }
        n += k
    }
    return n
}

// SmCloseLate closes w after ms milliseconds of the run's clock.
SmCloseLate(io: Io, w: PipeWriter, ms: i64) {
    io.NoCancel().Sleep(time.Millis(ms)) else {}
    w.Close()
}

test "Sim: a Recorder answers Unsupported for Spawn by name and by path, Pipe, Pty.Open and Run, and Spawn of no args is NotFound" (sys: &System) {
    var rec := sim.Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    bad := raw.Atomic[u32].New(0)
    SmAll(rec.Io(), sys.heap, sys.args[0], SmName(), &bad)
    rec.Free()
    SmOk(bad.Load())
}

test "Sim: a child is cat: what is written to its stdin comes back on its stdout, stderr ends with it, and Wait gives 0 once stdin closes, by name and by path, seeds 1 to 3" (sys: &System) {
    var by_name: [2]str = [SmName(), "x"]
    var by_path: [2]str = [sys.args[0], "x"]
    for seed in 1..4 {
        var s := SmNew(sys.arena, seed)
        io := s.Io()
        for args in [by_name[..], by_path[..]] {
            (child, stdin, stdout, stderr) := SmCat(sys.arena, io, args, Stdio.Pipe, Stdio.Pipe, Stdio.Pipe)
            w := SmIn(stdin)
            o := SmOut(stdout)
            e := SmOut(stderr)
            Assert(child.Pid() >= 100)
            w.WriteAll(io, "hello".AsBytes()).Must()
            var b: [16]u8 = [0; 16]
            Assert(SmReadN(io, &o, mut b[..], 5) == 5 && SpText(b[0..5], "hello"))
            w.Close()
            Assert(o.Read(io, mut b[..]).Must() == 0)
            Assert(e.Read(io, mut b[..]).Must() == 0)
            st := child.Wait(io).Must()
            Assert(SpCode(st) == 0 && st.signal.IsNone())
            o.Close()
            e.Close()
        }
        // stdin Null: cat reads an empty input and ends at once.
        (child, stdin, stdout, stderr) := SmCat(sys.arena, io, by_name[..], Stdio.Null, Stdio.Pipe, Stdio.JoinStdout)
        Assert(stdin.IsNone() && stderr.IsNone())
        SpCloseEnds(stdin, none, stderr)
        o := SmOut(stdout)
        var b: [4]u8 = [0; 4]
        Assert(o.Read(io, mut b[..]).Must() == 0)
        Assert(SpCode(child.Wait(io).Must()) == 0)
        o.Close()
        s.Free()
    }
}

// SmChunks writes the pattern's first n bytes to w in chunks of at most 3,
// sleeping 0 to 4 ms of the run's clock before each, then after more ms
// closes w.
SmChunks(io: Io, w: PipeWriter, n: int, after: i64) {
    var b: [3]u8 = [0; 3]
    var off := 0
    var round := 0
    for off < n {
        io.NoCancel().Sleep(time.Millis(i64(round % 5))) else {}
        k := if n - off < 3 { n - off } else { 3 }
        PtPattern(mut b[0..k], off)
        w.WriteAll(io.NoCancel(), b[0..k]).Must()
        off += k
        round += 1
    }
    io.NoCancel().Sleep(time.Millis(after)) else {}
    w.Close()
}

// SmPtyChunks is SmChunks for a Pty, which it does not close.
SmPtyChunks(io: Io, p: &Pty, n: int) {
    var b: [3]u8 = [0; 3]
    var off := 0
    var round := 0
    for off < n {
        io.NoCancel().Sleep(time.Millis(i64(round % 5))) else {}
        k := if n - off < 3 { n - off } else { 3 }
        PtPattern(mut b[0..k], off)
        p.WriteAll(io.NoCancel(), b[0..k]).Must()
        off += k
        round += 1
    }
}

// SmCutRead is a Read of r into b under a deadline of ms: the bytes read
// (0 at the end), or -1 when the deadline cut it.
SmCutRead(io: Io, r: &PipeReader, b: mut []u8, ms: i64): int {
    var k := -1
    scope d := io.Scope() {
        d.CancelAfter(time.Millis(ms))
        k = r.Read(io, b) else e {
            Assert(PtSame(PtName(e), "Canceled"))
            -1
        }
    }
    return k
}

// SmPtyCutRead is SmCutRead for a Pty.
SmPtyCutRead(io: Io, p: &Pty, b: mut []u8, ms: i64): int {
    var k := -1
    scope d := io.Scope() {
        d.CancelAfter(time.Millis(ms))
        k = p.Read(io, b) else e {
            Assert(PtSame(PtName(e), "Canceled"))
            -1
        }
    }
    return k
}

// SmCutsTo reads r into b with each Read under a deadline of 0 to 6 ms,
// round after round, until b holds n bytes or the stream ends: the bytes
// and the Reads cut. At most 4,000 rounds.
SmCutsTo(io: Io, r: &PipeReader, b: mut []u8, n: int): (got: int, cuts: int) {
    var got := 0
    var cuts := 0
    for round in 0..4000 {
        if got == n {
            return (got: got, cuts: cuts)
        }
        k := SmCutRead(io, r, mut b[got..n], i64(round % 7))
        if k == 0 {
            return (got: got, cuts: cuts)
        }
        if k < 0 {
            cuts += 1
        } else {
            got += k
        }
    }
    Panic("PipeReader.Read: no end in 4,000 rounds")
}

// SmPtyCutsTo is SmCutsTo for a Pty.
SmPtyCutsTo(io: Io, p: &Pty, b: mut []u8, n: int): (got: int, cuts: int) {
    var got := 0
    var cuts := 0
    for round in 0..4000 {
        if got == n {
            return (got: got, cuts: cuts)
        }
        k := SmPtyCutRead(io, p, mut b[got..n], i64(round % 7))
        if k == 0 {
            return (got: got, cuts: cuts)
        }
        if k < 0 {
            cuts += 1
        } else {
            got += k
        }
    }
    Panic("Pty.Read: no end in 4,000 rounds")
}

// SmCatEnd closes w, checks that o ends and that the child exits 0, and
// closes o.
SmCatEnd(io: Io, child: Child, w: PipeWriter, o: PipeReader) {
    w.Close()
    var b: [4]u8 = [0; 4]
    Assert(o.Read(io, mut b[..]).Must() == 0)
    Assert(SpCode(child.Wait(io).Must()) == 0)
    o.Close()
}

test "Sim: Spawn and Run of missing are NotFound; Run echoes its input with code 0, gives an empty input the null device, and is TooLarge past its limit, seeds 1 to 3" (sys: &System) {
    var missing: [1]str = ["missing"]
    var by_name: [2]str = [SmName(), "x"]
    for seed in 1..4 {
        var s := SmNew(sys.arena, seed)
        io := s.Io()
        Assert(PtSame(PtName(SmSpawn(sys.arena, io, missing[..])), "NotFound"))
        Assert(PtSame(PtName(SmRun(sys.arena, io, missing[..], b"in")), "NotFound"))
        out := Run(sys.arena, io, by_name[..], b"echoed", 100).Must()
        Assert(out.code == 0 && SpText(out.stdout, "echoed") && out.stderr.Len() == 0)
        none_in := Run(sys.arena, io, by_name[..], b"", 100).Must()
        Assert(none_in.code == 0 && none_in.stdout.Len() == 0 && none_in.stderr.Len() == 0)
        big := sys.arena.Slice[u8](300).Must()
        PtPattern(mut big[..], 0)
        var failed := false
        _ = Run(sys.arena, io, by_name[..], big[..], 100) else e {
            failed = PtSame(PtName(e), "TooLarge")
        }
        Assert(failed)
        s.Free()
    }
}

test "Sim: Pipe is a stream pair that ends after its writer closes; a Pty echoes what is written, resizes, and ends after Kill ends its child, seeds 1 to 3" (sys: &System) {
    var by_name: [2]str = [SmName(), "x"]
    for seed in 1..4 {
        var s := SmNew(sys.arena, seed)
        io := s.Io()
        (r, w) := Pipe(io).Must()
        w.WriteAll(io, "abc".AsBytes()).Must()
        var b: [16]u8 = [0; 16]
        Assert(SmReadN(io, &r, mut b[..], 3) == 3 && SpText(b[0..3], "abc"))
        w.Close()
        Assert(r.Read(io, mut b[..]).Must() == 0)
        r.Close()
        p := Pty.Open(io, 80, 24).Must()
        p.Resize(io, 120, 40).Must()
        var c := DefaultCommand
        c.args = by_name[..]
        c.terminal = some(&p)
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        Assert(stdin.IsNone() && stdout.IsNone() && stderr.IsNone())
        SpCloseEnds(stdin, stdout, stderr)
        p.WriteAll(io, "typed".AsBytes()).Must()
        Assert(SmPtyReadN(io, &p, mut b[..], 5) == 5 && SpText(b[0..5], "typed"))
        child.Kill().Must()
        Assert(SmKilled(child.Wait(io).Must()))
        Assert(p.Read(io, mut b[..]).Must() == 0)
        p.Close()
        s.Free()
    }
}

// SmWaitRun is one Sim of seed: a child's Wait cut by a 1 ms deadline
// keeps the child; then Waits under deadlines of 0 to 6 ms, round after
// round, while a task closes stdin after 20 ms, end with code 0; a second
// child's Wait cut the same way, then Kill, and the same Wait gives the
// kill. The Waits cut, mixed with the run's clock.
SmWaitRun(a: &Arena, seed: int): i64 {
    var by_name: [2]str = [SmName(), "x"]
    var s := SmNew(a, seed)
    io := s.Io()
    t0 := time.Now(io)
    (child, stdin, stdout, stderr) := SmCat(a, io, by_name[..], Stdio.Pipe, Stdio.Null, Stdio.Null)
    SpCloseEnds(none, stdout, stderr)
    w := SmIn(stdin)
    (st, back) := SpWaitCut(io, child, 1)
    Assert(st.IsNone())
    c := back else {
        Panic("a cut Wait gave no child back")
    }
    var code := -1
    var cuts := 0
    scope g := io.Scope() {
        g.go SmCloseLate(io, w, 20)
        (code: k, cuts: n) := SpWaitCuts(io, c, 0, 0)
        code = k
        cuts = n
    }
    Assert(code == 0 && cuts >= 1)
    (child: child2, stdin: stdin2, stdout: stdout2, stderr: stderr2) := SmCat(a, io, by_name[..], Stdio.Pipe, Stdio.Pipe, Stdio.Null)
    SpCloseEnds(none, none, stderr2)
    (st: st2, back: back2) := SpWaitCut(io, child2, 1)
    Assert(st2.IsNone())
    c2 := back2 else {
        Panic("a cut Wait gave no child back")
    }
    c2.Kill().Must()
    Assert(SmKilled(c2.Wait(io).Must()))
    // The child is gone: its stdout ends, and a write to its stdin fails.
    o2 := SmOut(stdout2)
    var b: [4]u8 = [0; 4]
    Assert(o2.Read(io, mut b[..]).Must() == 0)
    w2 := SmIn(stdin2)
    var failed := false
    w2.WriteAll(io, "x".AsBytes()) else {
        failed = true
    }
    Assert(failed)
    w2.Close()
    o2.Close()
    mix := i64(cuts) * 1_000_000_007 +% time.Now(io).Since(t0).Nanos()
    s.Free()
    return mix
}

test "Sim: Child.Wait cut by deadlines under a Sim keeps the child each time; the same Wait gives 0 once stdin closes, and the kill after Kill; a seed's run is the same when replayed, seeds 1 to 8" (sys: &System) {
    for seed in 1..9 {
        m := SmWaitRun(sys.arena, seed)
        Assert(SmWaitRun(sys.arena, seed) == m)
    }
}

// SmReadRun is one Sim of seed: a proc.Pipe and a child's stdout, each
// read under deadlines of 0 to 6 ms, round after round, while a task
// writes 300 bytes in chunks at 0 to 4 ms; no byte is lost or reordered,
// the pipe then ends, and the child exits 0 once stdin closes. The Reads
// cut, mixed with the run's clock.
SmReadRun(a: &Arena, seed: int): i64 {
    var by_name: [2]str = [SmName(), "x"]
    var s := SmNew(a, seed)
    io := s.Io()
    t0 := time.Now(io)
    var b: [301]u8 = [0; 301]
    (r, w) := Pipe(io).Must()
    var got := 0
    var cuts := 0
    scope g := io.Scope() {
        g.go SmChunks(io, w, 300, 0)
        (got: k, cuts: n) := SmCutsTo(io, &r, mut b[..], 301)
        got = k
        cuts = n
    }
    Assert(got == 300 && PtIsPattern(b[0..300]) && cuts >= 1)
    r.Close()
    (child, stdin, stdout, stderr) := SmCat(a, io, by_name[..], Stdio.Pipe, Stdio.Pipe, Stdio.Null)
    SpCloseEnds(none, none, stderr)
    w2 := SmIn(stdin)
    o := SmOut(stdout)
    var got2 := 0
    var cuts2 := 0
    scope g := io.Scope() {
        // The writer keeps stdin open 50 ms past its last chunk: every
        // byte has arrived by then (a Sim's latency is under 1 ms), so
        // its close loses none in flight.
        g.go SmChunks(io, w2, 300, 50)
        (got: k, cuts: n) := SmCutsTo(io, &o, mut b[..], 300)
        got2 = k
        cuts2 = n
    }
    Assert(got2 == 300 && PtIsPattern(b[0..300]) && cuts2 >= 1)
    Assert(o.Read(io, mut b[..]).Must() == 0)
    Assert(SpCode(child.Wait(io).Must()) == 0)
    o.Close()
    mix := i64(cuts) * 1_000_003 +% i64(cuts2) * 1_000_000_007 +% time.Now(io).Since(t0).Nanos()
    s.Free()
    return mix
}

test "Sim: PipeReader.Read cut by deadlines under a Sim loses no byte, on a proc.Pipe and on a child's stdout; a seed's run is the same when replayed, seeds 1 to 8" (sys: &System) {
    for seed in 1..9 {
        m := SmReadRun(sys.arena, seed)
        Assert(SmReadRun(sys.arena, seed) == m)
    }
}

// SmPtyLate writes data to p after ms milliseconds of the run's clock.
SmPtyLate(io: Io, p: &Pty, ms: i64, data: str) {
    io.NoCancel().Sleep(time.Millis(ms)) else {}
    p.WriteAll(io.NoCancel(), data.AsBytes()).Must()
}

test "Sim: Pty.Read cut by a deadline under a Sim consumes nothing, the same read then returns what is written later, and reads cut round after round lose no byte, seeds 1 to 8" (sys: &System) {
    var by_name: [2]str = [SmName(), "x"]
    for seed in 1..9 {
        var s := SmNew(sys.arena, seed)
        io := s.Io()
        p := Pty.Open(io, 80, 24).Must()
        var c := DefaultCommand
        c.args = by_name[..]
        c.terminal = some(&p)
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, stdout, stderr)
        var b: [301]u8 = [0; 301]
        scope g := io.Scope() {
            g.go SmPtyLate(io, &p, 5, "late")
            var cut := false
            scope d := io.Scope() {
                d.CancelAfter(time.Millis(1))
                _ = p.Read(io, mut b[..]) else e {
                    cut = PtSame(PtName(e), "Canceled") && d.Expired()
                }
            }
            Assert(cut)
            Assert(SmPtyReadN(io, &p, mut b[..], 4) == 4 && SpText(b[0..4], "late"))
        }
        var got := 0
        var cuts := 0
        scope g := io.Scope() {
            g.go SmPtyChunks(io, &p, 300)
            (got: k, cuts: n) := SmPtyCutsTo(io, &p, mut b[..], 300)
            got = k
            cuts = n
        }
        Assert(got == 300 && PtIsPattern(b[0..300]) && cuts >= 1)
        child.Kill().Must()
        Assert(SmKilled(child.Wait(io).Must()))
        Assert(p.Read(io, mut b[..]).Must() == 0)
        p.Close()
        s.Free()
    }
}

// SmNap sleeps ms milliseconds of the run's clock. A WriteAll cut runs
// beside it: in a run of one task a write that nothing can drain is
// Canceled at once (std.sim's deadlock rule), not at its deadline.
SmNap(io: Io, ms: i64) {
    io.NoCancel().Sleep(time.Millis(ms)) else {}
}

test "Sim: PipeWriter.WriteAll and Pty.WriteAll cut by a deadline on a full stream under a Sim are Canceled, and what the reader gets is a nonempty proper prefix of the data, seeds 1 to 8" (sys: &System) {
    var by_name: [2]str = [SmName(), "x"]
    n := 200_000
    data := sys.arena.Slice[u8](n).Must()
    PtPattern(mut data[..], 0)
    got := sys.arena.Slice[u8](n + 1).Must()
    for seed in 1..9 {
        var s := SmNew(sys.arena, seed)
        io := s.Io()
        // A child's stdin, its stdout unread: cat's one stream fills.
        (child, stdin, stdout, stderr) := SmCat(sys.arena, io, by_name[..], Stdio.Pipe, Stdio.Pipe, Stdio.Null)
        SpCloseEnds(none, none, stderr)
        w := SmIn(stdin)
        o := SmOut(stdout)
        var failed := false
        scope g := io.Scope() {
            g.go SmNap(io, 10)
            scope d := io.Scope() {
                d.CancelAfter(time.Millis(2))
                w.WriteAll(io, data[..]) else e {
                    failed = PtSame(PtName(e), "Canceled") && d.Expired()
                }
            }
        }
        Assert(failed)
        k := SmIdle(io, &o, mut got[..], 0)
        Assert(k > 0 && k < n && PtIsPattern(got[0..k]))
        SmCatEnd(io, child, w, o)
        // A proc.Pipe, its reader idle.
        (r: r, w: pw) := Pipe(io).Must()
        failed = false
        scope g := io.Scope() {
            g.go SmNap(io, 10)
            scope d := io.Scope() {
                d.CancelAfter(time.Millis(2))
                pw.WriteAll(io, data[..]) else e {
                    failed = PtSame(PtName(e), "Canceled") && d.Expired()
                }
            }
        }
        Assert(failed)
        pw.Close()
        var k2 := 0
        SpDrain(io, &r, mut got[..], mut k2)
        Assert(k2 > 0 && k2 < n && PtIsPattern(got[0..k2]))
        r.Close()
        // A Pty with a child, nothing reading it.
        p := Pty.Open(io, 80, 24).Must()
        var c := DefaultCommand
        c.args = by_name[..]
        c.terminal = some(&p)
        (child: pc, stdin: pi, stdout: po, stderr: pe) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(pi, po, pe)
        failed = false
        scope g := io.Scope() {
            g.go SmNap(io, 10)
            scope d := io.Scope() {
                d.CancelAfter(time.Millis(2))
                p.WriteAll(io, data[..]) else e {
                    failed = PtSame(PtName(e), "Canceled") && d.Expired()
                }
            }
        }
        Assert(failed)
        k3 := SmPtyIdle(io, &p, mut got[..], 0)
        Assert(k3 > 0 && k3 < n && PtIsPattern(got[0..k3]))
        pc.Kill().Must()
        Assert(SmKilled(pc.Wait(io).Must()))
        var b: [4]u8 = [0; 4]
        Assert(p.Read(io, mut b[..]).Must() == 0)
        p.Close()
        s.Free()
    }
}

test "Sim: Spawn and Run in a cancelled scope under a Sim are Canceled and start no child, and the same Spawn after the scope starts one, seeds 1 to 3" (sys: &System) {
    var by_name: [2]str = [SmName(), "x"]
    for seed in 1..4 {
        var s := SmNew(sys.arena, seed)
        io := s.Io()
        scope d := io.Scope() {
            d.Cancel()
            Assert(PtSame(PtName(SmSpawn(sys.arena, io, by_name[..])), "Canceled"))
            Assert(PtSame(PtName(SmRun(sys.arena, io, by_name[..], b"in")), "Canceled"))
        }
        (child, stdin, stdout, stderr) := SmCat(sys.arena, io, by_name[..], Stdio.Null, Stdio.Null, Stdio.Null)
        SpCloseEnds(stdin, stdout, stderr)
        Assert(SpCode(child.Wait(io).Must()) == 0)
        s.Free()
    }
}

// SmSigRun is one Sim of seed: WaitSignal three times, then once under a
// 2 ms deadline beside a napping task; each is Canceled, since no signal
// reaches a simulation (std.sim answers a signal wait as a deadlock). The
// cuts, mixed with the run's clock.
SmSigRun(a: &Arena, seed: int): i64 {
    var s := SmNew(a, seed)
    io := s.Io()
    t0 := time.Now(io)
    var cuts := 0
    for _ in 0..3 {
        WaitSignal(io, Signal.Hangup) else {
            cuts += 1
        }
    }
    scope g := io.Scope() {
        g.go SmNap(io, 10)
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(2))
            WaitSignal(io, Signal.Interrupt) else {
                cuts += 1
            }
        }
    }
    Assert(cuts == 4)
    mix := i64(cuts) * 1_000_000_007 +% time.Now(io).Since(t0).Nanos()
    s.Free()
    return mix
}

test "Sim: WaitSignal under a Sim is Canceled, the same call again Canceled the same way, under a deadline too, since no signal reaches a simulation; a seed's run is the same when replayed, seeds 1 to 8" (sys: &System) {
    for seed in 1..9 {
        m := SmSigRun(sys.arena, seed)
        Assert(SmSigRun(sys.arena, seed) == m)
    }
}
