// Why: every native I/O operation that the poller completes (iocp,
// io_uring, epoll) has to park its task without holding the carrier and
// still be cancellable without losing a completion.
//
// What: Await and AwaitIn, which submit an operation and park until it
// completes, and WaitUnpinned.
//
// How: submit to the poller and park with the handshake until the
// completion. A cancellable wait is on its scope's blocked list from before
// the submit until after the completion, so a cancel either finds it there
// or is seen by the check after the submit. A Dekker pair on op_live and the
// cancel word makes at least one side call PollCancel.

import std.core
import std.raw
import std.rt
import std.sys

// WaitUnpinned spins, then yields the thread, until no cancel walk holds
// t's pin (rt.Lock's backoff): the walk calls one PollCancel and stores 0,
// and does not suspend in between.
WaitUnpinned(t: *Task) {
    unsafe "t is the running task" {
        var spins := 1
        // Edge: CancelRec's release of the pin, after its PollCancel.
        for (*t).cancel_pin.Load() != 0 {
            for _ in 0..spins {
                if (*t).cancel_pin.Load() == 0 {
                    break
                }
            }
            if spins < 64 {
                spins *= 2
            } else {
                sys.ThreadYield()
            }
        }
    }
}

/// Await submits op and parks the running task until op completes; the
/// operation's result, its error included, is then in op.res. A cancellable
/// wait in a cancelled scope ends with Canceled, either before the submission
/// or after the cancelled operation's completion, so op's buffer has one
/// owner throughout. Calls the scope's leave and enter hooks around the wait.
/// Returns Canceled when op.res says Canceled or the scope was already
/// cancelled, else None. O(1) besides the wait: one submission and one park;
/// allocates nothing (op and its buffer are the caller's).
export Await(op: *sys.Op, cancellable: bool): core.SysErr {
    t := MustCurrent()
    owed := HookLeave(t)
    r := AwaitIn(t, op, cancellable)
    HookEnter(t, owed)
    return r
}

// AwaitIn is Await between the task's hooks (hooks.mk).
AwaitIn(t: *Task, op: *sys.Op, cancellable: bool): core.SysErr {
    var sc := raw.Null[ScopeRec]()
    if cancellable {
        if Cancelled(t) {
            return core.SysErr.Canceled
        }
        locked := LockScope(t)
        if locked.cancelled {
            return core.SysErr.Canceled
        }
        sc = locked.sc
    }
    unsafe "op is in the waiting task's frame and does not move while pending" {
        (*op).waiter = raw.PtrCast[Task, u8](t)
        (*op).cancel.Store(0)
        // Edge: the handshake word is Pending before PollSubmit hands op over, so
        // the completer's Swap(Done) follows this store in S.
        (*op).state.Store(Pending)
        (*t).op = op
        if !raw.IsNull(sc) {
            // Registered under the scope lock, released before the submit
            // a cancel from here on marks op and calls
            // PollCancel, which is harmless before the submission and after
            // the completion (std.sys's contract). The task stays on the
            // list until Unblock, so the cancel never outlives op's frame.
            LinkBlocked(sc, t, WaitKindOp)
            rt.Unlock(&(*sc).lock)
        }
        // Edge: raised before the submission, so a PollNow that could reap this
        // completion sees npending > 0; the count orders nothing else.
        _ = (*TS()).npending.Add(1)
        if sys.PollSubmit(op) {
            // The completion may be reaped on another carrier before
            // this task has switched out; ParkUntil's handshake readies it
            // exactly once either way.
            Jitter(JitterSubmit)
            // The Dekker pair with CancelWait (seq_cst): op_live.Store(1)
            // then cancel.Load() here, cancel.Store(1) then op_live.Load()
            // there; at least one of the two calls PollCancel, after the
            // submission.
            // Edge: the Dekker pair's store (CancelWait reads op_live after its own
            // cancel store); see the comment above.
            (*t).op_live.Store(1)
            if (*op).cancel.Load() == 1 {
                sys.PollCancel(op)
            }
            ParkUntil(raw.Of(&(*op).state))
            // Edge: after the completion; a later walk that reads 0 does not cancel
            // an operation that is over.
            (*t).op_live.Store(0)
        } else {
            // Edge: none needed; the count for PollNow (no completion will come).
            _ = (*TS()).npending.Add(0xFFFF_FFFF)
        }
        if !raw.IsNull(sc) {
            Unblock(t)
            // A cancel walk that pinned this task calls PollCancel
            // on op after its locks; op is in the caller's frame, so wait
            // for that call to finish (CancelRec, a few hundred ns) before
            // returning. Edge: the pin was stored under the scope lock that
            // Unblock just took, so this load sees it.
            WaitUnpinned(t)
        }
        (*t).op = raw.Null[sys.Op]()
        if (*op).res.err == core.SysErr.Canceled {
            return core.SysErr.Canceled
        }
    }
    return core.SysErr.None
}
