// Why: a spawned process must be waited for or given up exactly once, and
// signals must never reach a reused pid; that ownership lives apart from the
// pipes, so a Wait has nothing to close before a cancel can end it.
//
// What: proc.Child (Wait, Signal, Kill, Pid, Detach) and proc.Status, over
// std.rt's ChildWait, ChildSignal and ChildDetach.
//
// How: a Child holds only the runtime handle of the process (a pidfd on
// Linux, made with the child; the process handle on Windows), its pid, and
// the group's job object on Windows. It owns no pipe and no memory, so it
// does not implement Free. Signal, Kill and Detach take no Io: their runtime
// calls are inline, never a cancellation point, so they go through the
// native Io with the cancel masked.

import std.core
import std.rt

/// A running child process, and its process group when it was started
/// with Command.group. Linear: end it with Wait or Detach. Send and Share,
/// so one task can Signal it while another reads its pipes.
export linear struct Child {
    h: uint
    ospid: i64
    job: uint
    group: bool
}

/// How a child ended.
///
/// code is the exit code: 0 to 255 on Linux, the 32-bit exit code on
/// Windows (1 after a Kill). It is none exactly when a signal ended the
/// child on Linux. signal is that signal when Signal has a member for it;
/// SIGKILL has none, so a Linux child ended by Kill has both fields none.
export struct Status {
    /// The exit code; none exactly when a signal ended the child on Linux.
    export code: ?int
    /// The signal that ended the child, when Signal has a member for it.
    export signal: ?Signal
}

// StatusOf is the Status of ChildWait's value v: an exit code, or minus
// the signal that ended a Linux child.
StatusOf(v: i64): Status {
    if v >= 0 {
        return Status(code: some(Trunc[int](v)), signal: none)
    }
    sig: ?Signal = match -v {
        1 { some(Signal.Hangup) }
        2 { some(Signal.Interrupt) }
        15 { some(Signal.Terminate) }
        _ { none }
    }
    return Status(code: none, signal: sig)
}

// WaitCode waits for the child h (job its Windows job, 0 none) and returns
// ChildWait's value: the exit code, or minus the signal on Linux. On any
// error the child is unreaped and the job unchanged.
WaitCode(io: Io, h: uint, job: uint): Error!i64 {
    return try Check(rt.ChildWait(io, h, job))
}

/// Waits until the child ends, and returns how it ended. When the child
/// leads a group, every process still in the group is killed before Wait
/// returns. The kill runs while the leader is not yet reaped, so it cannot
/// reach another group that reuses the id.
///
/// On success the child is consumed. On Canceled, or any other error, the
/// caller still owns it, running and not yet waited for: call Wait again,
/// Kill it and then Wait, or Detach it.
///
/// Wait reads no pipe. Read each of the child's pipes in a task before or
/// while waiting, or a child that fills a pipe blocks forever; a child
/// that reads its input to the end waits forever while its stdin is open.
///
/// Waiting holds no thread. O(1) besides the wait; allocates nothing.
export Child.Wait(self, io: Io): Error!Status {
    v := try WaitCode(io, self.h, self.job)
    rt.Abandon(self.h)
    Child(h: _, ospid: _, job: _, group: _) := self
    return StatusOf(v)
}

// SignalNumber is s's Linux number.
SignalNumber(s: Signal): u32 {
    return match s {
        .Interrupt { 2 }
        .Terminate { 15 }
        .Hangup { 1 }
    }
}

// SendSignal sends Linux signal sig to the child, or to its group.
SendSignal(c: &Child, sig: u32): Error!void {
    _ = try Check(rt.ChildSignal(core.IoMasked(rt.NativeIo()), c.h, c.group, c.ospid, c.job, sig))
}

/// Sends s to the child, or, with Command.group, to every process still in
/// its group (a process that left the group is not reached). Sending to a
/// child that has already ended succeeds.
///
/// Unsupported on Windows, which can send no signal but a kill to another
/// process; any other failure is the OS's. Not a cancellation point; O(1);
/// allocates nothing.
export Child.Signal(&self, s: Signal): Error!void {
    try SendSignal(self, SignalNumber(s))
}

/// Ends the child at once, or, with Command.group, every process still in
/// its group. Killing a child that has already ended succeeds; any failure
/// is the OS's. Call Wait afterwards: the Child is still owned.
///
/// On Linux this is SIGKILL. On Windows it is TerminateProcess with exit
/// code 1, or the end of the group's job object. Not a cancellation point;
/// O(1); allocates nothing.
export Child.Kill(&self): Error!void {
    try SendSignal(self, 9)
}

/// The child's OS process id, for display and for other programs. Wait,
/// Signal and Kill use the OS handle of the process, not this number, so
/// they never reach another process that reuses the id. O(1); allocates
/// nothing.
export Child.Pid(&self): i64 {
    return self.ospid
}

/// Gives the child up without waiting for it. The child and its group run
/// on, and the runtime collects the child's exit status when it ends, so
/// no zombie process is left behind. Infallible and not cancellable.
///
/// O(1). On Linux the first Detach in a process starts one thread, with
/// a 1 MiB stack reservation, and one epoll set, kept until exit, that
/// collect detached children as they end; if the thread cannot start, a
/// child is collected at the next Spawn or Detach. On Windows nothing
/// remains once the child's handles are closed.
export Child.Detach(self) {
    _ = rt.ChildDetach(core.IoMasked(rt.NativeIo()), self.h, self.job)
    Child(h: _, ospid: _, job: _, group: _) := self
}
