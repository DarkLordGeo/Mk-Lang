// Why: the semaphore's sleepers word (std/sys/linux/futex.mk) lets SemPost
// skip FUTEX_WAKE when no thread announced a wait; a race there loses a wake.
// What: RT-Os-Sem-Ring and RT-Os-Sem-Fan, over pool jobs (S9RingJob).
// How: each job runs on its own pool thread; a lost wake shows as a 3 s
// timeout. On Windows the blocks check the same contract over the system semaphore.

import std.core
import std.raw
import std.sys

// S9Ring: four semaphores in a ring and the failure count. Job k waits on
// s[k] and posts s[(k + 1) % 4]; one token goes round, so at nearly every
// hand-off the receiver has announced itself or is about to.
struct S9Ring {
    s: [4]sys.Sem
    rounds: i64
    bad: raw.Atomic[u32]
}

S9RingJob(op: *sys.Op) {
    unsafe "op.buf is the block's S9Ring, alive until every job returned" {
        r := raw.PtrCast[u8, S9Ring]((*op).buf)
        k := Trunc[int]((*op).pos)
        next := (k + 1) % 4
        if k == 0 {
            sys.SemPost(&(*r).s[next], 1)
        }
        for _ in 0..(*r).rounds {
            if !sys.SemWait(&(*r).s[k], 3_000_000_000) {
                // Edge: none; read by the block after every job returned.
                _ = (*r).bad.Add(1)
                break
            }
            sys.SemPost(&(*r).s[next], 1)
        }
        (*op).res = sys.OsResult(value: 1, err: core.SysErr.None, os: 0)
    }
}

// S9Fan: one poster, three waiters on one semaphore. Each round the poster
// posts k counts (1, 2 or 3) and must get k acknowledgements within 3 s:
// a post that wakes too few of the sleepers fails it.
struct S9Fan {
    work: sys.Sem
    ack: sys.Sem
    rounds: i64
    done: raw.Atomic[u32]
    bad: raw.Atomic[u32]
    taken: raw.Atomic[u32]
}

S9FanJob(op: *sys.Op) {
    unsafe "op.buf is the block's S9Fan, alive until every job returned" {
        f := raw.PtrCast[u8, S9Fan]((*op).buf)
        if (*op).pos == 0 {
            var r: i64 = 0
            for r < (*f).rounds {
                k := Trunc[u32](r % 3) + 1
                sys.SemPost(&(*f).work, k)
                for _ in 0..k {
                    if !sys.SemWait(&(*f).ack, 3_000_000_000) {
                        // Edge: none; read by the block after every job returned.
                        _ = (*f).bad.Add(1)
                    }
                }
                r += 1
            }
            // Edge: the Store precedes the SemPost's Cas, and each waiter
            // loads done after its SemWait took one of these three counts.
            (*f).done.Store(1)
            sys.SemPost(&(*f).work, 3)
        } else {
            for {
                if !sys.SemWait(&(*f).work, 3_000_000_000) {
                    // A waiter may sit idle while the others take the work;
                    // only the poster's acknowledgements judge a lost wake.
                    continue
                }
                // Edge: SemWait's Cas follows the poster's Store (above).
                if (*f).done.Load() == 1 {
                    break
                }
                // Edge: none; a count read by the block after the jobs.
                _ = (*f).taken.Add(1)
                sys.SemPost(&(*f).ack, 1)
            }
        }
        (*op).res = sys.OsResult(value: 1, err: core.SysErr.None, os: 0)
    }
}

S9Side(io: Io, job: fn(*sys.Op), at: uint, pos: i64, ok: &raw.Atomic[u32]) {
    var op := sys.NewOp(0, 0)
    op.pos = pos
    unsafe "op is in this frame until Blocking returns; at outlives the scope" {
        op.buf = raw.FromAddr[u8](at)
        _ = Blocking(job, raw.OfMut(mut op), false)
    }
    if op.res.value == 1 {
        // Edge: none; read after the scope joined this task.
        _ = ok.Add(1)
    }
}

test "RT-Os-Sem-Ring a token passed round four pool threads 20,000 times never waits out a lost wake" (sys: &System) {
    var ring := S9Ring(s: [PNewSem(), PNewSem(), PNewSem(), PNewSem()], rounds: 5_000, bad: raw.Atomic[u32].New(0))
    ok := raw.Atomic[u32].New(0)
    var at: uint = 0
    unsafe "ring lives in this frame until the scope ends" {
        at = raw.Addr(raw.Of(&ring))
    }
    scope s := sys.io.Scope() {
        for k in 0..4 {
            pos := i64(k)
            s.go S9Side(sys.io, S9RingJob, at, pos, &ok)
        }
    }
    Assert(ok.Load() == 4)
    Assert(ring.bad.Load() == 0)
}

test "RT-Os-Sem-Fan posts of 1, 2 and 3 counts wake that many of three sleepers, 3,000 rounds" (sys: &System) {
    var fan := S9Fan(work: PNewSem(), ack: PNewSem(), rounds: 3_000, done: raw.Atomic[u32].New(0), bad: raw.Atomic[u32].New(0), taken: raw.Atomic[u32].New(0))
    ok := raw.Atomic[u32].New(0)
    var at: uint = 0
    unsafe "fan lives in this frame until the scope ends" {
        at = raw.Addr(raw.Of(&fan))
    }
    scope s := sys.io.Scope() {
        for k in 0..4 {
            pos := i64(k)
            s.go S9Side(sys.io, S9FanJob, at, pos, &ok)
        }
    }
    Assert(ok.Load() == 4)
    Assert(fan.bad.Load() == 0)
    // 1 + 2 + 3 per three rounds.
    Assert(fan.taken.Load() == 6_000)
}
