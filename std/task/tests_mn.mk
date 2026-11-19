// Why: the M:N scheduler's bugs are lost wake-ups, double resumes and
// starved tasks, which show only under many carriers and interleavings.
// What: the RT-MN-* blocks (ping-pong, wake storms, foreign readies, timers,
// fairness), RT-Mutex-Contention, RT-Wait-Spread, RT-Spread, RT-NextWaits and
// the RT-Linger-* blocks, run by the scheduler harness and by mk test.
// How: each runs with MK_CARRIERS 1, 2, 4 and 8, and the stress harness runs
// it again over seeds with the jitter points on. A lost wake-up shows as the
// harness's watchdog; a double resume as the scheduler's own "readied twice"
// or "not runnable" stop (exit 101).

import std.core
import std.raw
import std.sys

// WordOf is an atomic's address as the u32 WaitWord takes.
WordOf(a: &raw.Atomic[u32]): *u32 {
    unsafe "raw.Atomic[u32] has u32's layout; the pointer is only passed on" {
        return raw.PtrCast[raw.Atomic[u32], u32](raw.Of(a))
    }
}

// Await until a == want, parking on it (spurious returns loop).
AwaitWord(a: &raw.Atomic[u32], want: u32) {
    for {
        v := a.Load()
        if v == want {
            return
        }
        _ = WaitWord(WordOf(a), v, false)
    }
}

// SetWord stores v and wakes every waiter.
SetWord(a: &raw.Atomic[u32], v: u32) {
    a.Store(v)
    WakeWord(WordOf(a), true)
}

// MnPing plays one side of a turn word: it waits for turn == me, then
// passes it to the other side, rounds times.
MnPing(io: Io, turn: &raw.Atomic[u32], me: u32, rounds: int) {
    for _ in 0..rounds {
        AwaitWord(turn, me)
        SetWord(turn, 1 - me)
    }
}

test "RT-MN-PingPong eight pairs pass a turn word 20,000 times each" (sys: &System) {
    turns: [8]raw.Atomic[u32] = [
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0)]
    scope s := sys.io.Scope() {
        for p in 0..8 {
            s.go MnPing(sys.io, &turns[p], 0, 20_000)
            s.go MnPing(sys.io, &turns[p], 1, 20_000)
        }
    }
    for p in 0..8 {
        Assert(turns[p].Load() == 0)
    }
}

// MnMark ORs the bit of the carrier it runs on into mask after 5 ms of
// work that does not suspend (by the clock, so no optimizer removes it),
// and counts itself.
MnMark(io: Io, mask: &raw.Atomic[u64], count: &raw.Atomic[u32]) {
    t0 := sys.Now()
    for sys.Now() - t0 < 5_000_000 {
    }
    t := Current()
    unsafe "t is the running task, so its carrier record is live (carrier records are never freed)" {
        bit := Trunc[u64](1) << Trunc[u64]((*(*t).carrier).index & 63)
        for {
            old := mask.Load()
            if mask.Cas(old, old | bit) {
                break
            }
        }
    }
    _ = count.Add(1)
}

test "RT-MN-Carriers busy tasks spread over more than one carrier" (sys: &System) {
    mask := raw.Atomic[u64].New(0)
    count := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..32 {
            s.go MnMark(sys.io, &mask, &count)
        }
    }
    Assert(count.Load() == 32)
    unsafe "the TaskState page Start mapped, which lives until exit" {
        want := (*TS()).want.Load()
        n := raw.Popcount64(mask.Load())
        Assert(n >= 1)
        if want >= 2 {
            Assert(n >= 2)
        }
        Assert(Trunc[u32](n) <= want)
        Assert((*TS()).ncarriers.Load() <= want)
    }
}

// MnWaiter waits for its own word to reach round, then counts, for rounds
// rounds.
MnWaiter(io: Io, w: &raw.Atomic[u32], done: &raw.Atomic[u32], rounds: u32) {
    for r in 1..rounds + 1 {
        AwaitWord(w, r)
        _ = done.Add(1)
        WakeWord(WordOf(done), true)
    }
}

test "RT-MN-WakeStorm 32 waiters woken in a scattered order, 400 rounds" (sys: &System) {
    words: [32]raw.Atomic[u32] = [
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
        raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0)]
    done := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for i in 0..32 {
            s.go MnWaiter(sys.io, &words[i], &done, 400)
        }
        for r in 1..401 {
            // 13 is odd, so i * 13 + r mod 32 visits every waiter once.
            for i in 0..32 {
                SetWord(&words[(i * 13 + r) % 32], Trunc[u32](r))
            }
            AwaitWord(&done, Trunc[u32](r * 32))
        }
    }
    Assert(done.Load() == 32 * 400)
}

// MnForeign is a thread std did not make a task of: it answers each request
// by storing it in resp and waking the waiter, off any task stack, so the
// waiter is readied through the global queue and an idle carrier must be
// woken for it.
struct MnRelay {
    req: raw.Atomic[u32]
    resp: raw.Atomic[u32]
    stop: raw.Atomic[u32]
    left: raw.Atomic[u32]
}

MnForeign(p: *u8) {
    unsafe "p is the test's relay, which outlives this thread's loop" {
        r := raw.PtrCast[u8, MnRelay](p)
        var seen: u32 = 0
        for (*r).stop.Load() == 0 {
            q := (*r).req.Load()
            if q != seen {
                seen = q
                (*r).resp.Store(q)
                WakeWord(WordOf(&(*r).resp), true)
            } else {
                sys.ThreadYield()
            }
        }
        (*r).left.Store(1)
    }
}

// MnRelayRun drives the relay: it asks the foreign thread n times and waits
// for each answer, then stops the thread; false when the thread could not
// be started.
MnRelayRun(n: int): bool {
    var relay := MnRelay(req: raw.Atomic[u32].New(0), resp: raw.Atomic[u32].New(0), stop: raw.Atomic[u32].New(0),
        left: raw.Atomic[u32].New(0))
    unsafe "the relay outlives the thread's loop: this function waits for left" {
        r := sys.ThreadStart(MnForeign, raw.PtrCast[MnRelay, u8](raw.OfMut(mut relay)), 1 << 20)
        if r.err != core.SysErr.None {
            return false
        }
    }
    for i in 1..n + 1 {
        relay.req.Store(Trunc[u32](i))
        AwaitWord(&relay.resp, Trunc[u32](i))
    }
    relay.stop.Store(1)
    for relay.left.Load() == 0 {
        sys.ThreadYield()
    }
    return true
}

test "RT-MN-ForeignReady a foreign thread readies a waiting task 20,000 times" (sys: &System) {
    Assert(MnRelayRun(20_000))
}

// MnSpinner keeps two tasks readying each other through runnext (each wakes
// the other and waits), until the flag is set or the round limit is
// reached; it reports the rounds it needed.
MnSpinner(io: Io, turn: &raw.Atomic[u32], me: u32, flag: &raw.Atomic[u32], rounds: &raw.Atomic[u32]) {
    for i in 0..2_000_000 {
        if flag.Load() != 0 {
            _ = rounds.Add(Trunc[u32](i))
            SetWord(turn, 1 - me)
            return
        }
        AwaitWordOr(turn, me, flag)
        SetWord(turn, 1 - me)
    }
    _ = rounds.Add(2_000_000)
}

// AwaitWordOr waits for a == want or a set flag.
AwaitWordOr(a: &raw.Atomic[u32], want: u32, flag: &raw.Atomic[u32]) {
    for {
        v := a.Load()
        if v == want || flag.Load() != 0 {
            return
        }
        _ = WaitWord(WordOf(a), v, false)
    }
}

MnFlag(io: Io, flag: &raw.Atomic[u32]) {
    flag.Store(1)
}

test "RT-MN-Fair a task queued behind a runnext ping-pong still runs" (sys: &System) {
    // On one carrier the pair keeps the runnext slot full, and the third
    // task sits in the deque, which the owner takes LIFO: it runs only
    // through TimerTick's oldest-task step, within about 61
    // schedules. 100,000 rounds is a bound no fair order comes near.
    turn := raw.Atomic[u32].New(0)
    flag := raw.Atomic[u32].New(0)
    rounds := raw.Atomic[u32].New(0)
    // Spawn order: each spawn takes runnext and pushes the previous one
    // onto the deque, so the flag task ends at the deque's top (oldest).
    scope s := sys.io.Scope() {
        s.go MnFlag(sys.io, &flag)
        s.go MnSpinner(sys.io, &turn, 0, &flag, &rounds)
        s.go MnSpinner(sys.io, &turn, 1, &flag, &rounds)
    }
    Assert(rounds.Load() < 100_000)
}

// MnChild yields once, then counts itself.
MnChild(io: Io, count: &raw.Atomic[u32]) {
    io.Yield() else {
        return
    }
    _ = count.Add(1)
}

// MnSpawner spawns n children into its own scope, then counts itself.
MnSpawner(io: Io, n: int, count: &raw.Atomic[u32]) {
    scope s := io.Scope() {
        for _ in 0..n {
            s.go MnChild(io, count)
        }
    }
    _ = count.Add(1)
}

test "RT-MN-SpawnMany 16 spawners of 1,000 yielding children each" (sys: &System) {
    count := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go MnSpawner(sys.io, 1_000, &count)
        }
    }
    Assert(count.Load() == 16 * 1_001)
}

// MnSleeper sleeps d nanoseconds and checks that the clock moved at least
// that far (a timer never fires early), then counts itself.
MnSleeper(io: Io, d: i64, early: &raw.Atomic[u32], count: &raw.Atomic[u32]) {
    t0 := sys.Now()
    io.Sleep(core.DurationOf(d)) else {
        return
    }
    if sys.Now() - t0 < d {
        _ = early.Add(1)
    }
    _ = count.Add(1)
}

test "RT-MN-Sleepers 2,000 sleepers of 0 to 3 ms on every carrier" (sys: &System) {
    early := raw.Atomic[u32].New(0)
    count := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for i in 0..2_000 {
            s.go MnSleeper(sys.io, Trunc[i64]((i * 7919) % 3_000_000), &early, &count)
        }
    }
    Assert(count.Load() == 2_000)
    Assert(early.Load() == 0)
}

// MnAdder adds 1 to the counter under the mutex n times.
MnAdder(io: Io, m: &Mutex[int], n: int) {
    for _ in 0..n {
        var g := m.Lock(io) else {
            return
        }
        v := *g.Ref()
        g.Store(v + 1)
        g.Unlock()
    }
}

test "RT-Mutex-Contention 16 tasks add 5,000 each under one Mutex" (sys: &System) {
    m := Mutex[int].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go MnAdder(sys.io, &m, 5_000)
        }
    }
    Assert(m.Into() == 80_000)
}

// MnTimed sleeps 2 ms and records that it woke.
MnTimed(io: Io, woke: &raw.Atomic[u32]) {
    io.Sleep(core.DurationOf(2_000_000)) else {
        return
    }
    woke.Store(1)
}

// MnBusy computes without suspending until woke is set or about 2 s pass
// (by the clock), so its carrier never reaches a schedule step.
MnBusy(io: Io, woke: &raw.Atomic[u32], late: &raw.Atomic[u32]) {
    t0 := sys.Now()
    for woke.Load() == 0 {
        if sys.Now() - t0 > 2_000_000_000 {
            late.Store(1)
            return
        }
    }
}

test "RT-MN-TimerOwner a timer fires while another carrier computes" (sys: &System) {
    // With two or more carriers the sleeper's timer must be fired by an idle
    // carrier while the busy one never schedules. With
    // one carrier the busy task holds it, so the bound does not apply.
    woke := raw.Atomic[u32].New(0)
    late := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go MnTimed(sys.io, &woke)
        s.go MnBusy(sys.io, &woke, &late)
    }
    unsafe "the TaskState page Start mapped, which lives until exit" {
        if (*TS()).want.Load() >= 2 {
            Assert(late.Load() == 0)
        }
    }
    Assert(woke.Load() == 1)
}

// MnSpin computes for ns nanoseconds without suspending.
MnSpin(io: Io, ns: i64) {
    t0 := sys.Now()
    for sys.Now() - t0 < ns {
    }
}

// MnGoBusy waits for start, then computes until woke is set (MnBusy).
MnGoBusy(io: Io, start: &raw.Atomic[u32], woke: &raw.Atomic[u32], late: &raw.Atomic[u32]) {
    AwaitWord(start, 1)
    MnBusy(io, woke, late)
}

// OthersIdle waits up to 1 s until at least three carriers are started and
// every one but the caller's is idle.
OthersIdle(): bool {
    t0 := sys.Now()
    for sys.Now() - t0 < 1_000_000_000 {
        unsafe "the TaskState page Start mapped, which lives until exit" {
            st := TS()
            n := (*st).ncarriers.Load()
            if n >= 3 && (*st).nidle.Load() + 1 == n {
                return true
            }
        }
    }
    return false
}

// MnKicker sets up the one schedule where only SleepFor's kick can fire a
// timer: every other carrier idle with no timer (so waiting with no
// timeout), then two tasks made runnable on this carrier (one wake-up, which
// at most one idle carrier answers), then this task sleeps and its carrier
// runs a task that never suspends. ok records that the schedule was reached.
MnKicker(io: Io, woke: &raw.Atomic[u32], late: &raw.Atomic[u32], ok: &raw.Atomic[u32]) {
    start := raw.Atomic[u32].New(0)
    scope warm := io.Scope() {
        for _ in 0..8 {
            warm.go MnSpin(io, 2_000_000)
        }
    }
    scope s := io.Scope() {
        s.go MnGoBusy(io, &start, woke, late)
        s.go MnGoBusy(io, &start, woke, late)
        if OthersIdle() {
            ok.Store(1)
        }
        SetWord(&start, 1)
        MnSpin(io, 1_000_000)
        io.Sleep(core.DurationOf(2_000_000)) else {
            woke.Store(1)
            return
        }
        woke.Store(1)
    }
}

test "RT-MN-TimerKick a new earliest timer wakes a carrier idle with no timeout" (sys: &System) {
    // The owed timer's kick (SleepFor, OweArm, Found): reached with three or
    // more carriers; with fewer the schedule cannot be built and only the
    // result is checked.
    woke := raw.Atomic[u32].New(0)
    late := raw.Atomic[u32].New(0)
    ok := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go MnKicker(sys.io, &woke, &late, &ok)
    }
    if ok.Load() == 1 {
        Assert(late.Load() == 0)
    }
    Assert(woke.Load() == 1)
}

// MnMaxLoad is the largest bucket count of n addresses base + k * stride.
MnMaxLoad(base: uint, stride: uint, n: int): int {
    var load: [4096]u16 = [0; 4096]
    var most := 0
    for k in 0..n {
        i := BucketIndex(base + Trunc[uint](k) * stride)
        load[i] += 1
        if int(load[i]) > most {
            most = int(load[i])
        }
    }
    return most
}

test "RT-Wait-Spread words at one offset of many task slots fall in different buckets" (sys: &System) {
    // 4,096 balls in 4,096 bins at random would give a largest bin of about
    // 6; an arithmetic progression under Fibonacci hashing gives at most 3.
    Assert(MnMaxLoad(0x10_0000_0000 + 0xF_FE40, 1 << 20, 4096) <= 3)
    Assert(MnMaxLoad(0x20_0000_0000 + 0xFF_FE40, 1 << 24, 4096) <= 3)
    // Neighbouring words of one structure (a Mutex array, a Chan's two
    // counters) are spread too.
    Assert(MnMaxLoad(0x7FF0_0000, 4, 4096) <= 3)
    Assert(MnMaxLoad(0x7FF0_0000, 64, 4096) <= 3)
}

// MnBarrier counts itself in and then spins, never suspending, until n
// tasks are in or 2 s pass; it counts the tasks that saw all n.
MnBarrier(io: Io, arrived: &raw.Atomic[u32], all: &raw.Atomic[u32], n: u32) {
    _ = arrived.Add(1)
    t0 := sys.Now()
    for arrived.Load() < n && sys.Now() - t0 < 2_000_000_000 {
    }
    if arrived.Load() >= n {
        _ = all.Add(1)
    }
}

test "RT-Spread one task spawns as many non-suspending tasks as there are carriers, and all of them run at once" (sys: &System) {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        n := (*TS()).want.Load()
        for _ in 0..20 {
            arrived := raw.Atomic[u32].New(0)
            all := raw.Atomic[u32].New(0)
            scope s := sys.io.Scope() {
                for _ in 0..n {
                    s.go MnBarrier(sys.io, &arrived, &all, n)
                }
            }
            // Every carrier got one: the spinners' hand-off (Found) woke a
            // carrier for each task left on a deque behind a running one.
            Assert(all.Load() == n)
        }
    }
}

// MnNextTask publishes the running task in cell and parks on word (the
// handshake, Pending) until the round's driver completes it; then it records
// the carrier it resumed on (index + 1) and sets ran.
MnNextTask(io: Io, cell: &raw.Atomic[u64], word: &raw.Atomic[u32], where: &raw.Atomic[u32], ran: &raw.Atomic[u32]) {
    unsafe "the running task; an atomic is its own address" {
        cell.Store(AddrOf(MustCurrent()))
        ParkUntil(raw.Of(word))
        where.Store((*Here()).index + 1)
    }
    ran.Store(1)
}

// MnNextHold is the task the fairness step runs ahead of runnext: it parks
// like MnNextTask, then computes without suspending until ran is set or 2 s
// pass (late), then completes the driver's handshake word.
MnNextHold(io: Io, cell: &raw.Atomic[u64], word: &raw.Atomic[u32], where: &raw.Atomic[u32], ran: &raw.Atomic[u32], late: &raw.Atomic[u32], driver: &raw.Atomic[u64], dword: &raw.Atomic[u32]) {
    unsafe "the running task; an atomic is its own address" {
        cell.Store(AddrOf(MustCurrent()))
        ParkUntil(raw.Of(word))
        where.Store((*Here()).index + 1)
    }
    t0 := sys.Now()
    for ran.Load() == 0 {
        if sys.Now() - t0 > 2_000_000_000 {
            late.Store(1)
            break
        }
    }
    unsafe "the driver published itself before it parked on dword" {
        // The driver stores its cell before it parks; if it has not parked
        // yet, Complete does not ready it and its own RunAfter does.
        for driver.Load() == 0 {
        }
        Complete(raw.Of(dword), TaskAt(driver.Load()))
    }
}

// MnNextRound builds NextWaits' schedule once and reports it in reached
// (1 when built and the held task ran on this carrier): a task T in this
// carrier's empty runnext with no carrier woken (as ReadySched leaves it),
// a task G on the global queue with no carrier woken, every other carrier
// idle, and this carrier's tick one short of TimerTick. When this task
// parks, the fairness step runs G here ahead of T, and G does not suspend
// until T has run: only the wake NextWaits makes gets T to another carrier
// (StealRounds' last round) before G's 2 s limit.
MnNextRound(io: Io, reached: &raw.Atomic[u32], late: &raw.Atomic[u32]) {
    cell_t := raw.Atomic[u64].New(0)
    cell_g := raw.Atomic[u64].New(0)
    cell_a := raw.Atomic[u64].New(0)
    word_t := raw.Atomic[u32].New(Pending)
    word_g := raw.Atomic[u32].New(Pending)
    word_a := raw.Atomic[u32].New(Pending)
    where_t := raw.Atomic[u32].New(0)
    where_g := raw.Atomic[u32].New(0)
    ran := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        s.go MnNextTask(io, &cell_t, &word_t, &where_t, &ran)
        s.go MnNextHold(io, &cell_g, &word_g, &where_g, &ran, late, &cell_a, &word_a)
        // Both parked (another carrier ran them: this task does not
        // suspend), then every other carrier idle and none spinning.
        var built := false
        t0 := sys.Now()
        for sys.Now() - t0 < 1_000_000_000 {
            if word_t.Load() == Parked && word_g.Load() == Parked {
                unsafe "the TaskState page Start mapped, which lives until exit" {
                    st := TS()
                    n := (*st).ncarriers.Load()
                    if (*st).nspinning.Load() == 0 && (*st).nidle.Load() + 1 == n {
                        built = true
                        break
                    }
                }
            }
        }
        unsafe "this task's carrier; the handshake words of two parked (or parking) tasks" {
            c := Here()
            me := (*c).index + 1
            // Completes is true only for a parked task, whose cell was
            // stored before it parked; otherwise its own RunAfter readies it.
            if Completes(raw.Of(&word_t)) {
                if ReadyPut(c, TaskAt(cell_t.Load())) {
                    built = false
                }
            } else {
                built = false
            }
            if Completes(raw.Of(&word_g)) {
                _ = ReadyPut(raw.Null[Carrier](), TaskAt(cell_g.Load()))
            } else {
                built = false
            }
            (*c).tick = TimerTick - 1
            cell_a.Store(AddrOf(MustCurrent()))
            ParkUntil(raw.Of(&word_a))
            if built && where_g.Load() == me {
                reached.Store(1)
            }
        }
    }
    Assert(ran.Load() == 1)
}

test "RT-NextWaits the fairness step that runs a global task ahead of a runnext left without a wake wakes a carrier for it" (sys: &System) {
    // NextWaits (sched.mk): without its WakeOne, T waits in runnext behind
    // G for G's whole 2 s. With one carrier nobody can take T, so the
    // schedule is not built there.
    unsafe "the TaskState page Start mapped, which lives until exit" {
        if (*TS()).want.Load() < 2 {
            return
        }
    }
    var hits := 0
    for _ in 0..20 {
        reached := raw.Atomic[u32].New(0)
        late := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go MnNextRound(sys.io, &reached, &late)
        }
        if reached.Load() == 1 {
            hits += 1
            Assert(late.Load() == 0)
        }
    }
    // The schedule must be built at least once, or the block tests nothing.
    Assert(hits > 0)
}

// ---- Linger: a spinner beside a busy pair goes idle again ----

test "RT-Linger-Ends a spinner that lingers beside a ping-pong pair goes idle again within LingerMax" (sys: &System) {
    // Linger (sched.mk) keeps a spinner searching while another carrier's
    // runnext stays busy, for at most LingerMax (200 us) per run. Without
    // that bound the spinner beside the pair below would linger for the
    // pair's whole run (about 125 ms here) and never leave for Idle; with
    // it, it leaves hundreds of times. The counter is Linger's false
    // returns (the statement after one is Idle), not the unarmed PollWait
    // returns this block first read: those also depend on Idle's AnyWork
    // recheck and on the OS scheduling every carrier, and with mk test's
    // blocks side by side they fell to 10 on a quiet machine and under 5
    // with fuzzers running (a development machine). With one carrier there is no spinner.
    unsafe "the TaskState page Start mapped, which lives until exit" {
        if (*TS()).want.Load() < 2 {
            return
        }
    }
    turn := raw.Atomic[u32].New(0)
    var e0: u64 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: none; a test counter.
        e0 = (*TS()).linger_ends.Load()
    }
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.go MnPing(sys.io, &turn, 0, 500_000)
        s.go MnPing(sys.io, &turn, 1, 500_000)
    }
    took := BenchNow() - t0
    var e1: u64 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: none; a test counter.
        e1 = (*TS()).linger_ends.Load()
    }
    // At least one (a spinner that never stops lingering gives 0).
    // Measured on a development machine: 496 to 1,762 alone at 2 to 16
    // carriers; 36 to 419 in mk test --std std.task (blocks side by
    // side); 6 to 136 with 16 busy loops beside it. The bound itself is
    // RT-Linger-Rule's, by the clock.
    Assert(took > 0)
    Assert(turn.Load() == 0)
    Assert(e1 - e0 >= 1)
}

// LingerCases runs Linger on a carrier record of its own (zeroed pages,
// never a started carrier) in the three cases of its rule, and returns how
// many went wrong. Clock values are set far from now, so no preemption
// between a store and Linger's own clock read changes a case.
LingerCases(): int {
    p := sys.Reserve(CarrierBytes(1), 65536)
    if raw.IsNull(p) || !sys.Commit(p, CarrierBytes(1)) {
        return 100
    }
    far: i64 = 1_000_000_000
    var bad := 0
    unsafe "a zeroed carrier record of this function's own, no thread runs on it" {
        c := raw.PtrCast[u8, Carrier](p)
        // A runnext seen now, no linger yet: linger, and start the clock.
        (*c).next_seen_at = sys.Now() + far
        (*c).linger_from = 0
        if !Linger(c) || (*c).linger_from == 0 {
            bad += 1
        }
        // Still seen, but LingerMax has passed: leave, and clear the clock.
        (*c).linger_from = sys.Now() - LingerMax - far
        if Linger(c) || (*c).linger_from != 0 {
            bad += 1
        }
        // No runnext seen within LingerNs: leave at once.
        (*c).next_seen_at = sys.Now() - LingerNs - far
        (*c).linger_from = 0
        if Linger(c) || (*c).linger_from != 0 {
            bad += 1
        }
    }
    sys.Release(p, CarrierBytes(1))
    return bad
}

test "RT-Linger-Rule a spinner lingers only while a runnext was seen within LingerNs, and for at most LingerMax" (sys: &System) {
    Assert(LingerCases() == 0)
}
