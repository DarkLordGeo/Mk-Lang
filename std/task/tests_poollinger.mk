// Why: a carrier lingers as a spinner while its task waits on a young pool
// job, and a pool thread spins before it sleeps; both must stop on time.
// What: RT-Os-PoolLinger-Ends, RT-Os-PoolLinger-Rule and RT-Os-PoolSpin-Burst,
// over sched.mk's PoolLingerNs and pool.mk's PoolSpin (PlLong, PlRuleCases, PlMany).
// How: each block runs alone in its process, so the counters it reads are its
// own; run by the scheduler harness at 1 to 8 carriers, dev and release.

import std.core
import std.raw
import std.rt
import std.sys

// PlLong is a pool job that waits on the never-posted semaphore at op.buf,
// 1 ms at a time, until it sees no carrier spinning, for at most 2 s:
// op.res.value is 1 when it saw nspinning 0, else 0. Its task's carrier
// lingers as a spinner while the job is young (pool_wait), and must stop
// within PoolLingerNs of its first fruitless search.
PlLong(op: *sys.Op) {
    t0 := BenchNow()
    var seen: i64 = 0
    unsafe "op.buf is the block's semaphore; the TaskState page" {
        s := raw.PtrCast[u8, sys.Sem]((*op).buf)
        for BenchNow() - t0 < 2_000_000_000 {
            _ = sys.SemWait(&(*s), 1_000_000)
            // Edge: none needed; any later value is the spinners' own count.
            if (*TS()).nspinning.Load() == 0 {
                seen = 1
                break
            }
        }
        (*op).res = sys.OsResult(value: seen, err: core.SysErr.None, os: 0)
    }
}

test "RT-Os-PoolLinger-Ends a carrier lingering for a pool job stops spinning while a long job still runs" (sys: &System) {
    var sem := PNewSem()
    // Twice, so the second job finds a pool thread that spun and slept.
    for _ in 0..2 {
        Assert(PRun(PlLong, raw.OfMut(mut sem), 0, false) == 1)
    }
}

// PlRuleCases runs Linger on a zeroed carrier record of its own (never a
// started carrier) in the pool linger's three cases, as LingerCases does
// for the runnext linger, and returns how many went wrong. No runnext was
// seen (next_seen_at far in the past), so only the pool clause decides.
PlRuleCases(): int {
    p := sys.Reserve(CarrierBytes(1), 65536)
    if raw.IsNull(p) || !sys.Commit(p, CarrierBytes(1)) {
        return 100
    }
    far: i64 = 1_000_000_000
    var bad := 0
    unsafe "a zeroed carrier record of this function's own, no thread runs on it" {
        c := raw.PtrCast[u8, Carrier](p)
        (*c).next_seen_at = sys.Now() - LingerNs - far
        // A task just parked in Blocking, no linger yet: linger, start the
        // clock, keep the flag.
        (*c).pool_wait = true
        (*c).linger_from = 0
        if !Linger(c) || (*c).linger_from == 0 || !(*c).pool_wait {
            bad += 1
        }
        // PoolLingerNs has passed: leave, clear the clock and the flag.
        (*c).linger_from = sys.Now() - PoolLingerNs - far
        if Linger(c) || (*c).linger_from != 0 || (*c).pool_wait {
            bad += 1
        }
        // No pool park and no runnext seen: leave at once.
        (*c).linger_from = 0
        if Linger(c) || (*c).linger_from != 0 {
            bad += 1
        }
        // Found ends the pool linger (the carrier has work again).
        (*c).pool_wait = true
        (*c).spinning = false
        Found(c)
        if (*c).pool_wait {
            bad += 1
        }
    }
    sys.Release(p, CarrierBytes(1))
    return bad
}

test "RT-Os-PoolLinger-Rule a carrier lingers for a pool park for at most PoolLingerNs, and Found ends it" (sys: &System) {
    Assert(PlRuleCases() == 0)
}

// PlMany runs n quick jobs (PQuick) and counts the wrong results in bad.
PlMany(io: Io, base: i64, n: int, bad: &raw.Atomic[u32]) {
    for k in 0..n {
        if PRun(PQuick, raw.Null[sys.Sem](), base + Trunc[i64](k), false) != (base + Trunc[i64](k)) * 3 {
            _ = bad.Add(1)
        }
    }
}

test "RT-Os-PoolSpin-Burst 4 tasks x 5,000 back-to-back quick jobs, every result right, at most one pool thread per outstanding job" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..4 {
            s.go PlMany(sys.io, Trunc[i64](k) * 1_000_000, 5_000, &bad)
        }
    }
    Assert(bad.Load() == 0)
    st := TS()
    unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
        rt.Lock(&(*st).plock)
        // Every queued job was taken (the spin claims nothing; the hint is
        // back to 0), and no more threads were started than jobs were ever
        // outstanding at once (4, one per task): a thread is counted
        // waiting from the plock section that ends its job (RunJob), so a
        // task's next job finds it. With the count raised only back in
        // PoolMain, a task that won the race to its next enqueue would start
        // a thread each time (tens of threads, up to pmax).
        ok := (*st).pqueued == 0 && (*st).pqueued_n.Load() == 0 && (*st).pthreads <= 4
        rt.Unlock(&(*st).plock)
        Assert(ok)
    }
}

// WsRuleCases drives the adaptive spin's rule (WaitSpinGo, WaitSpinSaw)
// through its cases and returns how many went wrong.
WsRuleCases(): int {
    var bad := 0
    var w := WaitSpin(misses: 0, skipped: 0)
    // After a job that was not a wait the thread always spins, and the
    // state is untouched.
    for _ in 0..100 {
        if !WaitSpinGo(mut w, false) {
            bad += 1
        }
    }
    if w.misses != 0 || w.skipped != 0 {
        bad += 1
    }
    // WaitSpinMisses misses in a row after wait jobs: each still spins.
    for _ in 0..WaitSpinMisses {
        if !WaitSpinGo(mut w, true) {
            bad += 1
        }
        WaitSpinSaw(mut w, false)
    }
    // Then it spins once every WaitSpinProbe-th time: WaitSpinProbe skips,
    // then a probe.
    for _ in 0..WaitSpinProbe {
        if WaitSpinGo(mut w, true) {
            bad += 1
        }
    }
    if !WaitSpinGo(mut w, true) {
        bad += 1
    }
    // A probe that misses keeps it probing; one that hits resets it, so the
    // next wait job's spins run again.
    WaitSpinSaw(mut w, false)
    if WaitSpinGo(mut w, true) {
        bad += 1
    }
    for _ in 1..WaitSpinProbe {
        _ = WaitSpinGo(mut w, true)
    }
    if !WaitSpinGo(mut w, true) {
        bad += 1
    }
    WaitSpinSaw(mut w, true)
    if w.misses != 0 || !WaitSpinGo(mut w, true) {
        bad += 1
    }
    // A job that was not a wait spins even while the waits' spins are off.
    w = WaitSpin(misses: WaitSpinMisses, skipped: 0)
    if !WaitSpinGo(mut w, false) || w.skipped != 0 {
        bad += 1
    }
    return bad
}

test "RT-Os-PoolWaitSpin-Rule after wait jobs a pool thread spins until WaitSpinMisses misses in a row, then once every WaitSpinProbe-th time, and a hit resets it" (sys: &System) {
    Assert(WsRuleCases() == 0)
}
