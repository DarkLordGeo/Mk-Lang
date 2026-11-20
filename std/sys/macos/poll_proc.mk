// Why: a waiting child must hold no thread, and macOS has no pidfd: the
// kernel reports a child's exit through kqueue's EVFILT_PROC on its pid.
//
// What: the child-wait half of the kqueue backend (poll.mk): ProcSubmit,
// ProcCancel and ProcEvent, the pid-keyed wait list, ExitCode and the reap.
//
// How: OpProcWait's op goes on a list keyed by pid under a lock, then the
// pid is registered with EV_ONESHOT and NOTE_EXIT; a non-reaping waitid
// peek catches a child that ended before the registration. On the event the
// child is reaped with waitpid (after a group kill for a group leader), and
// its status becomes the exit code: 0 to 255, or minus the signal.

import std.core
import std.raw

// ProcLock and ProcUnlock take and release the child-wait list's lock.
ProcLock() {
    unsafe "the state page" {
        SpinLock(raw.Of(&(*PP()).proc_lock))
    }
}

ProcUnlock() {
    unsafe "the state page" {
        SpinUnlock(raw.Of(&(*PP()).proc_lock))
    }
}

// ProcUnlinkLocked takes op off the child-wait list (the caller holds the
// lock): true when it was there.
ProcUnlinkLocked(op: *Op): bool {
    pp := PP()
    unsafe "the state page; the list under its lock" {
        var prev := raw.Null[Op]()
        var cur := (*pp).proc_head
        for !raw.IsNull(cur) {
            nx := Next(cur)
            if raw.Same(cur, op) {
                if raw.IsNull(prev) {
                    (*pp).proc_head = nx
                } else {
                    SetNext(prev, nx)
                }
                return true
            }
            prev = cur
            cur = nx
        }
    }
    return false
}

ProcUnlink(op: *Op): bool {
    ProcLock()
    r := ProcUnlinkLocked(op)
    ProcUnlock()
    return r
}

// ProcTakePid takes the first op waiting on pid off the list, or null.
ProcTakePid(pid: u64): *Op {
    pp := PP()
    ProcLock()
    unsafe "the state page; the list under its lock" {
        var cur := (*pp).proc_head
        for !raw.IsNull(cur) {
            if u64((*cur).h) == pid {
                _ = ProcUnlinkLocked(cur)
                ProcUnlock()
                return cur
            }
            cur = Next(cur)
        }
    }
    ProcUnlock()
    return raw.Null[Op]()
}

// ExitCode is a wait status as the exit code: 0 to 255 for an exit, minus
// the signal that ended the child.
ExitCode(st: i64): i64 {
    sig := st & 0x7F
    return if sig == 0 { (st >> 8) & 0xFF } else { -sig }
}

// ProcFinish reaps op's child, which has ended (or is gone): op.pos 1 marks
// a group leader (std.rt's Spawn), whose group is killed first, while the
// unreaped leader still holds the group's id, which no new process can then
// get (ESRCH, nothing left in the group, is fine). The reap is a blocking
// waitpid (EINTR repeated), which returns at once for an ended child. A
// child already reaped elsewhere (ECHILD) takes the event's status when
// have, else its error (Other): a second reap after the first is ECHILD,
// and std.task keeps the first answer.
ProcFinish(op: *Op, have: bool, status: i64) {
    unsafe "op is owned by this thread; kill and waitpid pass the status word in this frame" {
        pid := Trunc[i32]((*op).h)
        if (*op).pos == 1 {
            _ = kill(0 - pid, 9)
        }
        var st: [1]i32 = [0]
        for {
            r := waitpid(pid, raw.DataMut(mut st[..]), 0)
            if r == pid {
                (*op).res = Ok(ExitCode(i64(st[0])))
                return
            }
            if r != -1 {
                (*op).res = FailWith(core.SysErr.Other)
                return
            }
            e := Errno()
            if e == Eintr {
                continue
            }
            if have && e == Echild {
                (*op).res = Ok(ExitCode(status & 0xFFFF_FFFF))
                return
            }
            (*op).res = Fail(e)
            return
        }
    }
}

// ProcExited peeks at the child pid without reaping it (waitid P_PID with
// WEXITED, WNOHANG and WNOWAIT; EINTR repeated): true when it has ended or
// is not this process's child (ECHILD: the reap then says which), false
// while it runs (si_pid, at 12 of Darwin's siginfo_t, stays 0) and on any
// other error, which leaves the op to the exit event.
ProcExited(pid: u64): bool {
    var si: [13]u64 = [0; 13]
    unsafe "si holds a siginfo_t (104 bytes)" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut si[..]))
        for {
            r := waitid(PPid, Trunc[u32](pid), p, WExited | WNoHang | WNoWait)
            if r == 0 {
                return Get32(p, 12) != 0
            }
            e := Errno()
            if e != Eintr {
                return e == Echild
            }
        }
    }
}

// ProcSubmit starts a child wait (h the child's pid): op goes on the list,
// then the pid gets an EVFILT_PROC NOTE_EXIT registration (EV_ONESHOT; its
// udata is unused, so a late event finds the pid on the list or nothing).
// A child that ended before the registration is found either way: Darwin
// refuses the registration (ESRCH), or the peek after it sees the ended
// child; then op is finished now, unless an event's handler took it off
// the list first, whose completion comes through PollWait.
ProcSubmit(op: *Op): bool {
    pp := PP()
    unsafe "op is the caller's operation; the list under its lock" {
        pid := u64((*op).h)
        if pid == 0 || pid > 0x7FFF_FFFF {
            (*op).res = FailWith(core.SysErr.Other)
            return false
        }
        ProcLock()
        SetNext(op, (*pp).proc_head)
        (*pp).proc_head = op
        ProcUnlock()
        e := KqChange(Kq(), pid, FiltProc, EvAdd | EvOneShot, NoteExit | NoteExitStatus)
        if e != 0 {
            if !ProcUnlink(op) {
                return true
            }
            if e == Esrch {
                ProcFinish(op, false, 0)
            } else {
                (*op).res = Fail(e)
            }
            return false
        }
        if ProcExited(pid) && ProcUnlink(op) {
            ProcFinish(op, false, 0)
            return false
        }
        return true
    }
}

// ProcCancel cancels a child wait still on the list: off the list, its
// registration removed when no other op waits on the pid (under the lock,
// so a new wait's registration comes after the delete), and queued as
// Canceled (then PollWake). An op already off the list is being finished
// by an event's handler, or has completed.
ProcCancel(op: *Op) {
    pp := PP()
    unsafe "op is pending (the one-owner rule); the list under its lock" {
        pid := u64((*op).h)
        ProcLock()
        if !ProcUnlinkLocked(op) {
            ProcUnlock()
            return
        }
        var other := false
        var cur := (*pp).proc_head
        for !raw.IsNull(cur) {
            if u64((*cur).h) == pid {
                other = true
                break
            }
            cur = Next(cur)
        }
        if !other {
            _ = KqChange(Kq(), pid, FiltProc, EvDelete, 0)
        }
        ProcUnlock()
        (*op).res = FailWith(core.SysErr.Canceled)
        PushDone(op)
        PollWake()
    }
}

// ProcEvent handles a child's NOTE_EXIT: every op waiting on pid is taken
// off the list, finished (the first reaps; have and status are the event's
// wait status) and delivered.
ProcEvent(pid: u64, have: bool, status: i64, out: mut []*Op, k: int): int {
    var n := k
    for {
        op := ProcTakePid(pid)
        if raw.IsNull(op) {
            return n
        }
        ProcFinish(op, have, status)
        n = Deliver(op, out, n)
    }
}
