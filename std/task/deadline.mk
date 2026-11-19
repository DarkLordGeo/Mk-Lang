// Why: a deadline is a property of a scope (Scope.CancelAfter, Expired,
// Remaining); when it passes, the scope is cancelled as by Cancel, and the
// scope's exit must never race a fire still reading its record.
//
// What: NativeDeadline, the native table's deadline entry; DeadlineNote,
// DeadlineFire and Disarm; and DeadlineAt, DeadlineDepth and
// DeadlineExpired, which std.sim's Recorder reads.
//
// How: a deadline is the scope record's dl (a TimerRec), an entry of one
// carrier's timer heap (timer.mk) tagged as a scope (ScopeTag). States: Idle
// (none), Armed (in a heap), Fired (popped by a FireHeap, DeadlineFire
// running), Cancelled (removed, or DeadlineFire done). Lock order: the
// scope's lock, then one heap's tlock; the fire takes the scope's lock with
// the heap's lock released.
//
// Invariants, each with its test (std/task/tests_deadline.mk):
// - Earliest: only the earliest deadline counts: a CancelAfter later
//   than the armed one changes nothing ("RT8-Dl-Earliest").
// - Expired: Expired is true exactly when the scope's own deadline
//   set its flag; the flag's single compare-and-swap decides between the
//   deadline, Cancel and an ancestor's walk ("RT8-Dl-RaceCancel",
//   "RT8-Dl-CancelFirst", "RT8-Dl-Ancestor").
// - Exit sync: Join does not return while DeadlineFire runs on its
//   record: DeadlineFire's last access to the record is its store of
//   Cancelled, which Disarm waits for ("RT8-Dl-ExitWindow").
// - Owed: an armed deadline earlier than every idle carrier's wake
//   gets a carrier (OweArm, then WakeOne), so it fires while the arming task
//   computes ("RT8-Dl-Busy").

import std.core
import std.raw
import std.rt
import std.sys

/// NativeDeadline is the native table's deadline entry: op 0 arms rec's
/// deadline nanos from now on sys.Now's clock (nanos <= 0 fires it at once);
/// op 1 disarms it (Join). Always None. O(log n) in the armed timers, plus
/// a cancel walk when it fires at once; allocates nothing (the heap array
/// grows through rt.HeapAlloc, and a failure there is fatal, as SleepFor's).
export NativeDeadline(io: Io, rec: *u8, nanos: i64, op: u32): core.IoResult {
    r := RecAt(rec)
    if op == 1 {
        Disarm(r)
        return NoErr(0)
    }
    now := sys.Now()
    if nanos <= 0 {
        // A zero or negative d fires at once, as a deadline that has
        // passed: Expired when the scope was not cancelled first.
        DeadlineNote(r, now)
        DeadlineFire(r, raw.Null[Carrier]())
        return NoErr(0)
    }
    at := if nanos > MaxDeadline - 1 - now { MaxDeadline - 1 } else { now + nanos }
    st := TS()
    var kick := false
    unsafe "a live scope record; a carrier's timer heap under its tlock" {
        rt.Lock(&(*r).lock)
        // Edge: a cancel walk's CAS of the flag comes before its DisarmLocked
        // of r under r's lock (CancelWalk, FirstUncancelled), so this Load
        // reads the flag set, or that walk disarms what is armed below.
        if !DeadlineEarlier(r, at) || (*r).cancelled.Load() != 0 {
            // Only the earliest deadline counts, and a cancelled scope never expires (its
            // walk disarmed it; DeadlineEarlier still recorded at).
            rt.Unlock(&(*r).lock)
            return NoErr(0)
        }
        tr := raw.Of(&(*r).dl)
        // Edge: under r's lock, the only lock Armed is stored under: Idle
        // and Cancelled stay so while it is held (a fire's Fired to
        // Cancelled is its last use of r), and Armed may turn Fired, which
        // the heap's lock settles below.
        s := (*r).dl.state.Load()
        if s == TimerFired {
            // The earlier deadline is firing now; it cancels the scope.
            rt.Unlock(&(*r).lock)
            return NoErr(0)
        }
        var h := raw.Null[Carrier]()
        if s == TimerArmed {
            // An armed deadline moves inside its own heap, so its
            // heap part never changes while it is Armed (timer.mk).
            h = LockHeapOf(tr)
            // Edge: under the heap's tlock, as FireHeap's TimerFired.
            if (*r).dl.state.Load() == TimerFired {
                rt.Unlock(&(*h).tlock)
                rt.Unlock(&(*r).lock)
                return NoErr(0)
            }
            RemoveAt(h, PosOf(tr))
        } else {
            h = HeapHere()
            rt.Lock(&(*h).tlock)
        }
        (*r).dl.deadline = at
        if !Insert(h, EntryOfScope(r)) {
            rt.Unlock(&(*h).tlock)
            rt.Unlock(&(*r).lock)
            rt.Fatal("cannot grow the timer heap")
        }
        // Edge: under the heap's tlock and r's lock, the deadline's only
        // Store of Armed, after the insert wrote its heap part: FireHeap's
        // pop overwrites it with Fired under the same tlock; the disarms
        // read it, then lock the heap it names (LockHeapOf), and
        // DeadlineEarlier reads it under r's lock.
        (*r).dl.state.Store(TimerArmed)
        PublishNext(h)
        // Edge: after PublishNext's store in S (its comment).
        kick = at < (*st).armed_at.Load()
        rt.Unlock(&(*h).tlock)
        rt.Unlock(&(*r).lock)
    }
    if kick {
        // The owed arm (test "RT-MN-TimerOwner"): the arming task may compute for a
        // long time without a schedule step, so an idle carrier is woken to
        // arm for this deadline (ArmIdle); OweArm keeps the debt if none is
        // idle now.
        OweArm()
        WakeOne()
    }
    return NoErr(0)
}

// DeadlineEarlier records at as r's deadline when r has none or a later
// one (dl_set, dl.deadline), and reports whether it did; r's lock is held.
DeadlineEarlier(r: *ScopeRec, at: i64): bool {
    unsafe "r's lock is held" {
        if (*r).dl_set != 0 && (*r).dl.deadline <= at {
            return false
        }
        (*r).dl_set = 1
        // Edge: r's lock, not a heap lock: Armed is stored only under r's lock
        // (NativeDeadline), so a non-Armed read holds while it is held; an
        // Armed one may turn Fired meanwhile, and NativeDeadline
        // reads it again under the heap's tlock.
        if (*r).dl.state.Load() != TimerArmed {
            // An armed entry's deadline is moved under its heap's tlock by
            // the caller; any other state is out of every heap.
            (*r).dl.deadline = at
        }
        return true
    }
}

// DeadlineNote records a deadline at, the earliest, without arming: a
// deadline that fires at once (NativeDeadline with d <= 0). A later
// deadline still armed is disarmed first (DisarmLocked: under r's lock,
// then its heap's tlock), so at is recorded whatever the timer's state,
// and Remaining reads 0 once the zero deadline fired
// (test "RT8-Dl-ZeroAfterArmed"). A deadline that another carrier already popped
// (Fired) is out of the heap, so its deadline field is r's lock's alone.
DeadlineNote(r: *ScopeRec, at: i64) {
    unsafe "a live scope record" {
        rt.Lock(&(*r).lock)
        if (*r).dl_set == 0 || at < (*r).dl.deadline {
            (*r).dl_set = 1
            DisarmLocked(r)
            (*r).dl.deadline = at
        }
        rt.Unlock(&(*r).lock)
    }
}

/// DeadlineSet is DeadlineNote for std.sim: it records at (on the Sim's
/// clock) as rec's deadline when it is the earliest, for Remaining and
/// Join, and reports whether it was; false also when rec is cancelled
/// already (a cancelled scope never expires). O(1); allocates nothing.
export DeadlineSet(rec: *u8, at: i64): bool {
    r := RecAt(rec)
    unsafe "a live scope record" {
        rt.Lock(&(*r).lock)
        // Edge: as NativeDeadline's; a cancel whose CAS this Load misses
        // meets the Sim's fire at the flag's one CAS, which decides Expired.
        ok := DeadlineEarlier(r, at) && (*r).cancelled.Load() == 0
        rt.Unlock(&(*r).lock)
        return ok
    }
}

/// DeadlineArmed reports whether rec has a deadline recorded (dl_set), so
/// its scope's Join will disarm it through the scope's Io (std.sim's
/// Recorder keeps the record listed until then). O(1); allocates nothing.
export DeadlineArmed(rec: *u8): bool {
    r := RecAt(rec)
    unsafe "a live scope record" {
        rt.Lock(&(*r).lock)
        set := (*r).dl_set != 0
        rt.Unlock(&(*r).lock)
        return set
    }
}

/// DeadlineFireRec is DeadlineFire for std.sim, at a choice when its clock
/// has passed rec's deadline. O(scopes under rec plus their blocked tasks).
export DeadlineFireRec(rec: *u8) {
    DeadlineFire(RecAt(rec), raw.Null[Carrier]())
}

/// DeadlineAt is the deadline recorded for rec (dl.deadline, on the clock
/// of the Io that armed it), none when it has none: what std.sim's
/// Recorder logs for an arm, so its Replayer records the same one and
/// Remaining replays. O(1); allocates nothing.
export DeadlineAt(rec: *u8): ?i64 {
    r := RecAt(rec)
    unsafe "a live scope record" {
        rt.Lock(&(*r).lock)
        set := (*r).dl_set != 0
        at := (*r).dl.deadline
        rt.Unlock(&(*r).lock)
        if !set {
            return none
        }
        return some(at)
    }
}

/// DeadlineDepth is rec's level in its chain (ScopeRec.depth, which
/// strictly grows down a chain), so std.sim's Recorder logs a descendant's
/// fire before an ancestor's. O(1); allocates nothing.
export DeadlineDepth(rec: *u8): u32 {
    r := RecAt(rec)
    unsafe "a live scope record; depth is set at the scope's open and never changes" {
        return (*r).depth
    }
}

/// DeadlineExpired: rec's own deadline set its cancel flag (Expired), so
/// std.sim's Recorder logs the fire. O(1): one load; allocates
/// nothing.
export DeadlineExpired(rec: *u8): bool {
    r := RecAt(rec)
    unsafe "a live scope record" {
        // Edge: as Expired's load of the flag's one CAS.
        return (*r).cancelled.Load() == CancelledByDeadline
    }
}

// DeadlineFire is r's deadline firing: a cancel walk whose flag CAS, under
// r's lock, records the cause (Expired), then the walk's work after
// the locks. Its last access to r is the store of TimerCancelled, which
// Disarm waits for, made only when popped: h is the heap
// this call's FireHeap popped the entry from, null for a synchronous fire.
// The store takes h's lock, the one the entry's heap part still names
// (LockHeapOf), so it never reads r to find it; a wake after it goes by the
// tasks' addresses only, never r's.
DeadlineFire(r: *ScopeRec, h: *Carrier) {
    var acc := CancelLists(ready: raw.Null[Task](), poll: raw.Null[Task](), interrupt: false)
    _ = CancelWalk(r, raw.OfMut(mut acc), true)
    unsafe "a live scope record: Join's Disarm waits for this store" {
        // Only the carrier that popped the entry (Armed to Fired) releases
        // it: a synchronous fire (d <= 0, a Sim's) never stores the state,
        // or it could release a record another carrier's fire still reads.
        if !raw.IsNull(h) {
            rt.Lock(&(*h).tlock)
            // Edge: the release of the record to Join, whose Disarm loads
            // TimerCancelled (before or under this lock) ahead of reusing r;
            // every access of r by the walk precedes it.
            (*r).dl.state.Store(TimerCancelled)
            rt.Unlock(&(*h).tlock)
        }
    }
    CancelFinish(acc)
}

// DisarmLocked removes r's armed deadline from its heap (Armed to
// Cancelled), under r's lock (a cancel walk visiting r), then that heap's
// lock.
DisarmLocked(r: *ScopeRec) {
    unsafe "r's lock is held; a carrier's timer heap under its tlock" {
        // Edge: a stale Armed is rechecked under the heap's lock; a stale
        // non-Armed means a FireHeap popped it (its fire finds the flag set)
        // or it was never armed. Armed is stored only under r's lock, held.
        if (*r).dl.state.Load() != TimerArmed {
            return
        }
        tr := raw.Of(&(*r).dl)
        h := LockHeapOf(tr)
        // Edge: under the heap's tlock, as FireHeap's Store of Fired: the
        // recheck and the Store of Cancelled are one step against the pop.
        if (*r).dl.state.Load() == TimerArmed {
            RemoveAt(h, PosOf(tr))
            // Edge: as above.
            (*r).dl.state.Store(TimerCancelled)
            PublishNext(h)
        }
        rt.Unlock(&(*h).tlock)
    }
}

// Disarm is the scope's exit (Join, after its tasks ended): an armed
// deadline leaves its heap; a deadline being fired is waited out, so the
// record is not reused while DeadlineFire reads it. The
// wait is a yield loop: the fire is a bounded cancel walk running on
// another carrier's scheduler stack, which never waits for this task. No
// arm runs beside it (the scope's own tasks have ended), so a deadline
// found Idle or Cancelled stays so and takes no lock.
Disarm(r: *ScopeRec) {
    for {
        var s: u32 = TimerIdle
        unsafe "a live scope record; a carrier's timer heap under its tlock" {
            // Edge: Cancelled is DeadlineFire's last store (or a disarm's),
            // so reading it orders every access of r by the fire before
            // Join reuses r.
            s = (*r).dl.state.Load()
            if s == TimerIdle || s == TimerCancelled {
                return
            }
            tr := raw.Of(&(*r).dl)
            h := LockHeapOf(tr)
            // Edge: under the heap's tlock: Fired means DeadlineFire's Store
            // of Cancelled is still to come, so the loop waits. Armed to
            // Cancelled is as DisarmLocked's.
            s = (*r).dl.state.Load()
            if s == TimerArmed {
                RemoveAt(h, PosOf(tr))
                // Edge: as above.
                (*r).dl.state.Store(TimerCancelled)
                PublishNext(h)
            }
            rt.Unlock(&(*h).tlock)
        }
        if s != TimerFired {
            return
        }
        sys.ThreadYield()
    }
}

// RecAt is the scope record whose address the deadline entry was given:
// std.task's own CancelAfter and Join pass a live record's.
RecAt(rec: *u8): *ScopeRec {
    unsafe "rec is a live scope record's address (CancelAfter, Join)" {
        return raw.PtrCast[u8, ScopeRec](rec)
    }
}
