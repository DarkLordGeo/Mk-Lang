// Why: a name lookup must end at its task's cancel, at once, and must not
// hold a thread while the system resolver waits on the network.
//
// What: OpResolve on the completion port: the submit, the completion
// routine, the cancel and its tail, and the test probes ResolveProbe,
// ResolveCancelProbe and ResolveRaceProbe.
//
// How: a lookup is GetAddrInfoExW in its asynchronous form with a completion
// routine (Windows 8 and later), so a waiting lookup holds no thread of
// this process and a cancel ends it at once through GetAddrInfoExCancel.
// GetAddrInfoW on the pool would hold a pool thread, and the task, until
// the system resolver gave up (11.1 s for an .invalid name, measured on a
// Windows 11 development machine). The shape is .NET's (System.Net.NameResolutionPal.Windows: GetAddrInfoExW with a
// completion callback, GetAddrInfoExCancel from the cancellation).
//
// Measured on the same machine (a Go probe): a numeric name answers at
// once (0) and the routine is never called; every other answer, success or
// error, comes through the routine on a system thread; GetAddrInfoExCancel
// runs the routine itself, with 10111 (WSA_E_CANCELLED), on the cancelling
// thread before it returns, 0 to 0.5 ms after the call.
//
// The os area: the OVERLAPPED at 0 (the OS's until the routine runs), the
// lookup's cancel handle at 40, its answer list at 48, the state word at 64
// (OsProcState: 0 idle, 1 armed, 2 ended, 4 a cancel is inside
// GetAddrInfoExCancel, 5 the routine ran while a cancel was inside it, 6 the
// one cancel was issued and the routine has not run: armed for the routine,
// final for every later cancel), the routine's error at 68, and at 80 the
// page that holds the wide name and the hints for the lookup's whole life
// (nothing documents that GetAddrInfoExW copies them). Offsets 32 (OsBack) and 56 (the accept handoff word, 0 here) are
// the dequeuer's.

import std.core
import std.raw

const OsResolveCancel: int = 40
const OsResolveList: int = 48
const OsResolveErr: int = 68
const OsResolvePage: int = 80
const ResolveCancelling: u32 = 4
const ResolveDoneCancelling: u32 = 5
const ResolveCancelIssued: u32 = 6
const WsaECancelled: u32 = 10111
const NameNamespaceAll: u32 = 0

// SubmitResolve starts the lookup of op.path into the op.len SockAddr
// records at op.buf. false: it ended at once (a numeric name, or an error
// before any wait), op.res set and no routine call follows; true: the
// routine posts op's one packet.
SubmitResolve(op: *Op, o: *u8): bool {
    unsafe "ws2_32's GetAddrInfoExW; op is the caller's pending operation" {
        if !WinsockReady() {
            (*op).res = FailWith(core.SysErr.Other)
            return false
        }
        pg := Reserve(4096, 4096)
        if raw.IsNull(pg) {
            (*op).res = FailWith(core.SysErr.Other)
            return false
        }
        if !Commit(pg, 4096) {
            Release(pg, 4096)
            (*op).res = FailWith(core.SysErr.Other)
            return false
        }
        name := raw.PtrCast[u8, u16](pg)
        if (*op).path.Len() == 0 || Wide((*op).path, raw.SliceMut(name, 512)) < 0 {
            Release(pg, 4096)
            (*op).res = FailWith(core.SysErr.InvalidAddress)
            return false
        }
        // ADDRINFOEXW (72 bytes on x64): ai_socktype at 8, ai_protocol at
        // 12, the rest 0 (one answer per address, as ResolveAll asks).
        hints := raw.Offset(pg, 2048)
        Put32(hints, 8, Trunc[u32](i64(SockStream)))
        Put32(hints, 12, Trunc[u32](i64(IpProtoTcp)))
        Put64(o, OsResolvePage, u64(raw.Addr(pg)))
        // Armed before the call: the routine may run before it returns.
        // Edge: SC with the routine's and the cancel's Cas.
        (*ProcState(o)).Store(ProcArmed)
        get := raw.Read(raw.PtrCast[u64, fn(*u16, *u16, u32, *u8, *u8, *u8, *u8, *u8, fn(u32, u32, *u8), *u8): i32](
            WsFn(WsGetAddrInfoEx)))
        e := Trunc[u32](i64(get(name, raw.Null[u16](), NameNamespaceAll, raw.Null[u8](), hints,
            raw.Offset(o, OsResolveList), raw.Null[u8](), o, ResolveDone, raw.Offset(o, OsResolveCancel))))
        if e == WsaIoPending {
            // From here op is the routine's and the dequeuer's: o is not
            // touched again on this path.
            return true
        }
        (*ProcState(o)).Store(0)
        Put32(o, OsResolveErr, e)
        ResolveFinish(op, o)
        return false
    }
}

// ResolveDone is the lookup's completion routine. It runs on a system
// thread, or on a cancelling thread inside GetAddrInfoExCancel (atomics and
// one system call only): ResolveDoneWith with the port's post.
ResolveDone(err: u32, bytes: u32, o: *u8) {
    ResolveDoneWith(err, o, ResolvePost)
}

// ResolvePost posts the lookup's one packet to the port.
ResolvePost(o: *u8) {
    unsafe "kernel32's PostQueuedCompletionStatus; o is the lookup's os area, the packet's key" {
        _ = PostQueuedCompletionStatus(Port(), 0, 0, o)
    }
}

// ResolveDoneWith is the routine's protocol, its post passed in so a probe
// can count it (ResolveRaceProbe). It records the error, then: from armed,
// or from cancel-issued (6: the one cancel call has returned), it
// takes the word to ended and posts op's one packet; from cancelling (a
// cancel is inside GetAddrInfoExCancel, on this thread or another) it takes
// the word to done-while-cancelling and returns without posting, and the
// canceller posts once its call has returned (ResolveCancelTail). A failed
// Cas means the canceller moved the word between the Load and the Cas
// (cancelling to cancel-issued, or armed to cancelling), so the loop reads
// it again: returning there would lose the packet and hang the task (the
// single-pass mutant ResolveRaceProbe catches). So the routine never waits
// for the canceller: spinning (SwitchToThread) until another thread's
// GetAddrInfoExCancel returns would deadlock if that call waits for a
// routine already running (an answer arriving as the cancel lands), and at
// 1 carrier the stuck call is the only carrier. The os area outlives both:
// the task is parked until the packet, which comes after the routine has stored the error and, on the
// cancel path, after the cancel call returned. Edge: the error is stored
// before the Cas; the poster's Cas or Load reads it with the word, and the
// dequeuer reads it after GetQueuedCompletionStatusEx (the port's
// release/acquire edge).
ResolveDoneWith(err: u32, o: *u8, post: fn(*u8)) {
    unsafe "o is the os area of a pending lookup, alive until its packet is dequeued and the task resumes" {
        Put32(o, OsResolveErr, err)
        st := ProcState(o)
        for {
            s := (*st).Load()
            if s == ProcArmed || s == ResolveCancelIssued {
                // Edge: SC; of this Cas and the canceller's Cas from the same
                // word one wins, so one side posts.
                if (*st).Cas(s, ProcEnded) {
                    post(o)
                    return
                }
            } else if s == ResolveCancelling {
                // Edge: SC; the canceller's Cas from cancelling fails after
                // this one and it posts (it then reads the error stored
                // above, ordered before this Cas).
                if (*st).Cas(ResolveCancelling, ResolveDoneCancelling) {
                    return
                }
            } else {
                // Idle, ended or done: no lookup is pending (never reached:
                // one routine call per pending lookup).
                return
            }
        }
    }
}

// ResolveCancel asks the OS to end a pending lookup; the routine then
// posts its packet with WSA_E_CANCELLED (or the answer, if it came first).
// A cancel before the submission or after the routine finds no armed word
// and does nothing; of two cancels at once (Await's own and a cancel walk's)
// one finds the word cancelling and does nothing, and a cancel after the
// first one's call returned finds cancel-issued and does nothing.
// It runs only while the waiting task keeps op alive (Await's own call, or a
// cancel walk the task waits for: WaitUnpinned), and only after the
// submission returned (CancelWait's Dekker pair, task/scope.mk), so the
// handle word at 40 is the one GetAddrInfoExW wrote.
ResolveCancel(o: *u8) {
    ResolveCancelWith(o, ResolveOsCancel, ResolvePost)
}

// ResolveOsCancel is ws2_32's GetAddrInfoExCancel on the handle at hp.
ResolveOsCancel(hp: *u8) {
    unsafe "ws2_32's GetAddrInfoExCancel on the lookup's live handle word" {
        cancel := raw.Read(raw.PtrCast[u64, fn(*u8): i32](WsFn(WsGetAddrInfoExCancel)))
        _ = cancel(hp)
    }
}

// ResolveCancelWith is ResolveCancel's protocol, its cancel call and post
// passed in so a probe can count them (ResolveCancelProbe). At most one
// cancel call per lookup: only the Cas from armed issues one, and
// after its call the word goes to cancel-issued, never back to armed. A
// second call could not help (the first already asked the OS to end the
// lookup), and it would pass a handle whose lookup may have completed with
// its routine still queued on a system thread: if Winsock reused the value,
// it could cancel another task's lookup.
ResolveCancelWith(o: *u8, cancel: fn(*u8), post: fn(*u8)) {
    st := ProcState(o)
    unsafe "o is the os area of a lookup its task keeps alive" {
        // Edge: SC; while the word is cancelling the routine does not post,
        // so the os area (and the handle word this call reads) lives until
        // the call returns. The OS's own lookup context, which the handle
        // names, may end while the call runs: the routine runs on a system
        // thread at any moment, so no caller can keep a cancel from racing
        // the completion, and Winsock answers a cancel of a lookup that
        // already completed with an error (WSA_INVALID_HANDLE); .NET's
        // NameResolutionPal calls GetAddrInfoExCancel with no coordination
        // with its callback at all. Holding the routine back would not keep
        // the OS's context alive either.
        // Edge: the Cas from armed is the one exit of the armed state: the
        // cancel and the routine race for it, and the loser leaves the request
        // to the winner.
        if !(*st).Cas(ProcArmed, ResolveCancelling) {
            return
        }
        cancel(raw.Offset(o, OsResolveCancel))
    }
    ResolveCancelTail(o, post)
}

// ResolveCancelTail ends a cancel whose call has returned: to cancel-issued
// if the routine has not run (it posts when it does); a routine that ran in
// the meantime (inside the call or on another thread) left the word done
// while cancelling, and its packet is this thread's to post.
ResolveCancelTail(o: *u8, post: fn(*u8)) {
    st := ProcState(o)
    unsafe "o is the os area of a lookup whose word is cancelling: the routine does not post" {
        // Edge: SC; against the routine's Cas from cancelling: one wins.
        if (*st).Cas(ResolveCancelling, ResolveCancelIssued) {
            return
        }
        // Edge: SC; the routine's Cas to done follows its error store.
        if (*st).Cas(ResolveDoneCancelling, ProcEnded) {
            post(o)
        }
    }
}

// ResolveFinish turns an ended lookup into op.res: the addresses of the
// list in order, decoded into op.buf (at most op.len; the rest of a longer
// list is dropped), then the list and the page freed. 11001
// (WSAHOST_NOT_FOUND) and 11004 (WSANO_DATA) are NotFound; a cancelled
// lookup, or one that failed after a cancel, is Canceled.
ResolveFinish(op: *Op, o: *u8) {
    unsafe "the lookup is over: its list and page are this thread's" {
        err := Get32(o, OsResolveErr)
        list := Get64(o, OsResolveList)
        var n := 0
        if err == 0 {
            out := raw.SliceMut(raw.PtrCast[u8, SockAddr]((*op).buf), (*op).len)
            var cur := list
            for cur != 0 && n < out.Len() {
                // ADDRINFOEXW: ai_addr at 32, ai_next at 64.
                ai := raw.FromAddr[u8](Trunc[uint](cur))
                sa := Get64(ai, 32)
                if sa != 0 && DecodeAddr(raw.FromAddr[u8](Trunc[uint](sa)), mut out[n]) {
                    n += 1
                }
                cur = Get64(ai, 64)
            }
        }
        if list != 0 {
            free := raw.Read(raw.PtrCast[u64, fn(u64)](WsFn(WsFreeAddrInfoEx)))
            free(list)
            Put64(o, OsResolveList, 0)
        }
        pg := Get64(o, OsResolvePage)
        if pg != 0 {
            Release(raw.FromAddr[u8](Trunc[uint](pg)), 4096)
            Put64(o, OsResolvePage, 0)
        }
        // Edge: a canceller stores cancel before its GetAddrInfoExCancel.
        (*op).res = if err == 0 {
            Ok(i64(n))
        } else if err == WsaECancelled || (*op).cancel.Load() == 1 {
            FailWith(core.SysErr.Canceled)
        } else if err == 11001 || err == 11004 {
            OsResult(value: 0, err: core.SysErr.NotFound, os: i64(err))
        } else {
            Fail(err)
        }
    }
}

/// ResolveProbe is the routine's side of a lookup whose cancel is inside
/// GetAddrInfoExCancel when the answer comes, with no lookup and no packet
/// (the RoutineNoWait block of task/tests_iocancel.mk). An os area on its
/// own page is set to cancelling, as ResolveCancel leaves it during the
/// call, and ResolveDone runs on a new thread, as on the system's: the
/// routine must return within 2 s and leave the word done-while-cancelling
/// (5) for the canceller. 1: so; 2: it did not return in time (the routine
/// waits for the canceller: a spin there deadlocks when GetAddrInfoExCancel
/// waits for a routine already running); 3: it returned but left another
/// word; 4: no page or no thread. On 2 the word
/// is set idle, so the routine returns without posting, and the page is
/// left to it. The thread's handle (ThreadStart's value) is not closed: a
/// test-only leak of one handle per call, which no block that judges a
/// handle count calls.
/// Tests only. Allocates nothing that outlives the call.
export ResolveProbe(): u32 {
    unsafe "a page of this process; the probe thread reads it until it sets done" {
        pg := Reserve(4096, 4096)
        if raw.IsNull(pg) {
            return 4
        }
        if !Commit(pg, 4096) {
            Release(pg, 4096)
            return 4
        }
        st := ProcState(pg)
        // Edge: SC; the thread's start follows these stores (CreateThread
        // is a full barrier).
        (*st).Store(ResolveCancelling)
        done := raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(pg, 2048))
        (*done).Store(0)
        r := ThreadStart(ResolveProbeEntry, pg, 65536)
        if r.err != core.SysErr.None {
            Release(pg, 4096)
            return 4
        }
        t0 := Now()
        for (*done).Load() == 0 {
            if Now() - t0 > 2_000_000_000 {
                // Edge: SC; the waiting routine's next Load sees idle and
                // returns without a post.
                (*st).Store(0)
                return 2
            }
            ThreadYield()
        }
        // Edge: the done store follows the routine's Cas in its thread.
        s := (*st).Load()
        Release(pg, 4096)
        return if s == ResolveDoneCancelling { 1 } else { 3 }
    }
}

// ResolveProbeEntry runs the routine on the probe's area, then says done.
ResolveProbeEntry(pg: *u8) {
    ResolveDone(WsaECancelled, 0, pg)
    unsafe "the probe's page lives until done is seen" {
        // Edge: SC; the probe reads the state word after it sees this.
        (*raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(pg, 2048))).Store(1)
    }
}

// The probes' page (ResolveCancelProbe, ResolveRaceProbe): the os area's
// words at their offsets, and four counters of their own past it.
const ProbeCancels: int = 2048
const ProbePosts: int = 2052
const ProbeGo: int = 2056
const ProbeDone: int = 2060

ProbeWord(pg: *u8, at: int): *raw.Atomic[u32] {
    unsafe "pg is a probe's page" {
        return raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(pg, at))
    }
}

// A stub GetAddrInfoExCancel: it counts the call and runs no routine.
ProbeCancel(hp: *u8) {
    unsafe "hp is the handle word of a probe's page" {
        // Edge: SC; the probe reads the count after the cancel returned.
        _ = (*ProbeWord(raw.Offset(hp, -OsResolveCancel), ProbeCancels)).Add(1)
    }
}

// A stub post: it counts the packet.
ProbePost(o: *u8) {
    unsafe "o is a probe's page" {
        // Edge: SC; the probe reads the count after the poster said done.
        _ = (*ProbeWord(o, ProbePosts)).Add(1)
    }
}

// ProbePage is a fresh committed page, or null.
ProbePage(): *u8 {
    pg := Reserve(4096, 4096)
    if raw.IsNull(pg) {
        return pg
    }
    if !Commit(pg, 4096) {
        Release(pg, 4096)
        return raw.Null[u8]()
    }
    return pg
}

/// ResolveCancelProbe (task/tests_iocancel.mk, "RT-Resolve-OneCancel"): a
/// lookup whose routine has not run is cancelled twice, as Await's own
/// PollCancel and a cancel walk's may both do, with a stub cancel call that
/// runs no routine; then the routine runs.
/// 1: one cancel call, no post until the routine, the word cancel-issued in
/// between, and the routine's one post leaves it ended; 2: a second cancel
/// call reached the OS; 3: a wrong word or post count; 4: no page.
/// Tests only. Allocates nothing that outlives the call.
export ResolveCancelProbe(): u32 {
    pg := ProbePage()
    if raw.IsNull(pg) {
        return 4
    }
    unsafe "a page of this process, used by this thread only" {
        st := ProcState(pg)
        // Edge: SC; one thread, so any order holds.
        (*st).Store(ProcArmed)
        ResolveCancelWith(pg, ProbeCancel, ProbePost)
        ResolveCancelWith(pg, ProbeCancel, ProbePost)
        var r: u32 = 1
        if (*ProbeWord(pg, ProbeCancels)).Load() != 1 {
            r = 2
        } else if (*st).Load() != ResolveCancelIssued || (*ProbeWord(pg, ProbePosts)).Load() != 0 {
            r = 3
        } else {
            ResolveDoneWith(WsaECancelled, pg, ProbePost)
            // Edge: SC; this thread ran the routine just above, so these Loads
            // read its own stores.
            if (*st).Load() != ProcEnded || (*ProbeWord(pg, ProbePosts)).Load() != 1 {
                r = 3
            }
        }
        Release(pg, 4096)
        return r
    }
}

// ProbeSpin waits until w reads want, or until stop reads its quit value
// (any value at least 0xFFFF_FF00 there); false on quit. It loads, then
// yields the thread now and then, so two probe threads on one CPU progress.
ProbeSpin(w: *raw.Atomic[u32], want: u32, stop: *raw.Atomic[u32]): bool {
    unsafe "w and stop are words of a live probe page" {
        var k := 0
        for {
            // Edge: SC; each wait reads the other thread's last store.
            if (*w).Load() == want {
                return true
            }
            if (*stop).Load() >= 0xFFFF_FF00 {
                return false
            }
            k += 1
            if k % 256 == 0 {
                ThreadYield()
            }
        }
    }
}

// ProbeDelay spins n loads of w, to move the two threads' steps against
// each other from round to round.
ProbeDelay(w: *raw.Atomic[u32], n: int) {
    unsafe "w is a word of a live probe page" {
        for _ in 0..n {
            // Edge: none; a delay loop.
            _ = (*w).Load()
        }
    }
}

/// ResolveRaceProbe (task/tests_iocancel.mk, "RT-Resolve-RoutineRace"):
/// rounds runs of the interleaving where the routine runs on a system
/// thread while the canceller's call returns.
/// Each round the word is cancelling (a cancel is inside its call); a second
/// thread runs the routine (ResolveDoneWith) while this one runs the cancel's
/// tail (ResolveCancelTail), each after a delay that varies by round, with a
/// stub post. Whichever Cas wins, exactly one post comes and the word ends
/// ended: from cancelling, the routine's Cas to 5 and the tail's Cas to 6
/// race, and the routine that loses must read the word again and post from
/// 6. 1: so in every round; 2: a round with no post or two; 3: the word not
/// ended; 4: no page or no thread; 5: the routine thread did not finish a
/// round within 10 s (its page is then left to it).
/// Tests only. O(rounds); allocates nothing that outlives the call.
export ResolveRaceProbe(rounds: int): u32 {
    pg := ProbePage()
    if raw.IsNull(pg) {
        return 4
    }
    st := ProcState(pg)
    gw := ProbeWord(pg, ProbeGo)
    dw := ProbeWord(pg, ProbeDone)
    pw := ProbeWord(pg, ProbePosts)
    unsafe "a page of this process; the routine thread uses it until it stores done 0xFFFF_FFFF" {
        // Edge: SC; the thread's start follows these stores (CreateThread
        // is a full barrier).
        (*gw).Store(0)
        (*dw).Store(0)
        r := ThreadStart(ProbeRoutineEntry, pg, 65536)
        if r.err != core.SysErr.None {
            Release(pg, 4096)
            return 4
        }
        var res: u32 = 1
        for i in 0..rounds {
            round := Trunc[u32](i64(i + 1))
            // Edge: SC; the word is set before go, which the thread reads
            // before it runs the routine.
            (*st).Store(ResolveCancelling)
            (*gw).Store(round)
            ProbeDelay(gw, (i * 13) % 97)
            ResolveCancelTail(pg, ProbePost)
            t0 := Now()
            for (*dw).Load() != round {
                if Now() - t0 > 10_000_000_000 {
                    // Edge: SC; the quit value, which the routine thread's
                    // ProbeSpin reads.
                    (*gw).Store(0xFFFF_FFFF)
                    return 5
                }
                ThreadYield()
            }
            // Edge: SC; the thread's post and Cas precede its done store.
            if (*pw).Load() != round {
                res = 2
                break
            }
            if (*st).Load() != ProcEnded {
                res = 3
                break
            }
        }
        // Edge: SC; the thread leaves its loop on the quit value and says so.
        (*gw).Store(0xFFFF_FFFF)
        t1 := Now()
        for (*dw).Load() != 0xFFFF_FFFF {
            if Now() - t1 > 10_000_000_000 {
                return 5
            }
            ThreadYield()
        }
        Release(pg, 4096)
        return res
    }
}

// ProbeRoutineEntry is the routine's thread of ResolveRaceProbe: each round
// it waits for go, runs the routine after its delay, and says done.
ProbeRoutineEntry(pg: *u8) {
    gw := ProbeWord(pg, ProbeGo)
    dw := ProbeWord(pg, ProbeDone)
    var round: u32 = 1
    var i := 0
    unsafe "the probe's page lives until done reads 0xFFFF_FFFF" {
        for ProbeSpin(gw, round, gw) {
            ProbeDelay(gw, i % 7)
            ResolveDoneWith(WsaECancelled, pg, ProbePost)
            // Edge: SC; the probe reads the post count and the word after
            // this.
            (*dw).Store(round)
            round += 1
            i += 1
        }
        // Edge: SC; the probe releases the page after it reads this.
        (*dw).Store(0xFFFF_FFFF)
    }
}
