// Why: concurrency bugs hide in rare interleavings. Under a Sim's Io the
// tasks run one at a time, in an order drawn from the seed, so one seed
// gives one interleaving, every time, and many seeds explore many.
//
// What: the seeded scheduler: task records (TRec, MainRec, JoinRec), the
// park words, the hooks std.task calls (SimEnter, SimBorn, SimEnd,
// SimLeave), the choice (Decide, Best, ChangePoint), deadlock and cancel
// handling (Deadlock, CancelWaits, CutMark) and the note fan (FanLeave).
//
// How: one baton per Sim, granted by PCT's priorities with two seeded
// change points; the clock jumps to the next event when no task can run.
// The rules for births, notes, time and parking follow.
//
// Under a Sim's Io, tasks run one at a time, in an order drawn from the seed.
// std.task calls the Io's enter(task) before a task's code runs and after
// each of its suspension points, and leave(task) at each suspension point and
// when the task's function returns (std/task/hooks.mk). A Sim holds one
// baton: enter parks the task until the baton is granted to it, leave gives
// it up and grants it to the next task, and the Sim's own entries that would
// wait (a futex wait on an unchanged word, a sleep, a receive or accept with
// nothing to take, a yield) mark the holder blocked and park it the same way.
//
// The choice is PCT's (Burckhardt, Kothari, Musuvathi and Nagarakatte
// 2010, "A Randomized Scheduler with Probabilistic Guarantees of Finding
// Bugs"): each task has a priority drawn from the seed when it is born,
// the highest-priority runnable task runs, and at two seeded operation
// counts the running task drops below every other (the change points that
// give PCT its depth-3 bound). A yield drops the yielder the same way, so
// a yield loop hands the baton round. A priority is a function of the
// seed and the task's birth rank, and no choice draws from the stream, so
// a task that runs and parks again with no effect (a Join that finds its
// scope still live) changes no later choice.
//
// Births and ends (std.task's hooks). std.task announces each
// task reserved under the Sim's Io on its spawner's stack before the task
// can run (leave with task.BornBit), each final leave (task.LastBit), and,
// when a spawner reaches its next leaf, a note that it stopped spawning
// for now (task.NoteBits). A born task is registered at once with the
// next birth rank (its spawn order); a choice waits, with the baton free,
// until every born task has arrived at its first enter and no spawner
// outside the baton (main, a native task) still owes its note. So the
// tasks a choice sees are the same in every run, whoever spawned them:
// the main task's births all precede the first choice after its next
// leaf (its Join, its native wait), and a holder's births happen while it
// holds the baton. A task outside the baton that a wait of the run
// returned to (main after its Recv) owes a note the same way, since until
// its next leaf it may wake or spawn tasks: a choice made before it would
// call a deadlock that main is about to end. It resumes only once the
// baton is free (MainWait), so until that leaf no task of the run runs
// beside it, and what it reads of the clock or of shared memory is the
// same in every run. An ended task leaves the run
// at its last leave.
//
// Time. When no task can run, the clock jumps to the earliest sleeper (a
// discrete-event simulation). A native waiter's Sleep or Yield (main's) is
// such a sleeper: once the clock reaches its deadline it runs before the
// next choice (MainDue). When none sleeps and every task is blocked,
// the run is deadlocked and each blocked operation answers Canceled, as a
// one-task run's does. A task that waits natively in Join or in a scope's
// limit gate names that scope (task.HookHold), and every task names its
// own (task.HookHome), so the Sim knows when such a wait ends: when no
// task of the scope is left (Join) or fewer than the limit (the gate);
// for a scope of the run, until then it is stuck like a blocked task,
// and once it is satisfied the choice waits for the task to come back
// (a scope of another Sim's run or a native one makes it away: SimLeave). A native task outside the
// baton (main) that waits so sends the note it owes from that wait's
// leaf, with the scope in its hold, and owes the scope's own Io one there
// even when it owed this Sim none (task.HoldSet); a spawn into a
// scope of another Io sends no note before that leaf (task.OweNote keeps
// notes owed to two Ios, and a third Io's goes through the note fan,
// FanLeave, which hands it to every live Sim at the leaf). When the scope
// was opened from an Io of this run, the Sim's own, a NoCancel of it or
// a Recorder's over it (task.HookHoldIo, RunKey), the Sim records it
// (JoinRec) and makes it owe another note; once the wait is satisfied
// the run's choices wait for that note, so no deadlock is called before
// its next leaf: it may still send what a blocked task waits for.
// A Join of any other scope (a native one, another Sim's) holds nothing:
// the Sim cannot see a wait for tasks outside the run, as it cannot see
// any other native wait of main, so it counts it as main's suspension and
// may call a deadlock that main's return would have ended, at a point
// that follows real time. Main busy-polling after a wait of the
// run, with no leaf, holds the run until its next leaf, as after any
// wait of the run. A task away in any other
// native leaf (real I/O, a slot wait) may come back at any time: no
// deadlock is called while one is away. No rule waits on real time.
//
// Parking. The tasks of slot i park on static words, ParkPer per slot
// (ParkWord), never on Sim memory, so a task woken after Free or Restart
// reads only static memory; it then finds the token changed and runs
// unscheduled, and every Io call of the dead run answers Canceled. A grant
// bumps the grantee's own word, so only it wakes and rechecks
// whether it holds the baton; Restart and Free bump every word of the slot.
// A waiter outside the baton (main's waits) parks on MainWords[i], which
// every wake, network event and deadlock of the slot's run bumps, and the
// choice when a sleeping waiter's deadline comes (MainDue).

import std.core
import std.raw
import std.task

// Task states. TNew: born, not yet arrived at its first enter. TJoin: in
// Join or a limit gate (hold, limit) that no task of the Sim has yet
// satisfied. TBack: away in a satisfied Join or gate, so it comes back
// without any task of the Sim running.
const TNew: u32 = 1
const TReady: u32 = 2
const TRun: u32 = 3
const TBlocked: u32 = 4
const TAway: u32 = 5
const TJoin: u32 = 6
const TBack: u32 = 7

// What a blocked task waits for.
const BWord: u32 = 1
const BSleep: u32 = 2
const BNet: u32 = 3

// The span PCT's change points are drawn from, in steps: counted
// operations (the Gate) and the holder's waits, sleeps and yields, which
// are the program's own events, never the runtime's.
const ChangeSpan: u64 = 512

// A task of the current run. prio: higher runs first; key: the word's
// address (BWord); until: the clock at which a sleeper wakes, or -1; seq:
// the order blocked tasks wake in on one word (FIFO); canceled: the
// deadlock rule woke it; home: its scope (task.HookHome), 0 until it
// arrives; here: inside its enter hook or running, not in a native leaf;
// hold and limit: the scope and limit of its Join or gate (TJoin); nt:
// its native task (0 until it arrives); wio: the Io of its blocked wait,
// whose NoCancel bit says whether its scope's cancel ends the wait
// (CancelWaits); weigh: it is parked in a weighed network wait (NetBlock:
// an accept's or a stream receive's), whose take is weighed against cut,
// the instant of the deadline fire that cancelled its scope chain while
// it waited, -1 for none and -2 for a chain cancelled before any fire
// (CutMark).
struct TRec {
    id: u64
    state: u32
    kind: u32
    prio: i64
    key: uint
    until: i64
    seq: u64
    canceled: bool
    home: uint
    here: bool
    hold: uint
    limit: u32
    pw: int
    nt: uint
    wio: ?Io
    weigh: bool
    cut: i64
}

// A native waiter outside the baton (main), parked on MainWords[slot]: its
// task record's address, the clock at which its sleep or yield ends, -1
// for a wait that only an event of the run ends, and the Io it waits
// through, whose NoCancel mask decides whether a cancel ends the wait
// (MainCancels).
struct MainRec {
    a: uint
    due: i64
    io: Io
}

// A native task outside the baton (main) waiting in a Join (limit 0) or
// a limit gate of scope hold, which the Sim cannot see: its task record's
// address, recorded from its note (SimLeave) only when hold is a scope of
// this run (RunKey), and kept until a note of it for another wait.
// A note for the same hold and limit is the same wait going round its
// loop, so the record stays, and with it done. done: the run satisfies
// the wait (ResolveJoins), so the joiner is coming back and the run's
// choices wait for its next note; it is cleared again if the scope fills
// (another spawner took the gate's slot).
struct JoinRec {
    a: uint
    hold: uint
    limit: u32
    done: bool
}

struct ParkSet {
    w: [64]u32
}

// Each slot has ParkPer park words; a task parks on word (birth rank - 1)
// mod ParkPer, so a grant wakes the grantee alone (tasks past the
// 256th share words, and a shared word's other parkers wake, recheck and
// park again).
const ParkPer: int = 256

struct ParkTable {
    w: [16384]u32
}

var ParkWords: ParkTable = ParkTable(w: [0; 16384])
var MainWords: ParkSet = ParkSet(w: [0; 64])

ParkWord(slot: int, k: int): *u32 {
    unsafe "ParkWords is static and never freed" {
        return raw.Of(&ParkWords.w[slot * ParkPer + k])
    }
}

ParkAtomic(slot: int, k: int): &raw.Atomic[u32] {
    unsafe "a u32 in static memory has raw.Atomic[u32]'s layout" {
        return &*raw.PtrCast[u32, raw.Atomic[u32]](ParkWord(slot, k))
    }
}

MainWord(slot: int): *u32 {
    unsafe "MainWords is static and never freed" {
        return raw.Of(&MainWords.w[slot])
    }
}

MainAtomic(slot: int): &raw.Atomic[u32] {
    unsafe "a u32 in static memory has raw.Atomic[u32]'s layout" {
        return &*raw.PtrCast[u32, raw.Atomic[u32]](MainWord(slot))
    }
}

// Bump wakes every task parked on slot's word; each rechecks the baton.
// No lost wake: a parker loads the word under the slot's lock and parks
// only if it is still unchanged at WaitWord's compare (under the bucket
// lock); a bump runs under the slot's lock, so it either precedes the
// parker's load (the parker sees the new holder before it parks), or
// changes the word before the compare, or finds the parker enqueued. The
// Add is seq_cst (raw.Atomic); WakeWord walks the bucket under its lock.
// used is the number of words the slot's tasks were ever given (the
// Sim's birth ranks, at most ParkPer), so a Sim that never spawned pays
// no wake at its Restart or Free.
Bump(slot: int, used: u64) {
    m := if used < Cast[u64](ParkPer) { Cast[int](used) } else { ParkPer }
    for k in 0..m {
        BumpOne(slot, k)
    }
}

// BumpOne wakes the tasks parked on slot's word k (a grant's grantee).
BumpOne(slot: int, k: int) {
    _ = ParkAtomic(slot, k).Add(1)
    task.WakeWord(ParkWord(slot, k), true)
}

// BumpMain wakes every native waiter of slot (SimWait outside the baton),
// by Bump's argument over MainWords.
BumpMain(slot: int) {
    _ = MainAtomic(slot).Add(1)
    task.WakeWord(MainWord(slot), true)
}

// SchedInit is the scheduler's part of Sim.New and Restart.
SchedInit(w: mut World, seed: u64) {
    w.sseed = Mix(seed ^ 0x5CED_5CED_5CED_5CED)
    SchedReset(w)
}

// DrawCps draws the two change points of the window of span steps after
// base. Windows double: 512 steps, then 1,024, then 2,048, so a
// run of k steps has about 2 log2(k / 512) change points, each window's
// uniform over it (Burckhardt et al. 2010 draw d - 1 points uniformly over
// the run's k steps; the run's k is not known in advance, so each doubling
// window stands for a run that ends in it). Drawn from the priority seed,
// the run's epoch and the window, never from the world's stream.
DrawCps(w: mut World, base: u64, span: u64) {
    k := w.sseed +% Cast[u64](w.epoch) *% 0xA24BAED4963EE407 +% base *% Gold
    w.cps = [base + 1 + Mix(k +% 1) % span, base + 1 + Mix(k +% 2) % span]
    w.cpend = base + span
}

// SchedReset forgets the tasks of the run (Restart): their Io is dead, and
// so is every native waiter's (it finds the token changed).
SchedReset(w: mut World) {
    w.tasks.Truncate(0)
    w.pend.Truncate(0)
    w.holder = 0
    w.run = 0
    w.hooked = false
    w.low = -1
    w.events += 1
    w.mwait = 0
    w.mains.Truncate(0)
    w.joins.Truncate(0)
    w.dls.Truncate(0)
    w.mturn = false
    // each run counts its own steps from 0 and draws its own
    // change points.
    w.steps = 0
    DrawCps(w, 0, ChangeSpan)
}

FindT(w: &World, id: u64): int {
    for i in 0..w.tasks.Len() {
        if w.tasks.Get(i).id == id {
            return i
        }
    }
    return -1
}

// AddT registers id with the next birth rank; -1 when out of memory (the
// task then runs unscheduled).
AddT(w: mut World, id: u64, st: u32): int {
    w.ranks += 1
    prio := Cast[i64](Mix(w.sseed +% w.ranks *% Gold) >> 2)
    if !w.tasks.Push(TRec(id: id, state: st, kind: 0, prio: prio, key: 0, until: -1, seq: 0, canceled: false,
        home: 0, here: false, hold: 0, limit: 0, pw: Cast[int]((w.ranks - 1) % Cast[u64](ParkPer)), nt: 0, wio: none,
        weigh: false, cut: -1)) {
        return -1
    }
    return w.tasks.Len() - 1
}

AnyState(w: &World, st: u32): bool {
    for i in 0..w.tasks.Len() {
        if w.tasks.Get(i).state == st {
            return true
        }
    }
    return false
}

// Pending spawners, by task record address: tasks outside the baton that
// reserved tasks of this run, or that a wait of this run returned to
// (SimWait), and owe their note (no leaf since).
FindPend(w: &World, a: uint): int {
    for i in 0..w.pend.Len() {
        if w.pend.Get(i) == a {
            return i
        }
    }
    return -1
}

AddPend(w: mut World, a: uint) {
    if FindPend(&*w, a) < 0 {
        // Out of memory: the spawner is not waited for (runs may then
        // differ).
        _ = w.pend.Push(a)
    }
}

DropPend(w: mut World, a: uint) {
    i := FindPend(&*w, a)
    if i >= 0 {
        _ = w.pend.RemoveAt(i)
    }
}

// Members counts the tasks of the run whose scope is key (a task leaves
// the run at its last leave).
Members(w: &World, key: uint): int {
    var n := 0
    for i in 0..w.tasks.Len() {
        if w.tasks.Get(i).home == key {
            n += 1
        }
    }
    return n
}

// Resolve turns each TJoin task whose wait the run now satisfies into a
// runnable task (here: parked in its enter hook after its native wait
// returned early) or a returning one (TBack: its wait returns once the
// ended tasks are counted, with no task of the Sim running). Called only
// with every born task arrived, so every home is known.
Resolve(w: mut World) {
    for i in 0..w.tasks.Len() {
        t := w.tasks.Get(i)
        if t.state != TJoin {
            continue
        }
        if Satisfied(&*w, t.hold, t.limit) {
            if t.here {
                w.tasks.Mut(i).state = TReady
            } else {
                w.tasks.Mut(i).state = TBack
            }
            w.events += 1
        }
    }
}

// Satisfied: a wait for scope hold with limit (0 for a Join) ends once
// the run's tasks of hold are counted out.
Satisfied(w: &World, hold: uint, limit: u32): bool {
    n := Members(&*w, hold)
    return (limit == 0 && n == 0) || (limit > 0 && n < Cast[int](limit))
}

// ResolveJoins marks each native joiner whose wait the run now satisfies
// done, and one whose scope filled again not done. A done joiner
// comes back from its Join or gate and, until its next leaf sends the note
// it owes (SimLeave re-owed it), may still wake or spawn tasks, as main
// after a wait of the run (Owe). So no choice is made, and no deadlock
// called, while one is done: true then. Called only with every born task
// arrived, as Resolve, so a scope's members are all counted. O(joiners x
// tasks).
ResolveJoins(w: mut World): bool {
    var any := false
    for i in 0..w.joins.Len() {
        j := w.joins.Get(i)
        s := Satisfied(&*w, j.hold, j.limit)
        if s != j.done {
            w.joins.Mut(i).done = s
            w.events += 1
        }
        any = any || s
    }
    return any
}

// FindJoin is the index of the native joiner at a, or -1.
FindJoin(w: &World, a: uint): int {
    for i in 0..w.joins.Len() {
        if w.joins.Get(i).a == a {
            return i
        }
    }
    return -1
}

// RunKey is the slot and token of the Sim run behind io: io's own for a
// Sim's Io, the inner Io's for a Recorder's (followed, a Recorder over a
// Recorder too); 0 for any other Io (the native one, a Replayer's, a freed
// tape's). Two Ios of one run have one key, whatever their NoCancel mask.
// Only a Sim's and a tape's Io name a registry slot and a token, and the
// slot says which it holds (SlotTape). It takes slot locks, so call it
// with no lock held. O(depth).
RunKey(io: Io): uint {
    var x := io
    for _ in 0..8 {
        f := core.IoFlags(x)
        i := Cast[int](f >> 48)
        tok := u64((f >> 1) & uint(TokenMask))
        if i >= SlotCount || tok == 0 {
            return 0
        }
        LockSlot(i)
        live := SlotToken(i) == tok
        tape := SlotTape(i)
        UnlockSlot(i)
        if !live {
            return 0
        }
        if !tape {
            return (Cast[uint](i) << 48) | (uint(tok) << 1)
        }
        m := HookMode(x)
        if !m.ok || m.replay {
            return 0
        }
        x = m.inner
    }
    return 0
}

// DropJoin forgets the native joiner at a.
DropJoin(w: mut World, a: uint) {
    for i in 0..w.joins.Len() {
        if w.joins.Get(i).a == a {
            _ = w.joins.RemoveAt(i)
            return
        }
    }
}

// Best is the runnable task of highest priority, or -1.
Best(w: &World): int {
    var b := -1
    for i in 0..w.tasks.Len() {
        t := w.tasks.Get(i)
        if t.state == TReady && (b < 0 || t.prio > w.tasks.Get(b).prio) {
            b = i
        }
    }
    return b
}

// Timed is the blocked task that wakes first by the clock, or -1.
Timed(w: &World): int {
    var b := -1
    for i in 0..w.tasks.Len() {
        t := w.tasks.Get(i)
        if t.state != TBlocked || t.until < 0 {
            continue
        }
        if b < 0 || t.until < w.tasks.Get(b).until ||
            (t.until == w.tasks.Get(b).until && t.prio > w.tasks.Get(b).prio) {
            b = i
        }
    }
    return b
}

// Overdue readies every sleeper whose wake time the clock has reached.
Overdue(w: mut World) {
    for i in 0..w.tasks.Len() {
        r := w.tasks.Mut(i)
        if r.state == TBlocked && r.until >= 0 && r.until <= w.clock {
            r.state = TReady
            w.events += 1
        }
    }
}

Grant(w: mut World, slot: int, i: int) {
    t := w.tasks.Mut(i)
    t.state = TRun
    w.holder = t.id
    w.events += 1
    BumpOne(slot, t.pw)
}

// Demote drops task i below every other (a PCT change point, a yield).
Demote(w: mut World, i: int) {
    w.tasks.Mut(i).prio = w.low
    w.low -= 1
}

// ChangePoint is PCT's step: at the drawn step counts the running task
// drops below the others.
// Only the holder's operations are steps: main's operations
// outside the baton, before any task exists or while tasks run, never
// use up a change point. True when the holder was demoted.
ChangePoint(w: mut World): bool {
    if !IsHolder(&*w) {
        return false
    }
    w.steps += 1
    if w.steps > w.cpend {
        DrawCps(w, w.cpend, (w.cpend + ChangeSpan) & ~(ChangeSpan - 1))
    }
    if w.steps != w.cps[0] && w.steps != w.cps[1] {
        return false
    }
    i := FindT(&*w, w.holder)
    if i < 0 {
        return false
    }
    Demote(w, i)
    return true
}

// Halted reports whether the calling task's scope is cancelled and io
// lets that end its operation: the check the native Io
// makes before every cancellable operation (task.Stop), so a CancelPoint
// (a sleep of 0) and a yield under a Sim answer Canceled as they do
// natively. Off a task stack (main) nothing is cancelled.
Halted(io: Io): bool {
    t := task.Current()
    return !raw.IsNull(t) && task.IsCancelled(t, io)
}

// CancelWaits makes runnable, with Canceled, every task blocked in a
// cancellable wait (word, sleep or network) whose scope was cancelled since
// it blocked, as the native cancel walk wakes its blocked waiters. It runs
// at each choice (Decide), so where the cancel lands among the Sim's
// events is the schedule's, and a replay repeats it.
CancelWaits(w: mut World) {
    for i in 0..w.tasks.Len() {
        r := w.tasks.Mut(i)
        if r.state != TBlocked || r.nt == 0 {
            continue
        }
        match r.wio {
            some(wio) {
                var stop := false
                unsafe "nt is the native task of a record of this run: it is blocked in BlockAs, so its stack and its scope are live" {
                    stop = task.IsCancelled(raw.FromAddr[task.Task](r.nt), wio)
                }
                if stop {
                    r.state = TReady
                    r.canceled = true
                    w.events += 1
                }
            }
            none {}
        }
    }
}

// CutMark gives each weighed waiter (TRec.weigh, NetBlock) not yet marked
// whose scope chain is cancelled now the mark at: FireDeadlines calls it
// with -2 before its first fire, so a chain an explicit Cancel or a fire
// outside a choice cancelled is never weighed, and with each fired
// deadline's instant after that fire, so a waiter's cut is the instant of
// the deadline that cancelled its own chain, whatever other scopes'
// deadlines fire at the same choice. O(tasks); allocates nothing.
CutMark(w: mut World, at: i64) {
    for i in 0..w.tasks.Len() {
        r := w.tasks.Mut(i)
        if !r.weigh || r.cut != -1 || r.nt == 0 || (r.state != TBlocked && r.state != TReady) {
            continue
        }
        match r.wio {
            some(wio) {
                var cut := false
                unsafe "nt is the native task of a record of this run in a weighed wait: NetBlock sets weigh under the slot's lock and clears it under that lock once BlockAs returns, and a record blocked or readied but not granted is parked in Block, so its stack and its scope are live" {
                    cut = task.IsCancelled(raw.FromAddr[task.Task](r.nt), wio)
                }
                if cut {
                    r.cut = at
                }
            }
            none {}
        }
    }
}

// MainCancels makes pending every native waiter (main) whose scope is
// cancelled while it waits in MainWait. The native cancel walk wakes such
// a waiter at once, outside the Sim's schedule, so without this a choice
// made before it is back ran the run on (another task's sleep, the clock's
// jump) and the waiter took its Canceled only at that later event.
// Pending, no choice is made until it has taken its answer
// and sent the note it then owes (MainWait's Owe), as SimWake does for a
// woken waiter. Each choice (Decide) runs it first, so where the cancel
// lands is the schedule's and a replay repeats it. O(waiters) per choice;
// allocates only AddPend's slot.
MainCancels(w: mut World) {
    for i in 0..w.mains.Len() {
        m := w.mains.Get(i)
        var cut := false
        unsafe "a is the native task of a waiter between MainWait's push and its DropMain: it is inside that Io call, so its task record and its scopes are live" {
            cut = task.IsCancelled(raw.FromAddr[task.Task](m.a), m.io)
        }
        if cut {
            AddPend(w, m.a)
        }
    }
}

// Deadlock: every blocked operation answers Canceled, a native waiter's
// (main's) too. A native waiter woken by it owes a note (WakeMains), so
// it takes its answer before any task of the run moves on.
Deadlock(w: mut World) {
    w.dlk += 1
    if w.mwait > 0 {
        WakeMains(w)
    }
    for i in 0..w.tasks.Len() {
        r := w.tasks.Mut(i)
        if r.state == TBlocked {
            r.state = TReady
            r.canceled = true
        }
    }
}

// Decide grants the baton (the caller holds e's lock and w.holder is 0),
// or leaves it free when the choice needs a fact still to come: a born
// task's arrival, a pending spawner's note, a returning task's arrival,
// an away task's return. Whoever brings the fact calls Decide again, so
// Decide never waits, on real time or otherwise.
Decide(e: &Entry) {
    w := WorldOf(e)
    if w.mturn {
        // The baton is free: a native waiter that a wake returned while a
        // task held it may run now (MainWait), before any choice.
        w.mturn = false
        BumpMain(e.slot)
    }
    if w.tasks.Len() == 0 {
        // Nothing is left to wake a native waiter: its wait is Canceled.
        if w.mwait > 0 {
            w.dlk += 1
            BumpMain(e.slot)
        }
        return
    }
    MainCancels(w)
    if w.pend.Len() > 0 || AnyState(&*w, TNew) {
        return
    }
    Resolve(w)
    if ResolveJoins(w) || AnyState(&*w, TBack) {
        return
    }
    for {
        // the clock passes deadlines without a choice (now, disk
        // latency, an arrival), so every sleeper whose deadline has passed
        // is runnable before the choice, not only when nothing else is.
        FireDeadlines(w)
        Overdue(w)
        CancelWaits(w)
        // A deadline just fired may have cancelled a native waiter's
        // scope: it takes its Canceled at this clock, before any choice.
        MainCancels(w)
        if w.pend.Len() > 0 {
            return
        }
        if MainDue(w) {
            return
        }
        b := Best(&*w)
        if b >= 0 {
            Grant(w, e.slot, b)
            return
        }
        // The clock jumps to the next event: a sleeping task's wake or a
        // sleeping native waiter's (on a tie both are due, and MainDue runs
        // the waiter first). A sleeping waiter is an event to come, so the
        // run is not deadlocked while one sleeps.
        // A scope deadline is an event too; on a tie with a sleeper
        // it fires first (FireDeadlines runs before Overdue).
        dl := NextDeadline(&*w)
        if dl >= 0 && (Timed(&*w) < 0 || dl <= w.tasks.Get(Timed(&*w)).until) && (MainNext(&*w) < 0 || dl <= MainNext(&*w)) {
            if dl > w.clock {
                w.clock = dl
            }
            continue
        }
        t := Timed(&*w)
        md := MainNext(&*w)
        if t >= 0 && (md < 0 || w.tasks.Get(t).until <= md) {
            if w.tasks.Get(t).until > w.clock {
                w.clock = w.tasks.Get(t).until
            }
            w.tasks.Mut(t).state = TReady
            continue
        }
        if md >= 0 {
            if md > w.clock {
                w.clock = md
            }
            continue
        }
        if AnyState(&*w, TAway) || !AnyState(&*w, TBlocked) {
            return
        }
        Deadlock(w)
        if w.pend.Len() > 0 {
            return
        }
    }
}

// IsHolder: the calling task holds this world's baton.
IsHolder(w: &World): bool {
    if !w.hooked || w.holder == 0 {
        return false
    }
    t := task.Current()
    return !raw.IsNull(t) && w.run == raw.Addr(t)
}

// Block parks the holder (the caller holds e's lock and IsHolder) in the
// state its record was given (blocked, or ready after a yield) until the
// baton comes back: the park's leave hook makes the choice, its enter hook
// waits for the grant. False when the Sim died meanwhile (e not ok);
// otherwise e is locked again and the result's canceled is the deadlock
// rule's verdict.
Block(io: Io, e: mut Entry): bool {
    slot := e.slot
    w := WorldOf(&*e)
    i := FindT(&*w, w.holder)
    pw := if i >= 0 { w.tasks.Get(i).pw } else { 0 }
    // Loaded under the slot's lock, before Leave releases it: a Bump after
    // this load runs under that lock too, so it changes the word before
    // WaitWord's compare and the wait returns (Bump's no-lost-wake edge).
    cur := ParkAtomic(slot, pw).Load()
    Leave(&*e)
    _ = task.WaitWord(ParkWord(slot, pw), cur, false)
    *e = Enter(io)
    return e.ok
}

// BlockAs marks the holder blocked on kind (key, until) and parks it;
// Canceled when the Sim died or the run deadlocked, else None.
BlockAs(io: Io, e: mut Entry, kind: u32, key: uint, until: i64): core.SysErr {
    w := WorldOf(&*e)
    i := FindT(&*w, w.holder)
    if i < 0 {
        return core.SysErr.Canceled
    }
    // a cancellable wait in a cancelled scope answers Canceled before
    // it starts, as the native Io's does (task.IsCancelled honours the
    // NoCancel bit).
    if Halted(io) {
        return core.SysErr.Canceled
    }
    _ = ChangePoint(w)
    w.bseq += 1
    r := w.tasks.Mut(i)
    r.wio = some(io)
    r.state = TBlocked
    r.kind = kind
    r.key = key
    r.until = until
    r.seq = w.bseq
    r.canceled = false
    w.events += 1
    if !Block(io, e) {
        return core.SysErr.Canceled
    }
    w2 := WorldOf(&*e)
    j := FindT(&*w2, w2.holder)
    if j < 0 {
        return core.SysErr.Canceled
    }
    if w2.tasks.Get(j).canceled {
        w2.tasks.Mut(j).canceled = false
        return core.SysErr.Canceled
    }
    return core.SysErr.None
}

// WakeNet makes every task blocked on the network runnable: a send, a
// close, a dial or a reset may be what it waits for; each retries.
WakeNet(w: mut World) {
    if !w.hooked {
        return
    }
    for i in 0..w.tasks.Len() {
        r := w.tasks.Mut(i)
        if r.state == TBlocked && r.kind == BNet {
            r.state = TReady
            w.events += 1
        }
    }
    if w.mwait > 0 {
        WakeMains(w)
    }
}

// WakeMains wakes every native waiter (main's waits on MainWords[slot]).
// Each one waiting for an event runs again before any choice: it owes a
// note from now (MainWait's Owe), so a task of the run that blocks before
// the waiter is back is no deadlock. A sleeping waiter only rechecks its
// deadline (MainDue readies it).
WakeMains(w: mut World) {
    if w.hooked && w.tasks.Len() > 0 {
        for i in 0..w.mains.Len() {
            m := w.mains.Get(i)
            if m.due < 0 {
                AddPend(w, m.a)
            }
        }
    }
    BumpMain(w.slot)
}

// MainDue readies every sleeping native waiter whose deadline the clock
// has reached: it is pending from now, so the choice waits for its next
// leaf, and it runs before the tasks then ready. True when one was.
MainDue(w: mut World): bool {
    var due := false
    for i in 0..w.mains.Len() {
        m := w.mains.Get(i)
        if m.due >= 0 && m.due <= w.clock {
            AddPend(w, m.a)
            due = true
        }
    }
    if due {
        BumpMain(w.slot)
    }
    return due
}

// MainNext is the earliest deadline of a sleeping native waiter, or -1.
MainNext(w: &World): i64 {
    var b: i64 = -1
    for i in 0..w.mains.Len() {
        d := w.mains.Get(i).due
        if d >= 0 && (b < 0 || d < b) {
            b = d
        }
    }
    return b
}

// WakeAddr makes the tasks blocked on addr runnable, in the order they
// blocked: one, or all.
WakeAddr(w: mut World, addr: uint, all: bool) {
    for {
        var b := -1
        for i in 0..w.tasks.Len() {
            t := w.tasks.Get(i)
            if t.state == TBlocked && t.kind == BWord && t.key == addr &&
                (b < 0 || t.seq < w.tasks.Get(b).seq) {
                b = i
            }
        }
        if b < 0 {
            return
        }
        w.tasks.Mut(b).state = TReady
        w.events += 1
        if !all {
            return
        }
    }
}

// Settle chooses when an entry outside a hook made a task runnable while
// the baton was free (main woke one, or closed a stream).
Settle(io: Io, e: mut Entry) {
    w := WorldOf(&*e)
    if w.hooked && w.holder == 0 {
        Decide(&*e)
    }
}

// ---------------------------------------------------------------------------
// The hooks (IoTable.enter and leave).

SimEnter(io: Io, id: u64) {
    var e := Enter(io)
    if !e.ok {
        return
    }
    var w := WorldOf(&e)
    var i := FindT(&*w, id)
    if i < 0 {
        // A task whose birth found no memory for its record.
        i = AddT(w, id, TReady)
        if i < 0 {
            Leave(&e)
            return
        }
        w.events += 1
    }
    w.hooked = true
    if w.tasks.Get(i).home == 0 {
        w.tasks.Mut(i).home = task.HookHome(task.Current())
    }
    w.tasks.Mut(i).nt = raw.Addr(task.Current())
    w.tasks.Mut(i).here = true
    st := w.tasks.Get(i).state
    if st == TNew || st == TAway || st == TBack {
        w.tasks.Mut(i).state = TReady
        w.events += 1
    }
    // A TJoin task back before its wait was satisfied (its native wait
    // returned early) waits here, still TJoin, until Resolve readies it.
    for {
        if w.holder == id {
            w.run = raw.Addr(task.Current())
            Leave(&e)
            return
        }
        if w.holder == 0 {
            Decide(&e)
            if w.holder == id {
                continue
            }
        }
        pw := w.tasks.Get(FindT(&*w, id)).pw
        // Under the slot's lock, as in Block: Bump's no-lost-wake edge.
        cur := ParkAtomic(e.slot, pw).Load()
        slot := e.slot
        Leave(&e)
        _ = task.WaitWord(ParkWord(slot, pw), cur, false)
        e = Enter(io)
        if !e.ok {
            return
        }
        w = WorldOf(&e)
        if FindT(&*w, id) < 0 {
            // The run was reset under the same token: nothing to wait for.
            Leave(&e)
            return
        }
    }
}

// SimBorn registers a task reserved under this run (on its spawner's
// stack); a spawner outside the baton owes a note before any choice.
SimBorn(w: mut World, id: u64) {
    if FindT(&*w, id) < 0 {
        _ = AddT(w, id, TNew)
    }
    w.hooked = true
    w.events += 1
    sp := task.Current()
    if !raw.IsNull(sp) && !IsHolder(&*w) {
        AddPend(w, raw.Addr(sp))
    }
}

// SimEnd removes a task whose function returned (on its own stack); the
// baton, if it held it, is free.
SimEnd(w: mut World, id: u64) {
    i := FindT(&*w, id)
    if i >= 0 {
        _ = w.tasks.RemoveAt(i)
    }
    w.events += 1
    if w.holder == id {
        w.holder = 0
        w.run = 0
    }
}

SimLeave(io: Io, x: u64) {
    // A plain leave into a Join or gate of a scope not of this run
    // (another Sim's, a native one) is a wait the Sim cannot see: the task
    // is TAway, not TJoin, which such a scope satisfies at once (it has no
    // member here), so the task would be TBack and the run make no choice
    // until it came back, while that wait may need this run's choices
    // through another Sim's JoinRec for it.
    // RunKey takes slot locks, so it is read before Enter.
    var foreign := false
    if (x & task.NoteBits) == 0 {
        cur := task.Current()
        if task.HookHold(cur) != 0 {
            // An Io RunKey cannot resolve (a tape chain past its bound)
            // is not taken for foreign: only a native scope or another
            // live Sim's run is.
            hio := task.HookHoldIo(cur)
            hk := RunKey(hio)
            foreign = (hk != 0 && hk != RunKey(io)) || core.IsNative(hio)
        }
    }
    var e := Enter(io)
    if !e.ok {
        return
    }
    w := WorldOf(&e)
    fact := x & task.NoteBits
    id := x & ~task.NoteBits
    if fact == task.BornBit {
        SimBorn(w, id)
        Leave(&e)
        return
    }
    if fact == task.NoteBits {
        cur := task.Current()
        a := raw.Addr(cur)
        hold := task.HookHold(cur)
        if hold != 0 && FindT(&*w, id) < 0 {
            // A native task (main) is about to wait in a Join or a
            // limit gate of hold, a wait the Sim does not see. When hold
            // is a scope of this run it owes another note (on its own
            // stack, no lock held), so once the run satisfies the wait
            // (ResolveJoins) no choice is made until it is back at its
            // next leaf. Its record, and its pending as a spawner, stay
            // until the lock is held again, so no choice, and no
            // deadlock, falls between: a task of hold may end meanwhile.
            // Any other scope, a native one or another Sim's, holds
            // nothing: the Sim cannot see that wait.
            lim := task.HookHoldLimit(cur)
            Leave(&e)
            ours := RunKey(task.HookHoldIo(cur)) == RunKey(io)
            owed := ours && task.HookOwe(io)
            e = Enter(io)
            if !e.ok {
                return
            }
            w2 := WorldOf(&e)
            if !owed && FindPend(&*w2, a) < 0 && FindJoin(&*w2, a) < 0 {
                // Nothing of this run waits for the task: a note of
                // another run's through the fan.
                Leave(&e)
                return
            }
            DropPend(w2, a)
            var k := FindJoin(&*w2, a)
            if k >= 0 && (!owed || w2.joins.Get(k).hold != hold || w2.joins.Get(k).limit != lim) {
                _ = w2.joins.RemoveAt(k)
                k = -1
            }
            if owed && k < 0 {
                // Out of memory: the joiner is not waited for (a deadlock
                // may then be called while it returns).
                _ = w2.joins.Push(JoinRec(a: a, hold: hold, limit: lim, done: false))
            }
            if w2.holder == 0 {
                Decide(&e)
            }
            Leave(&e)
            return
        }
        if FindPend(&*w, a) < 0 && FindJoin(&*w, a) < 0 {
            // Nothing of this run waits for the task: a note of another
            // run's through the fan, or a task of the run's own.
            Leave(&e)
            return
        }
        DropJoin(w, a)
        DropPend(w, a)
        if w.holder == 0 {
            Decide(&e)
        }
        Leave(&e)
        return
    }
    if fact == task.LastBit {
        SimEnd(w, id)
        if w.holder == 0 {
            Decide(&e)
        }
        Leave(&e)
        return
    }
    i := FindT(&*w, id)
    if i >= 0 {
        w.tasks.Mut(i).here = false
    }
    if w.holder != id {
        Leave(&e)
        return
    }
    if i >= 0 && w.tasks.Get(i).state == TRun {
        cur := task.Current()
        hold := task.HookHold(cur)
        r := w.tasks.Mut(i)
        if hold != 0 && !foreign {
            r.state = TJoin
            r.hold = hold
            r.limit = task.HookHoldLimit(cur)
        } else {
            r.state = TAway
        }
    }
    w.holder = 0
    w.run = 0
    w.events += 1
    Decide(&e)
    Leave(&e)
}

// The note fan: task.HookNote sends a note here, besides the two
// Ios a task keeps owed, when a third Io was owed one since its last leaf
// (task.OweNote keeps no third). The fan hands it to every live Sim, since
// each Io a task owes a note behind std.sim (a Sim's own, a NoCancel of
// it, a Recorder's over it) ends at one, and a Sim that waits for nothing
// of that task ignores it (SimLeave). Only its leave is ever called; an Io
// of it has flags 0, so every other entry answers Canceled (Enter).
const FanTable = core.IoTable(open: SimOpen, read: SimRead, write: SimWrite, size: SimSize,
    sync: SimSync, close: SimClose, rename: SimRename, remove: SimRemove, listen: SimListen,
    accept: SimAccept, dial: SimDial, recv: SimRecv, send: SimSend, now: SimNow,
    sleep: SimSleep, random: SimRandom, yield: SimYield, signal: SimSignal, wait: SimWait,
    wake: SimWake, mkdir: SimMkdir, stat: SimStat, opendir: SimOpenDir, readdir: SimReadDir,
    udp: SimUdp, sendto: SimSendTo, recvfrom: SimRecvFrom, resolve: SimResolve, spawn: SimSpawn,
    procwait: SimProcWait, prockill: SimProcKill, inherited: SimInherited,
    enter: core.InertHook, leave: FanLeave, deadline: SimDeadline, stdio: SimStdio,
    call: SimCall)

// FanSet makes FanTable std.task's note fan (Sim.New). O(1).
FanSet() {
    unsafe "FanTable is a static IoTable, live for the whole program" {
        task.NoteFanSet(raw.Of(&FanTable))
    }
}

// FanLeave sends note x to each live Sim (a tape's slot forwards to a Sim
// of its own). A slot freed or restarted meanwhile ignores it: its Io's
// token no longer matches. O(SlotCount), a slot lock each.
FanLeave(io: Io, x: u64) {
    if (x & task.NoteBits) != task.NoteBits {
        return
    }
    for i in 0..SlotCount {
        LockSlot(i)
        t := SlotToken(i)
        tape := SlotTape(i)
        UnlockSlot(i)
        if t != 0 && !tape {
            SimLeave(IoFor(i, t), x)
        }
    }
}
