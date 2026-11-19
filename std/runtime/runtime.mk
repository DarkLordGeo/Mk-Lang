// Why: programs need a few runtime facts and controls (carriers, stacks,
// blocking calls, wakers, dedicated threads) without reaching into the
// private std.rt, std.sys and std.task.
//
// What: Processors, Carriers, StackBounds, StackRemaining, StackSize.Bytes,
// Blocking, Waker, PanicInfo, Dedicated, Thread and Joined. AddFaultFilter
// is in fault.mk, ForeignIo in foreign.mk, Cycles in cycles.mk and
// WriteDump in dump.mk.
//
// How: thin public doors over the private modules; Dedicated and Thread
// run on std.task's pinned carriers. There is no thread-local storage:
// tasks move between threads and the runtime finds the current task from
// the stack pointer, so a program passes its context explicitly.

import std.core
import std.raw
import std.rt
import std.sys
import std.task

/// The number of processors the OS reports this process may run on. O(1)
/// after the first call's system call; allocates nothing.
export Processors(): int {
    return sys.CpuCount()
}

/// The number of carrier threads in use: MK_CARRIERS, a library's start
/// option, or the processor count; 1 in a program with no task. O(1);
/// allocates nothing.
export Carriers(): int {
    return task.CarriersInUse()
}

/// The running task's usable stack, [lo, hi). lo is above the guard page
/// and, on Windows, above the reserve the overflow report keeps; the
/// task's frames grow down from hi. On a stack that is no task's, such as
/// a carrier's system stack or the main thread with no task, it is that
/// thread's stack with its guard area left out. It is (0, 0) where the OS
/// gives no bounds: Linux's main thread before tasks start, and a foreign
/// thread. O(1): a few compares, or one system call off a task; allocates
/// nothing.
export StackBounds(): (lo: uint, hi: uint) {
    slot := rt.CurrentSlot()
    if !raw.IsNull(slot) {
        hi := raw.Addr(slot)
        top := hi + Trunc[uint](rt.HeaderBytes)
        lo := rt.SlotBase(top) + 4096 + task.SlotGuarantee()
        return (lo: lo, hi: hi)
    }
    b := sys.ThreadStackBounds()
    if b.lo == 0 && b.hi == 0 {
        return (lo: 0, hi: 0)
    }
    return (lo: b.lo + task.CarrierGuarantee(), hi: b.hi)
}

/// The bytes left on the running stack above its usable low bound: the
/// stack pointer less StackBounds' lo, or 0 when the stack is at or past
/// it or has no known bounds. It is a handful of instructions on a task,
/// so an interpreter may call it on every call and fail with its own error
/// before the guard page. O(1); allocates nothing.
export StackRemaining(): uint {
    sp := rt.StackPointer()
    b := StackBounds()
    if b.lo == 0 || sp <= b.lo {
        return 0
    }
    return sp - b.lo
}

// BlockingRec is one Blocking call's state address and function, in the
// caller's frame until the pool job ends (the task waits for it). The state
// stays in the caller's parameter until the job runs (as Chan.Send's x
// stays in its frame until a receiver takes it).
struct BlockingRec[T] {
    state: *T
    f: fn(T): T
}

// BlockingJob runs on a pool thread: it moves the state out of the caller's
// parameter, calls f and moves the result back in its place. op.buf holds
// the record's address.
BlockingJob[T](op: *sys.Op) {
    unsafe "op.buf is the BlockingRec of the Blocking call that waits for this job, live in its frame until the job returns; the state's bits are moved out and a value moved back in before that frame reads them again" {
        rec := raw.PtrCast[u8, BlockingRec[T]]((*op).buf)
        p := (*rec).state
        f := (*rec).f
        raw.Write(p, f(raw.Read(p)))
    }
}

/// Runs f(state) on the blocking pool, off the carriers, and returns its
/// result. Use it for a foreign call that blocks a thread, such as a C
/// library's wait or a raw system call, so that no carrier is held.
///
/// Fails with Canceled when io's scope is cancelled, or becomes cancelled
/// while the job waits in the pool's queue: the job is taken out unrun and
/// the caller still owns state, as a failed Chan.Send leaves the value
/// with the sender. On success state is consumed. Once f has started it
/// runs to its end and its result is returned, cancelled or not: a foreign
/// blocking call cannot be stopped from outside, so this is the one wait
/// a deadline cannot cut.
///
/// Off a task, or on an Io that is not the runtime's own (a Sim's), f runs
/// on the calling thread. The pool is shared with std's own blocking work:
/// 64 threads plus the wait jobs, 1,024 threads in all. O(1) besides f:
/// one queue append and one park; allocates nothing on the heap.
export Blocking[T: Send](io: Io, state: T, f: fn(T): T): Canceled!T {
    try io.Sleep(core.DurationOf(0))
    if !core.IsNative(io) || raw.IsNull(rt.CurrentSlot()) {
        return f(state)
    }
    var op := sys.NewOp(0, 0)
    var e := core.SysErr.None
    var rec := BlockingRec[T](state: raw.Null[T](), f: f)
    unsafe "rec and state outlive the job: BlockingWhole parks this task until the job has returned, or takes it out of the queue unrun" {
        rec.state = raw.Of(&state)
        op.buf = raw.PtrCast[BlockingRec[T], u8](raw.OfMut(mut rec))
        e = task.BlockingWhole(BlockingJob[T], raw.OfMut(mut op), task.Cancellable(io))
    }
    if e == core.SysErr.Canceled {
        fail Canceled
    }
    unsafe "the job ran: state's slot holds f's result, moved there by BlockingJob; the original bits were moved out, so the parameter is forgotten" {
        out := raw.Read(rec.state)
        raw.Forget(state)
        return out
    }
}

/// A wake that a task waits for and any thread may give, a foreign one
/// included. It is one word, so it may live in a `var` global or in memory
/// a C library holds a pointer to. A Wake before the Wait is kept: the
/// next Wait returns at once and consumes it.
export struct Waker {
    word: raw.Atomic[u32]
}

/// A Waker with no wake pending. O(1); allocates nothing.
export Waker.New(): Waker {
    return Waker(word: raw.Atomic[u32].New(0))
}

/// Waits until a Wake, then consumes it; returns at once when one is
/// pending. It may return early, as a wait on an atomic word does, so the
/// caller rechecks what it waits for. Fails with Canceled when io's scope
/// is or becomes cancelled. Off a task it is panic 16. O(1) besides the
/// wait; allocates nothing.
export Waker.Wait(&self, io: Io): Canceled!void {
    // Edge: seq_cst; the Swap takes a Wake's Store of 1 (and what the
    // waker wrote before it) or leaves the word 0 for the wait to compare.
    if self.word.Swap(0) == 1 {
        return
    }
    w := raw.Addr(raw.Of(&self.word))
    z: u32 = 0
    if core.IoWaitWord(io, w, raw.Addr(raw.Of(&z))) {
        fail Canceled
    }
    // Edge: seq_cst; consumes the wake that ended the wait, if any.
    _ = self.word.Swap(0)
}

/// Gives the wake: wakes the task waiting on it, or keeps the wake for the
/// next Wait. It may be called from any thread, a foreign one included,
/// and needs no Io. O(1); allocates nothing.
export Waker.Wake(&self) {
    // Edge: seq_cst; the Store publishes the waker's writes to the Swap of
    // the Wait it ends, and comes before IoWakeWord's fence and wakes.
    self.word.Store(1)
    core.IoWakeWord(raw.Addr(raw.Of(&self.word)), true)
}

/// An Io whose tasks run on one carrier thread of their own, which never
/// steals work and is never stolen from. Use it for code that needs one OS
/// thread, such as a library bound to the thread that started it. Every
/// task spawned from a scope opened from this Io runs there, and so do
/// their tasks. The carrier and its thread live until the process exits.
/// The carrier fires its tasks' sleeps; the shared carriers collect their
/// I/O completions. The Io's own operations run as io's do, on the
/// calling task.
///
/// A NoCancel copy of it is dedicated too, and io's own cancellation mask
/// is kept. A Sim's or another non-native Io is returned as it is, since a
/// Sim runs one task at a time already. A wrapping Io over a dedicated one,
/// such as std.sim's Recorder, is not dedicated: its scopes spawn on the
/// shared carriers. On Windows the carrier's thread handle stays open
/// until exit, so a handle-leak check around the call counts it.
///
/// Panics (code 8) when all 256 carrier records are in use, or when the
/// OS refuses the thread. Past the free records above the carrier count,
/// each call lowers by one the count of shared carriers not yet started.
/// O(1) system calls; reserves an 8 MiB system stack and commits the
/// carrier's record.
export Dedicated(io: Io): Io {
    return task.PinDedicated(io)
}

/// A running Thread. It is linear, so it is always joined with Join.
export linear struct Joined {
    i: int
}

/// Runs f on a new carrier of its own, whose thread runs nothing else. f
/// gets a full Io pinned to that carrier, as Dedicated's is, on a task
/// with a 16 MiB stack, as Main has. The carrier ends once f and every task
/// spawned on it have ended. f runs outside io's scopes: a cancel of the
/// caller's scope does not reach it, only Join.
///
/// With a Sim's or another non-native Io, f runs on the calling task, with
/// io, before Thread returns, as Blocking's f does; so it does with a
/// wrapping Io over a dedicated one, such as std.sim's Recorder.
///
/// Panics (code 8) when all 256 carrier records are in use or the OS
/// refuses the thread. A spawn through f's Io after the carrier ended
/// panics (code 8). O(1) system calls; takes a 16 MiB task slot, waiting
/// for one as a spawn does, and reserves an 8 MiB system stack.
export Thread(io: Io, f: fn(Io)): Joined {
    if !core.IsNative(io) {
        f(io)
        return Joined(i: -1)
    }
    return Joined(i: task.PinThread(f))
}

/// Waits until the Thread's carrier has ended, then joins its thread and
/// frees its record. The OS join runs on the blocking pool, so no carrier
/// is blocked.
///
/// Fails with Canceled when io's scope is or becomes cancelled, or its
/// deadline passes, while it waits. The Joined then stays the caller's, as
/// a failed Chan.Send leaves the value with the sender, to Join again, for
/// instance from a NoCancel Io. Off a task it is panic 16. O(1) besides
/// the wait; allocates nothing.
export Joined.Join(self, io: Io): Canceled!void {
    if self.i >= 0 {
        w := task.PinDone(self.i)
        for {
            unsafe "w is the done word of the Thread's record, which lives until PinJoin frees it below" {
                // Edge: seq_cst; the carrier's store of 1 comes before its
                // wake, and its thread is past its loop then.
                if (*raw.FromAddr[raw.Atomic[u32]](w)).Load() != 0 {
                    break
                }
            }
            z: u32 = 0
            if core.IoWaitWord(io, w, raw.Addr(raw.Of(&z))) {
                fail Canceled
            }
        }
    }
    Joined(i) := self
    if i >= 0 {
        task.PinJoin(i)
    }
}

/// What a panic hook receives: the panic code (see the panic code table
/// in the std.runtime docs), the message, and the source position of the
/// panicking code. Only C ABI values cross, so a hook may be written in C.
export struct PanicInfo {
    /// The panic code.
    export code: u32
    /// The panic's message.
    export message: str
    /// The source file of the panicking code.
    export file: str
    /// The line of the panicking code.
    export line: int
    /// The column of the panicking code.
    export column: int
}
