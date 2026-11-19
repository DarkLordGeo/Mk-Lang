// Why: sleeps and scope deadlines need timers that fire on time with many
// carriers, without one global lock that every sleep would contend on.
//
// What: the per-carrier timer heaps: SleepFor and SleepForIn, the insert,
// removal and fire paths (FireTimersOn, FireOthers, FireAll, FireRotate),
// CancelTimer, and the idle carrier's arming (ArmIdle, DisarmIdle, OweArm).
//
// How: one binary min-heap of deadlines per carrier, each under its own
// spinlock (Carrier.tlock), as Go's per-P timers. An entry is a sleeping task
// (Task.timer) or a scope's deadline (ScopeRec.dl): the address of the Task,
// or of the ScopeRec with bit 0 set (both are 8-aligned, so the bit is
// free). The arrays grow through rt.HeapAlloc directly, never the Heap's
// table, so a test's leak count never sees them.

// Which heap. A timer is armed on the heap of the carrier that runs
// the arming task (carrier 0's off a task), and its TimerRec.index names
// that heap beside its position (HeapOf, PosOf). The heap part of the word
// is written only by an insert, and a timer is inserted only while it is
// neither Armed nor Fired: a sleep by its own task, a scope's deadline under
// the scope's lock (a re-arm of an Armed deadline moves it inside its own
// heap, NativeDeadline). So while a timer is Armed or Fired its heap is
// fixed, and an unlocked reader that finds it Armed (CancelTimer,
// DisarmLocked, Disarm) locks the heap the word names (LockHeapOf) and
// rechecks the state there. The pop does not rewrite the word, so a fired
// deadline's heap is still the one it was popped from, whose lock
// DeadlineFire's last store takes.
//
// Who fires. A carrier fires its own heap's due timers at its schedule steps
// (FireTimersOn), and fires every other heap's due timers before it steals
// (FireOthers), after each idle wait (FireAll) and, one other heap per
// TimerTick step, round robin (FireRotate), so a heap whose carrier runs a
// task that never suspends is still served by a busy carrier. An idle
// carrier arms for the earliest of every heap's published tnext (ArmIdle).
// Lock order: a scope's lock, then one heap lock; no path holds two
// heap locks, and armed_at and need_arm are atomics, taken under no lock.

import std.core
import std.raw
import std.rt
import std.sys

const TimerIdle: u32 = 0
const TimerArmed: u32 = 1
const TimerFired: u32 = 2
const TimerCancelled: u32 = 3

// ScopeTag marks a heap entry that is a ScopeRec's address.
const ScopeTag: uint = 1

// TimerRec.index is the heap's carrier index above PosBits and the position
// below; MaxCarriers (256) fits the 8 bits left. A heap holds at most
// MaxHeapTimers entries (Grow refuses more, which is fatal, as any growth
// failure of the timer heap).
const PosBits: u32 = 24
const PosMask: u32 = 0xFF_FFFF
const MaxHeapTimers: int = 0x100_0000

// EntryOfTask and EntryOfScope are the heap entries of a sleeping task and
// of a scope's deadline.
EntryOfTask(t: *Task): uint {
    return raw.Addr(t)
}

EntryOfScope(r: *ScopeRec): uint {
    return raw.Addr(r) | ScopeTag
}

// TimerOf is the TimerRec an entry names: the task's timer or the scope's
// dl. The entry is live: a queued task is parked, and a queued scope's Join
// has not passed its Disarm.
TimerOf(e: uint): *TimerRec {
    unsafe "a live heap entry: a parked task or an armed scope record" {
        if (e & ScopeTag) != 0 {
            return raw.Of(&(*raw.FromAddr[ScopeRec](e & ~ScopeTag)).dl)
        }
        return raw.Of(&(*raw.FromAddr[Task](e)).timer)
    }
}

// PosOf is tr's position in its heap; the caller holds that heap's lock.
PosOf(tr: *TimerRec): int {
    unsafe "a timer whose heap's lock the caller holds" {
        // Edge: none, relaxed (order 0): index is written under the heap's
        // lock, which is held.
        return int((*tr).index.LoadOrd(0) & PosMask)
    }
}

// HeapOf is the carrier whose heap tr was last inserted into. Read without
// the heap's lock it is exact while tr is Armed or Fired (the header); the
// position bits beside it may be moving.
HeapOf(tr: *TimerRec): *Carrier {
    unsafe "a timer record of a live task or scope" {
        // Edge: none of its own, relaxed (order 0): the heap bits do not
        // change while the timer is Armed or Fired, and the caller's
        // seq_cst load of Armed (or its lock) orders the insert's store
        // before this read.
        return CarrierAt(int((*tr).index.LoadOrd(0) >> PosBits))
    }
}

// LockHeapOf locks the heap tr names and returns it. The heap part is
// rechecked under the lock: it cannot change while tr is Armed or Fired,
// so the loop runs once, and it guards the read against a caller that
// found tr in another state.
LockHeapOf(tr: *TimerRec): *Carrier {
    for {
        h := HeapOf(tr)
        Jitter(JitterTimer)
        unsafe "a started carrier's heap" {
            rt.Lock(&(*h).tlock)
            if raw.Addr(HeapOf(tr)) == raw.Addr(h) {
                return h
            }
            rt.Unlock(&(*h).tlock)
        }
    }
}

// HeapHere is the heap a scope's deadline armed now goes to (NativeDeadline):
// the running task's carrier's, or carrier 0's off a task and on a pinned
// carrier. A pinned carrier fires only its own tasks' sleeps (SleepForIn,
// on t.carrier's heap): no other carrier looks at its heap (its index is
// past ScanCarriers), and its task may compute for long without a
// schedule step, so a deadline, which cancels a scope whose tasks run
// anywhere, goes to a heap the shared carriers fire.
HeapHere(): *Carrier {
    c := Here()
    unsafe "the running task's carrier record" {
        return if raw.IsNull(c) || !raw.IsNull((*c).home) { C0() } else { c }
    }
}

// At is the i-th entry of h.
At(h: *Carrier, i: int): uint {
    unsafe "i < ntimers, under h's timer lock" {
        return raw.Read(raw.Offset((*h).timers, i))
    }
}

SetAt(h: *Carrier, i: int, e: uint) {
    unsafe "i < cap_timers, under h's timer lock" {
        raw.Write(raw.Offset((*h).timers, i), e)
        // Edge: none of its own, relaxed (order 0): read under this lock
        // (PosOf), or for its heap bits by an unlocked HeapOf that the
        // state's seq_cst store orders (header).
        (*TimerOf(e)).index.StoreOrd((*h).index << PosBits | Trunc[u32](i), 0)
    }
}

Deadline(h: *Carrier, i: int): i64 {
    unsafe "the caller holds h's tlock and i < ntimers, so entry i is queued" {
        return (*TimerOf(At(h, i))).deadline
    }
}

// Grow doubles h's array, to at most MaxHeapTimers; false when the heap is
// exhausted or full.
Grow(h: *Carrier): bool {
    unsafe "the timer array, under h's timer lock" {
        if (*h).cap_timers >= MaxHeapTimers {
            return false
        }
        var n := (*h).cap_timers * 2
        if n < 64 {
            n = 64
        }
        if n > MaxHeapTimers {
            n = MaxHeapTimers
        }
        p := raw.PtrCast[u8, uint](rt.HeapAlloc(n * 8, 8))
        if raw.IsNull(p) {
            return false
        }
        if (*h).ntimers > 0 {
            raw.Copy(p, (*h).timers, (*h).ntimers)
        }
        if (*h).cap_timers > 0 {
            rt.HeapFree(raw.PtrCast[uint, u8]((*h).timers), (*h).cap_timers * 8, 8)
        }
        (*h).timers = p
        (*h).cap_timers = n
        return true
    }
}

// SiftUp moves entry i towards the root while its deadline is earlier than
// its parent's.
SiftUp(h: *Carrier, var i: int) {
    for i > 0 {
        parent := (i - 1) / 2
        if Deadline(h, parent) <= Deadline(h, i) {
            break
        }
        p := At(h, parent)
        SetAt(h, parent, At(h, i))
        SetAt(h, i, p)
        i = parent
    }
}

// SiftDown moves entry i towards the leaves while a child's deadline is
// earlier.
SiftDown(h: *Carrier, var i: int) {
    unsafe "the timer heap, under its lock" {
        n := (*h).ntimers
        for {
            l := 2 * i + 1
            if l >= n {
                break
            }
            m := if l + 1 < n && Deadline(h, l + 1) < Deadline(h, l) { l + 1 } else { l }
            if Deadline(h, i) <= Deadline(h, m) {
                break
            }
            x := At(h, m)
            SetAt(h, m, At(h, i))
            SetAt(h, i, x)
            i = m
        }
    }
}

// Insert adds e (its deadline set) to h and sifts it up: O(log n). It
// writes e's heap part (SetAt).
Insert(h: *Carrier, e: uint): bool {
    unsafe "the timer heap, under its lock" {
        if (*h).ntimers == (*h).cap_timers && !Grow(h) {
            return false
        }
        i := (*h).ntimers
        (*h).ntimers += 1
        SetAt(h, i, e)
        SiftUp(h, i)
        return true
    }
}

// RemoveAt removes entry i of h (a cancelled timer, found by its index):
// the last entry takes its place and sifts whichever way its deadline
// needs. O(log n).
RemoveAt(h: *Carrier, i: int) {
    unsafe "the timer heap, under its lock; i < ntimers" {
        (*h).ntimers -= 1
        n := (*h).ntimers
        if i == n {
            return
        }
        moved := At(h, n)
        SetAt(h, i, moved)
        SiftUp(h, i)
        SiftDown(h, PosOf(TimerOf(moved)))
    }
}

// PopMin removes h's earliest entry.
PopMin(h: *Carrier): uint {
    top := At(h, 0)
    RemoveAt(h, 0)
    return top
}

// MaxDeadline is armed_at when no idle carrier waits for a timer, and
// tnext when a heap is empty.
const MaxDeadline: i64 = 0x7FFF_FFFF_FFFF_FFFF

// PublishNext stores h's earliest deadline in its tnext; h's tlock is held
// and the heap has just changed.
PublishNext(h: *Carrier) {
    unsafe "the timer heap, under its lock" {
        d := if (*h).ntimers > 0 { Deadline(h, 0) } else { MaxDeadline }
        // ArmIdle scans the heaps' tnext (Earliest) and then lowers armed_at
        // by compare-and-swap; DisarmIdle clears it by compare-and-swap
        // while it still holds its own deadline, and then scans. So
        // either the arm loads an armed_at later than its deadline and
        // kicks, or an idler armed no later than it sees it in DisarmIdle's
        // scan and hands it on, or stays busy until its next ArmIdle when
        // another idler armed since (ArmIdle has the cases and the invariant).
        // Edge: written under h's tlock after the heap change, and before
        // the arming path's load of armed_at (NativeDeadline, SleepForIn)
        // in S. A FireHeap's unlocked load may see an older value, which
        // only delays a fire to its next check.
        (*h).tnext.Store(Trunc[u64](d))
    }
}

// NextOf is h's published earliest deadline, MaxDeadline when it is empty.
NextOf(h: *Carrier): i64 {
    unsafe "a committed carrier record; tnext is an atomic" {
        // Edge: PublishNext's store, under h's tlock.
        return Trunc[i64]((*h).tnext.Load())
    }
}

// Earliest is the least published deadline over every started carrier's
// heap (MaxDeadline when all are empty): ArmIdle's timeout and DisarmIdle's
// test for timers that remain. A carrier counted before its thread exists
// has an empty heap (CarriersInit set its tnext); a timer armed on a
// carrier started after the count was read meets the armed_at argument
// from the arming side (PublishNext).
Earliest(): i64 {
    var d := MaxDeadline
    n := int(ScanCarriers())
    for i in 0..n {
        x := NextOf(CarrierAt(i))
        if x < d {
            d = x
        }
    }
    return d
}

// FireTimersOn readies, on c, every task of c's own heap whose deadline
// passed, and fires every such scope deadline (DeadlineFire); true when any
// was. While none is due it takes no lock: tnext, the earliest deadline, is
// compared with the clock first, and with no timer at all the clock is not
// read (Go's timer0When).
FireTimersOn(c: *Carrier): bool {
    due := NextOf(c)
    if due == MaxDeadline {
        return false
    }
    now := sys.Now()
    if due > now {
        return false
    }
    return FireHeap(c, c, now)
}

// FireOthers fires, on c, the due timers of every other started carrier's
// heap (the timer steal: their carriers may run tasks that never
// suspend); true when any fired. The clock is read once, and only when some
// heap is not empty.
FireOthers(c: *Carrier): bool {
    var any := false
    var now: i64 = -1
    n := int(ScanCarriers())
    unsafe "c is the calling thread's carrier" {
        me := int((*c).index)
        for i in 0..n {
            if i == me {
                continue
            }
            h := CarrierAt(i)
            due := NextOf(h)
            if due == MaxDeadline {
                continue
            }
            if now < 0 {
                now = sys.Now()
            }
            if due <= now && FireHeap(c, h, now) {
                any = true
            }
        }
    }
    return any
}

// FireAll is FireTimersOn, then FireOthers: an idle carrier back from its
// wait, which may have woken for any heap's deadline (ArmIdle).
FireAll(c: *Carrier): bool {
    a := FireTimersOn(c)
    b := FireOthers(c)
    return a || b
}

// FireRotate fires the due timers of one other started carrier's heap, the
// next in turn after the one c looked at last (Carrier.tturn): the TimerTick
// step's share of the timer steal, so a heap whose carrier runs a task that
// never suspends is served within (carriers - 1) TimerTick steps, that is
// (carriers - 1) x TimerTick schedules, of each busy carrier when no carrier
// is idle (one TimerTick with the one heap), at the cost of
// one load of one other carrier's tnext.
FireRotate(c: *Carrier) {
    n := ScanCarriers()
    if n <= 1 {
        return
    }
    unsafe "c is the calling thread's carrier" {
        var i := ((*c).tturn + 1) % n
        if i == (*c).index {
            i = (i + 1) % n
        }
        (*c).tturn = i
        h := CarrierAt(int(i))
        due := NextOf(h)
        if due == MaxDeadline {
            return
        }
        now := sys.Now()
        if due <= now {
            _ = FireHeap(c, h, now)
        }
    }
}

// FireHeap readies on c the tasks of h whose deadline is at or before now,
// and fires h's scope deadlines that are; true when any was. c is the
// calling thread's carrier, h any started carrier (c itself, or a steal).
FireHeap(c: *Carrier, h: *Carrier, now: i64): bool {
    var any := false
    unsafe "a started carrier's heap, read under its tlock" {
        for {
            // The due tasks are collected under h's tlock (through
            // Task.link, which a parked task does not use) and readied
            // after it: ReadySched can reach PollWake or a carrier's
            // ThreadStart, which should not run under a spinlock. A fired
            // task is readied by this call only: CancelTimer no longer finds
            // it armed. A due scope deadline stops the collection: it is
            // fired after the lock (its cancel walk takes scope locks, which
            // come before a heap lock), and the loop takes it again.
            var list := raw.Null[Task]()
            var fired := raw.Null[ScopeRec]()
            rt.Lock(&(*h).tlock)
            for (*h).ntimers > 0 && Deadline(h, 0) <= now {
                e := PopMin(h)
                // Edge: under h's tlock: CancelTimer, NativeDeadline and the
                // disarms read the state under it (LockHeapOf) and leave a
                // fired timer to this call.
                (*TimerOf(e)).state.Store(TimerFired)
                if (e & ScopeTag) != 0 {
                    fired = raw.FromAddr[ScopeRec](e & ~ScopeTag)
                    break
                }
                t := raw.FromAddr[Task](e)
                (*t).link = list
                list = t
            }
            PublishNext(h)
            rt.Unlock(&(*h).tlock)
            for !raw.IsNull(list) {
                next := (*list).link
                ReadySched(c, list)
                list = next
                any = true
            }
            if raw.IsNull(fired) {
                break
            }
            Jitter(JitterTimer)
            DeadlineFire(fired, h)
            any = true
        }
    }
    return any
}

// ArmIdle is an idle carrier's poller timeout: the time to the
// earliest deadline of every heap when no other idle carrier already waits
// for one at least as early, else -1 (forever). The second result is the
// deadline it armed, or MaxDeadline. The timer-owner invariant (the
// RT-MN-TimerOwner block checks it): while a
// timer exists, either some carrier is not idle (it fires its own heap at
// its next schedule step, and the others' before it steals or idles) or an
// idle carrier waits with a timeout no later than the earliest deadline.
// armed_at is lowered only by this compare-and-swap, which follows the scan
// (Earliest); DisarmIdle clears it to MaxDeadline by compare-and-swap
// while it still holds that idler's deadline, and then scans. An arm
// publishes its heap's tnext and then loads armed_at, so an arm this scan
// missed loads armed_at after the scan, in S. When its deadline is earlier
// than the value it loads it kicks (OweArm). Otherwise that value v is the
// deadline some idler armed, no later than the arm's: this one's, or an
// older one the CAS has yet to replace. v's idler's DisarmIdle clears v
// after that load and then scans, so it sees the timer: it hands it on
// (OweArm), or finds that another idler armed since and leaves the timers
// to it while it is itself not idle (the invariant's first case) until its
// next ArmIdle, whose scan sees the timer. If a lower CAS replaced v first,
// that CAS's idler does the same. So no arm is lost, though not every arm
// this scan missed kicks.
ArmIdle(): (timeout: i64, armed: i64) {
    st := TS()
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // After this call armed_at is no later than the earliest deadline
        // it saw, so no kick is owed (need_arm, OweArm).
        // Edge: cleared before this carrier's scan (Earliest): an OweArm
        // whose store follows this one is for a timer this scan (or a later
        // one) sees, and an OweArm that skipped its store on the old 1
        // loaded it before this store, after its timer's PublishNext.
        (*st).need_arm.Store(0)
        d := Earliest()
        if d == MaxDeadline {
            return (timeout: -1, armed: MaxDeadline)
        }
        for {
            // Edge: the arm side's argument (PublishNext): this load and
            // the CAS after it follow the scan in S.
            a := (*st).armed_at.Load()
            if d >= a {
                return (timeout: -1, armed: MaxDeadline)
            }
            // Edge: as above; a failed CAS means another idler armed (or
            // disarmed) meanwhile, and the loop compares again.
            if (*st).armed_at.Cas(a, d) {
                break
            }
        }
        var left := d - sys.Now()
        if left < 0 {
            left = 0
        }
        return (timeout: left, armed: d)
    }
}

// DisarmIdle runs when the carrier that armed d has returned from its wait
// (and has fired the timers that were due, Idle). It clears armed_at when no
// later idler armed an earlier deadline, and when timers remain it owes
// them an armed carrier (OweArm): this carrier arms itself if it finds no
// work, or wakes an idle one from Found if it does (the hand-off that keeps
// the timer-owner invariant when this carrier goes on to run tasks).
DisarmIdle(d: i64) {
    if d == MaxDeadline {
        return
    }
    st := TS()
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: the clear comes before the scan in S, and an arm publishes
        // its tnext before it loads armed_at (PublishNext); ArmIdle's
        // comment has the cases, an arm that loads a later idler's
        // armed_at among them.
        _ = (*st).armed_at.Cas(d, MaxDeadline)
        // Edge: as the Cas above; another idler that armed since keeps the
        // timers (its own DisarmIdle hands them on).
        if (*st).armed_at.Load() == MaxDeadline && Earliest() != MaxDeadline {
            OweArm()
        }
    }
}

// OweArm records that the timers need an armed idle carrier (need_arm). It
// runs on a carrier's scheduler loop (RunAfter after SleepFor's new
// earliest deadline, DisarmIdle) or after a scope's arm (NativeDeadline),
// which next either goes idle, where ArmIdle arms for the earliest deadline,
// or finds a task, where Found wakes an idle carrier while need_arm is set;
// a woken carrier that finds work instead of going idle wakes another from
// its own Found, so the kick reaches a carrier that does go idle and arms
// (the timer-owner invariant). The wake is deferred to Found, not made here: a
// sleeper's carrier that goes idle arms itself, and an immediate PollWake
// would wake a second carrier for nothing on every sleep. The store is
// skipped while the word is 1 already, so carriers that arm deadlines at
// once do not all write its line.
OweArm() {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        st := TS()
        // Edge: stored before this carrier's Found or ArmIdle, which read
        // it (Found's comment has the rest of the argument). A load of 1
        // precedes ArmIdle's clear in S, so its scan follows this timer's
        // PublishNext (ArmIdle).
        if (*st).need_arm.Load() == 0 {
            // Edge: as above.
            (*st).need_arm.Store(1)
        }
    }
}

// SleepFor parks the running task for nanos; the first cancellation
// check is the caller's. When cancellable the wait is registered on the
// task's inner scope (in lock order: the scope lock, then the heap lock)
// and a cancel removes the timer and returns Canceled; else None.
// Out of memory for the timer array is fatal: std has no error to report.
SleepFor(nanos: i64, cancellable: bool): core.SysErr {
    t := MustCurrent()
    owed := HookLeave(t)
    r := SleepForIn(t, nanos, cancellable)
    HookEnter(t, owed)
    return r
}

// SleepForIn is SleepFor between the task's hooks (hooks.mk). The timer
// goes to the heap of t's carrier, and t parks on that heap's lock.
SleepForIn(t: *Task, nanos: i64, cancellable: bool): core.SysErr {
    st := TS()
    now := sys.Now()
    d := if nanos > MaxDeadline - 1 - now { MaxDeadline - 1 } else { now + nanos }
    var sc := raw.Null[ScopeRec]()
    if cancellable {
        locked := LockScope(t)
        if locked.cancelled {
            return core.SysErr.Canceled
        }
        sc = locked.sc
    }
    unsafe "the running task and its carrier's timer heap" {
        h := (*t).carrier
        (*t).timer.deadline = d
        // Edge: before the insert under h's tlock; CancelTimer and FireHeap
        // read it under that lock.
        (*t).timer.state.Store(TimerArmed)
        rt.Lock(&(*h).tlock)
        if !Insert(h, EntryOfTask(t)) {
            rt.Unlock(&(*h).tlock)
            rt.Fatal("cannot grow the timer heap")
        }
        PublishNext(h)
        if !raw.IsNull(sc) {
            LinkBlocked(sc, t, WaitKindTimer)
            rt.Unlock(&(*sc).lock)
        }
        // The timer-owner invariant: a deadline earlier than every idle carrier's
        // wake-up needs a carrier to arm for it. This carrier's RunAfter
        // records the debt after it releases the lock (OweArm); until its
        // Found or ArmIdle this carrier is busy, which keeps the invariant,
        // and whoever arms scans every heap's tnext, this one's included.
        // Edge: after PublishNext's store in S (its comment).
        if d < (*st).armed_at.Load() {
            ParkKick(raw.Of(&(*h).tlock))
        } else {
            Park(raw.Of(&(*h).tlock))
        }
        if !raw.IsNull(sc) {
            Unblock(t)
        }
        // Edge: written under the heap's tlock before the Ready that
        // resumed this task.
        return if (*t).timer.state.Load() == TimerCancelled { core.SysErr.Canceled } else { core.SysErr.None }
    }
}

// CancelTimer removes t's timer when it is still armed (TimerArmed to
// TimerCancelled) and reports whether it did; a timer that fired already is
// left to FireHeap, which readied the task. The caller holds t's scope
// lock (CancelWait), then the timer's heap lock (the lock order). t is in this
// timer wait (it was inserted before it was linked blocked), so its heap
// part is this sleep's.
CancelTimer(t: *Task): bool {
    unsafe "t is in a timer wait, registered on a blocked list whose lock is held" {
        tr := raw.Of(&(*t).timer)
        h := LockHeapOf(tr)
        // Edge: under the heap's tlock, as every write of it.
        if (*t).timer.state.Load() != TimerArmed {
            rt.Unlock(&(*h).tlock)
            return false
        }
        RemoveAt(h, PosOf(tr))
        // Edge: under the heap's tlock, as FireHeap's TimerFired; SleepFor
        // reads it after its Park returns, which the Ready after this
        // section follows.
        (*t).timer.state.Store(TimerCancelled)
        PublishNext(h)
        rt.Unlock(&(*h).tlock)
        return true
    }
}
