// Why: an Io table's enter and leave hooks (hooks.mk) let a scheduler such
// as std.sim see every suspension; a missed, doubled or nested call breaks it.
// What: the RT-Os-Hooks-* blocks and RT-Os-Inherit-Cancel, over the HkState
// counting table (HkTable, HkReset, HkFind, HkWork, HkCheck).
// How: a table with the native entries and counting hooks records every call
// per task; run by the scheduler harness at 1 to 8 carriers, both backends.

// The blocks check per task that the first call is its born leave (on the
// spawner's stack), then an enter, that enter and leave alternate, that the
// last call is a leave with LastBit, that every call runs on the task its id
// names, that the spawner's note runs on the spawner, and that a hook which
// itself suspends calls no hook. Dev and release builds both run them.

import std.core
import std.raw

// The records: 64 of 4 words each in the test's frame (HkBase holds its
// address): a task's id (0 while free), its enters, its leaves and its
// state (0 never seen, 3 born, 1 between enter and leave, 2 after a leave,
// 4 after the last leave).
struct HkState {
    base: raw.Atomic[u64]
    bad: raw.Atomic[u32]
    full: raw.Atomic[u32]
    // 1: enter suspends (a yield, then a cancellable 50 us sleep) before
    // it returns; 0: the hooks only count.
    suspend: raw.Atomic[u32]
    nested: raw.Atomic[u32]
    // The spawners' notes (NoteBits) seen.
    notes: raw.Atomic[u32]
}

var Hk: HkState = HkState(base: raw.Atomic[u64].New(0), bad: raw.Atomic[u32].New(0),
    full: raw.Atomic[u32].New(0), suspend: raw.Atomic[u32].New(0), nested: raw.Atomic[u32].New(0),
    notes: raw.Atomic[u32].New(0))

const HkMax: int = 64
const HkId: int = 0
const HkEnters: int = 1
const HkLeaves: int = 2
const HkStateW: int = 3

// HkW is word f of record i.
HkW(i: int, f: int): &raw.Atomic[u64] {
    unsafe "Hk.base is a live [256]u64 of the running test; an atomic u64 is a u64" {
        return &*raw.FromAddr[raw.Atomic[u64]](Trunc[uint](Hk.base.Load()) + Trunc[uint](8 * (4 * i + f)))
    }
}

const HkTable = core.IoTable(open: NativeOpen, read: NativeRead, write: NativeWrite, size: NativeSize,
    sync: NativeSync, close: NativeClose, rename: NativeRename, remove: NativeRemove, listen: NativeListen,
    accept: NativeAccept, dial: NativeDial, recv: NativeRecv, send: NativeSend, now: NativeNow,
    sleep: NativeSleep, random: NativeRandom, yield: NativeYield, signal: NativeSignal, wait: NativeWait,
    wake: NativeWake, mkdir: NativeMkdir, stat: NativeStat, opendir: NativeOpenDir, readdir: NativeReadDir,
    udp: NativeUdp, sendto: NativeSendTo, recvfrom: NativeRecvFrom, resolve: NativeResolveAll,
    spawn: NativeSpawn, procwait: NativeProcWait, prockill: NativeProcKill, inherited: NativeInherited,
    enter: HkEnter, leave: HkLeave, deadline: NativeDeadline, stdio: NativeStdio,
    call: NativeCall)

HkIo(): Io {
    unsafe "HkTable is a static IoTable, live for the whole program" {
        return core.MakeIo(raw.Of(&HkTable), 0)
    }
}

// HkReset points the records at mem, all free, and clears the counts.
HkReset(mem: mut []u64) {
    for i in 0..mem.Len() {
        mem[i] = 0
    }
    unsafe "mem outlives every hooked scope of the test" {
        Hk.base.Store(u64(raw.Addr(raw.Data(mem[..]))))
    }
    Hk.bad.Store(0)
    Hk.full.Store(0)
    Hk.nested.Store(0)
    Hk.notes.Store(0)
}

// HkFind is id's record, claimed on first sight; -1 when all are taken.
HkFind(id: u64): int {
    for i in 0..HkMax {
        if HkW(i, HkId).Load() == id || HkW(i, HkId).Cas(0, id) || HkW(i, HkId).Load() == id {
            return i
        }
    }
    _ = Hk.full.Add(1)
    return -1
}

// HkOnTask: the hook runs on the task whose id it was given.
HkOnTask(id: u64): bool {
    t := Current()
    return !raw.IsNull(t) && HookId(t) == id
}

HkEnter(io: Io, id: u64) {
    if !HkOnTask(id) {
        _ = Hk.bad.Add(1)
        return
    }
    r := HkFind(id)
    if r < 0 {
        return
    }
    // Born first (3), or after a plain leave (2).
    st := HkW(r, HkStateW).Load()
    if st != 3 && st != 2 {
        _ = Hk.bad.Add(1)
    }
    HkW(r, HkStateW).Store(1)
    _ = HkW(r, HkEnters).Add(1)
    if Hk.suspend.Load() == 1 {
        // A hook that waits: these leaves must call no hook (HookBit); a
        // nested enter would find state 1 above, a nested leave would
        // count a leave between this enter and its return.
        l0 := HkW(r, HkLeaves).Load()
        e0 := HkW(r, HkEnters).Load()
        _ = NativeYield(io)
        _ = NativeSleep(io, 50_000)
        if HkW(r, HkLeaves).Load() != l0 || HkW(r, HkEnters).Load() != e0 {
            _ = Hk.bad.Add(1)
        }
        _ = Hk.nested.Add(1)
    }
}

HkLeave(io: Io, x: u64) {
    id := x & ~NoteBits
    fact := x & NoteBits
    if fact == NoteBits {
        // The spawner's note: on the spawner, which is no record's task.
        if !HkOnTask(id) {
            _ = Hk.bad.Add(1)
        }
        _ = Hk.notes.Add(1)
        return
    }
    if fact == BornBit {
        // The birth: on the spawner's stack, the first call for id.
        if HkOnTask(id) {
            _ = Hk.bad.Add(1)
        }
        r := HkFind(id)
        if r < 0 {
            return
        }
        if HkW(r, HkStateW).Load() != 0 {
            _ = Hk.bad.Add(1)
        }
        HkW(r, HkStateW).Store(3)
        return
    }
    if !HkOnTask(id) {
        _ = Hk.bad.Add(1)
        return
    }
    r := HkFind(id)
    if r < 0 {
        return
    }
    if HkW(r, HkStateW).Load() != 1 {
        _ = Hk.bad.Add(1)
    }
    if fact == LastBit {
        HkW(r, HkStateW).Store(4)
    } else {
        HkW(r, HkStateW).Store(2)
    }
    _ = HkW(r, HkLeaves).Add(1)
}

// HkWork passes through every leaf: YieldNow (yield), SleepFor (sleep),
// WaitWord (a turn word shared with a partner), Blocking or Await (stat,
// and a UDP datagram to itself), rounds times.
HkWork(io: Io, turn: &raw.Atomic[u32], me: u32, rounds: int, udp: bool) {
    var h: uint = 0
    var to := core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0)
    to.ip[0] = 127
    to.ip[3] = 1
    if udp {
        u := NativeUdp(io, to)
        Assert(u.err == core.SysErr.None)
        h = Trunc[uint](u.value)
        to.port = Trunc[u16](u.os)
    }
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    var buf: [8]u8 = [0; 8]
    var from := core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0)
    for k in 0..rounds {
        Assert(NativeYield(io).err == core.SysErr.None)
        Assert(NativeSleep(io, Trunc[i64](k % 3) * 1_000).err == core.SysErr.None)
        AwaitWord(turn, me)
        SetWord(turn, 1 - me)
        Assert(NativeStat(io, ".", mut st).err == core.SysErr.None)
        if udp {
            buf[0] = Trunc[u8](k)
            Assert(NativeSendTo(io, h, buf[..], to).err == core.SysErr.None)
            r := NativeRecvFrom(io, h, mut buf[..], mut from)
            Assert(r.err == core.SysErr.None && r.value == 8 && buf[0] == Trunc[u8](k))
        }
    }
    if udp {
        Assert(NativeClose(io, h).err == core.SysErr.None)
    }
}

// HkCheck: every record that was claimed was born, alternated, ended after
// its last leave, and saw as many enters as leaves, at least least of each.
HkCheck(tasks: int, least: u64) {
    Assert(Hk.bad.Load() == 0)
    Assert(Hk.full.Load() == 0)
    var seen := 0
    for i in 0..HkMax {
        if HkW(i, HkId).Load() != 0 {
            seen += 1
            e := HkW(i, HkEnters).Load()
            Assert(e == HkW(i, HkLeaves).Load())
            Assert(e >= least)
            Assert(HkW(i, HkStateW).Load() == 4)
        }
    }
    Assert(seen == tasks)
}

test "RT-Os-Hooks-Pair: enter and leave alternate on each task of a hooked scope, through every leaf" (sys: &System) {
    io := HkIo()
    var mem: [256]u64 = [0; 256]
    Hk.suspend.Store(0)
    for _ in 0..4 {
        HkReset(mut mem[..])
        turns: [4]raw.Atomic[u32] = [raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
            raw.Atomic[u32].New(0)]
        scope s := io.Scope() {
            for p in 0..4 {
                s.go HkWork(io, &turns[p], 0, 50, p % 2 == 0)
                s.go HkWork(io, &turns[p], 1, 50, p % 2 == 1)
            }
        }
        // Each task: one enter first, one leave last, and at least the 2
        // leaf calls of each of its 50 rounds that always run one (the
        // yield and the stat; a sleep of 0 and a turn already ours call
        // no leaf).
        HkCheck(8, 101)
        // The body spawned 8 into io with no leaf between: one note, at
        // its Join's wait or at this yield, whichever comes first.
        _ = NativeYield(sys.io)
        Assert(Hk.notes.Load() == 1)
    }
    // A native scope calls no hook: its tasks never reach HkFind.
    HkReset(mut mem[..])
    turn := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go HkWork(sys.io, &turn, 0, 20, true)
        s.go HkWork(sys.io, &turn, 1, 20, false)
    }
    _ = NativeYield(sys.io)
    HkCheck(0, 0)
    Assert(Hk.notes.Load() == 0)
}

test "RT-Os-Hooks-Nested: an enter that suspends calls no hook, and still alternates" (sys: &System) {
    io := HkIo()
    var mem: [256]u64 = [0; 256]
    HkReset(mut mem[..])
    Hk.suspend.Store(1)
    turns: [3]raw.Atomic[u32] = [raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0)]
    scope s := io.Scope() {
        for p in 0..3 {
            s.go HkWork(io, &turns[p], 0, 20, p == 0)
            s.go HkWork(io, &turns[p], 1, 20, false)
        }
    }
    Hk.suspend.Store(0)
    HkCheck(6, 41)
    Assert(Hk.nested.Load() >= 6 * 41)
}

// HkSleeper sleeps in a loop until its sleep is Canceled, and counts its
// end.
HkSleeper(io: Io, ended: &raw.Atomic[u32]) {
    for {
        if NativeSleep(io, 200_000).err == core.SysErr.Canceled {
            break
        }
    }
    _ = ended.Add(1)
}

test "RT-Os-Hooks-Cancel: a scope cancelled while its tasks wait in their hooks ends, with every pair balanced" (sys: &System) {
    io := HkIo()
    var mem: [256]u64 = [0; 256]
    for round in 0..8 {
        HkReset(mut mem[..])
        // Odd rounds: the cancel finds most tasks inside enter's own
        // sleep (suspend 1), whose Canceled answer enter ignores.
        Hk.suspend.Store(Trunc[u32](round % 2))
        ended := raw.Atomic[u32].New(0)
        scope s := io.Scope() {
            for _ in 0..6 {
                s.go HkSleeper(io, &ended)
            }
            _ = NativeSleep(sys.io, Trunc[i64](round) * 300_000)
            s.Cancel()
        }
        Hk.suspend.Store(0)
        Assert(ended.Load() == 6)
        HkCheck(6, 1)
    }
}

// HkExact calls three leaves rounds times, each exactly once a round: a
// WaitWord on a word that already differs (it returns at once, and still
// passes its hooks), YieldNow and SleepFor.
HkExact(io: Io, rounds: int) {
    w := raw.Atomic[u32].New(1)
    for _ in 0..rounds {
        Assert(WaitWord(WordOf(&w), 0, true) == core.SysErr.None)
        Assert(NativeYield(io).err == core.SysErr.None)
        Assert(NativeSleep(io, 1_000).err == core.SysErr.None)
    }
}

test "RT-Os-Hooks-Count: each leaf call is one leave and one enter, plus the first enter and the last leave" (sys: &System) {
    io := HkIo()
    var mem: [256]u64 = [0; 256]
    HkReset(mut mem[..])
    scope s := io.Scope() {
        for k in 0..5 {
            s.go HkExact(io, 10 * (k + 1))
        }
    }
    HkCheck(5, 31)
    var sum: u64 = 0
    for i in 0..5 {
        sum += HkW(i, HkLeaves).Load()
    }
    // 3 leaves a round, rounds 10 to 50, and one more each at the end.
    Assert(sum == 3 * (10 + 20 + 30 + 40 + 50) + 5)
}

// HiAccept is one accept on the inherited listener l: res 1 when it took a
// connection (closed at once), 2 when Canceled, 3 any other answer.
HiAccept(io: Io, l: uint, res: &raw.Atomic[u32]) {
    a := NativeAccept(io, l)
    if a.err == core.SysErr.None {
        _ = NativeClose(io, Trunc[uint](a.value))
        res.Store(1)
    } else if a.err == core.SysErr.Canceled {
        res.Store(2)
    } else {
        res.Store(3)
    }
}

// HiRounds is the Inherit-Cancel block's child: rounds accepts on the
// inherited listener, each cancelled; in odd rounds the child first dials
// its own listener, so the cancel races a connection arriving. The exit
// code: 0, or 5 when handles leaked, 6 when an accept answered neither a
// connection nor Canceled, 7 when no round was cancelled.
HiRounds(io: Io, l: uint, port: i64, rounds: int): i32 {
    var before := 0
    var cancelled := 0
    for round in 0..rounds + 10 {
        if round == 10 {
            // After 10 warm-up rounds (pool threads, the poller's first
            // registrations), the handle count must stay.
            before = PHandles()
        }
        res := raw.Atomic[u32].New(0)
        var d: uint = 0
        scope s := io.Scope() {
            s.go HiAccept(io, l, &res)
            if round % 2 == 1 {
                var ab: [32]u8 = [0; 32]
                r := NativeDial(io, NAddr(mut ab, port))
                if r.err == core.SysErr.None {
                    d = Trunc[uint](r.value)
                }
            } else {
                _ = NativeSleep(io, Trunc[i64](round % 5) * 200_000)
            }
            s.Cancel()
        }
        if d != 0 {
            NAbort(io, d)
        }
        v := res.Load()
        if v != 1 && v != 2 {
            return 6
        }
        if v == 2 {
            cancelled += 1
        }
    }
    if cancelled == 0 {
        return 7
    }
    // A leak per cancel would be rounds or more; a pool thread started on
    // the way is 1 or 2 (TestRtIo_DirWindows' drift).
    return if PHandles() > before + 4 { 5 } else { 0 }
}

test "RT-Os-Inherit-Cancel 200 cancelled accepts on an inherited listener, half racing a dial, leak nothing" (sys: &System) {
    i := NativeInherited(sys.io)
    if i.err == core.SysErr.None {
        PExit(HiRounds(sys.io, Trunc[uint](i.value), i.os, 200))
    }
    Assert(i.err == core.SysErr.NotFound)
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    c := PChild(sys, lh, "C")
    Assert(PWait(sys.io, Trunc[uint](c.value)) == 0)
    PClose(sys.io, Trunc[uint](c.value))
    PClose(sys.io, lh)
}
