// Why: every task lives in a scope, which must not end before its tasks
// do, keeps their first error, and cancels everything nested in it on an
// early exit, Cancel or deadline, without missing a task that is about to
// wait.
//
// What: ScopeRec, the scope record in place; Enter, Fail, Join, Cancel,
// CancelAfter, Expired, Remaining and LimitHit (the Scope intrinsics); the
// cancel walk (CancelRec, CancelWalk, CancelFinish) and the blocked-list
// registration (LockScope, LinkBlocked, Unblock, CancelWait).
//
// How: Cancel sets the flags of the scope tree, which every cancellable
// operation checks before it starts, and cancels the wait of every task
// already blocked in it (each scope's blocked list). A cancellable wait
// registers under the scope's lock after rechecking the flag, so a cancel
// either is seen or finds the wait. A NoCancel Io opens a boundary scope
// that no walk from above reaches.

import std.core
import std.raw
import std.rt
import std.sys

/// The first 128 bytes of a Scope (std.core's view struct Scope { words:
/// [16]u64 }); Scope[E] is { hdr: Scope, err: E!void }, err at 128.
export struct ScopeRec {
    live: raw.Atomic[u32]
    cancelled: raw.Atomic[u32]
    lock: raw.Atomic[u32]
    // "1 once Join started": nothing reads it, so Join does not
    // write it; the word keeps its place in the layout.
    joining: raw.Atomic[u32]
    limit: u32
    class: u8
    pad: [3]u8
    err_size: u32
    // The scope's level: a root 1, a child its parent's plus 1, raised for a boundary above the mask depth of the
    // Io it was opened from (Enter, PastMask); Main's empty chain is 0.
    // Levels strictly grow down a chain.
    depth: u32
    parent: *ScopeRec
    owner: *Task
    first_child: *ScopeRec
    next_sibling: *ScopeRec
    prev_sibling: *ScopeRec
    blocked: *Task
    io: Io
    site: *rt.Site
    // The deadline's heap entry, 24 bytes (timer.mk: deadline written
    // under this lock and its heap's tlock, index and state under that
    // tlock); bdepth, the level of the nearest boundary at or above this
    // scope, 0 when none (bdepth == depth: a scope opened from a NoCancel
    // Io whose mask covers its parent, linked under no parent); dl_set, 1
    // once a deadline was set (under this lock).
    dl: TimerRec
    bdepth: u32
    dl_set: u32
}

// The values of ScopeRec.cancelled: 0 live, 1 cancelled, CancelledByDeadline
// when the scope's own deadline set the flag (Expired). A flag leaves
// 0 once, by compare-and-swap, and never changes again while the record
// lives; every reader that asks "cancelled?" tests it against 0.
const CancelledByDeadline: u32 = 3

const ErrOffset: int = 128

// RecOf is the record of a scope local.
RecOf(s: &Scope): *ScopeRec {
    unsafe "a Scope's 128 bytes are std.task's ScopeRec" {
        return raw.PtrCast[Scope, ScopeRec](raw.Of(s))
    }
}

// LiveWord is the scope's live counter as the address WaitWord takes.
LiveWord(r: *ScopeRec): *u32 {
    unsafe "a live scope record" {
        return raw.PtrCast[raw.Atomic[u32], u32](raw.Of(&(*r).live))
    }
}

// Cancelled: true when the task's inner scope is cancelled; Main's chain is
// empty. One load, whatever the nesting depth: a check that walks the
// chain up parent would make every cancellable operation O(depth) and
// building a chain of D scopes O(D^2).
//
// Invariant (flag down): once Cancel(a) has set a's flag, every scope
// nested in a has its own flag set by the time a task of it could miss the
// cancel. Two writers keep it. Enter copies the parent's flag under the
// parent's lock, in the same section that links the new scope; a walk sets
// a record's flag (CancelWalk's Swap) before it takes that record's lock to
// visit its children, so under the parent's lock Enter either sees the flag
// or links the child where the walk will find it. And the walk sets the
// flag of every child it finds. A scope whose flag is still 0 while an
// ancestor's is 1 is one a walk has not reached yet; a task there that
// checks this flag and misses registers its wait under that scope's lock,
// which the walk takes later (after the flag), so it is on the blocked list
// when the walk runs (no missed wait, CancelRec). Tests: "RT-R17-CancelNested",
// "RT-F2-SleepCancelOrder" (under jitter), "RT-Cancel-FlagDown".
Cancelled(t: *Task): bool {
    unsafe "the task's inner scope outlives the task's use of it" {
        s := (*t).inner
        if raw.IsNull(s) {
            return false
        }
        // Edge: reads the flag CancelWalk's Swap (or Enter's copy) wrote;
        // a stale 0 is closed by the recheck under the scope's lock
        // (LockScope), which the walk takes after its Swap.
        return (*s).cancelled.Load() != 0
    }
}

/// IsCancelled is Cancelled, unless io is masked (Io.NoCancel) and t's
/// inner scope is not past the mask (MaskedPast).
/// O(1): one load of the inner scope's flag, which a cancel sets on every
/// nested scope, and for a masked io two loads more; allocates nothing.
export IsCancelled(t: *Task, io: Io): bool {
    if (core.IoFlags(io) & 1) != 0 && !MaskedPast(t, io) {
        return false
    }
    return Cancelled(t)
}

/// MaskedPast: io is masked at depth D (core.IoMaskDepth) and t's inner
/// scope lies under a boundary past the mask (PastMask), a scope opened
/// from a masked Io after the mask. Its flag is then set only by cancels of
/// scopes below that boundary, which the mask does not cover, since a
/// cancel walk never crosses a boundary upwards (Enter links a boundary
/// under no parent). Invariant (the mask): an operation on an Io masked at D
/// is cancelled only by a Cancel, early exit or deadline of a boundary
/// opened from a masked Io after the mask, or of a scope under one. Tests:
/// "RT8-Mask-Before", "RT8-Mask-After", "RT8-Mask-Late",
/// "RT8-Mask-Nested". O(1).
export MaskedPast(t: *Task, io: Io): bool {
    if raw.IsNull(t) {
        return false
    }
    unsafe "the task's inner scope outlives the task's use of it" {
        s := (*t).inner
        return !raw.IsNull(s) && PastMask((*s).bdepth, io)
    }
}

// PastMask is the one test of the NoCancel boundary, shared by Enter and
// MaskedPast: a scope whose nearest boundary has level b lies past io's
// mask, taken at depth D, when b > D. Levels (ScopeRec.depth) strictly
// grow down a chain, so every scope enclosing the NoCancel call has a
// level <= D, which is what NoCancel recorded (Depth); and Enter gives a
// boundary opened from the masked Io a level > D, wherever and whenever
// it is opened, even after the Io left the scopes it was taken in. An Io
// masked by IoMasked (D = MaskDepthMax) is past no boundary: Enter does
// not raise a level above it. O(1).
PastMask(b: u32, io: Io): bool {
    return b > core.IoMaskDepth(io)
}

/// ChainRegister gives std.core what its chain check reads (core.ChainInit,
/// core.ChainState): the stack regions, where a slot's task record lies
/// (rt.HeaderBytes and TaskOffset below its top), and the offsets of
/// Task.inner and ScopeRec.cancelled, taken from field addresses so they
/// follow any layout change. Called once by Start after the regions exist,
/// before the first task. O(1); allocates nothing.
ChainRegister() {
    unsafe "field addresses of a record at a made-up address; nothing is read or written there" {
        t := raw.FromAddr[Task](4096)
        s := raw.FromAddr[ScopeRec](4096)
        inner := u64(raw.Addr(raw.Of(&(*t).inner))) - 4096
        flag := u64(raw.Addr(raw.Of(&(*s).cancelled))) - 4096
        core.ChainInit(rt.RegionWordsOf(0), rt.RegionWordsOf(1), u64(rt.HeaderBytes + TaskOffset), inner, flag)
    }
}

/// DepthHooks is what std.core's TaskDepth and stream lock call
/// (core.DepthInit, start.mk).
var DepthHooks: core.DepthHook = core.DepthHook(depth: Depth, mute: HookMute, unmute: HookUnmute)

/// Depth is the running task's scope level: its inner scope's (ScopeRec's
/// depth), 0 off a task or on Main's empty chain, which Io.NoCancel records
/// (core.TaskDepth). O(1); allocates nothing.
export Depth(): u32 {
    t := Current()
    if raw.IsNull(t) {
        return 0
    }
    unsafe "the running task's own inner scope" {
        s := (*t).inner
        return if raw.IsNull(s) { 0 } else { (*s).depth }
    }
}

/// Enter initializes the scope record in place: the lowering of
/// Io.Scope (limit 0), Io.ScopeOf[E] (err_size = SizeOf[E!void]()) and
/// Io.ScopeWith (limit l.tasks, or -1 when l.tasks < 1, which panics here).
/// Panics when the limit is below 0 or off a task stack. O(1): one link under
/// the parent's lock; allocates nothing (the record is the caller's).
export Enter(s: *u8, io: Io, limit: int, stack: StackSize, err_size: int, site: &rt.Site) {
    if limit < 0 {
        rt.PanicMsg(rt.PanicExplicit, site, "Limits.tasks must be at least 1")
    }
    t := Current()
    if raw.IsNull(t) {
        rt.PanicAt(rt.PanicSuspendInCallback, site)
    }
    unsafe "s is the scope local's storage: 128 bytes, then err when err_size > 0" {
        raw.Fill(s, 0, ErrOffset)
        r := raw.PtrCast[u8, ScopeRec](s)
        // Limits.tasks is an int: a limit above the u32 range is no limit
        // the scope can reach, so it is clamped, never wrapped.
        (*r).limit = if limit > 0xFFFF_FFFF { 0xFFFF_FFFF } else { Trunc[u32](limit) }
        match stack {
            .Default {}
            .Large {
                (*r).class = rt.ClassLarge
            }
            .Bytes(n) {
                // A class of its own per power of two, its region
                // reserved on first use (rt's ClassForBytes).
                c := rt.ClassForBytes(n)
                if c == 255 {
                    rt.PanicMsg(rt.PanicExplicit, site, "StackSize.Bytes is above 512 MiB, or its slot region cannot be reserved")
                }
                (*r).class = c
            }
        }
        (*r).err_size = Trunc[u32](err_size)
        (*r).io = io
        (*r).site = raw.Of(site)
        (*r).owner = t
        parent := (*t).inner
        (*r).parent = parent
        // depth is the scope's level: strictly greater than its parent's,
        // and greater than the mask depth of the Io a boundary is opened
        // from (below). bdepth is the level of the nearest boundary (a
        // scope opened from a masked Io) at or above this one, 0
        // when none: a root opened from an unmasked Io is no boundary, so
        // an Io masked before it (in Main) stays masked inside it.
        var pdepth: u32 = 0
        var pbound: u32 = 0
        if !raw.IsNull(parent) {
            pdepth = (*parent).depth
            pbound = (*parent).bdepth
        }
        (*r).depth = if pdepth == 0xFFFF_FFFF { pdepth } else { pdepth + 1 }
        (*r).bdepth = pbound
        masked := (core.IoFlags(io) & 1) != 0
        if masked && !PastMask(pbound, io) {
            // The mask: a scope opened from a NoCancel Io whose
            // mask covers the parent is a new boundary. It is linked under
            // no parent, so no walk of the scopes above reaches it, and it
            // copies no flag; its own Cancel and deadline cancel what runs
            // under it. Its level is raised above the mask depth D, so an
            // operation on that Io under it is past the mask (PastMask)
            // wherever the Io was taken: the scopes that enclosed the
            // NoCancel call all have levels <= D, every boundary opened
            // from the Io has one > D. parent stays recorded: Join gives
            // the owner it back, and Remaining stops here (bdepth ==
            // depth).
            m := core.IoMaskDepth(io)
            if uint(m) < core.MaskDepthMax && (*r).depth <= m {
                (*r).depth = m + 1
            }
            (*r).bdepth = (*r).depth
        } else if !raw.IsNull(parent) {
            // An unmasked Io, or a masked one already past its mask here
            // (the parent lies under a boundary opened from it): an
            // ordinary child, linked so its parent's Cancel and deadline
            // reach it (test "RT8-Mask-Nested").
            rt.Lock(&(*parent).lock)
            (*r).next_sibling = (*parent).first_child
            if !raw.IsNull((*parent).first_child) {
                (*(*parent).first_child).prev_sibling = r
            }
            (*parent).first_child = r
            // Flag down: a scope nested in a cancelled
            // one is cancelled. Edge: the copy is made under the parent's
            // lock, after the link; a walk swaps the parent's flag before
            // it takes this lock, so either this load sees 1 or the walk
            // finds r in the children list and sets r's flag itself.
            if (*parent).cancelled.Load() != 0 {
                // Counted before the store (core.CancelLiveAdd).
                core.CancelLiveAdd(1)
                (*r).cancelled.Store(1)
            }
            rt.Unlock(&(*parent).lock)
        }
        (*t).inner = r
        if err_size > 0 {
            raw.Write(raw.PtrCast[u8, u32](raw.Offset(s, ErrOffset)), 0)
        }
    }
}

// RootFor makes the running task's next scope a root when io is NoCancel
// so no cancel of the scopes the task runs in reaches that
// scope or its tasks, as none reaches the NoCancel caller: Enter links a
// scope under the task's inner one and copies its cancelled flag, so the
// inner scope is unset until EndRoot. The library's own internal scopes
// use it, whose tasks wait cancellably to end each other (the resolver's
// timeout). (false, null) when io is cancellable or off a task.
// O(1); allocates nothing.
RootFor(io: Io): (on: bool, inner: *ScopeRec) {
    t := Current()
    if raw.IsNull(t) || Cancellable(io) {
        return (on: false, inner: raw.Null[ScopeRec]())
    }
    unsafe "t is the running task; only it reads or writes its inner scope here" {
        inner := (*t).inner
        (*t).inner = raw.Null[ScopeRec]()
        return (on: true, inner: inner)
    }
}

// EndRoot gives the running task back the inner scope RootFor took, after
// the root scope's Join, which left inner at that scope's parent (null).
EndRoot(on: bool, inner: *ScopeRec) {
    if !on {
        return
    }
    unsafe "the running task's own field" {
        (*Current()).inner = inner
    }
}

// Fail records a task's error as the scope's first; later ones
// are dropped.
Fail(t: *Task) {
    unsafe "the task's scope outlives it; err holds its E!void value" {
        r := (*t).home
        code := raw.Read(raw.PtrCast[u8, u32]((*t).err))
        dst := raw.PtrCast[ScopeRec, u8](r)
        word := raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(dst, ErrOffset))
        // Edge: the first error wins this compare-and-swap; its copy
        // of the payload below precedes this task's live decrement (TaskEntry),
        // which the joiner's load of 0 follows, so Join reads the whole error.
        if (*word).Cas(0, code) {
            n := int((*r).err_size)
            if n > 4 {
                raw.Copy(raw.Offset(dst, ErrOffset + 4), raw.Offset((*t).err, 4), n - 4)
            }
            CancelRec(r)
        }
    }
}

/// Join is the StJoin lowering: cancel first on an early exit, wait
/// until no task of s remains (including tasks spawned during the wait),
/// unlink s, and on the fall-through exit of a failed Scope[E] restart the
/// body's trace at the scope's site.
/// O(n) for the cancel walk over the n scopes and blocked tasks below s when
/// cancel is true, O(1) otherwise, besides the wait; allocates nothing.
export Join(s: &Scope, cancel: bool) {
    r := RecOf(s)
    unsafe "a live scope record, on its body's task" {
        // The joining word is not written: nothing reads it,
        // and a seq_cst store is an xchg on x86_64.
        if cancel {
            CancelRec(r)
        }
        for {
            // Edge: reads every task end's decrement (TaskEntry); 0 means each
            // ended task's last access to the scope preceded it.
            n := (*r).live.Load()
            if n == 0 {
                break
            }
            // The joined scope for the joiner's leave hook (HookHold).
            me := Current()
            HoldSet(me, r, false)
            _ = WaitWord(LiveWord(r), n, false)
            HoldClear(me)
        }
        // The scope's exit disarms its deadline synchronously
        // (Disarm waits out a fire already running), before the unlink and
        // the final flag read below: after it no timer can set the flag.
        // dl_set was written under this record's lock by CancelAfter, on
        // this task or on a task of s whose end the live load above read.
        if (*r).dl_set != 0 {
            _ = core.IoDeadline((*r).io, raw.PtrCast[ScopeRec, u8](r), 0, 1)
        }
        parent := (*r).parent
        // A boundary (bdepth == depth) was never linked.
        if !raw.IsNull(parent) && (*r).bdepth != (*r).depth {
            rt.Lock(&(*parent).lock)
            if raw.IsNull((*r).prev_sibling) {
                (*parent).first_child = (*r).next_sibling
            } else {
                (*(*r).prev_sibling).next_sibling = (*r).next_sibling
            }
            if !raw.IsNull((*r).next_sibling) {
                (*(*r).next_sibling).prev_sibling = (*r).prev_sibling
            }
            rt.Unlock(&(*parent).lock)
        }
        // The scope has ended: no task is inside it (live is 0), and
        // no walk can reach it (it is unlinked under its parent's lock, the
        // lock a walk holds while it swaps a child's flag; a root scope is
        // reached only through its own record, by its tasks and its owner).
        // So its flag is final here, and a set one leaves the count.
        // Edge: this load follows the unlink, which follows any walk's Swap.
        if (*r).cancelled.Load() != 0 {
            core.CancelLiveAdd(-1)
        }
        t := (*r).owner
        (*t).inner = parent
        if !cancel && (*r).err_size > 0 && rt.Traced() {
            code := raw.Read(raw.PtrCast[u8, u32](raw.Offset(raw.PtrCast[ScopeRec, u8](r), ErrOffset)))
            if code != 0 {
                rt.TraceReset(HeaderOf(t), (*r).site)
            }
        }
    }
}

/// Cancel is the intrinsic Scope.Cancel.
/// Idempotent. O(n) for the n scopes nested in s and the tasks blocked in them;
/// allocates nothing.
export Cancel(s: &Scope) {
    CancelRec(RecOf(s))
}

/// CancelAfter is the intrinsic Scope.CancelAfter.
/// The deadline is d from now on the clock of the Io s was opened from
/// (the native timer heap, a Sim's clock, or none in the evaluator and
/// on an Io with no clock); once it passes, s is cancelled as by Cancel
/// and Expired is true if s was not cancelled first. Only the earliest
/// deadline counts; d <= 0 fires at once. O(log n) in the timers armed;
/// allocates nothing (the heap array grows through rt.HeapAlloc).
export CancelAfter(s: &Scope, d: core.Duration) {
    r := RecOf(s)
    unsafe "a live scope record; its io is the Io given to Enter" {
        // The scope's own Io's clock: the native heap, a Sim's simulated
        // clock, or none (InertDeadline: no clock, arms nothing).
        _ = core.IoDeadline((*r).io, raw.PtrCast[ScopeRec, u8](r), d.Nanos(), 0)
    }
}

/// Expired is the intrinsic Scope.Expired: whether s's own deadline
/// set its cancel flag, which was clear until then. The flag's one
/// compare-and-swap decides, so a Cancel and a deadline that race give
/// exactly one cause, and the answer never changes while s lives. O(1):
/// one load; allocates nothing.
export Expired(s: &Scope): bool {
    r := RecOf(s)
    unsafe "a live scope record" {
        // Edge: reads the flag's one CAS (DeadlineWalk or a cancel walk);
        // an operation that returned Canceled read the same flag, so it
        // reads the cause with it.
        return (*r).cancelled.Load() == CancelledByDeadline
    }
}

/// Remaining is the intrinsic Scope.Remaining: the least time left
/// over the deadlines of s and its ancestors up to the nearest boundary
/// (the deadlines above a NoCancel mask do not cancel s), at
/// least zero; none when none has a deadline. Each deadline is read on its
/// own Io's clock. O(depth of the chain): one lock per scope and one clock
/// read per scope with a deadline; allocates nothing.
export Remaining(s: &Scope): ?core.Duration {
    var x := RecOf(s)
    var best: i64 = MaxDeadline
    var any := false
    unsafe "live scope records: a scope's ancestors outlive it" {
        for {
            rt.Lock(&(*x).lock)
            set := (*x).dl_set != 0
            at := (*x).dl.deadline
            io := (*x).io
            rt.Unlock(&(*x).lock)
            if set {
                // The clock is read outside the lock: a Sim's now entry is
                // one of its operations.
                now := core.IoNow(io)
                if now.err == core.SysErr.None {
                    // Both are clock readings (at is saturated below
                    // MaxDeadline), so the difference does not overflow.
                    var left := at - now.value
                    if left < 0 {
                        left = 0
                    }
                    if !any || left < best {
                        best = left
                    }
                    any = true
                }
            }
            p := (*x).parent
            if raw.IsNull(p) || (*x).bdepth == (*x).depth {
                break
            }
            x = p
        }
    }
    if !any {
        return none
    }
    return some(core.DurationOf(best))
}

/// LimitPassed reports whether s has a deadline of its own (CancelAfter)
/// whose instant has passed on the clock of the Io s was opened from, read
/// now; false when s has none or that Io has no clock. Unlike Expired it
/// does not ask which deadline cancelled s. O(1): one lock and one clock
/// read; allocates nothing.
export LimitPassed(s: &Scope): bool {
    r := RecOf(s)
    unsafe "a live scope record" {
        rt.Lock(&(*r).lock)
        set := (*r).dl_set != 0
        at := (*r).dl.deadline
        io := (*r).io
        rt.Unlock(&(*r).lock)
        if !set {
            return false
        }
        // The clock is read outside the lock, as Remaining's.
        now := core.IoNow(io)
        return now.err == core.SysErr.None && now.value >= at
    }
}

/// LimitHit is std's TimedOut mapping for a scope whose deadline is one of
/// std's limits: a failure under s reports time.TimedOut when s
/// expired, or when it is Canceled (canceled true) and s's own deadline
/// has passed (LimitPassed). With one timer heap per carrier an ancestor's
/// later deadline can fire before s's own and cancel s first, so Expired
/// alone would report Canceled although both deadlines have passed.
/// Expired is read first: the clock is read only for a Canceled it does
/// not explain. O(1); allocates nothing.
export LimitHit(s: &Scope, canceled: bool): bool {
    return Expired(s) || (canceled && LimitPassed(s))
}

// CancelRec sets the flag on r and every scope nested in it, parent first,
// and cancels the wait of every task on each record's blocked list;
// idempotent. Lock order: r's lock, then its
// children's (parent before child), then one wait lock per blocked task.
//
// Invariant (no missed wait): a task in a cancellable wait is on its inner
// scope's blocked list from before its wait is published (under the scope
// lock, then the wait lock) until after it resumes (Unblock, under the scope
// lock). The flag is set before the walk takes r's lock, and a task
// registers only after checking the flags under that lock, so a task either
// sees the flag and does not wait, or is on the list when the walk runs.
CancelRec(r: *ScopeRec) {
    // The walk collects the tasks whose waits it cancelled and readies them
    // after every scope lock is released: Ready can reach PollWake or a
    // carrier's ThreadStart, which should not run under a spinlock. It also
    // collects the submitted poller operations to cancel, whose PollCancel
    // is a system call (CancelIoEx, an io_uring submission, or epoll's
    // cancel and its completion's Ready), so none runs under a scope lock
    // either (no system call under a spin lock).
    var acc := CancelLists(ready: raw.Null[Task](), poll: raw.Null[Task](), interrupt: false)
    _ = CancelWalk(r, raw.OfMut(mut acc), false)
    CancelFinish(acc)
}

// CancelFinish does what a cancel walk left, after every scope lock.
CancelFinish(acc: CancelLists) {
    // The pool threads of the jobs this walk listed are interrupted
    // here, after the scope locks (ThreadInterrupt is a system call).
    if acc.interrupt {
        PoolInterrupt()
        // An interrupt that landed before the call blocked is sent again
        // only by a carrier that schedules or goes idle (PoolReinterrupt).
        // Carriers already waiting in PollWait with no timeout would sleep
        // through that while the canceller computes, so one is woken: it
        // finds no work and waits again, capped at ReinterruptNs.
        WakeOne()
    }
    var p := acc.poll
    for !raw.IsNull(p) {
        unsafe "a pinned task: Await does not return, so op stays live, until the pin is 0" {
            next := (*p).cancel_next
            sys.PollCancel((*p).op)
            // Edge: the release of the pin follows PollCancel; Await reads
            // 0 before it returns and its frame (op) goes.
            (*p).cancel_pin.Store(0)
            p = next
        }
    }
    var list := acc.ready
    for !raw.IsNull(list) {
        unsafe "a cancelled waiter, parked; only this walk readies it" {
            // Read before Ready: the global queue reuses link.
            next := (*list).link
            Ready(list)
            list = next
        }
    }
}

// CancelLists is what a cancel walk leaves to do once every scope lock is
// released: tasks to ready (through Task.link) and pinned tasks whose
// operation it must PollCancel (through Task.cancel_next).
struct CancelLists {
    ready: *Task
    poll: *Task
    // A pool job in its call was listed for an interrupt.
    interrupt: bool
}

// CancelWalk sets r's flag and the flag of every scope nested in it, and
// under each record's lock cancels every blocked wait there (pushing each
// task whose wait it ended on the list, through Task.link, which a parked
// task does not use) and disarms an armed deadline (a scope cancelled
// first never expires). With deadline true it is r's own deadline firing
// (DeadlineFire): r's flag is set to CancelledByDeadline under r's lock
// (Expired is decided under the scope's lock). True when this walk
// set r's flag.
//
// The walk is iterative: a pre-order walk over first_child and
// next_sibling that climbs back through parent, so its stack use is
// constant whatever the nesting depth (a recursive walk would overflow a
// task's stack under deep nesting). The records on the path from r to
// the current one stay locked, parent before child (the lock order), and each
// is released when the walk climbs out of it; so a record's children list
// and every next_sibling read on it are read under its lock. A child whose
// flag was already set is skipped without its lock: the cancel that set it
// walks that subtree. A boundary (a NoCancel scope) is in no children list, so no
// walk reaches it from above. O(records + blocked tasks) time, O(1) stack;
// the number of locks held at once is the depth of the current record.
CancelWalk(r: *ScopeRec, acc: *CancelLists, deadline: bool): bool {
    unsafe "a live scope record and its children; each visited record is locked" {
        // Edge: the flag's CAS (seq_cst) is before, or under, the lock
        // below, so a task that rechecks under this lock sees it
        // (LockScope), and Enter, which copies under the lock, sees it
        // (flag down). The CAS is the flag's only change from 0,
        // against FirstUncancelled's of an ancestor's walk, so exactly one
        // cause wins (Expired). Counted before the CAS, undone when
        // the flag was set already.
        core.CancelLiveAdd(1)
        if deadline {
            rt.Lock(&(*r).lock)
            // Edge: as above; the deadline's CAS is taken under r's lock.
            if !(*r).cancelled.Cas(0, CancelledByDeadline) {
                rt.Unlock(&(*r).lock)
                core.CancelLiveAdd(-1)
                return false
            }
        } else {
            // Edge: as above; this CAS is before r's lock, taken below.
            if !(*r).cancelled.Cas(0, 1) {
                core.CancelLiveAdd(-1)
                return false
            }
            rt.Lock(&(*r).lock)
        }
        CancelBlocked(r, acc)
        DisarmLocked(r)
        var cur := r
        for {
            // cur is locked and its blocked waits are cancelled: descend
            // into its first child not yet cancelled, else climb.
            var down := FirstUncancelled((*cur).first_child)
            if raw.IsNull(down) {
                for {
                    if raw.Same(cur, r) {
                        rt.Unlock(&(*r).lock)
                        return true
                    }
                    // cur's parent is locked (it is on the path), so cur's
                    // next_sibling is stable while it is read.
                    sib := (*cur).next_sibling
                    up := (*cur).parent
                    rt.Unlock(&(*cur).lock)
                    down = FirstUncancelled(sib)
                    if !raw.IsNull(down) {
                        break
                    }
                    cur = up
                }
            }
            rt.Lock(&(*down).lock)
            CancelBlocked(down, acc)
            DisarmLocked(down)
            cur = down
        }
    }
    return false
}

// FirstUncancelled is c or the first of its later siblings whose flag this
// call set (0 to 1), or null; the siblings' parent is locked.
FirstUncancelled(c: *ScopeRec): *ScopeRec {
    var x := c
    unsafe "sibling records under their parent's lock" {
        // Edge: as CancelWalk's own CAS: the flag before the child's lock.
        // Each CAS counted before it, undone when it fails.
        for !raw.IsNull(x) {
            core.CancelLiveAdd(1)
            if (*x).cancelled.Cas(0, 1) {
                break
            }
            core.CancelLiveAdd(-1)
            x = (*x).next_sibling
        }
    }
    return x
}

// CancelBlocked cancels the wait of every task on r's blocked list (r's
// lock is held) and pushes what is left to do on acc.
CancelBlocked(r: *ScopeRec, acc: *CancelLists) {
    unsafe "r's lock is held: no task on its list can unlink itself" {
        var t := (*r).blocked
        for !raw.IsNull(t) {
            // Read before the cancel: t cannot unlink itself while this
            // lock is held (Unblock takes it), and it is readied only
            // after the walk.
            next := (*t).blocked_next
            what := CancelWait(t)
            if what == CancelReady {
                (*t).link = (*acc).ready
                (*acc).ready = t
            } else if what == CancelPoll {
                (*t).cancel_next = (*acc).poll
                (*acc).poll = t
            } else if what == CancelInterrupt {
                (*acc).interrupt = true
            }
            t = next
        }
    }
}

// What CancelWait leaves to its walk, after the locks.
const CancelNone: u32 = 0
const CancelReady: u32 = 1
const CancelPoll: u32 = 2
const CancelInterrupt: u32 = 3

// The kinds of cancellable wait (Task.wait_kind).
const WaitKindNone: u32 = 0
const WaitKindWord: u32 = 1
const WaitKindTimer: u32 = 2
const WaitKindOp: u32 = 3
const WaitKindJob: u32 = 4

// LockScope is the first half of registering a cancellable wait: it takes
// the task's inner scope's lock and rechecks the
// cancellation flags under it. Main's chain is empty: (null, false), no lock.
// Cancelled: (null, true), no lock. Otherwise the lock is held on return;
// the caller publishes its wait under the wait's own lock, then calls
// LinkBlocked and releases the scope lock.
LockScope(t: *Task): (sc: *ScopeRec, cancelled: bool) {
    unsafe "the running task's inner scope outlives its wait" {
        sc := (*t).inner
        if raw.IsNull(sc) {
            return (sc: sc, cancelled: false)
        }
        // The window the no-missed-wait invariant closes: a cancel may land between
        // the caller's first check and this lock.
        Jitter(JitterCancel)
        rt.Lock(&(*sc).lock)
        // Edge: the recheck under the lock the walk takes after its Swap:
        // a cancel that this load misses finds the task on the list.
        if (*sc).cancelled.Load() != 0 {
            rt.Unlock(&(*sc).lock)
            return (sc: raw.Null[ScopeRec](), cancelled: true)
        }
        return (sc: sc, cancelled: false)
    }
}

// LinkBlocked puts t on sc's blocked list for a wait of kind; sc's lock is
// held.
LinkBlocked(sc: *ScopeRec, t: *Task, kind: u32) {
    unsafe "sc's lock is held; t is the running task" {
        (*t).wait_kind = kind
        (*t).blocked_in = sc
        (*t).blocked_prev = raw.Null[Task]()
        (*t).blocked_next = (*sc).blocked
        if !raw.IsNull((*sc).blocked) {
            (*(*sc).blocked).blocked_prev = t
        }
        (*sc).blocked = t
    }
}

// Unblock takes t off its blocked list after its wait ended; a cancel walk
// holding the list's lock finishes first.
Unblock(t: *Task) {
    unsafe "t is the running task; blocked_in is the scope it registered with" {
        sc := (*t).blocked_in
        if raw.IsNull(sc) {
            return
        }
        rt.Lock(&(*sc).lock)
        if raw.IsNull((*t).blocked_prev) {
            (*sc).blocked = (*t).blocked_next
        } else {
            (*(*t).blocked_prev).blocked_next = (*t).blocked_next
        }
        if !raw.IsNull((*t).blocked_next) {
            (*(*t).blocked_next).blocked_prev = (*t).blocked_prev
        }
        (*t).blocked_next = raw.Null[Task]()
        (*t).blocked_prev = raw.Null[Task]()
        (*t).blocked_in = raw.Null[ScopeRec]()
        (*t).wait_kind = WaitKindNone
        rt.Unlock(&(*sc).lock)
    }
}

// CancelWait cancels the wait of t, a task on a blocked list whose lock the
// caller holds (CancelRec). Each kind's wait leaves its waiting state at
// most once, under its own lock, so the cancel and the normal wake never
// both ready t: a word wait leaves its bucket (WaitQueued to WaitCancelled),
// a timer leaves the heap (TimerArmed to TimerCancelled), a queued pool job
// leaves the pool queue (JobQueued to JobDone), and an operation is marked
// and handed to PollCancel, whose completion still arrives through
// CompleteOn. CancelReady when the walk must ready t (a word
// or timer wait this cancel ended, a queued job whose task had parked);
// CancelPoll when the walk must PollCancel t's operation, which this call
// pinned; CancelInterrupt when it listed t's pool job in its call, which
// the walk interrupts after its locks; else CancelNone (the
// wait's own path completes it: the poller's reaper, the pool thread, or
// the task's own RunAfter for a job completed before its task switched
// out).
CancelWait(t: *Task): u32 {
    unsafe "t is on the blocked list whose lock is held, so it is still inside its wait" {
        kind := (*t).wait_kind
        if kind == WaitKindWord {
            if CancelWordWait(t) {
                return CancelReady
            }
        } else if kind == WaitKindTimer {
            if CancelTimer(t) {
                return CancelReady
            }
        } else if kind == WaitKindOp {
            op := (*t).op
            if !raw.IsNull(op) {
                // Dekker pair with Await (all seq_cst): here cancel.Store(1)
                // then op_live.Load(); there op_live.Store(1) then
                // cancel.Load(). In the total order one store precedes the
                // other side's load, so at least one side calls PollCancel,
                // and each only after PollSubmit returned true: no cancel
                // reaches std.sys before or during the submission. A late
                // PollCancel (after the completion was reaped) is harmless
                // (std.sys's contract).
                (*op).cancel.Store(1)
                // Edge: the Dekker pair's load (comment above).
                if (*t).op_live.Load() == 1 {
                    // The PollCancel runs after the locks. Edge:
                    // the pin is stored under this scope lock, before Await's
                    // Unblock can take it; Await reads the pin after Unblock,
                    // so it sees 1 and waits until CancelRec's PollCancel is
                    // done (op stays in Await's frame until then).
                    (*t).cancel_pin.Store(1)
                    return CancelPoll
                }
            }
        } else if kind == WaitKindJob {
            return CancelJob(t)
        }
    }
    return CancelNone
}
