// Why: each carrier has its own timer heap (timer.mk), and timers move,
// get stolen and race cancels; a timer in the wrong heap is lost.
// What: the RT8-Tm-* blocks (Migrate, ScopeLoop, Steal, Rotate, CancelRace,
// ExitWindow, Armed, ScanClamp) and the heap check TmHeapsClean.
// How: each block ends by checking that every heap is empty and every heap
// index names the heap it is in. Run by the scheduler harness at 1 to 8
// carriers, at 1 and 16 by std/rt_deadline_test.go, and over jitter seeds
// by TestRtSched_Stress. Bounds are loose (a lost timer shows as a wait past
// DlBound or the watchdog), so the blocks are not timing tests.

import std.core
import std.raw
import std.rt

// TmHeapsClean checks every started carrier's heap under its lock: each
// entry's index names this heap and its position, a sleeping task's entry
// is on the heap of the carrier it parked on (its carrier field, which only
// a resume changes, and a task in a heap is not resumed before its entry
// leaves it), the heap order holds, and tnext is the root's deadline
// (MaxDeadline when empty). It returns the entries left over all heaps.
TmHeapsClean(): int {
    var left := 0
    n := int(ScanCarriers())
    for i in 0..n {
        h := CarrierAt(i)
        unsafe "a started carrier's heap, read under its tlock" {
            rt.Lock(&(*h).tlock)
            m := (*h).ntimers
            for k in 0..m {
                e := At(h, k)
                tr := TimerOf(e)
                Assert(raw.Addr(HeapOf(tr)) == raw.Addr(h))
                if (e & ScopeTag) == 0 {
                    Assert(raw.Addr((*raw.FromAddr[Task](e)).carrier) == raw.Addr(h))
                }
                Assert(PosOf(tr) == k)
                Assert((*tr).state.Load() == TimerArmed)
                if k > 0 {
                    Assert(Deadline(h, (k - 1) / 2) <= Deadline(h, k))
                }
            }
            want := if m > 0 { Deadline(h, 0) } else { MaxDeadline }
            Assert(NextOf(h) == want)
            rt.Unlock(&(*h).tlock)
            left += m
        }
    }
    return left
}

// TmCarriers starts every carrier MK_CARRIERS allows (NStartCarriers) and
// returns how many run. A sleep's kick wakes an idle carrier but starts
// none (OweArm, Found), so a block whose hogs must leave one carrier free
// counts the started ones, not the configured ones.
TmCarriers(io: Io): u32 {
    NStartCarriers(io)
    return ScanCarriers()
}

// TmDeadlineHeap is the address of the heap s's deadline names (HeapOf),
// read while s lives and its deadline is Armed or Fired.
TmDeadlineHeap(s: &Scope): uint {
    unsafe "a live scope's record" {
        return raw.Addr(HeapOf(raw.Of(&(*RecOf(s)).dl)))
    }
}

// TmMigrate arms a far deadline, moves between carriers (YieldNow goes
// through the global queue, so any carrier may resume it), arms an earlier
// one (a re-arm inside the first heap), moves again, and either
// lets it fire during a sleep or leaves it for Join's disarm, on whichever
// carrier then runs it. It checks that the first arm lands on the running
// carrier's heap and that the re-arm leaves it there. bad counts rounds
// that ended wrong.
TmMigrate(io: Io, rounds: int, seed: u64, bad: &raw.Atomic[u32]) {
    var x := seed | 1
    for _ in 0..rounds {
        x ^= x << 13
        x ^= x >> 7
        x ^= x << 17
        fire := (x & 1) == 0
        scope s := io.Scope() {
            s.CancelAfter(core.DurationOf(DlLong))
            // The deadline is on the heap of the carrier running this task.
            h0 := TmDeadlineHeap(&s)
            if h0 != raw.Addr(Here()) {
                _ = bad.Add(1)
            }
            for _ in 0..Trunc[int]((x >> 1) & 3) {
                YieldNow()
            }
            // Earlier: re-armed in its own heap, wherever this task runs.
            s.CancelAfter(core.DurationOf(if fire { Trunc[i64]((x >> 3) & 63) * 1_000 + 1 } else { DlLong / 2 }))
            // Armed or already fired, it names the heap it was first armed
            // on: the pop does not rewrite its index.
            if TmDeadlineHeap(&s) != h0 {
                _ = bad.Add(1)
            }
            for _ in 0..Trunc[int]((x >> 9) & 3) {
                YieldNow()
            }
            if fire {
                io.Sleep(core.DurationOf(DlLong)) else {}
                if !s.Expired() {
                    _ = bad.Add(1)
                }
            } else if s.Expired() {
                _ = bad.Add(1)
            }
        }
    }
}

test "RT8-Tm-Migrate a deadline armed on one carrier, re-armed and disarmed or fired after its task moved, 16 tasks" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..16 {
            s.go TmMigrate(sys.io, 1_500, Trunc[u64](k + 1) *% 0x9E37_79B9_7F4A_7C15, &bad)
        }
    }
    Assert(bad.Load() == 0)
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

// TmScopes runs rounds of scope + CancelAfter + exit, the scopedeadline
// bench row's loop: a deadline that never fires, armed
// and disarmed on this task's carrier's heap.
TmScopes(io: Io, rounds: int, bad: &raw.Atomic[u32]) {
    for _ in 0..rounds {
        scope s := io.Scope() {
            s.CancelAfter(core.DurationOf(DlLong))
            if s.Expired() {
                _ = bad.Add(1)
            }
        }
    }
}

test "RT8-Tm-ScopeLoop 16 tasks each arm and disarm 20,000 scope deadlines that never fire" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go TmScopes(sys.io, 20_000, &bad)
        }
    }
    Assert(bad.Load() == 0)
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

// TmSleeper counts itself started (TmStarted, the high half of done),
// sleeps d nanoseconds (not cancellable: Main's chain), then counts itself
// done (the low half).
TmSleeper(io: Io, d: i64, done: &raw.Atomic[u32]) {
    _ = done.Add(TmStarted)
    io.NoCancel().Sleep(core.DurationOf(d)) else {}
    _ = done.Add(1)
}

const TmStarted: u32 = 0x1_0000

// TmHog waits, through schedule steps, until every sleeper has started
// (so none is left queued behind it), then computes without a schedule
// step until every sleeper is done or the bound passes; late is set when
// the bound passed.
TmHog(io: Io, done: &raw.Atomic[u32], want: u32, late: &raw.Atomic[u32]) {
    t0 := BenchNow()
    for done.Load() / TmStarted < want {
        YieldNow()
        if BenchNow() - t0 > DlBound {
            late.Store(1)
            return
        }
    }
    for done.Load() % TmStarted < want {
        if BenchNow() - t0 > DlBound {
            late.Store(1)
            return
        }
    }
}

// TmYielder runs schedule steps (YieldNow) until every sleeper is done or
// the bound passes: a busy carrier that does reach its TimerTick steps.
TmYielder(io: Io, done: &raw.Atomic[u32], want: u32, late: &raw.Atomic[u32]) {
    t0 := BenchNow()
    for done.Load() % TmStarted < want {
        YieldNow()
        if BenchNow() - t0 > DlBound {
            late.Store(1)
            return
        }
    }
}

test "RT8-Tm-Steal sleepers whose carriers then run tasks that never suspend are woken by the other carriers' steal of their timers" (sys: &System) {
    w := TmCarriers(sys.io)
    // One carrier: the hog holds it, and nothing else can run; the case
    // needs a second carrier.
    if w < 2 {
        return
    }
    for round in 0..20 {
        done := raw.Atomic[u32].New(0)
        late := raw.Atomic[u32].New(0)
        sleepers: u32 = 8
        scope s := sys.io.Scope() {
            // The sleepers arm on whichever carriers run them, then those
            // carriers take up hogs, which do not suspend until every
            // sleeper woke: one carrier is left free (w - 1 hogs), and it
            // must fire the hogs' carriers' timers (FireOthers, FireAll).
            for k in 0..int(sleepers) {
                s.go TmSleeper(sys.io, 200_000 + i64(k) * 50_000 + i64(round) * 10_000, &done)
            }
            for _ in 0..int(w) - 1 {
                s.go TmHog(sys.io, &done, sleepers, &late)
            }
        }
        Assert(late.Load() == 0)
        Assert(done.Load() % TmStarted == sleepers)
    }
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

test "RT8-Tm-Rotate with every carrier busy, a sleeper behind a task that never suspends is woken by a busy carrier's TimerTick step" (sys: &System) {
    w := TmCarriers(sys.io)
    for round in 0..20 {
        done := raw.Atomic[u32].New(0)
        late := raw.Atomic[u32].New(0)
        sleepers: u32 = 4
        scope s := sys.io.Scope() {
            for k in 0..int(sleepers) {
                s.go TmSleeper(sys.io, 300_000 + i64(k) * 100_000 + i64(round) * 10_000, &done)
            }
            // Every carrier gets a task: one hog (when there are two or
            // more carriers) and yielders on the rest, so no carrier is idle
            // and a hog's carrier's timers are fired only by a yielder's
            // TimerTick step (FireRotate) or a search (FireOthers).
            var yielders := w
            if w >= 2 {
                s.go TmHog(sys.io, &done, sleepers, &late)
                yielders = w - 1
            }
            for _ in 0..int(yielders) {
                s.go TmYielder(sys.io, &done, sleepers, &late)
            }
        }
        Assert(late.Load() == 0)
        Assert(done.Load() % TmStarted == sleepers)
    }
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

// TmCancelLater cancels s after spinning for ns, so the cancel lands before,
// during or after the deadline's fire, which may be on another carrier.
TmCancelLater(s: &Scope, ns: i64) {
    t0 := BenchNow()
    for BenchNow() - t0 < ns {
    }
    s.Cancel()
}

// TmRaceRounds: each round a scope with a 0 to 39 us deadline, a sleeper
// task inside it (its timer cancelled by whichever comes first, on
// whichever carrier), a nested scope with its own deadline (disarmed by an
// ancestor's cancel walk, DisarmLocked, or fired by its own, each perhaps
// on another carrier than the one that armed it), and a task that cancels
// the outer scope at a moment that may race either fire. Each cause is
// stable while its scope lives, and the sleeper always ends Canceled.
TmRaceRounds(io: Io, rounds: int, seed: u64, bad: &raw.Atomic[u32]) {
    var x := seed | 1
    for _ in 0..rounds {
        x ^= x << 13
        x ^= x >> 7
        x ^= x << 17
        got := raw.Atomic[u32].New(0)
        scope s := io.Scope() {
            s.CancelAfter(core.DurationOf(Trunc[i64](x % 40) * 1_000))
            s.go TmCancelLater(&s, Trunc[i64]((x >> 8) % 40) * 1_000)
            s.go DlSlept(io, &got)
            scope inner := io.Scope() {
                inner.CancelAfter(core.DurationOf(Trunc[i64]((x >> 16) % 60) * 1_000))
                io.Sleep(core.DurationOf(DlLong)) else {}
                b := inner.Expired()
                YieldNow()
                if inner.Expired() != b {
                    _ = bad.Add(1)
                }
            }
            io.Sleep(core.DurationOf(DlLong)) else {}
            a := s.Expired()
            YieldNow()
            if s.Expired() != a {
                _ = bad.Add(1)
            }
        }
        if got.Load() != 1 {
            _ = bad.Add(1)
        }
    }
}

test "RT8-Tm-CancelRace deadlines, sleeps and nested deadlines raced by a Cancel from another task, each fired, cancelled or disarmed on any carrier, 8 tasks" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go TmRaceRounds(sys.io, 400, Trunc[u64](k + 7) *% 0xBF58_476D_1CE4_E5B9, &bad)
        }
    }
    Assert(bad.Load() == 0)
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

// TmExitRounds is RT8-Dl-ExitWindow's round on one of many tasks: a
// deadline that fires as its scope exits, popped by whichever carrier finds
// it due (its own, a steal or an idle one), which Join waits out.
TmExitRounds(io: Io, rounds: int, n: &raw.Atomic[u32]) {
    for round in 0..rounds {
        scope s := io.Scope() {
            s.go DlQuick(io, n)
            s.CancelAfter(core.DurationOf(Trunc[i64](round % 50) * 1_000))
            t0 := BenchNow()
            for BenchNow() - t0 < Trunc[i64](round % 37) * 1_000 {
            }
        }
    }
}

test "RT8-Tm-ExitWindow 8 tasks each fire deadlines as their scopes exit, 2,500 rounds each: Join waits out a fire on any carrier" (sys: &System) {
    n := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go TmExitRounds(sys.io, 2_500, &n)
        }
    }
    Assert(n.Load() == 20_000)
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

// TmHolder opens a scope with a far deadline and sleeps in it until a
// cancel ends the sleep: two armed entries (the deadline and the sleep) on
// the heap of whichever carrier runs it. armed counts it once both are
// armed (the sleep's insert is the Park's, so armed is raised just before
// it and the test waits for the heaps to show it); out is 1 on Canceled.
// bad counts a deadline that is not on the running carrier's heap (the
// sleep's is checked by TmHeapsClean).
TmHolder(io: Io, armed: &raw.Atomic[u32], out: &raw.Atomic[u32], bad: &raw.Atomic[u32]) {
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(DlLong))
        if TmDeadlineHeap(&s) != raw.Addr(Here()) {
            _ = bad.Add(1)
        }
        _ = armed.Add(1)
        io.Sleep(core.DurationOf(DlLong)) else {
            _ = out.Add(1)
        }
    }
}

test "RT8-Tm-Armed 32 tasks hold a deadline and a sleep each on their carriers' heaps; every entry names its heap and position, then a cancel from Main removes them on whichever carrier" (sys: &System) {
    // Every carrier started first, so the holders spread over the heaps.
    _ = TmCarriers(sys.io)
    armed := raw.Atomic[u32].New(0)
    out := raw.Atomic[u32].New(0)
    bad := raw.Atomic[u32].New(0)
    var seen := 0
    scope s := sys.io.Scope() {
        for _ in 0..32 {
            s.go TmHolder(sys.io, &armed, &out, &bad)
        }
        t0 := BenchNow()
        for {
            if armed.Load() == 32 {
                seen = TmHeapsClean()
                if seen == 64 || BenchNow() - t0 > DlBound {
                    break
                }
            }
            YieldNow()
            Assert(BenchNow() - t0 <= DlBound)
        }
        s.Cancel()
    }
    Assert(seen == 64)
    Assert(out.Load() == 32)
    Assert(bad.Load() == 0)
    Assert(TmHeapsClean() == 0)
    Assert(core.CancelQuiet())
}

test "RT8-Tm-ScanClamp with ncarriers at want + 1, as StartCarrier leaves it for a moment, the scans of the started carriers read only records below want" (sys: &System) {
    // StartCarrier raises ncarriers before it compares with want. Record
    // want is never initialized, may lie past the committed 64 KiB units
    // (at 49 and 98 carriers its deque words do; std/rt_timers_test.go
    // runs this block there), and at 256 is past StealRounds' seen array.
    unsafe "the TaskState page Start mapped, which lives until exit" {
        st := TS()
        w := (*st).want.Load()
        old := (*st).ncarriers.Load()
        d := w + 1 - old
        _ = (*st).ncarriers.Add(d)
        Assert(ScanCarriers() == w)
        // Main is the only task, so no deque or runnext holds one.
        Assert(raw.IsNull(StealRounds(Here())))
        _ = AnyWork()
        Assert(Earliest() == MaxDeadline)
        _ = FireOthers(Here())
        // FireRotate, the fifth scan, through every index in turn: its
        // turn never reaches record want.
        for _ in 0..int(w) + 1 {
            FireRotate(Here())
            Assert((*Here()).tturn < w)
        }
        _ = (*st).ncarriers.Add(0 -% d)
        Assert((*st).ncarriers.Load() == old)
    }
    Assert(TmHeapsClean() == 0)
}
