// Why: some interleavings of the pollers (a cancel racing a resubmission, an
// edge between a try and its recording) and some OS paths (old kernels, short
// directory reads) cannot be reached from outside, but tests must drive them.
//
// What: SysTestHook, which turns a numbered test point on or off with a
// callback, and TestOn and TestPoint, which std.sys calls at each point.
//
// How: each point is a few atomic words in HookState; off, a point costs one
// atomic load. This file is the same in the Linux and Windows variants; a
// point a variant does not have never runs. The points:
// 1 the signal install window (after the Cas that makes a thread the
//   installer, before rt_sigaction or SetConsoleCtrlHandler);
// 2 the resubmission window (io_uring Resubmit: after the push, before the
//   cancel check; IOCP ReissueAccept: before the AcceptEx);
// 3 Windows HrArm: slot 0 behaves as dirty and still pending;
// 4 io_uring: SubmitPrepared records the first user_data it stamps, and
//   UringCancel names that recorded value instead of the op's current one
//   (a late cancel of an old submission);
// 5 io_uring: the first op CQE reaped while the point is on reports EINTR,
//   so the op goes through Resubmit with no cancel; IOCP: the first accept
//   packet while it is on reports a reset, so the op goes through
//   ReissueAccept (ResubCancel: the cancel check after the resubmission);
// 6 epoll: EpollReap waits with epoll_pwait (milliseconds), the path of
//   every kernel before 5.11, which a newer kernel never takes (EpollMs);
// 7 IOCP: a finite PollWait posts one stray timer packet (HrKey) before its
//   first wait, so the wait must go on to its deadline (StrayTimer);
// 8 Windows SemWait: one WaitForSingleObject waits at most 2 ms, as each
//   call of a timeout past the 49.7-day millisecond cap does, so the wait
//   must go on to its deadline (SemLong);
// 9 io_uring Push: returns before its io_uring_enter, as an EBUSY or EAGAIN
//   leaves the SQE published and unconsumed, so a later push must submit it
//   (PushAll);
// 10 IOCP ReissueAccept: after the reissued AcceptEx, before the cancel
//   check and the release of the handoff word, where another thread's
//   PollWait may dequeue the new packet (ReissueRace; io_uring's twin is
//   point 2, which is after its push).
// 11 Windows: each GetFinalPathNameByHandleW of the *At calls (removeat.mk
//   AtPath, and DirOpenAt's own path for a stream opened by a whole path),
//   so a test counts them;
// 12 a directory stream's refill that goes to the OS (DirFill on both
//   targets, once per call that makes a system call), so a test counts the
//   fills a listing takes;
// 13 Linux DirFill: each statx it makes for one entry, so a test counts
//   them against the directories listed;
// 14 Windows DirFill: while on, a refill's first ask is given 1,024 bytes,
//   so DirMore asks again into the rest of the block and chains what comes
//   back, a path NTFS never takes on its own.

import std.raw

const TestPoints: int = 15

struct HookState {
    on: [15]raw.Atomic[u32]
    cb: [15]raw.Atomic[u64]
    arg: [15]raw.Atomic[u64]
    ran: [15]raw.Atomic[u64]
    // Point 4's recorded user_data (0: none yet).
    saved: raw.Atomic[u64]
    // Point 5: 1 once its one CQE was turned into EINTR.
    faked: raw.Atomic[u32]
}

var Hooks: HookState = HookState(
    on: [raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0)],
    cb: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)],
    arg: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)],
    ran: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)],
    saved: raw.Atomic[u64].New(0), faked: raw.Atomic[u32].New(0))

/// SysTestHook installs (on) or removes cb(arg) at test point `point` and
/// returns how many times the point ran so far (0 for an unknown point).
/// Turning point 4 on clears its recorded user_data, and turning point 5 on
/// rearms its one fake EINTR. For tests only.
/// O(1); allocates nothing.
export SysTestHook(point: u32, cb: fn(*u8), arg: *u8, on: bool): u64 {
    if point == 0 || int(point) >= TestPoints {
        return 0
    }
    p := int(point)
    // cb and arg are stored before on's Store(1), and TestPoint reads them
    // after its Load sees 1: the hook's publication edge (sequentially
    // consistent store and load).
    if on {
        var w: u64 = 0
        unsafe "the bytes of a fn value are its code address" {
            raw.Write(raw.PtrCast[u64, fn(*u8)](raw.OfMut(mut w)), cb)
        }
        Hooks.cb[p].Store(w)
        Hooks.arg[p].Store(u64(raw.Addr(arg)))
        if point == 4 {
            // Edge: none; the records are cleared before on is stored below.
            Hooks.saved.Store(0)
        }
        if point == 5 {
            Hooks.faked.Store(0)
        }
        // Edge: the Store of 1 publishes cb and arg, stored above, to
        // TestPoint's Load of 1.
        Hooks.on[p].Store(1)
    } else {
        Hooks.on[p].Store(0)
    }
    // Edge: none; a counter.
    return Hooks.ran[p].Load()
}

// TestOn: point p is on.
TestOn(p: int): bool {
    // Edge: SysTestHook's Store of on, after cb and arg.
    return Hooks.on[p].Load() == 1
}

// TestPoint runs point p's callback when it is on; true when it ran.
TestPoint(p: int): bool {
    // Edge: on is loaded before cb and arg, which were stored before it.
    if Hooks.on[p].Load() != 1 {
        return false
    }
    var w := Hooks.cb[p].Load()
    unsafe "the hook's words hold a fn(*u8) and its argument" {
        f := raw.Read(raw.PtrCast[u64, fn(*u8)](raw.OfMut(mut w)))
        f(raw.FromAddr[u8](Trunc[uint](Hooks.arg[p].Load())))
    }
    // A counter: nothing is published by it.
    // Edge: none; a counter.
    _ = Hooks.ran[p].Add(1)
    return true
}
