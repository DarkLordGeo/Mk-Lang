// Why: a NoCancel mask masks scope cancellation but a dead run is not a
// scope cancellation, so core's one cancellation check (a sleep of 0) must
// tell the two apart on every Io a simulation makes.
// What: blocks on that check through a Sim's, a Recorder's and a
// Replayer's Io, masked and not, in live, crashed, restarted and freed
// runs; helpers NcLock, NcCheck and NcDead.
// How: each block locks a free Mutex or runs the check under cancelled
// scopes inside and around the mask. Run by mk test --std=std.sim.

import std.core
import std.fs
import std.raw

// NcLock is 1 when a Lock of the free mutex m through io goes on, 0 when
// it is Canceled.
NcLock(m: &Mutex[int], io: Io): int {
    var g := m.Lock(io) else {
        return 0
    }
    g.Unlock()
    return 1
}

// NcCheck is 1 when core's check (SyncCancel) through io goes on, 0 when it
// is Canceled.
NcCheck(io: Io): int {
    core.SyncCancel(io) else {
        return 0
    }
    return 1
}

// NcDead: a counted operation answers Canceled (the run crashed).
NcDead(io: Io): bool {
    fs.Remove(io, "mk_nocancel_none") else e {
        return match e {
            Canceled { true }
            _ { false }
        }
    }
    return false
}

// A NoCancel mask masks scope cancellation, so under a Sim's masked Io a
// cancelled scope around the mask does not cancel the check, and a
// cancelled scope opened from the masked Io does: a free Lock is
// Canceled there. core's check asks the Sim's entry for a masked Io too; a
// check that skipped every masked Io of another table would take the lock.
test "under a Sim, a cancelled scope opened from the masked Io cancels a free Lock, and the scope around the mask does not" (sys: &System) {
    m := Mutex[int].New(0)
    var s := try Sim.New(sys.arena, 11)
    io := s.Io()
    var outer := 9
    var outer_check := 9
    var own := 9
    var own_check := 9
    scope a := io.Scope() {
        a.Cancel()
        q := io.NoCancel()
        outer = NcLock(&m, q)
        outer_check = NcCheck(q)
        scope b := q.Scope() {
            b.Cancel()
            own = NcLock(&m, q)
            own_check = NcCheck(q)
        }
    }
    s.Free()
    Assert(outer == 1)
    Assert(outer_check == 1)
    Assert(own == 0)
    Assert(own_check == 0)
}

// A dead run (crashed, restarted or freed) is not a scope cancellation, so
// a NoCancel mask does not let it through: the masked check of a dead run
// goes on, and a masked free Lock in cleanup takes the lock, while
// an unmasked one is Canceled, as every operation of a crashed run is.
test "the masked check of a crashed, restarted or freed Sim run goes on; the unmasked one is Canceled" (sys: &System) {
    m := Mutex[int].New(0)
    var s := try Sim.New(sys.arena, 11)
    s.CrashAfterRandomOps()
    io := s.Io()
    var k := 0
    for k < 100000 && !NcDead(io) {
        k += 1
    }
    Assert(k < 100000)
    crash_masked := NcLock(&m, io.NoCancel())
    crash_check := NcCheck(io.NoCancel())
    crash_plain := NcLock(&m, io)
    nio := s.Restart()
    old_masked := NcLock(&m, io.NoCancel())
    old_plain := NcLock(&m, io)
    new_masked := NcLock(&m, nio.NoCancel())
    new_plain := NcLock(&m, nio)
    var stale_boundary := 9
    // A cancelled scope opened from the stale masked Io still cancels the
    // check under it: the dead run lets only the mask through.
    q := io.NoCancel()
    scope b := q.Scope() {
        b.Cancel()
        stale_boundary = NcLock(&m, q)
    }
    s.Free()
    freed_masked := NcLock(&m, nio.NoCancel())
    freed_plain := NcLock(&m, nio)
    Assert(crash_masked == 1)
    Assert(crash_check == 1)
    Assert(crash_plain == 0)
    Assert(old_masked == 1)
    Assert(old_plain == 0)
    Assert(new_masked == 1)
    Assert(new_plain == 1)
    Assert(stale_boundary == 0)
    Assert(freed_masked == 1)
    Assert(freed_plain == 0)
}

// A Recorder logs its inner Io's sleep result as it is (core.IoSleep), so
// only Canceled is a cancellation through it too: over an Io with no OS
// (core.InertIo, whose sleep answers Other) the check and a free Lock go
// on, masked or not, as on the inner Io itself. A freed Recorder's masked
// check goes on as a dead Sim run's does; its unmasked one is Canceled.
test "a Recorder passes its inner Io's check result through, and a freed Recorder's masked check goes on" (sys: &System) {
    m := Mutex[int].New(0)
    inert := core.MakeIo(raw.Of(&core.InertIo), 0)
    var rec := try Recorder.New(inert)
    ri := rec.Io()
    plain := NcLock(&m, ri)
    masked := NcLock(&m, ri.NoCancel())
    check := NcCheck(ri)
    masked_check := NcCheck(ri.NoCancel())
    rec.Free()
    freed_masked := NcLock(&m, ri.NoCancel())
    freed_plain := NcLock(&m, ri)
    Assert(plain == 1)
    Assert(masked == 1)
    Assert(check == 1)
    Assert(masked_check == 1)
    Assert(NcLock(&m, inert) == 1)
    Assert(NcLock(&m, inert.NoCancel()) == 1)
    Assert(freed_masked == 1)
    Assert(freed_plain == 0)
}

// A recording replays the checks it logged: a Replayer over the log of a
// Recorder over the native Io answers each logged check as recorded, and a
// cancelled scope's check, recorded as Canceled, replays as Canceled.
test "a Replayer answers the logged checks as recorded" (sys: &System) {
    m := Mutex[int].New(0)
    var rec := try Recorder.New(sys.io)
    ri := rec.Io()
    var got: [4]int = [9; 4]
    got[0] = NcCheck(ri)
    got[1] = NcLock(&m, ri.NoCancel())
    scope a := ri.Scope() {
        a.Cancel()
        got[2] = NcCheck(ri)
        got[3] = NcCheck(ri.NoCancel())
    }
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    var rep := try Replayer.New(log)
    pi := rep.Io()
    var again: [4]int = [9; 4]
    again[0] = NcCheck(pi)
    again[1] = NcLock(&m, pi.NoCancel())
    scope b := pi.Scope() {
        b.Cancel()
        again[2] = NcCheck(pi)
        again[3] = NcCheck(pi.NoCancel())
    }
    diverged := rep.Diverged()
    rep.Free()
    Assert(got[0] == 1)
    Assert(got[1] == 1)
    Assert(got[2] == 0)
    Assert(got[3] == 1)
    for i in 0..4 {
        Assert(again[i] == got[i])
    }
    Assert(!diverged)
}

// NcSleep is 1 when a sleep of 1 ms through io goes on, 0 when it is
// Canceled: a real wait, not the check.
NcSleep(io: Io): int {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return 0
    }
    return 1
}

// NcBoundary is NcLock of a masked free Lock under a cancelled scope
// opened from io's masked Io.
NcBoundary(m: &Mutex[int], io: Io): int {
    q := io.NoCancel()
    var r := 9
    scope b := q.Scope() {
        b.Cancel()
        r = NcLock(m, q)
    }
    return r
}

// A Replayer that cannot replay a masked check, because it has diverged,
// run out of log or meets another op at the next record, is a dead run,
// not a scope cancellation: the check is marked diverged and goes on, so a
// masked free Lock takes the mutex, as under a dead Sim run. An unmasked
// check there is Canceled, and so is a masked one under a cancelled scope
// opened from the masked Io.
test "a Replayer that cannot replay a masked check goes on: diverged, out of log, or another op next" (sys: &System) {
    m := Mutex[int].New(0)
    // A log of one other op: a read of the clock.
    var rec := try Recorder.New(sys.io)
    ri := rec.Io()
    _ = core.IoNow(ri)
    one := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    // An empty log: a Recorder that made no call.
    var rec0 := try Recorder.New(sys.io)
    nolog := rec0.Log(sys.arena) else {
        rec0.Free()
        Assert(false)
        return
    }
    rec0.Free()

    var other := try Replayer.New(one)
    oi := other.Io()
    other_lock := NcLock(&m, oi.NoCancel())
    other_div := other.Diverged()
    other_check := NcCheck(oi.NoCancel())
    other_plain := NcCheck(oi)
    other.Free()

    var empty := try Replayer.New(nolog)
    ei := empty.Io()
    empty_lock := NcLock(&m, ei.NoCancel())
    empty_div := empty.Diverged()
    empty_check := NcCheck(ei.NoCancel())
    empty_plain := NcCheck(ei)
    empty_bound := NcBoundary(&m, ei)
    empty.Free()

    // Diverged first by an unmasked call the log does not hold.
    var div := try Replayer.New(nolog)
    di := div.Io()
    div_first := NcCheck(di)
    div_div := div.Diverged()
    div_lock := NcLock(&m, di.NoCancel())
    div_check := NcCheck(di.NoCancel())
    div_bound := NcBoundary(&m, di)
    div.Free()

    Assert(other_lock == 1)
    Assert(other_div)
    Assert(other_check == 1)
    Assert(other_plain == 0)
    Assert(empty_lock == 1)
    Assert(empty_div)
    Assert(empty_check == 1)
    Assert(empty_plain == 0)
    Assert(empty_bound == 0)
    Assert(div_first == 0)
    Assert(div_div)
    Assert(div_lock == 1)
    Assert(div_check == 1)
    Assert(div_bound == 0)
}

// The dead-run rule covers the check only, a sleep of 0: a real wait of a
// dead run, a masked 1 ms sleep, stays Canceled, on a crashed, restarted
// and freed Sim and on a freed Recorder. A cancelled scope opened from the
// masked Io still cancels the check in a crashed run and on a freed
// Recorder.
test "a dead run's masked real wait stays Canceled, and a cancelled boundary from its masked Io still cancels the check" (sys: &System) {
    m := Mutex[int].New(0)
    var s := try Sim.New(sys.arena, 11)
    s.CrashAfterRandomOps()
    io := s.Io()
    var k := 0
    for k < 100000 && !NcDead(io) {
        k += 1
    }
    Assert(k < 100000)
    crash_sleep := NcSleep(io.NoCancel())
    crash_bound := NcBoundary(&m, io)
    crash_lock := NcLock(&m, io.NoCancel())
    _ = s.Restart()
    restart_sleep := NcSleep(io.NoCancel())
    s.Free()
    freed_sleep := NcSleep(io.NoCancel())
    var rec := try Recorder.New(sys.io)
    ri := rec.Io()
    rec.Free()
    rec_sleep := NcSleep(ri.NoCancel())
    rec_bound := NcBoundary(&m, ri)
    rec_lock := NcLock(&m, ri.NoCancel())
    Assert(crash_sleep == 0)
    Assert(crash_bound == 0)
    Assert(crash_lock == 1)
    Assert(restart_sleep == 0)
    Assert(freed_sleep == 0)
    Assert(rec_sleep == 0)
    Assert(rec_bound == 0)
    Assert(rec_lock == 1)
}
