// Why: a Sim must know when main, outside its baton, waits in a Join or a
// limit gate of the run, or it calls a deadlock that main's return would
// have ended; the notes a spawner owes are how it knows.
// What: blocks on main reaching such a wait owing the run no note, a third
// Io owed a note between two of main's waits, three Sims owed one note,
// tasks of one Sim waiting on another Sim's scope, and scopes opened
// through chains of Recorders; helpers NtRun, NtThird, NtCross, NtDeep.
// How: the native runs are the oracle. A wait for tasks outside every run
// (a native scope's) and two Sims joined across each other are waits the
// Sim cannot see, a documented limit. JnServer, JnNap, JnQuick and JnAddr
// are tests_join.mk's.

import std.atomic
import std.core
import std.net
import std.raw
import std.task
import std.time

// NtAway is a task of the run away in native waits (TAway), so no
// deadlock is called while it sleeps: it polls, 1 ms at a time and at most
// 2,000 times, until the Sim behind sio holds main (task record at a) as
// a native joiner, then returns. Over an Io of no live Sim (the native
// oracle) it sleeps 5 ms once.
NtAway(nat: Io, sio: Io, a: uint) {
    if !NtLive(sio) {
        nat.Sleep(time.Millis(5)) else {}
        return
    }
    for _ in 0..2000 {
        if NtJoinOf(sio, a) {
            return
        }
        nat.Sleep(time.Millis(1)) else {}
    }
}

// NtLive: sio is an Io of a live Sim.
NtLive(sio: Io): bool {
    e := Enter(sio)
    Leave(&e)
    return e.ok
}

// NtJoinOf: the Sim behind sio holds the task at a as a native joiner.
NtJoinOf(sio: Io, a: uint): bool {
    var e := Enter(sio)
    if !e.ok {
        return false
    }
    w := WorldOf(&e)
    j := FindJoin(&*w, a)
    Leave(&e)
    return j >= 0
}

// NtRun: a server task reads a connection to its end while main, on the
// other end, sends its note at a native wait (a 1 us sleep of nat) and then
// waits for the run owing it no note: mode 0 in g's Join, mode 1 in g's
// limit gate (limit 1). Either way the task it waits for is away natively
// until the Sim records main's wait (NtAway, bounded), so main reaches the
// wait before that task ends, and a Sim that never records it lets the
// task end at the bound. Then main sends 5 bytes and closes: got * 16 +
// code of the server.
NtRun(io: Io, nat: Io, mode: int): net.Error!int {
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var ab: [32]u8 = [0; 32]
    addr := JnAddr(mut ab, l.Port())
    var got := 0
    var code := -1
    me := raw.Addr(task.Current())
    scope outer := io.Scope() {
        outer.go JnServer(io, &l, mut got, mut code)
        stream := try net.Dial(io, addr)
        c := stream.Conn()
        if mode == 0 {
            scope g := io.Scope() {
                g.go NtAway(nat, io, me)
                nat.Sleep(time.Micros(1)) else {}
            }
        } else {
            scope g := io.ScopeWith(core.Limits(tasks: 1, stack: core.StackSize.Default)) {
                g.go NtAway(nat, io, me)
                nat.Sleep(time.Micros(1)) else {}
                g.go JnQuick(io)
            }
        }
        hello: [5]u8 = [104, 101, 108, 108, 111]
        c.WriteAll(io, hello[..]) else {}
        c.Close(io) else {
            c.Abandon()
        }
    }
    return got * 16 + code
}

// NtRunSim is NtRun in a fresh Sim of seed; -100 when the run failed.
NtRunSim(a: &Arena, seed: u64, nat: Io, mode: int): int {
    var sm := Sim.New(a, seed) else {
        return -100
    }
    r := NtRun(sm.Io(), nat, mode) else {
        -101
    }
    sm.Free()
    return r
}

// NtWaits: whether the Sim behind sio waits for the calling task, as a
// pending spawner (pend) and as a native joiner (join); both false for an
// Io of no live Sim.
NtWaits(sio: Io): (pend: bool, join: bool) {
    var e := Enter(sio)
    if !e.ok {
        return (pend: false, join: false)
    }
    w := WorldOf(&e)
    a := raw.Addr(task.Current())
    r := (pend: FindPend(&*w, a) >= 0, join: FindJoin(&*w, a) >= 0)
    Leave(&e)
    return r
}

// NtThird: main joins a napping task of the run (a JoinRec, owed a note),
// then spawns into h, opened from x, and in h's body into k, opened from y,
// so three Ios are owed a note with no leaf between: the Sim's own (the
// Join's), x and y. kept is whether the Sim behind sio still waits for main
// as a joiner and a spawner right after the third spawn, before main's
// next leaf. Mode 0: x and y are io.NoCancel() taken at two depths. Mode 1:
// x is io and y is mio, a Recorder's Io under io (io a Recorder's Io over
// it). Then main sends 5 bytes and closes: v is got * 16 + code.
NtThird(io: Io, mio: Io, sio: Io, mode: int): net.Error!(v: int, kept: bool) {
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var ab: [32]u8 = [0; 32]
    addr := JnAddr(mut ab, l.Port())
    var got := 0
    var code := -1
    var kept := false
    scope outer := io.Scope() {
        outer.go JnServer(io, &l, mut got, mut code)
        stream := try net.Dial(io, addr)
        c := stream.Conn()
        scope g := io.Scope() {
            g.go JnNap(io)
        }
        x := if mode == 0 { io.NoCancel() } else { io }
        scope h := x.Scope() {
            h.go JnQuick(io)
            y := if mode == 0 { io.NoCancel() } else { mio }
            scope k := y.Scope() {
                k.go JnQuick(io)
                s := NtWaits(sio)
                kept = s.pend && s.join
            }
        }
        hello: [5]u8 = [104, 101, 108, 108, 111]
        c.WriteAll(io, hello[..]) else {}
        c.Close(io) else {
            c.Abandon()
        }
    }
    return (v: got * 16 + code, kept: kept)
}

// NtThirdSim is NtThird in a fresh Sim of seed: mode 0 on the Sim's Io,
// mode 1 on a Recorder over a Recorder over it; v is -100 when the run
// failed.
NtThirdSim(a: &Arena, seed: u64, mode: int): (v: int, kept: bool) {
    var sm := Sim.New(a, seed) else {
        return (v: -100, kept: false)
    }
    if mode == 0 {
        r := NtThird(sm.Io(), sm.Io(), sm.Io(), 0) else {
            (v: -101, kept: false)
        }
        sm.Free()
        return r
    }
    var r1 := Recorder.New(sm.Io()) else {
        sm.Free()
        return (v: -100, kept: false)
    }
    var r2 := Recorder.New(r1.Io()) else {
        r1.Free()
        sm.Free()
        return (v: -100, kept: false)
    }
    r := NtThird(r2.Io(), r1.Io(), sm.Io(), 1) else {
        (v: -101, kept: false)
    }
    r2.Free()
    r1.Free()
    sm.Free()
    return r
}

// NtThreeSims: main spawns a quick task into a scope of each of three
// Sims, nested, with no leaf between, and reports for each whether it still
// waits for main's note right after the third spawn (bit i for Sim i).
NtThreeSims(a: &Arena, seed: u64): int {
    var s0 := Sim.New(a, seed) else {
        return -100
    }
    var s1 := Sim.New(a, seed +% 1) else {
        s0.Free()
        return -100
    }
    var s2 := Sim.New(a, seed +% 2) else {
        s1.Free()
        s0.Free()
        return -100
    }
    i0 := s0.Io()
    i1 := s1.Io()
    i2 := s2.Io()
    var bits := 0
    scope a0 := i0.Scope() {
        a0.go JnQuick(i0)
        scope a1 := i1.Scope() {
            a1.go JnQuick(i1)
            scope a2 := i2.Scope() {
                a2.go JnQuick(i2)
                if NtWaits(i0).pend {
                    bits += 1
                }
                if NtWaits(i1).pend {
                    bits += 2
                }
                if NtWaits(i2).pend {
                    bits += 4
                }
            }
        }
    }
    s2.Free()
    s1.Free()
    s0.Free()
    return bits
}

test "under a Sim, main reaching a Join or a limit gate of the run owing it no note (its last leaf a native sleep), while the task it waits for is away natively until the Sim records that wait and the peer's Read is blocked: the peer reads the 5 bytes main sends next, as over the native Io, for 8 seeds, each run twice with the same result" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for mode in 0..2 {
        native := NtRun(sys.io, sys.io, mode) else e {
            ar.Free()
            fail e
        }
        Assert(native == 5 * 16)
        for seed in 1..9 {
            r := NtRunSim(&ar, Trunc[u64](seed), sys.io, mode)
            again := NtRunSim(&ar, Trunc[u64](seed), sys.io, mode)
            Assert(r == native && again == r)
        }
    }
    ar.Free()
}

test "under a Sim, a third Io of the run owed a note between two of main's waits (two NoCancel Ios at two depths beside the Sim's own; a Recorder over a Recorder over the Sim): the Sim still waits for main's next leaf, and the peer reads the 5 bytes main sends next, as over the native Io, for 16 seeds, each run twice with the same result" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for mode in 0..2 {
        native := NtThird(sys.io, sys.io, sys.io, mode) else e {
            ar.Free()
            fail e
        }
        Assert(native.v == 5 * 16)
        for seed in 1..17 {
            r := NtThirdSim(&ar, Trunc[u64](seed), mode)
            again := NtThirdSim(&ar, Trunc[u64](seed), mode)
            Assert(r.kept && again.kept)
            Assert(r.v == native.v && again.v == r.v)
        }
    }
    ar.Free()
}

test "three Sims owed main's note at once: none is sent before main's next leaf, so each Sim still waits for it after the third spawn" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for seed in 1..9 {
        Assert(NtThreeSims(&ar, Trunc[u64](seed)) == 7)
    }
    ar.Free()
}

// NtNap sleeps ms milliseconds on io's clock.
NtNap(io: Io, ms: i64) {
    io.Sleep(time.Millis(ms)) else {}
}

// NtNothing returns at once.
NtNothing(io: Io) {}

// NtCrossS, a task of Sim A (ai): opens a scope of Sim B (bi) holding a
// 1 ms nap of B; in mode 1 sleeps 1 us on A's clock first (a leaf, which
// sends the spawn's note), then sends on ch, a channel of A, and waits in
// the scope's Join, or with gate in its limit-1 gate (a second spawn).
NtCrossS(ai: Io, bi: Io, ch: &Chan[int], mode: int, gate: bool, done: &raw.Atomic[u32]) {
    scope g := bi.ScopeWith(core.Limits(tasks: 1, stack: core.StackSize.Default)) {
        g.go NtNap(bi, 1)
        if mode == 1 {
            ai.Sleep(time.Micros(1)) else {}
        }
        ch.Send(ai, 1) else {}
        if gate {
            g.go NtNothing(bi)
        }
    }
    done.Store(1)
}

// NtCrossR, a task of Sim A: receives on ch, then joins a scope of Sim B
// holding a 10 ms nap of B.
NtCrossR(ai: Io, bi: Io, ch: &Chan[int], done: &raw.Atomic[u32]) {
    _ = ch.Recv(ai) else {
        return
    }
    scope g := bi.Scope() {
        g.go NtNap(bi, 10)
    }
    done.Store(1)
}

// NtCross runs NtCrossS and NtCrossR as tasks of ai's run: 11 when both
// ended.
NtCross(h: &Heap, ai: Io, bi: Io, mode: int, gate: bool): u32 {
    a := h.Alloc()
    ch := Chan[int].New(a, 1) else {
        return 0
    }
    d1 := raw.Atomic[u32].New(0)
    d2 := raw.Atomic[u32].New(0)
    scope ga := ai.Scope() {
        ga.go NtCrossS(ai, bi, &ch, mode, gate, &d1)
        ga.go NtCrossR(ai, bi, &ch, &d2)
    }
    ch.Free(a)
    return d1.Load() * 10 + d2.Load()
}

test "two tasks of one Sim wait in a Join or a limit gate of another Sim's scope, the second ready while the first waits: the program ends, as natively, for 6 seeds, the first task's spawn note sent with its wait or at a leaf before it" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for g in 0..2 {
        gate := g == 1
        for mode in 0..2 {
            Assert(NtCross(sys.heap, sys.io, sys.io, mode, gate) == 11)
            for seed in 1..7 {
                var sa := Sim.New(&ar, Trunc[u64](seed)) else e {
                    ar.Free()
                    fail e
                }
                var sb := Sim.New(&ar, Trunc[u64](seed) +% 77) else e {
                    sa.Free()
                    ar.Free()
                    fail e
                }
                r := NtCross(sys.heap, sa.Io(), sb.Io(), mode, gate)
                sb.Free()
                sa.Free()
                Assert(r == 11)
            }
        }
    }
    ar.Free()
}

// NtStuck receives on a channel of the run that nobody sends on, so the
// scope it is in is deadlocked: 5 when the Sim calls the deadlock (the
// Recv answers Canceled), 1 if it ever received.
NtStuck(io: Io, ch: &Chan[int], code: &atomic.AtomicInt) {
    _ = ch.Recv(io) else {
        code.Store(5)
        return
    }
    code.Store(1)
}

// NtDeepJoin is a task of the run: it opens a scope from deep, an Io
// some Recorders over the Sim's, spawns NtStuck into it and joins it.
NtDeepJoin(deep: Io, ch: &Chan[int], code: &atomic.AtomicInt) {
    scope g := deep.Scope() {
        g.go NtStuck(deep, ch, code)
    }
}

// NtDeep wraps io in n Recorders and runs NtDeepJoin under the last one,
// as a task of the Sim's own scope ai; NtStuck's code.
NtDeep(h: &Heap, ai: Io, io: Io, n: int): i64 {
    if n == 0 {
        a := h.Alloc()
        ch := Chan[int].New(a, 1) else {
            return -50
        }
        code := atomic.AtomicInt.New(-1)
        scope ga := ai.Scope() {
            ga.go NtDeepJoin(io, &ch, &code)
        }
        ch.Free(a)
        return code.Load()
    }
    var r := Recorder.New(io) else {
        return -60
    }
    v := NtDeep(h, ai, r.Io(), n - 1)
    r.Free()
    return v
}

test "a scope opened through 0, 7 or 8 Recorders over the Sim is the run's own: a task of the run that joins it while its member is deadlocked gets the deadlock reported, not a hang, for 3 seeds (8 is past RunKey's bound)" (sys: &System) {
    var ar := Arena.New(sys.heap).Must()
    defer ar.Free()
    depths: [3]int = [0, 7, 8]
    for d in 0..3 {
        for seed in 1..4 {
            var sa := Sim.New(&ar, Trunc[u64](seed)).Must()
            r := NtDeep(sys.heap, sa.Io(), sa.Io(), depths[d])
            sa.Free()
            Assert(r == 5)
        }
    }
}
