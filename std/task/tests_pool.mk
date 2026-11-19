// Why: blocking system calls run on pool threads; a job that ran on a
// carrier, or a cancel that lost a queued job, would stall the scheduler.
// What: RT-Pool-Basic, the pool path of RT-F1-ParkHandshake, RT-Pool-CancelQueued
// and RT-Pool-CancelBefore, run by the scheduler harness and by mk test.
// How: jobs are plain functions over sys.Op (PBlock, PQuick); a blocking job
// waits on a kernel semaphore the block posts, so a job that ran on a
// carrier would hold that carrier.

import std.core
import std.raw
import std.rt
import std.sys

// PBlock is a job that blocks its thread until the semaphore at op.buf is
// posted (at most 10 s), then stores 7 in op.res.value.
PBlock(op: *sys.Op) {
    unsafe "op.buf is the block's semaphore" {
        ok := sys.SemWait(&(*raw.PtrCast[u8, sys.Sem]((*op).buf)), 10_000_000_000)
        (*op).res = sys.OsResult(value: 7, err: core.SysErr.None, os: 0)
        if !ok {
            (*op).res.value = -1
        }
    }
}

// PQuick is a job that returns at once: op.res.value = op.pos * 3.
PQuick(op: *sys.Op) {
    unsafe "op is the caller's" {
        (*op).res = sys.OsResult(value: (*op).pos * 3, err: core.SysErr.None, os: 0)
    }
}

// PRun runs one job through the pool; the result value, or -2 for
// Canceled.
PRun(job: fn(*sys.Op), sem: *sys.Sem, pos: i64, cancellable: bool): i64 {
    var op := sys.NewOp(0, 0)
    op.pos = pos
    unsafe "op is in this frame until Blocking returns" {
        op.buf = raw.PtrCast[sys.Sem, u8](sem)
        e := Blocking(job, raw.OfMut(mut op), cancellable)
        if e == core.SysErr.Canceled {
            return -2
        }
    }
    return op.res.value
}

// PBlocker runs one blocking job and adds 1 to done when it returned 7.
PBlocker(io: Io, sem: uint, done: &raw.Atomic[u32]) {
    if PRun(PBlock, PSem(sem), 0, false) == 7 {
        _ = done.Add(1)
    }
}

// PSetter sets the word w after a 1 ms sleep, from a task.
PSetter(io: Io, w: &raw.Atomic[u32], sem: uint) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
    w.Store(1)
    unsafe "sem lives in the block's frame until the scope ends" {
        sys.SemPost(&(*PSem(sem)), 1)
    }
}

// PSem and PAddr pass a semaphore to a task by address (a pointer is not
// Send); the semaphore outlives the scope.
PSem(a: uint): *sys.Sem {
    unsafe "a is a semaphore's address" {
        return raw.FromAddr[sys.Sem](a)
    }
}

PAddr(s: &sys.Sem): uint {
    unsafe "the address of a semaphore in the block's frame" {
        return raw.Addr(raw.Of(s))
    }
}

// PPost posts n to s (a test block's sys parameter hides the module).
PPost(s: &sys.Sem, n: u32) {
    sys.SemPost(s, n)
}

PNewSem(): sys.Sem {
    var s := sys.NewSem()
    Assert(sys.SemInit(mut s))
    return s
}

test "RT-Pool-Basic a blocking job holds a pool thread, never the carrier" (sys: &System) {
    // With MK_CARRIERS=1, a job run on the carrier would wait for a post
    // that only a task on that carrier makes.
    var sem := PNewSem()
    w := raw.Atomic[u32].New(0)
    done := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go PBlocker(sys.io, PAddr(&sem), &done)
        s.go PSetter(sys.io, &w, PAddr(&sem))
    }
    Assert(done.Load() == 1 && w.Load() == 1)
}

// PMany runs n quick jobs and checks each result and a local.
PMany(io: Io, base: i64, n: int, bad: &raw.Atomic[u32]) {
    for k in 0..n {
        guard := base * 7 + Trunc[i64](k)
        r := PRun(PQuick, raw.Null[sys.Sem](), base + Trunc[i64](k), false)
        if r != (base + Trunc[i64](k)) * 3 || guard != base * 7 + Trunc[i64](k) {
            _ = bad.Add(1)
        }
    }
}

test "RT-F1-ParkHandshake pool path: 16 tasks x 2,000 jobs whose completions race the park" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..16 {
            s.go PMany(sys.io, Trunc[i64](k) * 100_000, 2_000, &bad)
        }
    }
    Assert(bad.Load() == 0)
}

// PCancelled runs one cancellable blocking job; got 1 when Canceled, 2 when
// it ran.
PCancelled(io: Io, sem: uint, got: &raw.Atomic[u32]) {
    r := PRun(PBlock, PSem(sem), 0, true)
    if r == -2 {
        got.Store(1)
    } else {
        got.Store(2)
    }
}

// PPoolIs is true once every pool thread runs a job and exactly queued
// jobs wait (at most 2 s of yields).
PPoolIs(queued: u32): bool {
    st := TS()
    t0 := BenchNow()
    for BenchNow() - t0 < 2_000_000_000 {
        unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
            rt.Lock(&(*st).plock)
            ok := (*st).pthreads == (*st).pmax && (*st).pwaiting == 0 && (*st).pqueued == queued
            rt.Unlock(&(*st).plock)
            if ok {
                return true
            }
        }
        YieldNow()
    }
    return false
}

test "RT-Pool-CancelQueued a queued job is removed and returns Canceled; a job in its call runs to its end" (sys: &System) {
    var sem := PNewSem()
    var max: u32 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        max = (*TS()).pmax
    }
    done := raw.Atomic[u32].New(0)
    queued := raw.Atomic[u32].New(0)
    incall := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope outer := sys.io.Scope() {
        // Every pool thread blocked in a job, outside the cancelled scope.
        for _ in 0..max {
            outer.go PBlocker(sys.io, PAddr(&sem), &done)
        }
        Assert(PPoolIs(0))
        scope s := sys.io.Scope() {
            s.go PCancelled(sys.io, PAddr(&sem), &queued)
            Assert(PPoolIs(1))
            s.Cancel()
        }
        // The queued job came back at once, with every thread still busy.
        Assert(queued.Load() == 1)
        Assert(BenchNow() - t0 < 5_000_000_000)
        PPost(&sem, max)
    }
    Assert(done.Load() == max)
    // A job in its call when the cancel comes: its own result (data wins),
    // after the post ends the call.
    scope s2 := sys.io.Scope() {
        s2.go PCancelled(sys.io, PAddr(&sem), &incall)
        unsafe "the TaskState page Start mapped, which lives until exit" {
            st := TS()
            t1 := BenchNow()
            for BenchNow() - t1 < 2_000_000_000 {
                rt.Lock(&(*st).plock)
                busy := (*st).pqueued == 0 && (*st).pwaiting + 1 <= (*st).pthreads
                rt.Unlock(&(*st).plock)
                if busy && CBlocked(RecOf(&s2)) == 1 {
                    break
                }
                YieldNow()
            }
        }
        s2.Cancel()
        PPost(&sem, 1)
    }
    Assert(incall.Load() == 1 || incall.Load() == 2)
}

test "RT-Pool-CancelBefore a cancelled scope's job never starts" (sys: &System) {
    var sem := PNewSem()
    got := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.Cancel()
        s.go PCancelled(sys.io, PAddr(&sem), &got)
    }
    Assert(got.Load() == 1)
    // A NoCancel job in a cancelled scope runs.
    scope s3 := sys.io.Scope() {
        s3.Cancel()
        Assert(PRun(PQuick, raw.OfMut(mut sem), 5, false) == 15)
    }
}
