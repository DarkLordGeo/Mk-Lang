// Stress repro of std/rt/term.mk's restore registry, Windows only, not built
// by any test. RT8-Term-Stress in std/rt/tests_term.mk is its uninstrumented form,
// which std's tests run. To run this one: copy std to a scratch directory,
// apply instr-windows-term.diff (next to this file) to its
// sys/windows/term.mk, copy this file in as rt/tests_zrv74.mk, and run
// `mk test --std=std.rt -run RV74-` with MK_STD set to the copy (dev and
// --release).
//
//
// Stress blocks for std/rt/term.mk under real threads, not part of std's
// own tests. Copied into a scratch std as std/rt/tests_zrv74.mk,
// with std/sys/windows/term.mk instrumented: kind 98 counts its restores by
// tag (h), kind 97 does too and the first restorer of it stalls until the
// test releases it (sys.RvCount, sys.RvSlowGet, sys.RvSlowSet).
//
// Threads: three registrants (kind 99, no restore), one thread flipping the
// alt flag of every slot, two holders (Register, SetAlt, SetMouse, SetAlt,
// Remove on their own ids, kind 98 tags 0..13), three exit-path threads
// (TermRestoreAll), of which a round runs one, two or three.
//
// Mode 0: every slot Ready (kind 99) when the round starts, so only an exit
// path frees one: a TermRegister that succeeds claimed a slot an exit path
// freed (the latch broken). Mode 1: two slots Ready, two Free: a
// registration may claim a Free slot before the latch; a slot still Ready
// after the round is a registered record no exit path restored. Mode 2:
// two kind-98 records (tags 14, 15) and the holders: a removed record is
// never restored, every other registered one is restored by an exit
// path. Mode 3 (block RV74-Slow): slot 0 holds a kind-97 record
// whose first restorer stalls for 5 to 120 ms (a slow, live holder inside
// the real TermRestoreAll), slots 1 to 3 kind 98; three exit paths and the
// registrants: no registration succeeds, no takeover frees slot 0, every
// record is restored, every slot is Free after the round.

import std.core
import std.raw
import std.sys

struct RvState {
    round: raw.Atomic[u32]
    quit: raw.Atomic[u32]
    started: raw.Atomic[u32]
    edone: raw.Atomic[u32]
    done: raw.Atomic[u32]
    left: raw.Atomic[u32]
    mode: raw.Atomic[u32]
    nexit: raw.Atomic[u32]
    bad: raw.Atomic[u32]
    bad3: raw.Atomic[u32]
    legit: raw.Atomic[u32]
    flips: raw.Atomic[u32]
    refused: raw.Atomic[u32]
    regd: raw.Atomic[u32]
    removed: raw.Atomic[u32]
    altok: raw.Atomic[u32]
}

var Rv: RvState = RvState(round: raw.Atomic[u32].New(0), quit: raw.Atomic[u32].New(0), started: raw.Atomic[u32].New(0),
    edone: raw.Atomic[u32].New(0), done: raw.Atomic[u32].New(0), left: raw.Atomic[u32].New(0), mode: raw.Atomic[u32].New(0),
    nexit: raw.Atomic[u32].New(1), bad: raw.Atomic[u32].New(0), bad3: raw.Atomic[u32].New(0), legit: raw.Atomic[u32].New(0),
    flips: raw.Atomic[u32].New(0), refused: raw.Atomic[u32].New(0), regd: raw.Atomic[u32].New(0),
    removed: raw.Atomic[u32].New(0), altok: raw.Atomic[u32].New(0))

// The non-exit threads (three registrants, the flipper, two holders), and all.
const RvWorkers: u32 = 6
const RvThreads: u32 = 9

// RvRec is a record whose restore makes no system call (a kind no target
// restores), so an exit path's step per slot is a few atomics.
RvRec(): TermSaved {
    return TermSaved(kind: 99, h: 0, out: 0, alt: false, mouse: false, len: 0, mode: [0; 64])
}

// RvTagged is a record of kind (97 or 98) whose restores are counted by tag.
RvTagged(kind: u32, tag: int): TermSaved {
    return TermSaved(kind: kind, h: Trunc[uint](tag), out: 0, alt: false, mouse: false, len: 0, mode: [0; 64])
}

// RvWait waits for a round other than seen; 0 once quit is set.
RvWait(seen: u32): u32 {
    for {
        if Rv.quit.Load() != 0 {
            return 0
        }
        r := Rv.round.Load()
        if r != seen {
            return r
        }
        sys.ThreadYield()
    }
}

// RvExitsDone is whether the round's exit paths returned.
RvExitsDone(): bool {
    return Rv.edone.Load() >= Rv.nexit.Load()
}

// RvNext is one xorshift step.
RvNext(x: u64): u64 {
    var y := x ^ (x << 13)
    y = y ^ (y >> 7)
    return y ^ (y << 17)
}

// RvSpin spins about n atomic loads.
RvSpin(n: int) {
    for _ in 0..n {
        _ = Rv.round.Load()
    }
}

RvReg(p: *u8) {
    var seen: u32 = 0
    for {
        r := RvWait(seen)
        if r == 0 {
            break
        }
        seen = r
        mode := Rv.mode.Load()
        _ = Rv.started.Add(1)
        if mode != 2 {
            var extra := 0
            for extra < 256 {
                id := TermRegister(RvRec())
                if id >= 0 {
                    if mode == 0 {
                        _ = Rv.bad.Add(1)
                    } else if mode == 3 {
                        _ = Rv.bad3.Add(1)
                    } else {
                        _ = Rv.legit.Add(1)
                    }
                } else {
                    _ = Rv.refused.Add(1)
                }
                if RvExitsDone() {
                    extra += 1
                }
                if mode == 3 {
                    sys.ThreadYield()
                }
            }
        }
        _ = Rv.done.Add(1)
    }
    _ = Rv.left.Add(1)
}

RvFlip(p: *u8) {
    var seen: u32 = 0
    for {
        r := RvWait(seen)
        if r == 0 {
            break
        }
        seen = r
        mode := Rv.mode.Load()
        _ = Rv.started.Add(1)
        var k := 0
        var extra := 0
        if mode == 2 {
            extra = 256
        }
        for extra < 256 {
            if TermSetAlt(k % 4, (k & 4) != 0) {
                _ = Rv.flips.Add(1)
            }
            k += 1
            if RvExitsDone() {
                extra += 1
            }
            if mode == 3 {
                sys.ThreadYield()
            }
        }
        _ = Rv.done.Add(1)
    }
    _ = Rv.left.Add(1)
}

// RvHoldRun is holder me: in mode 2 it registers tags me*7 to me*7+6 in
// turn, changes the flags of each record and removes it.
RvHoldRun(me: int) {
    var seen: u32 = 0
    var x: u64 = 0x1234_5678_9ABC_DEF1 + u64(Trunc[u32](me)) * 77
    for {
        r := RvWait(seen)
        if r == 0 {
            break
        }
        seen = r
        mode := Rv.mode.Load()
        _ = Rv.started.Add(1)
        if mode == 2 {
            for t in me * 7..me * 7 + 7 {
                x = RvNext(x)
                RvSpin(Trunc[int](x % 600))
                id := TermRegister(RvTagged(98, t))
                if id >= 0 {
                    bit: u32 = Trunc[u32](1 << t)
                    _ = Rv.regd.Add(bit)
                    if TermSetAlt(id, true) {
                        _ = Rv.altok.Add(bit)
                    }
                    _ = TermSetMouse(id, true)
                    x = RvNext(x)
                    RvSpin(Trunc[int](x % 300))
                    if TermRemove(id) {
                        _ = Rv.removed.Add(bit)
                    }
                }
            }
        }
        _ = Rv.done.Add(1)
    }
    _ = Rv.left.Add(1)
}

RvHold0(p: *u8) {
    RvHoldRun(0)
}

RvHold1(p: *u8) {
    RvHoldRun(1)
}

// RvExitRun waits until the other threads run the round, spins a varying
// while, then runs the exit path when it is one of the round's.
RvExitRun(me: u32) {
    var seen: u32 = 0
    var x: u64 = 0x9E37_79B9_7F4A_7C15 + u64(me)
    for {
        r := RvWait(seen)
        if r == 0 {
            break
        }
        seen = r
        for Rv.started.Load() < RvWorkers {
            sys.ThreadYield()
        }
        x = RvNext(x)
        RvSpin(Trunc[int](x % 2000))
        if me < Rv.nexit.Load() {
            TermRestoreAll()
            _ = Rv.edone.Add(1)
        }
        _ = Rv.done.Add(1)
    }
    _ = Rv.left.Add(1)
}

RvExit0(p: *u8) {
    RvExitRun(0)
}

RvExit1(p: *u8) {
    RvExitRun(1)
}

RvExit2(p: *u8) {
    RvExitRun(2)
}

// RvNote writes "<what> <n>" to standard error.
RvNote(what: str, n: i64) {
    var b: [64]u8 = [0; 64]
    w := what.AsBytes()
    var k := 0
    for i in 0..w.Len() {
        if k < 40 {
            b[k] = w[i]
            k += 1
        }
    }
    b[k] = 32
    k += 1
    var d: [20]u8 = [0; 20]
    var m := 0
    var x := n
    if x < 0 {
        x = 0
    }
    for {
        d[m] = Trunc[u8](48 + x % 10)
        m += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..m {
        b[k] = d[m - 1 - i]
        k += 1
    }
    b[k] = 10
    k += 1
    WriteErr(b[..k])
}

RvStart(f: fn(*u8)): bool {
    unsafe "the threads read only globals" {
        return sys.ThreadStart(f, raw.Null[u8](), 1 << 20).err == core.SysErr.None
    }
}

RvStartAll() {
    Assert(RvStart(RvReg))
    Assert(RvStart(RvReg))
    Assert(RvStart(RvReg))
    Assert(RvStart(RvFlip))
    Assert(RvStart(RvHold0))
    Assert(RvStart(RvHold1))
    Assert(RvStart(RvExit0))
    Assert(RvStart(RvExit1))
    Assert(RvStart(RvExit2))
}

RvStopAll() {
    Rv.quit.Store(1)
    for Rv.left.Load() < RvThreads {
        sys.ThreadYield()
    }
}

// RvReset frees every slot, clears the latch and the round's counters.
RvReset(mode: u32, nexit: u32) {
    for j in 0..TermSlots {
        Terms.state[j].Store(TermFree)
    }
    TermExiting.Store(0)
    sys.RvCountReset()
    sys.RvSlowSet(0)
    Rv.edone.Store(0)
    Rv.done.Store(0)
    Rv.started.Store(0)
    Rv.regd.Store(0)
    Rv.removed.Store(0)
    Rv.altok.Store(0)
    Rv.mode.Store(mode)
    Rv.nexit.Store(nexit)
}

// RvRoundWait waits for every thread to finish the round.
RvRoundWait() {
    for Rv.done.Load() < RvThreads {
        sys.ThreadYield()
    }
}

test "RV74-Stress the restore registry under real threads" {
    RvStartAll()
    rounds := 30000
    var unrestored0 := 0
    var unrestored1 := 0
    var held := 0
    var once := 0
    var lost := 0
    var phantom := 0
    var multi := 0
    var regs2 := 0
    var removes2 := 0
    var flagbad := 0
    var altseen := 0
    for round in 1..rounds + 1 {
        mode := Trunc[u32](round % 3)
        nexit := Trunc[u32](1 + (round / 3) % 3)
        RvReset(mode, nexit)
        if mode == 0 {
            for _ in 0..4 {
                Assert(TermRegister(RvRec()) >= 0)
            }
        } else if mode == 1 {
            for _ in 0..2 {
                Assert(TermRegister(RvRec()) >= 0)
            }
        } else {
            Assert(TermRegister(RvTagged(98, 14)) >= 0)
            Assert(TermRegister(RvTagged(98, 15)) >= 0)
        }
        Rv.round.Store(Trunc[u32](round))
        RvRoundWait()
        for j in 0..TermSlots {
            st := Terms.state[j].Load() & TermStateMask
            if st == TermReady {
                if mode == 0 {
                    unrestored0 += 1
                } else {
                    unrestored1 += 1
                }
            } else if st != TermFree {
                held += 1
            }
        }
        if mode == 2 {
            regd := Rv.regd.Load()
            removed := Rv.removed.Load()
            altok := Rv.altok.Load()
            for t in 0..14 {
                bit: u32 = Trunc[u32](1 << t)
                c := sys.RvCount(t)
                if (regd & bit) != 0 {
                    regs2 += 1
                    if (removed & bit) != 0 {
                        removes2 += 1
                        if c != 0 {
                            once += 1
                        }
                    } else if c == 0 {
                        lost += 1
                    } else {
                        if c > 1 {
                            multi += 1
                        }
                        // Old defect (c): the flag the holder's Cas set
                        // reaches every restore after it.
                        a := sys.RvAltCount(t)
                        if (altok & bit) != 0 {
                            altseen += 1
                            if a != c {
                                flagbad += 1
                            }
                        } else if a != 0 {
                            flagbad += 1
                        }
                    }
                } else if c != 0 {
                    phantom += 1
                }
            }
            for t in 14..16 {
                c := sys.RvCount(t)
                if c == 0 {
                    lost += 1
                } else if c > 1 {
                    multi += 1
                }
            }
        }
    }
    RvStopAll()
    RvNote("rv74-rounds", i64(rounds))
    RvNote("rv74-bad-registrations-mode0", i64(Rv.bad.Load()))
    RvNote("rv74-unrestored-mode0", i64(unrestored0))
    RvNote("rv74-legit-registrations-mode1", i64(Rv.legit.Load()))
    RvNote("rv74-unrestored-ready-mode1-2", i64(unrestored1))
    RvNote("rv74-held-at-end", i64(held))
    RvNote("rv74-mode2-holder-registrations", i64(regs2))
    RvNote("rv74-mode2-holder-removes", i64(removes2))
    RvNote("rv74-mode2-removed-but-restored", i64(once))
    RvNote("rv74-mode2-registered-never-restored", i64(lost))
    RvNote("rv74-mode2-restored-twice", i64(multi))
    RvNote("rv74-mode2-phantom-restores", i64(phantom))
    RvNote("rv74-mode2-taken-with-alt", i64(altseen))
    RvNote("rv74-mode2-wrong-alt-at-restore", i64(flagbad))
    RvNote("rv74-refused", i64(Rv.refused.Load()))
    RvNote("rv74-flips", i64(Rv.flips.Load()))
    for j in 0..TermSlots {
        Terms.state[j].Store(TermFree)
    }
    Assert(Rv.bad.Load() == 0)
    Assert(unrestored0 == 0)
    Assert(held == 0)
    Assert(once == 0)
    Assert(lost == 0)
    Assert(phantom == 0)
    Assert(flagbad == 0)
}

test "RV74-Slow a slow, live holder inside TermRestoreAll, three exit paths, registrants" {
    RvStartAll()
    rounds := 48
    var freed := 0
    var notall := 0
    var lost := 0
    var c0long := 0
    var c0short := 0
    var c0other := 0
    var multi := 0
    for round in 1..rounds + 1 {
        RvReset(3, 3)
        Assert(TermRegister(RvTagged(97, 0)) == 0)
        for t in 1..4 {
            Assert(TermRegister(RvTagged(98, t)) == t)
        }
        Rv.round.Store(Trunc[u32](round))
        for sys.RvSlowGet() != 1 {
            sys.ThreadYield()
        }
        k := round % 4
        hold: i64 = if k == 0 { 5_000_000 } else if k == 1 { 30_000_000 } else if k == 2 { 70_000_000 } else { 120_000_000 }
        t0 := sys.Now()
        for sys.Now() - t0 < hold {
            sys.ThreadYield()
        }
        // The stalled restorer holds slot 0: no takeover may have freed it.
        if (Terms.state[0].Load() & TermStateMask) != TermRestoring {
            freed += 1
        }
        sys.RvSlowSet(2)
        RvRoundWait()
        for j in 0..TermSlots {
            if Terms.state[j].Load() != TermFree {
                notall += 1
            }
        }
        c := sys.RvCount(0)
        if hold >= 70_000_000 {
            if c == 3 {
                c0long += 1
            } else {
                c0other += 1
            }
        } else if hold <= 30_000_000 {
            if c == 1 {
                c0short += 1
            } else {
                c0other += 1
            }
        }
        for t in 1..4 {
            ct := sys.RvCount(t)
            if ct == 0 {
                lost += 1
            } else if ct > 1 {
                multi += 1
            }
        }
    }
    RvStopAll()
    RvNote("rv74-slow-rounds", i64(rounds))
    RvNote("rv74-slow-bad-registrations", i64(Rv.bad3.Load()))
    RvNote("rv74-slow-held-slot-freed-by-taker", i64(freed))
    RvNote("rv74-slow-slots-not-free-after", i64(notall))
    RvNote("rv74-slow-slot0-3-restores-long-hold", i64(c0long))
    RvNote("rv74-slow-slot0-1-restore-short-hold", i64(c0short))
    RvNote("rv74-slow-slot0-other-count", i64(c0other))
    RvNote("rv74-slow-others-never-restored", i64(lost))
    RvNote("rv74-slow-others-restored-twice", i64(multi))
    for j in 0..TermSlots {
        Terms.state[j].Store(TermFree)
    }
    Assert(Rv.bad3.Load() == 0)
    Assert(freed == 0)
    Assert(notall == 0)
    Assert(lost == 0)
}
