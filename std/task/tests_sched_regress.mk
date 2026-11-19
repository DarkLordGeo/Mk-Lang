// Why: each block pins a scheduler bug that was found and fixed, so that it
// cannot come back.
// What: blocks for deep scope chains, O(1) cancellation checks, timer and
// reap wakes, slot and pool waits, release caps, wait buckets, channels and
// the scope exit order, each named after the rule it guards.
// How: controlled schedules (IdleGate, ReapGate, pool hooks) and timing rules
// on the fastest of several attempts; run by the scheduler harness and by mk
// test. Blocks whose names start with a stressed prefix (RT-Cancel-, RT-MN-,
// RT-Chan-, RT-R17-) also run under TestRtSched_Stress's jitter seeds.

import std.core
import std.raw
import std.rt
import std.sys

// The depth of the deep chains: five times the 20,000 levels at which the
// recursive cancel walk overflowed a Default slot. Every level is its own task, so no task's stack is deep.
// The RT-DeepChain- blocks are not in the stress prefixes. As RT-Cancel-
// blocks they took 144 s of a 263 s TestRtSched_Stress run (16 seeds, 2 to
// 16 carriers, about 0.75 s a run), and a
// chain has little concurrency for the jitter to vary beyond its leaf's
// wait. RT-Cancel-FlagDown is the stressed cancel-walk block.
const DcDepth: int = 100_000

// DcLevels is the chain depth on this target. On Linux every prepared slot
// is two VMAs (its guard and its stack), and the WSL Ubuntu of a development
// machine has vm.max_map_count 65530 (read on 2026-11-05), so
// about 32,000 tasks can live at once: a deeper chain waits for
// a slot for good. 25,000 is still above the 20,000 levels at which
// the recursive walk overflowed.
DcLevels(): int {
    return if Target.os == Os.Linux { 25_000 } else { DcDepth }
}

error DcErr

// DcLevel is one level of a chain: it opens a scope and spawns the next
// level into it. The last level counts itself built and sleeps 60 s
// cancellably, storing 1 in leaf when the sleep returned Canceled.
DcLevel(io: Io, left: int, leaf: &raw.Atomic[u32], built: &raw.Atomic[u32]) {
    if left == 0 {
        built.Store(1)
        io.Sleep(core.DurationOf(60_000_000_000)) else {
            leaf.Store(1)
            return
        }
        return
    }
    scope s := io.Scope() {
        s.go DcLevel(io, left - 1, leaf, built)
    }
}

// DcAwait waits, by sleeping, until built is set (the chain's leaf exists).
DcAwait(io: Io, built: &raw.Atomic[u32]) {
    for built.Load() == 0 {
        io.NoCancel().Sleep(core.DurationOf(1_000_000)) else {
        }
    }
}

// DcRoot builds a chain of DcDepth scopes under its own scope and cancels
// it with Scope.Cancel, on this task's Default-slot stack (not Main's
// Large one). took is the time from the cancel to the end of the join.
DcRoot(io: Io, leaf: &raw.Atomic[u32], took: &raw.Atomic[u64]) {
    built := raw.Atomic[u32].New(0)
    var t0: i64 = 0
    scope root := io.Scope() {
        root.go DcLevel(io, DcLevels(), leaf, &built)
        DcAwait(io, &built)
        t0 = BenchNow()
        root.Cancel()
    }
    took.Store(Trunc[u64](BenchNow() - t0))
}

test "RT-DeepChain-Cancel a cancel walks 100,000 nested scopes without recursion" (sys: &System) {
    leaf := raw.Atomic[u32].New(0)
    took := raw.Atomic[u64].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.go DcRoot(sys.io, &leaf, &took)
    }
    Assert(leaf.Load() == 1)
    Assert(took.Load() < 5_000_000_000)
    Assert(BenchNow() - t0 < 50_000_000_000)
}

// DcFailer waits until the chain is built, then fails: its scope's Fail
// is the call that walks the chain, on this task's stack.
DcFailer(io: Io, built: &raw.Atomic[u32], t0: &raw.Atomic[u64]): DcErr!void {
    DcAwait(io, built)
    t0.Store(Trunc[u64](BenchNow()))
    fail DcErr
}

// DcLevelE is the chain's first level as a task of a Scope[DcErr].
DcLevelE(io: Io, leaf: &raw.Atomic[u32], built: &raw.Atomic[u32]): DcErr!void {
    DcLevel(io, DcLevels(), leaf, built)
}

// DcFailRoot is DcRoot with the cancel made by a failing task (Fail).
DcFailRoot(io: Io, leaf: &raw.Atomic[u32], t0: &raw.Atomic[u64]): DcErr!void {
    built := raw.Atomic[u32].New(0)
    try scope root := io.ScopeOf[DcErr]() {
        root.go DcLevelE(io, leaf, &built)
        root.go DcFailer(io, &built, t0)
    }
}

DcFailTask(io: Io, leaf: &raw.Atomic[u32], took: &raw.Atomic[u64], failed: &raw.Atomic[u32]) {
    t0 := raw.Atomic[u64].New(0)
    DcFailRoot(io, leaf, &t0) else {
        failed.Store(1)
    }
    took.Store(Trunc[u64](BenchNow()) - t0.Load())
}

test "RT-DeepChain-Fail a failing task's Fail walks 100,000 nested scopes" (sys: &System) {
    leaf := raw.Atomic[u32].New(0)
    took := raw.Atomic[u64].New(0)
    failed := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go DcFailTask(sys.io, &leaf, &took, &failed)
    }
    Assert(failed.Load() == 1)
    Assert(leaf.Load() == 1)
    Assert(took.Load() < 5_000_000_000)
}

// DcEarly builds the chain and leaves the scope body early: Join(s, true)
// is the call that walks the chain.
DcEarly(io: Io, leaf: &raw.Atomic[u32], t0: &raw.Atomic[u64]) {
    built := raw.Atomic[u32].New(0)
    scope root := io.Scope() {
        root.go DcLevel(io, DcLevels(), leaf, &built)
        DcAwait(io, &built)
        t0.Store(Trunc[u64](BenchNow()))
        return
    }
}

DcEarlyTask(io: Io, leaf: &raw.Atomic[u32], took: &raw.Atomic[u64]) {
    t0 := raw.Atomic[u64].New(0)
    DcEarly(io, leaf, &t0)
    took.Store(Trunc[u64](BenchNow()) - t0.Load())
}

test "RT-DeepChain-Join an early exit's Join(s, true) walks 100,000 nested scopes" (sys: &System) {
    leaf := raw.Atomic[u32].New(0)
    took := raw.Atomic[u64].New(0)
    scope s := sys.io.Scope() {
        s.go DcEarlyTask(sys.io, &leaf, &took)
    }
    Assert(leaf.Load() == 1)
    Assert(took.Load() < 5_000_000_000)
}

// ---- The cancellation check is O(1) ----

// DbLevel is a chain level whose leaf returns at once.
DbLevel(io: Io, left: int) {
    if left == 0 {
        return
    }
    scope s := io.Scope() {
        s.go DbLevel(io, left - 1)
    }
}

// DbChain builds and joins a chain of n levels and returns the time.
DbChain(io: Io, n: int): i64 {
    t0 := BenchNow()
    scope s := io.Scope() {
        s.go DbLevel(io, n)
    }
    return BenchNow() - t0
}

// DbLinear is the deep-chain rule: the time per level at 4k levels is
// within 3x of that at k (a quadratic build is 4x), with 5 us of slack.
DbLinear(k: int, a: i64, c: i64): bool {
    return c / Trunc[i64](4 * k) <= 3 * (a / Trunc[i64](k)) + 5_000
}

// BenchMin is the smaller time, the first when there is none yet (0).
BenchMin(have: i64, t: i64): i64 {
    return if have == 0 || t < have { t } else { have }
}

test "RT-Bench-DeepChain building and joining chains of 10,000, 20,000 and 40,000 nested scopes is linear" (sys: &System) {
    // An Enter that walks its whole parent chain takes 142 ms, 365 ms,
    // 1,259 ms and 4,663 ms at 5k to 40k levels (once measured
    // numbers, release, 16 carriers), about 3.5 times per doubling.
    // On Linux a quarter of the sizes (DcLevels: vm.max_map_count).
    // Each size keeps its fastest of up to 5 attempts, and the rule is
    // checked after each: preemption only adds time, so a loaded machine
    // (9 fuzzers on 16 threads) stops failing a
    // linear build, while a quadratic one fails every attempt.
    k := if Target.os == Os.Linux { 2_500 } else { 10_000 }
    var a: i64 = 0
    var b: i64 = 0
    var c: i64 = 0
    var tries := 0
    for tries < 5 {
        a = BenchMin(a, DbChain(sys.io, k))
        b = BenchMin(b, DbChain(sys.io, 2 * k))
        c = BenchMin(c, DbChain(sys.io, 4 * k))
        tries += 1
        if DbLinear(k, a, c) {
            break
        }
    }
    var out := sys.Stdout()
    BenchLine(mut out, "deepchain-10k-level", k, a)
    BenchLine(mut out, "deepchain-20k-level", 2 * k, b)
    BenchLine(mut out, "deepchain-40k-level", 4 * k, c)
    out.Flush() else {
        return
    }
    Assert(DbLinear(k, a, c))
}

// DsLevel runs n zero sleeps (each a cancellation check) in a task
// nested left scopes deep and stores the time in out.
DsLevel(io: Io, left: int, n: int, out: &raw.Atomic[u64]) {
    if left == 0 {
        t0 := BenchNow()
        for _ in 0..n {
            io.Sleep(core.DurationOf(0)) else {
                return
            }
        }
        out.Store(Trunc[u64](BenchNow() - t0))
        return
    }
    scope s := io.Scope() {
        s.go DsLevel(io, left - 1, n, out)
    }
}

DsAt(io: Io, depth: int, n: int): i64 {
    out := raw.Atomic[u64].New(0)
    DsLevel(io, depth, n, &out)
    return Trunc[i64](out.Load())
}

test "RT-Bench-SleepDepth 200,000 zero sleeps at depth 1 and at depth 1,000 cost the same" (sys: &System) {
    // The chain walk made each check 1,000 dependent loads, each in another
    // task's slot; one load now. Within 2x plus 100 ns per sleep, on the
    // fastest of up to 5 attempts per depth (as RT-Bench-DeepChain).
    var a: i64 = 0
    var b: i64 = 0
    var tries := 0
    for tries < 5 {
        a = BenchMin(a, DsAt(sys.io, 1, 200_000))
        b = BenchMin(b, DsAt(sys.io, 1_000, 200_000))
        tries += 1
        if b / 200_000 <= 2 * (a / 200_000) + 100 {
            break
        }
    }
    var out := sys.Stdout()
    BenchLine(mut out, "sleep0-depth1", 200_000, a)
    BenchLine(mut out, "sleep0-depth1000", 200_000, b)
    out.Flush() else {
        return
    }
    Assert(b / 200_000 <= 2 * (a / 200_000) + 100)
}

// RfEnter enters nested scopes under an outer scope that is cancelled
// while it runs; once the outer Cancel has returned (done = 1), a
// cancellation point two scopes down must see it: Enter copies the
// parent's flag (RT-Cancel-FlagDown).
RfEnter(io: Io, done: &raw.Atomic[u32], bad: &raw.Atomic[u32], rounds: &raw.Atomic[u32]) {
    for _ in 0..100_000 {
        var stop := false
        scope n1 := io.Scope() {
            _ = RecOf(&n1)
            scope n2 := io.Scope() {
                _ = RecOf(&n2)
                seen := done.Load()
                var canceled := false
                io.Sleep(core.DurationOf(0)) else {
                    canceled = true
                }
                if seen == 1 && !canceled {
                    _ = bad.Add(1)
                }
                if seen == 1 {
                    stop = true
                }
            }
        }
        if stop {
            return
        }
        _ = rounds.Add(1)
    }
}

test "RT-Cancel-FlagDown scopes entered while a cancel runs, and after it, are cancelled" (sys: &System) {
    for _ in 0..20 {
        done := raw.Atomic[u32].New(0)
        bad := raw.Atomic[u32].New(0)
        rounds := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            for _ in 0..4 {
                s.go RfEnter(sys.io, &done, &bad, &rounds)
            }
            t0 := BenchNow()
            for rounds.Load() < 8 && BenchNow() - t0 < 1_000_000_000 {
                YieldNow()
            }
            s.Cancel()
            done.Store(1)
        }
        Assert(bad.Load() == 0)
    }
}

// ---- A timer expiry wakes one carrier ----

test "RT-MN-TimerNoSpuriousWake a lone sleeper's timers wake no carrier that waits without a timeout" (sys: &System) {
    var n: u32 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        n = (*TS()).want.Load()
    }
    if n < 2 {
        return
    }
    // Start every carrier, then let them go idle (no timer: no timeout).
    scope s := sys.io.Scope() {
        for _ in 0..n {
            s.go MnSpin(sys.io, 2_000_000)
        }
    }
    sys.io.Sleep(core.DurationOf(20_000_000)) else {
        Assert(false)
    }
    var w0: u64 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        w0 = (*TS()).idle_unarmed.Load()
    }
    for _ in 0..200 {
        sys.io.Sleep(core.DurationOf(200_000)) else {
            Assert(false)
        }
    }
    var w1: u64 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        w1 = (*TS()).idle_unarmed.Load()
    }
    // A sleep that woke a carrier waiting without a timeout twice
    // (SleepFor's kick and DisarmIdle's hand-off) gives about 400 here.
    Assert(w1 - w0 <= 40)
}

// ---- Reap's clear of waking loses no wake ----

struct RcState {
    arrived: raw.Atomic[u32]
    n: u32
    done: raw.Atomic[u32]
    proceed: raw.Atomic[u32]
    cell: raw.Atomic[u64]
    word: raw.Atomic[u32]
    where: raw.Atomic[u32]
    ran: raw.Atomic[u32]
    late: raw.Atomic[u32]
    built: raw.Atomic[u32]
}

// RcIn is the barrier: true when all n role tasks run at once (each on its
// own carrier) within 500 ms.
RcIn(st: &RcState): bool {
    _ = st.arrived.Add(1)
    t0 := BenchNow()
    for st.arrived.Load() < st.n && BenchNow() - t0 < 500_000_000 {
    }
    return st.arrived.Load() >= st.n
}

// RcHold keeps its carrier busy (never suspending) until done, at most 3 s.
RcHold(st: &RcState) {
    t0 := BenchNow()
    for st.done.Load() == 0 && BenchNow() - t0 < 3_000_000_000 {
    }
}

// RcReaper is carrier B: with every carrier busy it sets waking (a wake in
// flight, as after a PollWake some poll consumed) and polls; its Reap holds
// at ReapGate (armed for B's carrier only) just before it clears waking.
// The gates open themselves after 2 s (GateWait), so a schedule that is
// not built costs a round, never a hang.
RcReaper(io: Io, st: &RcState) {
    if RcIn(st) {
        unsafe "the TaskState page; this task's carrier" {
            (*TS()).waking.Store(1)
            ReapGateWho.Store((*Here()).index + 1)
        }
        ReapGate.Store(1)
        RReapNow()
        _ = ReapGate.Cas(1, 0)
    }
    RcHold(st)
}

// RcIdler ends when the readier says so (proceed), so its carrier goes idle
// and holds at IdleGate, which the readier armed.
RcIdler(io: Io, st: &RcState) {
    if RcIn(st) {
        t0 := BenchNow()
        for st.proceed.Load() == 0 && BenchNow() - t0 < 1_000_000_000 {
        }
    }
}

RcHolder(io: Io, st: &RcState) {
    _ = RcIn(st)
    RcHold(st)
}

// RcReadier is carrier W: once B holds at ReapGate it arms IdleGate and
// lets the idler end; once that carrier holds at IdleGate (counted idle,
// queues rechecked) it readies X into its own runnext (WakeOne's
// compare-and-swap fails on the stale waking), opens IdleGate (the idle
// carrier waits in PollWait with no timeout), opens ReapGate (B clears
// waking), and computes for up to 300 ms: only B's re-read of nidle
// wakes a carrier to take X. It alone arms and resets IdleGate,
// so no armed gate outlives the round.
RcReadier(io: Io, st: &RcState) {
    if RcIn(st) {
        t0 := BenchNow()
        for ReapGate.Load() != 2 && BenchNow() - t0 < 1_000_000_000 {
        }
        if ReapGate.Load() == 2 {
            IdleGate.Store(1)
            st.proceed.Store(1)
            t1 := BenchNow()
            for IdleGate.Load() != 2 && BenchNow() - t1 < 1_000_000_000 {
            }
            if IdleGate.Load() == 2 && ReapGate.Load() == 2 {
                unsafe "X parked on word before the roles started; this task's carrier" {
                    c := Here()
                    if Completes(raw.Of(&st.word)) {
                        if !ReadyPut(c, TaskAt(st.cell.Load())) {
                            WakeOne()
                            if (*TS()).waking.Load() == 1 {
                                st.built.Store(1)
                            }
                        } else {
                            WakeOne()
                        }
                    }
                }
            }
            IdleGate.Store(3)
            t2 := BenchNow()
            for BenchNow() - t2 < 1_000_000 {
            }
            ReapGate.Store(3)
            t3 := BenchNow()
            for st.ran.Load() == 0 && BenchNow() - t3 < 300_000_000 {
            }
            if st.built.Load() == 1 && st.ran.Load() == 0 {
                st.late.Store(1)
            }
        }
    }
    st.proceed.Store(1)
    IdleGate.Store(0)
    ReapGate.Store(0)
    // X is readied in every round: when the schedule was not built, it
    // is still parked on its word (Completes is true only once, so a
    // round that readied it above does nothing here).
    unsafe "X parked on word before the roles started" {
        if Completes(raw.Of(&st.word)) {
            Ready(TaskAt(st.cell.Load()))
        }
    }
    st.done.Store(1)
}

test "RT-MN-ReapClear a carrier that goes idle while a busy poll clears waking is still woken" (sys: &System) {
    var n: u32 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        n = (*TS()).want.Load()
    }
    if n < 3 {
        return
    }
    // At most 10 rounds or 5 s of them (mk test runs blocks in parallel,
    // and a round that cannot get every carrier costs up to about 5 s).
    var hits := 0
    var round := 0
    tstart := BenchNow()
    for hits < 3 && round < 10 && BenchNow() - tstart < 5_000_000_000 {
        round += 1
        var st := RcState(arrived: raw.Atomic[u32].New(0), n: n, done: raw.Atomic[u32].New(0),
            proceed: raw.Atomic[u32].New(0), cell: raw.Atomic[u64].New(0), word: raw.Atomic[u32].New(Pending),
            where: raw.Atomic[u32].New(0), ran: raw.Atomic[u32].New(0), late: raw.Atomic[u32].New(0),
            built: raw.Atomic[u32].New(0))
        scope s := sys.io.Scope() {
            s.go MnNextTask(sys.io, &st.cell, &st.word, &st.where, &st.ran)
            t0 := BenchNow()
            for st.word.Load() != Parked && BenchNow() - t0 < 1_000_000_000 {
                YieldNow()
            }
            s.go RcReaper(sys.io, &st)
            s.go RcReadier(sys.io, &st)
            s.go RcIdler(sys.io, &st)
            for _ in 3..n {
                s.go RcHolder(sys.io, &st)
            }
        }
        if st.built.Load() == 1 {
            hits += 1
            Assert(st.late.Load() == 0)
        }
    }
    // The schedule needs every carrier running a role at once; up to 8
    // carriers it must be built at least once, or the block tests nothing.
    // Above that (16 carriers on a 16-CPU machine shared with other work,
    // as under mk test) a round may never get every carrier, and only
    // built rounds are checked.
    if n <= 8 {
        Assert(hits > 0)
    }
}

// ---- A task end wakes one waiting spawn ----

R4Nap(io: Io) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
}

R4Spawner(io: Io) {
    scope s := io.Scope() {
        for _ in 0..200 {
            s.go R4Nap(io)
        }
    }
}

test "RT-Slot-WakeOne a task end wakes one spawn waiting for a slot, not all of them [env MK_TEST_SLOTS=64]" (sys: &System) {
    var w0: u64 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        w0 = (*TS()).slot_waits.Load()
    }
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go R4Spawner(sys.io)
        }
    }
    var w: u64 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        w = (*TS()).slot_waits.Load() - w0
    }
    // Dev: 64 slots for 16 spawners and 3,200 naps, so spawns wait; a
    // wake-all at every end would wake every waiting spawner (about 16), so
    // the bound below catches it.
    // Release ignores MK_TEST_SLOTS: nothing waits.
    if rt.UsableSlots(rt.ClassDefault) == 64 {
        Assert(w > 0)
        Assert(w <= 2 * 3_200 + 64)
    } else {
        Assert(w == 0)
    }
}

// ---- Release builds ignore MK_TEST_SLOTS and MK_TEST_POOL ----

// R5Meet counts itself in and waits until all n tasks are in.
R5Meet(io: Io, arrived: &raw.Atomic[u32], n: u32) {
    _ = arrived.Add(1)
    WakeWord(WordOf(arrived), true)
    for {
        v := arrived.Load()
        if v >= n {
            return
        }
        _ = WaitWord(WordOf(arrived), v, false)
    }
}

struct R5Pair {
    a: sys.Sem
    b: sys.Sem
}

// R5Job posts the other job's semaphore and waits up to 3 s for its own:
// two of them finish only on two pool threads. pos selects the side.
R5Job(op: *sys.Op) {
    unsafe "op.buf is the block's R5Pair" {
        p := raw.PtrCast[u8, R5Pair]((*op).buf)
        var ok := false
        if (*op).pos == 0 {
            sys.SemPost(&(*p).b, 1)
            ok = sys.SemWait(&(*p).a, 3_000_000_000)
        } else {
            sys.SemPost(&(*p).a, 1)
            ok = sys.SemWait(&(*p).b, 3_000_000_000)
        }
        (*op).res = sys.OsResult(value: 7, err: core.SysErr.None, os: 0)
        if !ok {
            (*op).res.value = -1
        }
    }
}

R5Side(io: Io, pair: uint, side: i64, got: &raw.Atomic[u32]) {
    var op := sys.NewOp(0, 0)
    op.pos = side
    unsafe "op is in this frame until Blocking returns; pair outlives the scope" {
        op.buf = raw.FromAddr[u8](pair)
        _ = Blocking(R5Job, raw.OfMut(mut op), false)
    }
    if op.res.value == 7 {
        _ = got.Add(1)
    }
}

test "RT-Release-IgnoresTestCaps a release build ignores MK_TEST_SLOTS and MK_TEST_POOL [env MK_TEST_SLOTS=16 MK_TEST_POOL=1]" (sys: &System) {
    var pmax: u32 = 0
    unsafe "the TaskState page; pmax is written once at start" {
        pmax = (*TS()).pmax
    }
    if rt.DevBuild() {
        // The caps are in force outside release.
        Assert(rt.UsableSlots(rt.ClassDefault) == 16)
        Assert(pmax == 1)
        return
    }
    Assert(rt.UsableSlots(rt.ClassDefault) > 16)
    Assert(pmax == MaxPoolThreads)
    // 32 tasks that all wait for each other: 16 slots would hang them.
    arrived := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..32 {
            s.go R5Meet(sys.io, &arrived, 32)
        }
    }
    Assert(arrived.Load() == 32)
    // Two pool jobs that wait for each other: one pool thread would make
    // both time out.
    var pair := R5Pair(a: PNewSem(), b: PNewSem())
    got := raw.Atomic[u32].New(0)
    var at: uint = 0
    unsafe "pair lives in this frame until the scope ends" {
        at = raw.Addr(raw.Of(&pair))
    }
    scope s2 := sys.io.Scope() {
        s2.go R5Side(sys.io, at, 0, &got)
        s2.go R5Side(sys.io, at, 1, &got)
    }
    Assert(got.Load() == 2)
}

// ---- A failed pool-thread start strands no queued job ----

// C3Run runs one quick job through the pool and stores its result + 1.
C3Run(io: Io, out: &raw.Atomic[u32]) {
    r := PRun(PQuick, raw.Null[sys.Sem](), 5, false)
    out.Store(Trunc[u32](r) + 1)
}

// C3Queued is the pool's queued-job count.
C3Queued(): u32 {
    st := TS()
    unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
        rt.Lock(&(*st).plock)
        q := (*st).pqueued
        rt.Unlock(&(*st).plock)
        return q
    }
}

test "RT-Pool-StartFails a failed pool-thread start runs every queued job, not only its own" (sys: &System) {
    // Controlled schedule: one pool thread at most (pmax 1, set here, so
    // the block runs in release too), every start fails (the hook), and the
    // first starter holds between its unlock and the start (the gate) while
    // a second task queues a job behind it without starting a thread.
    // A lost start would leave the second job queued with no pool thread,
    // and its task would never resume (the watchdog).
    st := TS()
    var p0: u32 = 0
    var old: u32 = 0
    var want: u32 = 0
    unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
        want = (*st).want.Load()
        rt.Lock(&(*st).plock)
        p0 = (*st).pthreads
        old = (*st).pmax
        (*st).pmax = 1
        rt.Unlock(&(*st).plock)
    }
    // A starter held at the gate holds its carrier: another must run the
    // second task. With a pool thread already started, no start happens.
    if want >= 2 && p0 == 0 {
        PoolHook.fail_start.Store(1)
        PoolHook.gate.Store(1)
        a := raw.Atomic[u32].New(0)
        b := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go C3Run(sys.io, &a)
            t0 := BenchNow()
            for PoolHook.gate.Load() != 2 && BenchNow() - t0 < 2_000_000_000 {
                YieldNow()
            }
            Assert(PoolHook.gate.Load() == 2)
            s.go C3Run(sys.io, &b)
            t1 := BenchNow()
            for C3Queued() < 2 && BenchNow() - t1 < 2_000_000_000 {
                YieldNow()
            }
            Assert(C3Queued() == 2)
            PoolHook.gate.Store(3)
        }
        PoolHook.gate.Store(0)
        PoolHook.fail_start.Store(0)
        Assert(a.Load() == 16 && b.Load() == 16)
    }
    unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
        rt.Lock(&(*st).plock)
        (*st).pmax = old
        rt.Unlock(&(*st).plock)
    }
}

// PiRun runs one cancellable job through the pool and stores how it ended
// in out: 1 Canceled, 2 the job ran (PQuick's 15), 3 anything else.
PiRun(io: Io, out: &raw.Atomic[u32]) {
    r := PRun(PQuick, raw.Null[sys.Sem](), 5, true)
    out.Store(match r {
        -2 { 1 }
        15 { 2 }
        _ { 3 }
    })
}

test "RT-Pool-InlineCancel a cancel that lands while a job is handed back to run inline ends it as Canceled, unrun" (sys: &System) {
    // Controlled schedule: one pool thread at most, every start fails, so
    // StartFailed hands the task's own job back (phase JobNew) and Blocking
    // runs it on the task's carrier; the inline gate holds the task there,
    // still on its scope's blocked list, while the scope is cancelled. The
    // cancel can only mark the job (CancelJob), so the inline path must read
    // the mark: if it did not, the job would run and Blocking return None.
    st := TS()
    var p0: u32 = 0
    var old: u32 = 0
    var want: u32 = 0
    unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
        want = (*st).want.Load()
        rt.Lock(&(*st).plock)
        p0 = (*st).pthreads
        old = (*st).pmax
        (*st).pmax = 1
        rt.Unlock(&(*st).plock)
    }
    // The held task holds its carrier: another runs the cancel. With a
    // pool thread already started, no start happens and the path is not
    // reached (mk test runs each block in its own process, where none is).
    if want >= 2 && p0 == 0 {
        PoolHook.fail_start.Store(1)
        PoolHook.inline.Store(1)
        got := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go PiRun(sys.io, &got)
            t0 := BenchNow()
            for PoolHook.inline.Load() != 2 && BenchNow() - t0 < 2_000_000_000 {
                YieldNow()
            }
            Assert(PoolHook.inline.Load() == 2)
            s.Cancel()
            PoolHook.inline.Store(3)
        }
        PoolHook.inline.Store(0)
        PoolHook.fail_start.Store(0)
        Assert(got.Load() == 1)
    }
    unsafe "the TaskState page Start mapped, which lives until exit; its pool fields under plock" {
        rt.Lock(&(*st).plock)
        (*st).pmax = old
        rt.Unlock(&(*st).plock)
    }
}

// ---- A word wait leaves its bucket in O(1) ----

// BwOnce waits once, cancellably, on w and records how it ended in bits
// 2k and 2k + 1 of outs: 1 woken, 2 cancelled.
BwOnce(io: Io, w: &u32, outs: &raw.Atomic[u64], k: int) {
    var e := core.SysErr.None
    unsafe "w is a word of the block's array, alive until the scope ends" {
        e = WaitWord(raw.Of(w), 0, true)
    }
    code: u64 = if e == core.SysErr.Canceled { 2 } else { 1 }
    _ = outs.Add(code << Trunc[u64](2 * k))
}

// BwOut is waiter k's result in outs.
BwOut(outs: &raw.Atomic[u64], k: int): u64 {
    return (outs.Load() >> Trunc[u64](2 * k)) & 3
}

// BwCheck walks the bucket of a and b under its lock: the forward and back
// links agree, tail is the last entry, every entry is queued. It returns
// the number of entries waiting on a and on b, or -1 and -1 when a link is
// wrong.
BwCheck(a: *u32, b: *u32): (na: int, nb: int) {
    bk := BucketOf(a)
    var na := 0
    var nb := 0
    var bad := false
    unsafe "the bucket's list, under its lock" {
        rt.Lock(&(*bk).lock)
        var prev := raw.Null[Task]()
        var t := (*bk).head
        for !raw.IsNull(t) {
            if !raw.Same((*t).wprev, prev) || (*t).wait.state != WaitQueued {
                bad = true
            }
            if raw.Same((*t).wait.addr, a) {
                na += 1
            } else if raw.Same((*t).wait.addr, b) {
                nb += 1
            }
            prev = t
            t = (*t).link
        }
        if !raw.Same((*bk).tail, prev) {
            bad = true
        }
        rt.Unlock(&(*bk).lock)
    }
    return if bad { (na: -1, nb: -1) } else { (na: na, nb: nb) }
}

struct BwWords {
    w: [8192]u32
}

// BwPair finds two words of ws in one bucket: indices i < j.
BwPair(ws: &BwWords): (i: int, j: int) {
    var first: [4096]i32 = [-1; 4096]
    for k in 0..8192 {
        var b := 0
        unsafe "only the address of a live element of ws.w is read; nothing is dereferenced" {
            b = BucketIndex(raw.Addr(raw.Of(&ws.w[k])))
        }
        if first[b] >= 0 {
            return (i: int(first[b]), j: k)
        }
        first[b] = Trunc[i32](k)
    }
    return (i: -1, j: -1)
}

test "RT-Wait-BucketLinks wakes and cancels keep a shared bucket's two-way list whole" (sys: &System) {
    var ws := BwWords(w: [0; 8192])
    (i, j) := BwPair(&ws)
    Assert(i >= 0)
    var a := raw.Null[u32]()
    var b := raw.Null[u32]()
    unsafe "two words of the array, alive for the whole block" {
        a = raw.Of(&ws.w[i])
        b = raw.Of(&ws.w[j])
    }
    outs := raw.Atomic[u64].New(0)
    scope s := sys.io.Scope() {
        // Waiters on a and b alternate in the one bucket: a b a b a b a b.
        for k in 0..8 {
            if k % 2 == 0 {
                s.go BwOnce(sys.io, &ws.w[i], &outs, k)
            } else {
                s.go BwOnce(sys.io, &ws.w[j], &outs, k)
            }
            CAwaitBlocked(&s, k + 1)
        }
        r0 := BwCheck(a, b)
        Assert(r0.na == 4 && r0.nb == 4)
        // Wake one a (the head), then every b (the middle and the tail).
        WakeWord(a, false)
        r1 := BwCheck(a, b)
        Assert(r1.na == 3 && r1.nb == 4)
        WakeWord(b, true)
        r2 := BwCheck(a, b)
        Assert(r2.na == 3 && r2.nb == 0)
        // The cancel removes the last three: the head, the middle, the end.
        s.Cancel()
    }
    r3 := BwCheck(a, b)
    Assert(r3.na == 0 && r3.nb == 0)
    Assert(BwOut(&outs, 0) == 1)
    for k in 1..8 {
        if k % 2 == 0 {
            Assert(BwOut(&outs, k) == 2)
        } else {
            Assert(BwOut(&outs, k) == 1)
        }
    }
}

BwSink(io: Io, w: &raw.Atomic[u32]) {
    _ = WaitWord(WordOf(w), 0, true)
}

// CAwaitBlockedN is CAwaitBlocked with a 20 s bound, for many tasks.
CAwaitBlockedN(s: &Scope, n: int) {
    t0 := BenchNow()
    for CBlocked(RecOf(s)) < n && BenchNow() - t0 < 20_000_000_000 {
        YieldNow()
    }
}

// CwWaiters is n tasks in a cancellable wait on one word, then a cancel;
// it returns the time from the cancel to the end of the join.
CwWaiters(io: Io, n: int): i64 {
    w := raw.Atomic[u32].New(0)
    var t0: i64 = 0
    scope s := io.Scope() {
        for _ in 0..n {
            s.go BwSink(io, &w)
        }
        CAwaitBlockedN(&s, n)
        t0 = BenchNow()
        s.Cancel()
    }
    return BenchNow() - t0
}

CwLinear(a: i64, b: i64): bool {
    return b / 20_000 <= 2 * (a / 5_000) + 2_000
}

test "RT-Bench-CancelWaiters cancelling 5,000 and 20,000 waiters of one word is linear" (sys: &System) {
    // Each size keeps its fastest of up to 5 attempts, stopping at the
    // first where the rule holds (as RT-Bench-DeepChain):
    // under mk test --std the blocks run in parallel in one process, and
    // one loaded attempt failed the rule there (driver
    // TestStdRuntimeTests on Windows); the quadratic bucket fails every
    // attempt.
    var a: i64 = 0
    var b: i64 = 0
    for _ in 0..5 {
        a = BenchMin(a, CwWaiters(sys.io, 5_000))
        b = BenchMin(b, CwWaiters(sys.io, 20_000))
        if CwLinear(a, b) {
            break
        }
    }
    var out := sys.Stdout()
    BenchLine(mut out, "cancel-waiters-5k", 5_000, a)
    BenchLine(mut out, "cancel-waiters-20k", 20_000, b)
    out.Flush() else {
        return
    }
    // The singly linked bucket made this N^2/2 list steps (16x the time
    // for 4x the waiters); linear is 4x. Within 2x per waiter plus 2 us.
    Assert(CwLinear(a, b))
}

// ---- An idle timer costs a searching carrier no lock ----

test "RT-Bench-PingPongSleeper 200,000 round trips of a turn word with one task asleep for 10 s" (sys: &System) {
    turn := raw.Atomic[u32].New(0)
    asleep := raw.Atomic[u32].New(0)
    var ns: i64 = 0
    scope outer := sys.io.Scope() {
        outer.go CSlept(sys.io, &asleep)
        sys.io.Sleep(core.DurationOf(1_000_000)) else {
            Assert(false)
        }
        t0 := BenchNow()
        scope s := sys.io.Scope() {
            s.go MnPing(sys.io, &turn, 0, 200_000)
            s.go MnPing(sys.io, &turn, 1, 200_000)
        }
        ns = BenchNow() - t0
        outer.Cancel()
    }
    Assert(asleep.Load() == 1)
    var out := sys.Stdout()
    BenchLine(mut out, "pingpong-switch-sleeper", 800_000, ns)
    out.Flush() else {
        return
    }
}

// ---- Channels and the scope exit order ----

// ChSend sends 0..n-1; it stores the number of sends that succeeded.
ChSend(io: Io, c: &Chan[int], n: int, sent: &raw.Atomic[u32]) {
    var k := 0
    for k < n {
        c.Send(io, k) else {
            break
        }
        k += 1
    }
    sent.Store(Trunc[u32](k))
}

// ChRecv receives until n values, Canceled or Closed; bad counts values out
// of order (each must equal the count so far). It closes the channel when
// it stops early, so the sender stops too.
ChRecv(io: Io, c: &Chan[int], n: int, got: &raw.Atomic[u32], bad: &raw.Atomic[u32]) {
    var k := 0
    for k < n {
        x := c.Recv(io) else {
            break
        }
        if x != k {
            _ = bad.Add(1)
        }
        k += 1
    }
    got.Store(Trunc[u32](k))
    if k < n {
        c.Close()
    }
}

test "RT-Chan-Rendezvous two tasks pass 200,000 values through a capacity-0 Chan in order" (sys: &System) {
    // A block arena, freed at the end, so mk test's leak count is back to
    // its baseline.
    var a := Arena.New(sys.heap) else {
        Assert(false)
        return
    }
    defer a.Free()
    c := Chan[int].New(a.Alloc(), 0) else {
        Assert(false)
        return
    }
    sent := raw.Atomic[u32].New(0)
    got := raw.Atomic[u32].New(0)
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go ChSend(sys.io, &c, 200_000, &sent)
        s.go ChRecv(sys.io, &c, 200_000, &got, &bad)
    }
    Assert(sent.Load() == 200_000 && got.Load() == 200_000)
    Assert(bad.Load() == 0)
}

test "RT-Chan-RendezvousCancel a receiver cancelled mid-stream loses and duplicates no value" (sys: &System) {
    var a := Arena.New(sys.heap) else {
        Assert(false)
        return
    }
    defer a.Free()
    for round in 0..20 {
        c := Chan[int].New(a.Alloc(), 0) else {
            Assert(false)
            return
        }
        sent := raw.Atomic[u32].New(0)
        got := raw.Atomic[u32].New(0)
        bad := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go ChSend(sys.io, &c, 1_000_000, &sent)
            scope r := sys.io.Scope() {
                r.go ChRecv(sys.io, &c, 1_000_000, &got, &bad)
                t0 := BenchNow()
                for BenchNow() - t0 < Trunc[i64](round + 1) * 100_000 {
                    YieldNow()
                }
                r.Cancel()
            }
        }
        // Rendezvous: a Send succeeds exactly when a receiver took its
        // value, and the receiver saw 0, 1, 2, ... in order.
        Assert(sent.Load() == got.Load())
        Assert(got.Load() < 1_000_000)
        Assert(bad.Load() == 0)
    }
}

// EoStore sets x to 7 after 20 ms, holding a loan of x.
EoStore(io: Io, x: &raw.Atomic[u32]) {
    io.NoCancel().Sleep(core.DurationOf(20_000_000)) else {
    }
    x.Store(7)
}

// EoBody records, from a defer in a scope body, what x held when the defer
// ran: the defer runs before the join (the scope exit order: defers, then
// the cancel of an early exit, then the join), so the task's store has not
// happened yet; and the defer reads x while the task's loan of it is live.
EoBody(io: Io, seen: &raw.Atomic[u32], after: &raw.Atomic[u32]) {
    x := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        defer seen.Store(x.Load() + 1)
        s.go EoStore(io, &x)
    }
    after.Store(x.Load())
}

test "RT-R17-ExitOrder a scope body's defer runs before the join and reads a local a task still borrows" (sys: &System) {
    seen := raw.Atomic[u32].New(0)
    after := raw.Atomic[u32].New(0)
    EoBody(sys.io, &seen, &after)
    Assert(seen.Load() == 1)
    Assert(after.Load() == 7)
}

// ---- The mutation Fail before the task end's decrement ----

// FeFail fails at once.
FeFail(io: Io): DcErr!void {
    fail DcErr
}

// FeRound runs one scope whose only task fails at once, so the body's join
// races the task's end; true when the try scope saw the error.
FeRound(io: Io): bool {
    var saw := false
    FeScope(io) else {
        saw = true
    }
    return saw
}

FeScope(io: Io): DcErr!void {
    try scope s := io.ScopeOf[DcErr]() {
        s.go FeFail(io)
    }
}

test "RT-R17-FailBeforeEnd the only task's error is the scope's error when the join races its end, 5,000 rounds" (sys: &System) {
    // TaskEntry records the error (Fail) before the live decrement that
    // ends the join (Fail: the copy happens before End decrements
    // live, so the joiner sees it). With the order reversed the joiner can
    // read live 0 and an error code of 0, and the try scope succeeds.
    var missed := 0
    for _ in 0..5_000 {
        if !FeRound(sys.io) {
            missed += 1
        }
    }
    Assert(missed == 0)
}
