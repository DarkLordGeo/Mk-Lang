// Why: the order of deadline fires in a log decides which scopes a replay
// reads as Expired, so where a replay fires each logged deadline must match
// the run it replays, or the replay must diverge.
// What: blocks on where a replay fires a logged deadline, what it does with
// a fire it cannot replay as recorded, the error codes a replayed call op
// gives back, and a log whose instants no clock wrote (TfSimTie,
// TfSimEarlier and other helpers).
// How: one task makes every tape call and the calls never overlap, the
// case a replay is exact for. Run by mk test --std=std.sim.

import std.fs
import std.time

// TfSpin computes on nat for ms milliseconds, with no call through a tape.
TfSpin(nat: Io, ms: i64) {
    t0 := time.Now(nat)
    for time.Now(nat).Since(t0).Nanos() < ms * 1_000_000 {
    }
}

// TfFeeder sends 1 on ch after 10 ms of the native clock.
TfFeeder(nat: Io, ch: &Chan[int]) {
    TfSpin(nat, 10)
    ch.Send(nat, 1) else {}
}

// TfWaitThenSleep, under a 200 ms deadline on io: a channel wait the
// feeder ends at 10 ms, then a 5 s sleep the deadline cuts. The result is
// the value received (-2 Canceled), plus 100 when the sleep was cut, times
// 10, plus 1 when the scope was Expired.
TfWaitThenSleep(io: Io, ch: &Chan[int]): i64 {
    var got: i64 = 0
    var exp: i64 = 0
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(200))
        v := ch.Recv(io) else {
            -2
        }
        got = i64(v)
        io.Sleep(time.Seconds(5)) else {
            got += 100
        }
        exp = if s.Expired() { 1 } else { 0 }
    }
    return got * 10 + exp
}

// TfWaitRun runs TfWaitThenSleep on io beside a native feeder.
TfWaitRun(sys: &System, io: Io, ch: &Chan[int]): i64 {
    var r: i64 = 0
    scope outer := sys.io.Scope() {
        outer.go TfFeeder(sys.io, ch)
        r = TfWaitThenSleep(io, ch)
    }
    return r
}

test "SIM-Tape-WaitThenCall a deadline that fires after a channel wait ended as recorded replays at the next call, not at that wait" (sys: &System) {
    a := sys.heap.Alloc()
    ch := Chan[int].New(a, 1) else {
        Assert(false)
        return
    }
    defer ch.Free(a)
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    r1 := TfWaitRun(sys, rec.Io(), &ch)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    // Received, then the sleep cut by the deadline, the scope Expired.
    Assert(r1 == 1011)
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    r2 := TfWaitRun(sys, rp.Io(), &ch)
    Assert(r2 == r1)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TfNested: a scope P with a 40 ms deadline holds a scope C with a 10 ms
// one, and a 200 ms NoCancel sleep spans both fires, so both are logged
// before the sleep's record. 10 when P was Expired, plus 1 when C was.
TfNested(io: Io): i64 {
    var pe: i64 = 0
    var ce: i64 = 0
    scope p := io.Scope() {
        p.CancelAfter(time.Millis(40))
        scope c := io.Scope() {
            c.CancelAfter(time.Millis(10))
            io.NoCancel().Sleep(time.Millis(200)) else {}
            _ = time.Now(io)
            ce = if c.Expired() { 1 } else { 0 }
        }
        pe = if p.Expired() { 10 } else { 0 }
    }
    return pe + ce
}

test "SIM-Tape-FireOrder an ancestor armed first with a later deadline and a descendant with an earlier one, both firing between two records, replay both Expired" (sys: &System) {
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    r1 := TfNested(rec.Io())
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    Assert(r1 == 11)
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    r2 := TfNested(rp.Io())
    Assert(r2 == r1)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TfCancelRace: a 10 ms deadline fires during 40 ms of work with no tape
// call, the program then cancels the scope (a no-op natively: the flag is
// set) and reads the tape's clock. 10 when Expired before the clock read,
// plus 1 when after it.
TfCancelRace(nat: Io, io: Io): i64 {
    var b: i64 = 0
    var a: i64 = 0
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(10))
        TfSpin(nat, 40)
        s.Cancel()
        b = if s.Expired() { 10 } else { 0 }
        _ = time.Now(io)
        a = if s.Expired() { 1 } else { 0 }
    }
    return b + a
}

test "SIM-Tape-CancelRace a Cancel between a fire and the next call, which the replay cannot place before the fire, diverges the replay instead of replaying another Expired" (sys: &System) {
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    r1 := TfCancelRace(sys.io, rec.Io())
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    // With one carrier nothing fires the deadline during the spin (a
    // carrier never preempts a task), so the Cancel comes first natively
    // and the scope is never Expired: then the replay must agree.
    Assert(r1 == 11 || r1 == 0)
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    r2 := TfCancelRace(sys.io, rp.Io())
    if r1 == 11 {
        // The replayed Cancel took the flag first, so the logged fire
        // cannot set it: a reported divergence, never a silent other
        // Expired.
        Assert(rp.Diverged())
    } else {
        Assert(r2 == r1 && !rp.Diverged() && rp.Done())
    }
    rp.Free()
}

// TfErr is e's name.
TfErr(a: &Arena, e: fs.Error | OutOfMemory): str from a {
    return a.Fmt("{e}", (e)).Must()
}

// TfSimOps makes call ops a Sim does not simulate (Cwd, SetModified,
// SetPermissions, Symlink: Unsupported) and one that fails (a Copy of a
// missing file: NotFound) on io, and names each answer.
TfSimOps(a: &Arena, io: Io): str from a {
    var s := "ops"
    cwd := fs.Cwd(a, io) else e {
        TfErr(a, e)
    }
    s = a.Fmt("{s} cwd={cwd}", (s, cwd)).Must()
    fs.SetModified(io, "f", time.FromUnix(1_700_000_000, 0)) else e {
        s = a.Fmt("{s} setmod={e}", (s, e)).Must()
    }
    fs.SetPermissions(io, "f", fs.Permissions(mode: 0o444)) else e {
        s = a.Fmt("{s} setperm={e}", (s, e)).Must()
    }
    _ = fs.Copy(io, "f", "g") else e {
        s = a.Fmt("{s} copy={e}", (s, e)).Must()
        0
    }
    fs.Symlink(io, "f", "l") else e {
        s = a.Fmt("{s} symlink={e}", (s, e)).Must()
    }
    return s
}

test "SIM-Tape-SimErrs a Recorder over a Sim replays each call op's error by name, Unsupported included" (sys: &System) {
    var sm := Sim.New(sys.arena, 1) else {
        Assert(false)
        return
    }
    var rec := Recorder.New(sm.Io()) else {
        sm.Free()
        Assert(false)
        return
    }
    s1 := TfSimOps(sys.arena, rec.Io())
    log := rec.Log(sys.arena) else {
        rec.Free()
        sm.Free()
        Assert(false)
        return
    }
    rec.Free()
    sm.Free()
    Assert(s1.Has("Unsupported"))
    Assert(s1.Has("copy=NotFound"))
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    s2 := TfSimOps(sys.arena, rp.Io())
    Assert(s1 == s2)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TfRemaining arms a 10 s deadline on io and reads Remaining (a clock read
// through io): 1 when some time is left, 0 when none, -1 for no deadline.
TfRemaining(io: Io): i64 {
    var r: i64 = -1
    scope sc := io.Scope() {
        sc.CancelAfter(time.Seconds(10))
        if d := sc.Remaining() {
            r = if d.Nanos() > 0 { 1 } else { 0 }
        }
    }
    return r
}

// TfNth is the offset of the first record of log whose op is op, or -1.
TfNth(log: []u8, op: u8): int {
    var at := 0
    for at + RecordHeader <= log.Len() {
        if log[at] == op {
            return at
        }
        at = at + RecordHeader + Cast[int](GetU64(log, at + 33) & 0xFFFF_FFFF)
    }
    return -1
}

// TfEdited replays log with its first ODeadline record's instant set to
// dl (unless dl is 0) and its first ONow record's reading set to now:
// true when the replay diverged.
TfEdited(sys: &System, log: []u8, dl: u64, now: u64): bool {
    b := sys.arena.Slice[u8](log.Len()) else {
        return false
    }
    for i in 0..log.Len() {
        b[i] = log[i]
    }
    d := TfNth(log, ODeadline)
    w := TfNth(log, ONow)
    if d < 0 || w < 0 {
        return false
    }
    if dl != 0 {
        PutAt(mut b[..], d + 9, dl)
    }
    PutAt(mut b[..], w + 9, now)
    var rp := Replayer.New(b[..]) else {
        return false
    }
    _ = TfRemaining(rp.Io())
    dv := rp.Diverged()
    rp.Free()
    return dv
}

test "SIM-Tape-EditedInstants a log whose deadline or clock reading no clock gives diverges the replay instead of overflowing Remaining" (sys: &System) {
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    Assert(TfRemaining(rec.Io()) == 1)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    // The deadline at i64's maximum and the clock read negative; the clock
    // read negative alone, against the recorded deadline.
    Assert(TfEdited(sys, log, 0x7FFF_FFFF_FFFF_FFFF, Trunc[u64](-1_000_000)))
    Assert(TfEdited(sys, log, 0, Trunc[u64](-1_000_000)))
    // Unedited, the log replays.
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    Assert(TfRemaining(rp.Io()) == 1)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TfSimTie: inside P, C is armed first and
// then P, both for 10 ms, and a NoCancel sleep of 50 ms passes both. A
// Sim fires a tie in arming order, C then P, so both are
// Expired; by the tie rule alone the log would put P first, and the
// replay's walk of P would take C's flag. 10 for P Expired, plus 1 for C.
TfSimTie(io: Io): i64 {
    var pe: i64 = 0
    var ce: i64 = 0
    scope p := io.Scope() {
        scope c := io.Scope() {
            c.CancelAfter(time.Millis(10))
            p.CancelAfter(time.Millis(10))
            io.NoCancel().Sleep(time.Millis(50)) else {}
            _ = time.Now(io)
            ce = if c.Expired() { 1 } else { 0 }
        }
        pe = if p.Expired() { 10 } else { 0 }
    }
    return pe + ce
}

// TfSimEarlier: P armed at 100 us, C at 500 ns, then P
// re-armed to 200 ns, which moves P to the end of the Sim's list; a file
// write's disk latency passes both at once. The Sim fires the earlier
// first, P, whose walk cancels C, so P alone is Expired, as
// natively, though C is first in the Sim's list: 10, and the replay
// must give the same. One fire is logged, so this case does not reach
// FiresFirst's depth-before-instant order; SIM-Tape-DepthFirst does.
TfSimEarlier(io: Io): i64 {
    var pe: i64 = 0
    var ce: i64 = 0
    scope p := io.Scope() {
        p.CancelAfter(time.Micros(100))
        scope c := io.Scope() {
            c.CancelAfter(time.Nanos(500))
            p.CancelAfter(time.Nanos(200))
            fs.WriteAll(io.NoCancel(), "tf-g2.txt", "x".AsBytes()) else {}
            _ = time.Now(io)
            ce = if c.Expired() { 1 } else { 0 }
        }
        pe = if p.Expired() { 10 } else { 0 }
    }
    return pe + ce
}

// TfSimCase records case which under a Recorder over a fresh Sim and
// replays it: the recorded value, and whether the replay gave the same
// value with no divergence and to the end.
TfSimCase(sys: &System, which: int): (r: i64, same: bool) {
    var sm := Sim.New(sys.arena, 1).Must()
    var rec := Recorder.New(sm.Io()) else {
        sm.Free()
        return (r: -1, same: false)
    }
    r1 := if which == 1 { TfSimTie(rec.Io()) } else { TfSimEarlier(rec.Io()) }
    log := rec.Log(sys.arena) else {
        rec.Free()
        sm.Free()
        return (r: -2, same: false)
    }
    rec.Free()
    sm.Free()
    var rp := Replayer.New(log) else {
        return (r: -3, same: false)
    }
    r2 := if which == 1 { TfSimTie(rp.Io()) } else { TfSimEarlier(rp.Io()) }
    ok := r2 == r1 && !rp.Diverged() && rp.Done()
    rp.Free()
    return (r: r1, same: ok)
}

test "SIM-Tape-SimFireOrder under a Recorder over a Sim, a descendant that fires before its ancestor on a tie (the Sim's arming order) replays both Expired, and an ancestor whose instant is the earlier fires first and replays alone Expired" (sys: &System) {
    for which in 1..3 {
        o := TfSimCase(sys, which)
        want: i64 = if which == 1 { 11 } else { 10 }
        Assert(o.r == want)
        Assert(o.same)
    }
}

// SIM-Tape-DepthFirst records TfNested under a Recorder over a Sim (C at
// 10 ms, then P at 40 ms, both fired at the sleep's choice and logged in
// one batch, C first), and edits a copy of the log so P's instant is 1 us
// before C's: the order no Sim reaches (P's walk would cancel
// C first), and that racing native carriers can. A Replayer of the copy
// still fires C and then P; a Recorder over it must log C first, the
// deeper scope, though P's instant is the earlier (FiresFirst).
test "SIM-Tape-DepthFirst a Recorder over a Replayer that fires a descendant and then its ancestor with the earlier instant, both at one call, logs the descendant first, and its log replays both Expired" (sys: &System) {
    var sm := Sim.New(sys.arena, 1).Must()
    var rec0 := Recorder.New(sm.Io()) else {
        sm.Free()
        Assert(false)
        return
    }
    r0 := TfNested(rec0.Io())
    log0 := rec0.Log(sys.arena) else {
        rec0.Free()
        sm.Free()
        Assert(false)
        return
    }
    rec0.Free()
    sm.Free()
    Assert(r0 == 11)
    p := TfNth(log0, ODeadline)
    Assert(p >= 0)
    c := p + RecordHeader + TfNth(log0[p + RecordHeader..], ODeadline)
    Assert(c > p + RecordHeader - 1)
    b := sys.arena.Slice[u8](log0.Len()) else {
        Assert(false)
        return
    }
    for i in 0..log0.Len() {
        b[i] = log0[i]
    }
    PutAt(mut b[..], p + 9, GetU64(log0, c + 9) - 1000)
    var rp1 := Replayer.New(b[..]) else {
        Assert(false)
        return
    }
    var rec := Recorder.New(rp1.Io()) else {
        rp1.Free()
        Assert(false)
        return
    }
    r1 := TfNested(rec.Io())
    log := rec.Log(sys.arena) else {
        rec.Free()
        rp1.Free()
        Assert(false)
        return
    }
    rec.Free()
    Assert(r1 == 11)
    Assert(!rp1.Diverged())
    Assert(rp1.Done())
    rp1.Free()
    // Replayed in instant order, P's walk would take C's flag first.
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    r2 := TfNested(rp.Io())
    Assert(r2 == 11)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}
