// Why: some OS calls block and no backend can do them asynchronously; run
// on a carrier they would stall every task queued there, so they run on
// separate pool threads, and a cancel must still reach them.
//
// What: Blocking, BlockingWhole and BlockingWait; the pool's job record
// PoolJob, Enqueue, RunJob and the thread body PoolMain; the cancel side
// (CancelJob, PoolInterrupt, PoolReinterrupt); PoolInit and the test hooks
// PoolHooks.
//
// How: a blocking OS call holds a pool thread, never a carrier. A PoolJob
// lives in Blocking's frame; the
// task parks with the handshake (ParkUntil) and the pool thread finishes
// it with Complete from a foreign thread (the ready goes to the global
// queue). Pool threads are started on demand, at most MaxPoolThreads (64)
// or MK_TEST_POOL besides one per wait job queued or in its call
// (BlockingWait), and at most PoolSlots in all, each on a 1 MiB
// stack, and never joined. They never exit either, so the pool keeps as
// many threads as it ever ran at once (a burst of n waiting reads leaves
// about n, up to PoolSlots), and an idle thread takes whatever job comes
// next, so after such a burst more than MaxPoolThreads calls that are not
// waits can run at once. The bound holds only while the pool has fewer
// than PoolSlots threads: past that, every new job queues, a wait or not
// (BlockingWait's limits).
//
// Lock order: a scope lock, then plock (a wait lock), then leaf locks
// (Complete's ready). plock is held across two kinds of system call only,
// neither of which blocks: SemPost, once per section, and the cancel's
// ThreadInterrupt (below), once per listed job in its call in each
// PoolInterrupt round: at most one per pool thread (PoolSlots), every
// ReinterruptNs while any listed job is still in its call.
//
// Cancelling a job in its call. A cancel walk that finds its
// task's job in phase JobInCall marks it (cancel 1, under plock), puts it on
// the pool's cancelled list and, after every scope lock is released
// (CancelRec: no system call under a spin lock), calls PoolInterrupt: under plock,
// each listed job still in its call gets sys.ThreadInterrupt of its pool
// thread (Windows CancelSynchronousIo, Linux tgkill with SIGURG). A job
// leaves the list under plock when its call returns (RunJob), and its pool
// thread needs plock to do that, so an interrupt sent under plock to a
// listed job's thread reaches that job's call or nothing (Windows), and the
// job it names is live. Two windows remain, each closed on the thread's
// side, never by timing:
// - late (a Linux signal still pending when the thread moved on): a call
//   that reports Canceled is repeated when its own job's cancel is 0
//   (RunJob), so an interrupt never bleeds into another task's job;
// - early (the interrupt came before the call blocked): the job stays
//   listed, and the carriers' schedulers re-send at most every
//   ReinterruptNs while the list is not empty (PoolReinterrupt; an idle
//   carrier caps its wait at ReinterruptNs then), until the call returns.
// A call the OS cannot interrupt keeps its task until it returns (a known
// limit); a result with data then wins over the cancel. Pool
// writes stay on WriteSync, which retries EINTR on Linux, so a cancelled
// pool write blocked on a full pipe is not interrupted there.

import std.core
import std.raw
import std.rt
import std.sys

/// MaxPoolThreads is the most threads the blocking pool starts beyond
/// one per wait job: a wait job (BlockingWait: a read or write of
/// a pipe, a terminal, a pty end or a socket off the poller, which may
/// wait for another process without bound) queued or in its call raises
/// the bound by one, so while the pool has fewer than PoolSlots threads,
/// waits leave 64 threads to the rest of the process's blocking work.
/// Threads started past 64 stay (the header), so after a burst more than
/// 64 calls that are not waits can run at once.
export const MaxPoolThreads: u32 = 64
/// PoolSlots is the most pool threads at once, wait jobs' included
/// (TaskState.pslots and pthr). At PoolSlots threads, all busy, every new
/// job queues, a wait or not; the
/// pool keeps the threads it started for the rest of the process's life,
/// up to PoolSlots.
export const PoolSlots: u32 = 1024
const PoolSlotWords: int = 16
const PoolStack: int = 1048576

// Phases of a job, under plock.
const JobNew: u32 = 0
const JobQueued: u32 = 1
const JobInCall: u32 = 2
const JobDone: u32 = 3

/// ReinterruptNs is how often listed jobs are interrupted again (at most
/// every 10 ms).
export const ReinterruptNs: i64 = 10_000_000

// PoolJob is one blocking call, in its Blocking frame.
struct PoolJob {
    job: fn(*sys.Op)
    op: *sys.Op
    task: *Task
    // The park handshake word: Pending until the pool thread or a cancel
    // completes the job.
    wake: raw.Atomic[u32]
    phase: u32
    cancel: u32
    canceled: bool
    // whole: a cancel ends the job only while it waits for a thread; once
    // in its call it runs to its end unmarked (BlockingWhole).
    whole: bool
    // wait: a call that may wait for another process without bound
    // (BlockingWait), counted in TaskState.pwaits from its queueing to
    // its end, so it never takes one of the pmax threads.
    wait: bool
    // slot is the pool thread running the job (its pthr index + 1), 0 when
    // none (queued, or run on a carrier by StartFailed); listed while on
    // the cancelled list (cnext, cprev). All under plock.
    slot: u32
    listed: bool
    next: *PoolJob
    prev: *PoolJob
    cnext: *PoolJob
    cprev: *PoolJob
}

// Test hooks (test "RT-Pool-StartFails"): while fail_start is 1, Enqueue treats
// every pool thread start as failed without making one; gate
// holds a starter between its unlock and the start, with IdleGate's
// protocol (0 off, 1 armed, 2 held, 3 released). Off, each costs a start
// (a thread creation) one atomic load.
// early (test "RT-F13-PoolCancelEarly") holds a pool thread with the same protocol
// after it took a job whose cancel was 0, just before the call, so a cancel
// lands in the early window; early_task names the one task whose job it
// holds (blocks run in parallel under mk test). Off, it costs a job one
// atomic load.
// inline (test "RT-Pool-InlineCancel") holds a task whose job was handed back to
// run on its own carrier (no pool thread could start), with the same
// protocol, before it leaves its scope's blocked list. Off, it costs that
// rare path one atomic load.
struct PoolHooks {
    fail_start: raw.Atomic[u32]
    gate: raw.Atomic[u32]
    early: raw.Atomic[u32]
    early_task: raw.Atomic[u64]
    inline: raw.Atomic[u32]
}

var PoolHook: PoolHooks = PoolHooks(fail_start: raw.Atomic[u32].New(0), gate: raw.Atomic[u32].New(0),
    early: raw.Atomic[u32].New(0), early_task: raw.Atomic[u64].New(0), inline: raw.Atomic[u32].New(0))

// PoolEarlyHold holds the pool thread of the named task's job when it
// reaches the armed early gate.
PoolEarlyHold(j: *PoolJob) {
    // Edge: the holder set its job's phase to JobInCall under plock before
    // its Cas to 2, so a test that sees 2 knows the job is in its call; the
    // named task stores early_task before its job is queued.
    if PoolHook.early.Load() != 1 {
        return
    }
    var mine := false
    unsafe "j is the job this pool thread just took; it is live" {
        mine = PoolHook.early_task.Load() == u64(raw.Addr((*j).task))
    }
    // Edge: the Cas to 2 (the comment at the top of this function).
    if mine && PoolHook.early.Cas(1, 2) {
        GateWait(raw.Of(&PoolHook.early))
    }
}

// PoolInlineHold holds the first task that reaches the armed inline gate.
PoolInlineHold() {
    // Edge: StartFailed set the job's phase back to JobNew under plock
    // before this task's Cas to 2, so a test that sees 2 knows a cancel now
    // finds the job handed back (CancelJob marks it, nothing more).
    if PoolHook.inline.Load() == 1 && PoolHook.inline.Cas(1, 2) {
        GateWait(raw.Of(&PoolHook.inline))
    }
}

// PoolGateHold holds the first starter that reaches the armed gate.
PoolGateHold() {
    // Edge: the holder's unlock of plock (and its job's queueing) comes
    // before its Cas to 2, so a test that sees 2 sees the job queued.
    if PoolHook.gate.Load() == 1 && PoolHook.gate.Cas(1, 2) {
        GateWait(raw.Of(&PoolHook.gate))
    }
}

// PoolInit makes the pool's semaphore and applies MK_TEST_POOL
// (Start passes -1 in a release build); no thread is started until the
// first job.
PoolInit(st: *TaskState, test_pool: int): bool {
    unsafe "the TaskState page, before any task exists" {
        (*st).pmax = MaxPoolThreads
        if test_pool >= 1 && test_pool < int(MaxPoolThreads) {
            (*st).pmax = Trunc[u32](test_pool)
        }
        (*st).psem = sys.NewSem()
        return sys.SemInit(mut (*st).psem)
    }
}

/// Blocking runs job(op) on a pool thread and parks the calling task until
/// it returns. Canceled when the caller's scope is cancelled before the job
/// started (checked first, again under the scope lock, and by the pool
/// thread before the call); else None, with the job's result in op.res. Off
/// a task stack (a callback from C, a foreign thread) the job runs on the
/// calling thread: nothing there can park. A pool that cannot start its
/// first thread runs the job on the calling carrier.
/// O(1) besides the job: one queue append under the pool lock, and a park;
/// allocates nothing on the heap (the PoolJob is in this frame), though it
/// starts a pool thread with a 1 MiB stack when every started one is busy and
/// fewer than MaxPoolThreads (pmax) plus the wait jobs queued or in their
/// call exist, and fewer than PoolSlots in all.
export Blocking(job: fn(*sys.Op), op: *sys.Op, cancellable: bool): core.SysErr {
    if !rt.UsesTasks() {
        // No task exists, so the call runs here, as BlockingIn runs it
        // off a task stack; there is no pool.
        job(op)
        return core.SysErr.None
    }
    t := Current()
    owed := HookLeave(t)
    r := BlockingIn(t, job, op, cancellable, false, false)
    HookEnter(t, owed)
    return r
}

/// BlockingWhole is Blocking for a job that must not be interrupted once it
/// runs (core.IoRouteWhole: a spawn, a pseudo-console close; and
/// runtime.Blocking, uncancellable): a cancel before a pool thread takes the
/// job ends it unrun, Canceled, so a job queued behind busy threads is cut
/// by its scope's cancel or deadline; in its call it is neither marked nor
/// interrupted, and its task waits for its end. Same cost as Blocking.
export BlockingWhole(job: fn(*sys.Op), op: *sys.Op, cancellable: bool): core.SysErr {
    if !rt.UsesTasks() {
        job(op)
        return core.SysErr.None
    }
    t := Current()
    owed := HookLeave(t)
    r := BlockingIn(t, job, op, cancellable, true, false)
    HookEnter(t, owed)
    return r
}

/// BlockingWait is Blocking for a call that may wait for another process
/// without bound: a read or write of a pipe, a terminal, a pty end or a
/// socket that is not on the poller (std.task's NativeRead and
/// NativeWrite). From its queueing to its end it raises the pool's thread
/// bound by one, so a thread is started for it when none is idle,
/// and the MaxPoolThreads threads the rest of the process's blocking work
/// runs on are not held by such waits. Three limits: at PoolSlots threads
/// in all, every new job queues, a wait or
/// not, so waits that fill the slots the other busy threads leave stall
/// the process's other blocking calls; the
/// threads started stay, so after a burst more than MaxPoolThreads other
/// calls can run at once; and a wait job gets no new thread while the
/// pool already has its bound's threads, all busy (other calls on the
/// threads a burst left), so it queues until one ends. Cancelled as
/// Blocking's jobs are. Same cost as Blocking.
export BlockingWait(job: fn(*sys.Op), op: *sys.Op, cancellable: bool): core.SysErr {
    if !rt.UsesTasks() {
        job(op)
        return core.SysErr.None
    }
    t := Current()
    owed := HookLeave(t)
    r := BlockingIn(t, job, op, cancellable, false, true)
    HookEnter(t, owed)
    return r
}

// BlockingIn is Blocking between the task's hooks (hooks.mk); t is null off
// a task stack.
BlockingIn(t: *Task, job: fn(*sys.Op), op: *sys.Op, cancellable: bool, whole: bool, wait: bool): core.SysErr {
    if raw.IsNull(t) {
        job(op)
        return core.SysErr.None
    }
    if cancellable && Cancelled(t) {
        return core.SysErr.Canceled
    }
    var j := PoolJob(job: job, op: op, task: t, wake: raw.Atomic[u32].New(Pending), phase: JobNew, cancel: 0,
        canceled: false, whole: whole, wait: wait, slot: 0, listed: false, next: raw.Null[PoolJob](), prev: raw.Null[PoolJob](),
        cnext: raw.Null[PoolJob](), cprev: raw.Null[PoolJob]())
    jp := raw.OfMut(mut j)
    var sc := raw.Null[ScopeRec]()
    unsafe "j is in this frame until ParkUntil returns and the task is unregistered" {
        (*t).job = raw.PtrCast[PoolJob, u8](jp)
        if cancellable {
            locked := LockScope(t)
            if locked.cancelled {
                (*t).job = raw.Null[u8]()
                return core.SysErr.Canceled
            }
            sc = locked.sc
            if !raw.IsNull(sc) {
                // A cancel from here on finds the job by t.job under the
                // scope lock and settles it under plock (CancelJob).
                LinkBlocked(sc, t, WaitKindJob)
                rt.Unlock(&(*sc).lock)
            }
        }
        if !Enqueue(jp) {
            // No pool thread exists and none could start: run it here. The
            // task leaves its scope's blocked list before t.job is cleared,
            // since a cancel walk reads t.job under that scope's lock
            // (CancelJob). A cancel that came after StartFailed handed the
            // job back (phase JobNew) only set j.cancel, under plock inside
            // the walk's scope lock; Unblock takes that lock, so the plain
            // read below is ordered after the write, and no later walk can
            // reach the job. Either mark, or a cancel whose walk has not
            // reached this task, ends the job unrun, as the pool thread's
            // check before the call does.
            PoolInlineHold()
            if !raw.IsNull(sc) {
                Unblock(t)
            }
            (*t).job = raw.Null[u8]()
            if cancellable && (j.cancel == 1 || Cancelled(t)) {
                return core.SysErr.Canceled
            }
            job(op)
            return core.SysErr.None
        }
        // This carrier lingers as a spinner when the park leaves it nothing
        // to run (sched.mk Linger), so a quick job's ready finds
        // nspinning > 0 and wakes nobody. Owner-only: t runs on its carrier.
        // Not on one CPU, where the pool thread needs this carrier's CPU.
        (*(*t).carrier).pool_wait = (*TS()).spin
        // The pool thread may finish the job before this
        // task has switched out; the handshake readies it once.
        Jitter(JitterSubmit)
        ParkUntil(raw.Of(&j.wake))
        if !raw.IsNull(sc) {
            Unblock(t)
        }
        (*t).job = raw.Null[u8]()
        if j.canceled {
            return core.SysErr.Canceled
        }
    }
    return core.SysErr.None
}

// Enqueue appends j to the pool queue and wakes a pool thread, starting
// one when every started thread is busy and fewer than pmax exist. A job
// cancelled before it was queued is completed here as Canceled. False
// when no pool thread exists and none could start (j is not queued).
Enqueue(j: *PoolJob): bool {
    st := TS()
    unsafe "the TaskState page; j is the caller's job" {
        rt.Lock(&(*st).plock)
        if (*j).cancel == 1 {
            (*j).phase = JobDone
            (*j).canceled = true
            rt.Unlock(&(*st).plock)
            Complete(raw.Of(&(*j).wake), (*j).task)
            return true
        }
        (*j).phase = JobQueued
        (*j).prev = (*st).ptail
        (*j).next = raw.Null[PoolJob]()
        if raw.IsNull((*st).ptail) {
            (*st).phead = j
        } else {
            (*(*st).ptail).next = j
        }
        (*st).ptail = j
        (*st).pqueued += 1
        // Edge: none; a hint for spinning pool threads (PoolSpin), stored
        // under plock with every change of pqueued; the semaphore stays the
        // only claim on a job.
        (*st).pqueued_n.Store((*st).pqueued)
        // A thread is started when the queued jobs outnumber the threads
        // waiting for one; pthreads is raised under the lock, so two
        // enqueuers never start one thread each for one waiting job.
        if (*j).wait {
            // Counted from here to the job's end (PoolWaitEnd), so a
            // thread starts for it while every other one is busy.
            (*st).pwaits += 1
        }
        // Wait jobs, queued or in their call, raise the bound one each,
        // so below PoolSlots they leave pmax threads to the
        // other jobs (BlockingWait's limits).
        start := (*st).pqueued > (*st).pwaiting && (*st).pthreads < (*st).pmax + (*st).pwaits &&
            (*st).pthreads < PoolSlots
        var slot := 0
        if start {
            (*st).pthreads += 1
            // The new thread is counted waiting from its start: it will take
            // a posted job, so a second enqueue before it runs
            // must not start another for the same job (PoolMain).
            (*st).pwaiting += 1
            // A free slot exists: the slots taken equal pthreads, which is
            // below PoolSlots (a failed start gives its slot back).
            slot = PoolSlotTake(st)
            (*st).pthr[slot] = 0
        }
        rt.Unlock(&(*st).plock)
        sys.SemPost(&(*st).psem, 1)
        if start {
            PoolGateHold()
            var started := false
            // Edge: none; a test switch read once per start.
            if PoolHook.fail_start.Load() == 0 {
                r := sys.ThreadStart(PoolMain, raw.FromAddr[u8](Trunc[uint](slot + 1)), PoolStack)
                started = r.err == core.SysErr.None
                if started {
                    // The thread may take a job before this store; that
                    // job's interrupt then waits for the next round, which
                    // skips pthr 0 (the early window's rule).
                    rt.Lock(&(*st).plock)
                    (*st).pthr[slot] = Trunc[u64](r.value)
                    rt.Unlock(&(*st).plock)
                }
            }
            if !started {
                return StartFailed(st, j, slot)
            }
        }
        return true
    }
}

// StartFailed is Enqueue's path when its pool thread could not start. While
// another thread exists (or is starting), the queue is its. When this was
// the last, nobody will ever take the queued jobs, the caller's and every
// other task's queued while the start was in flight, so the whole queue is
// taken back under plock and run here, on the calling task's
// carrier, as a pool that cannot start its first thread does. The
// caller's own job j is handed back (false: Blocking runs it); every other
// job is run and completed here with the pool thread's protocol (phase
// JobInCall under plock, so a concurrent CancelJob only marks it; JobDone
// under plock; then Complete), or completed as Canceled when its cancel
// came first. The jobs' semaphore posts stay: a later pool thread finds the
// queue empty for them and waits again (PoolMain).
StartFailed(st: *TaskState, j: *PoolJob, slot: int): bool {
    var mine := false
    var run := raw.Null[PoolJob]()
    var gone := raw.Null[PoolJob]()
    unsafe "the TaskState page; queued jobs live until their Complete" {
        rt.Lock(&(*st).plock)
        (*st).pthreads -= 1
        (*st).pwaiting -= 1
        PoolSlotGive(st, slot)
        if (*st).pthreads != 0 {
            rt.Unlock(&(*st).plock)
            return true
        }
        var k := (*st).phead
        (*st).phead = raw.Null[PoolJob]()
        (*st).ptail = raw.Null[PoolJob]()
        (*st).pqueued = 0
        // Edge: none; PoolSpin's hint (Enqueue).
        (*st).pqueued_n.Store(0)
        for !raw.IsNull(k) {
            next := (*k).next
            (*k).next = raw.Null[PoolJob]()
            (*k).prev = raw.Null[PoolJob]()
            if raw.Same(k, j) {
                // Handed back to run on its carrier, off the pool.
                PoolWaitEnd(st, k)
                (*k).phase = JobNew
                mine = true
            } else if (*k).cancel == 1 {
                PoolWaitEnd(st, k)
                (*k).phase = JobDone
                (*k).canceled = true
                (*k).next = gone
                gone = k
            } else {
                (*k).phase = JobInCall
                (*k).next = run
                run = k
            }
            k = next
        }
        rt.Unlock(&(*st).plock)
        for !raw.IsNull(gone) {
            next := (*gone).next
            Complete(raw.Of(&(*gone).wake), (*gone).task)
            gone = next
        }
        for !raw.IsNull(run) {
            next := (*run).next
            RunJob(st, run, false)
            run = next
        }
    }
    // j not on the queue: a cancel completed it already (CancelJob removed
    // it), so its task's handshake is Done; Blocking parks and resumes.
    return !mine
}

// RunJob runs a job its taker set to JobInCall under plock, then completes
// it: JobDone under plock (and off the cancelled list), then Complete. A
// call that reports Canceled while its own job's cancel is 0 was hit by an
// interrupt meant for another job (the late window) and is repeated, so
// an uncancelled job never returns Canceled from an interrupt; with cancel
// 1 the Canceled is the cancel's (and a result with data wins). After
// Complete the job may be gone (its task resumed); nothing reads it again,
// and Complete's arguments are read before its swap. A pool thread (pool
// true) counts itself waiting in the same plock section that ends the job,
// before Complete readies the task: that task's next job, which may be
// enqueued before this thread is back in PoolMain, then finds the thread
// counted (pqueued > pwaiting is false) and starts no new thread for it.
// Were the count raised only in PoolMain, a quick task's next enqueue would
// start a thread per round trip that it won, up to MaxPoolThreads.
RunJob(st: *TaskState, j: *PoolJob, pool: bool) {
    unsafe "a job taken off the queue; it lives until its Complete" {
        for {
            (*j).job((*j).op)
            rt.Lock(&(*st).plock)
            if (*(*j).op).res.err == core.SysErr.Canceled && (*j).cancel == 0 {
                rt.Unlock(&(*st).plock)
                continue
            }
            PoolWaitEnd(st, j)
            (*j).phase = JobDone
            if (*j).listed {
                Unlist(st, j)
            }
            if pool {
                (*st).pwaiting += 1
            }
            rt.Unlock(&(*st).plock)
            Complete(raw.Of(&(*j).wake), (*j).task)
            return
        }
    }
}

// PoolWaitEnd ends a wait job's count (Enqueue raised it when the job was
// queued) as the job leaves the pool, done or handed back; plock is held.
PoolWaitEnd(st: *TaskState, j: *PoolJob) {
    unsafe "the TaskState page under plock; j is queued or in its call, so live" {
        if (*j).wait {
            (*st).pwaits -= 1
        }
    }
}

// PoolSlotTake takes the lowest free thread slot; plock is held, and a
// free slot exists (fewer than PoolSlots threads).
PoolSlotTake(st: *TaskState): int {
    unsafe "the TaskState page under plock" {
        for w in 0..PoolSlotWords {
            free := ~(*st).pslots[w]
            if free != 0 {
                b := raw.Ctz64(free)
                (*st).pslots[w] |= u64(1) << Trunc[u64](b)
                return w * 64 + b
            }
        }
    }
    rt.Fatal("pool: no free thread slot")
}

// PoolSlotGive gives back a slot PoolSlotTake took; plock is held.
PoolSlotGive(st: *TaskState, slot: int) {
    unsafe "the TaskState page under plock" {
        (*st).pslots[slot / 64] &= ~(u64(1) << Trunc[u64](slot % 64))
    }
}

// List puts a job in its call on the cancelled list; plock is held.
List(st: *TaskState, j: *PoolJob) {
    unsafe "the TaskState page under plock; j is in its call, so live" {
        (*j).listed = true
        (*j).cprev = raw.Null[PoolJob]()
        (*j).cnext = (*st).pcancel
        if !raw.IsNull((*st).pcancel) {
            (*(*st).pcancel).cprev = j
        }
        (*st).pcancel = j
        // Edge: raised under plock with the list; a carrier whose gate load
        // (PoolListed) misses it is one round late, never wrong: the list
        // itself is read under plock.
        _ = (*st).pcancel_n.Add(1)
    }
}

// Unlist takes a job off the cancelled list; plock is held.
Unlist(st: *TaskState, j: *PoolJob) {
    unsafe "the TaskState page under plock; j is listed" {
        if raw.IsNull((*j).cprev) {
            (*st).pcancel = (*j).cnext
        } else {
            (*(*j).cprev).cnext = (*j).cnext
        }
        if !raw.IsNull((*j).cnext) {
            (*(*j).cnext).cprev = (*j).cprev
        }
        (*j).cnext = raw.Null[PoolJob]()
        (*j).cprev = raw.Null[PoolJob]()
        (*j).listed = false
        // Edge: lowered under plock after the job left the list (the gate,
        // as in List).
        _ = (*st).pcancel_n.Add(0xFFFF_FFFF)
    }
}

// PoolInterrupt interrupts the pool thread of every listed job still in its
// call, under plock (the header's argument), and records the round's clock.
// Called by a cancel walk after its locks and by PoolReinterrupt.
PoolInterrupt() {
    st := TS()
    now := sys.Now()
    unsafe "the TaskState page; listed jobs are live under plock" {
        rt.Lock(&(*st).plock)
        // Edge: none beyond plock; the clock only paces PoolReinterrupt.
        (*st).pint_at.Store(Trunc[u64](now))
        var j := (*st).pcancel
        for !raw.IsNull(j) {
            if (*j).phase == JobInCall && (*j).slot != 0 {
                th := (*st).pthr[(*j).slot - 1]
                if th != 0 {
                    _ = sys.ThreadInterrupt(Trunc[uint](th))
                }
            }
            j = (*j).cnext
        }
        rt.Unlock(&(*st).plock)
    }
}

// PoolReinterrupt is the early-window rule on a carrier's scheduler:
// while a listed job is still in its call, interrupt again at most every
// ReinterruptNs, one carrier per round (a Cas on the round's clock). With
// no job listed it costs one atomic load.
PoolReinterrupt() {
    if !PoolListed() {
        return
    }
    st := TS()
    now := Trunc[u64](sys.Now())
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: the Cas admits one carrier per round; nothing else is
        // published by it (PoolInterrupt reads the list under plock).
        last := (*st).pint_at.Load()
        if now -% last < Trunc[u64](ReinterruptNs) || !(*st).pint_at.Cas(last, now) {
            return
        }
    }
    PoolInterrupt()
}

// PoolListed: some cancelled job may still be in its call (an idle carrier
// then waits at most ReinterruptNs, Idle).
PoolListed(): bool {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: a gate (List's comment); the list is read under plock.
        return (*TS()).pcancel_n.Load() != 0
    }
}

// Unlink removes a queued job from the pool queue; plock is held.
Unlink(st: *TaskState, j: *PoolJob) {
    unsafe "the TaskState page under plock; j is queued" {
        if raw.IsNull((*j).prev) {
            (*st).phead = (*j).next
        } else {
            (*(*j).prev).next = (*j).next
        }
        if raw.IsNull((*j).next) {
            (*st).ptail = (*j).prev
        } else {
            (*(*j).next).prev = (*j).prev
        }
        (*j).next = raw.Null[PoolJob]()
        (*j).prev = raw.Null[PoolJob]()
        (*st).pqueued -= 1
        // Edge: none; PoolSpin's hint (Enqueue).
        (*st).pqueued_n.Store((*st).pqueued)
    }
}

// PoolSpinNs bounds how long an idle pool thread spins for its next job
// before it sleeps in SemWait. Without the spin a stat entry call on WSL 2
// costs about 100 us, nearly all of it the wakes of a sleeping pool thread
// and a sleeping carrier (3.9 voluntary context switches per call,
// measured on one development machine), against Go's 0.36 us os.Stat on its
// own thread.
// A thread that spins while its task's carrier lingers (sched.mk Linger)
// takes a quick job's successor without a futex sleep or a wake; the bound
// is Go's spinning-M budget in spirit (an idle M spins briefly before it
// parks, runtime.findRunnable), sized to the measured round trip.
const PoolSpinNs: i64 = 50_000
// After a wait job (a pipe, a terminal, a pty end: its successor often waits
// on another process), a pool thread's spin is adaptive (WaitSpin): after
// WaitSpinMisses spins in a row that found no job, it spins only every
// WaitSpinProbe-th time, and a spin that finds one resets it. Measured at
// two CPUs on one development machine, not idle: pipepair 586 to 214
// Mcycles, a 4 KiB-read benchmark 2,073 to 690, with no slower wall time;
// no change at one CPU, where the spin is off. Not spinning at all after a
// wait job cut CPU but made the 4 KiB benchmark's wall time 5 times longer,
// so the periodic spin stays.
const WaitSpinMisses: u32 = 4
const WaitSpinProbe: u32 = 16
// PoolSpinCheck is how many loads of the hint the spin makes between two
// clock reads (Now is a system call on Linux, clock.mk).
const PoolSpinCheck: int = 64

// PoolSpin is an idle pool thread's bounded spin before SemWait: it returns
// when the queue hint shows a job, or after PoolSpinNs. It claims nothing
// (SemWait follows it on every path, and the semaphore's count is the only
// claim), so it cannot lose or double a wake: a job posted during the spin
// leaves the count raised and SemWait takes it at once (no sleep), and a job
// another thread took first leaves this one in SemWait as before. The
// spinner was counted in pwaiting under plock before the spin, so Enqueue's
// start test (pqueued > pwaiting) does not start a thread for a job it will
// take.
PoolSpin(st: *TaskState): bool {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // On one CPU the job's task needs this thread's CPU (TaskState.spin).
        if !(*st).spin {
            return false
        }
    }
    // The clock starts after the first round of loads, so a job already
    // queued costs no clock read (a system call on Linux).
    var t0: i64 = -1
    unsafe "the TaskState page Start mapped, which lives until exit" {
        for {
            for _ in 0..PoolSpinCheck {
                // Edge: none; a hint (Enqueue's store), never a claim.
                if (*st).pqueued_n.Load() != 0 {
                    return true
                }
            }
            now := sys.Now()
            if t0 < 0 {
                t0 = now
            } else if now - t0 >= PoolSpinNs {
                return false
            }
        }
    }
}

// WaitSpin is one pool thread's adaptive spin state after wait jobs
// (WaitSpinMisses): misses counts its spins after a wait job that found no
// job since the last that did, skipped the spins it left out since the
// last probe. Owner-only, in PoolMain's frame.
struct WaitSpin {
    misses: u32
    skipped: u32
}

// WaitSpinGo is whether the thread spins now: always after a job that was
// not a wait; after a wait job while fewer than WaitSpinMisses spins in a
// row missed, else once every WaitSpinProbe-th time (the probe).
WaitSpinGo(w: mut WaitSpin, after_wait: bool): bool {
    if !after_wait {
        return true
    }
    if w.misses < WaitSpinMisses || w.skipped >= WaitSpinProbe {
        w.skipped = 0
        return true
    }
    w.skipped += 1
    return false
}

// WaitSpinSaw records a spin after a wait job: a hit resets the misses.
WaitSpinSaw(w: mut WaitSpin, hit: bool) {
    if hit {
        w.misses = 0
    } else if w.misses < WaitSpinMisses {
        w.misses += 1
    }
}

// PoolMain is a pool thread: wait for a job, run it, complete it. It runs
// off any task stack, so Complete readies through the global queue. Between
// jobs it spins for a while (PoolSpin) before it sleeps.
PoolMain(arg: *u8) {
    st := TS()
    // The thread's slot + 1, which its starter passed as the argument.
    me := Trunc[u32](raw.Addr(arg))
    unsafe "the TaskState page; a job lives until its Complete" {
        // Counted waiting from its start (Enqueue raised pwaiting with
        // pthreads) to its SemWait's return, and again from the plock
        // section that ends each job (RunJob, the cancelled arm and the
        // empty arm below). So pthreads is pwaiting plus the threads with a
        // job, and Enqueue starts a thread only while queued jobs outnumber
        // the threads that will take one: never more threads than jobs
        // outstanding at once (the PoolSpin-Burst block).
        var ws := WaitSpin(misses: 0, skipped: 0)
        var after_wait := false
        for {
            if WaitSpinGo(mut ws, after_wait) {
                hit := PoolSpin(st)
                if after_wait {
                    WaitSpinSaw(mut ws, hit)
                }
            }
            after_wait = false
            _ = sys.SemWait(&(*st).psem, -1)
            // A library's stop (lib.mk) posts one wake per pool thread.
            // Edge: StopLibrary stores stopping before its posts.
            if (*st).stopping.Load() != 0 {
                _ = (*st).pexited.Add(1)
                return
            }
            rt.Lock(&(*st).plock)
            (*st).pwaiting -= 1
            j := (*st).phead
            if raw.IsNull(j) {
                // The post of a job a cancel removed from the queue.
                (*st).pwaiting += 1
                rt.Unlock(&(*st).plock)
                continue
            }
            Unlink(st, j)
            if (*j).cancel == 1 {
                PoolWaitEnd(st, j)
                (*j).phase = JobDone
                (*j).canceled = true
                (*st).pwaiting += 1
                rt.Unlock(&(*st).plock)
                Complete(raw.Of(&(*j).wake), (*j).task)
                continue
            }
            (*j).phase = JobInCall
            (*j).slot = me
            // Read under plock, before RunJob: after its Complete the job
            // may be gone.
            after_wait = (*j).wait
            rt.Unlock(&(*st).plock)
            PoolEarlyHold(j)
            RunJob(st, j, true)
        }
    }
}

// CancelJob settles a blocked task's job for a cancel (the WaitKindJob arm
// of CancelWait, under the task's scope lock, so the job is live): not yet
// queued, it is marked and Enqueue completes it; queued, it is removed and
// its handshake completed as Canceled here; in its call, it is marked and
// listed, and CancelInterrupt tells the walk to call PoolInterrupt after
// its locks (the header); done, nothing. CancelReady when the task
// had parked and the walk must ready it after its locks (Ready can reach
// PollWake or a carrier's ThreadStart, which do not run under a
// scope lock); when it had not parked yet, its own RunAfter sees Done and
// readies it. The job stays live until that Ready: its task is
// parked in Blocking.
CancelJob(t: *Task): u32 {
    st := TS()
    unsafe "t is on a blocked list whose lock is held; its job is live" {
        j := raw.PtrCast[u8, PoolJob]((*t).job)
        if raw.IsNull(j) {
            return CancelNone
        }
        rt.Lock(&(*st).plock)
        ph := (*j).phase
        if ph == JobQueued {
            Unlink(st, j)
            PoolWaitEnd(st, j)
            (*j).phase = JobDone
            (*j).canceled = true
            rt.Unlock(&(*st).plock)
            return if Completes(raw.Of(&(*j).wake)) { CancelReady } else { CancelNone }
        }
        if ph == JobInCall && (*j).whole {
            // A whole job is not interrupted once it runs: its task waits
            // for its end, and the cancel is answered by the next check.
            rt.Unlock(&(*st).plock)
            return CancelNone
        }
        var what := CancelNone
        if ph == JobInCall && !(*j).listed {
            (*j).cancel = 1
            List(st, j)
            what = CancelInterrupt
        } else if ph != JobDone {
            (*j).cancel = 1
        }
        if ph != JobDone {
            // Edge: the job's cancel word, which a long job (CopyFile)
            // loads between steps on its pool thread; a step that misses
            // this store sees it at the next load, or ends at the
            // interrupt that follows.
            (*(*j).op).cancel.Store(1)
        }
        rt.Unlock(&(*st).plock)
        return what
    }
}
