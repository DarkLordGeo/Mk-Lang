// Why: the terminal-restore registry must restore every registered mode
// exactly once per exit path, under races between registrants, holders and
// several exit paths, and the only proof is the marks the restores write.
// What: the RT8-Term-* blocks over std/rt/term.mk: exits, panics, removed
// and held records, stale flags, kitty pops, SIGCONT continues, and a
// multi-thread stress block.
// How: each block registers TermKindTest records, whose restore writes the
// record's bytes to standard error, then leaves by one exit path;
// std/rt_term_test.go runs each block and counts the marks (once each,
// none for a removed record, twice for the record a slow exit path and the
// one that took it over both restore). The end-to-end case on proc.Pty is
// std.term's.

import std.core
import std.raw
import std.sys

// TermMark is a TermKindTest record that writes text to standard error,
// and leaves the alternate screen there when alt.
TermMark(text: str, alt: bool): TermSaved {
    var r := TermSaved(kind: sys.TermKindTest, h: sys.StdHandle(2), out: sys.StdHandle(2), alt: alt, mouse: false,
        len: text.Len(), mode: [0; 64])
    b := text.AsBytes()
    for i in 0..b.Len() {
        r.mode[i] = b[i]
    }
    return r
}

test "RT8-Term-Exit [exit 3: TERMX]" {
    Assert(TermRegister(TermMark("TERMX\n", false)) >= 0)
    Exit(3)
}

test "RT8-Term-Panic [exit 101: TERMP]" {
    Assert(TermRegister(TermMark("TERMP\n", false)) >= 0)
    // A failed Assert is a panic (exit 101).
    var no := false
    Assert(no)
}

test "RT8-Term-Removed [exit 4: TERMK]" {
    id := TermRegister(TermMark("TERMR\n", false))
    Assert(id >= 0)
    Assert(TermRemove(id))
    Assert(!TermRemove(id))
    Assert(TermRegister(TermMark("TERMK\n", false)) >= 0)
    Exit(4)
}

test "RT8-Term-Alt [exit 5: TERMA]" {
    id := TermRegister(TermMark("TERMA\n", false))
    Assert(id >= 0)
    Assert(TermSetAlt(id, true))
    Exit(5)
}

test "RT8-Term-Mouse [exit 8: TERMM]" {
    // Mouse capture and the alternate screen on: the exit path turns the
    // capture off, then leaves the alternate screen.
    id := TermRegister(TermMark("TERMM\n", false))
    Assert(id >= 0)
    Assert(TermSetMouse(id, true))
    Assert(TermSetAlt(id, true))
    Exit(8)
}

test "RT8-Term-MouseOnly [exit 9: TERMO]" {
    // A record that holds only the mouse flag (no mode bytes) is restored
    // too; a second record whose capture was turned off writes no off
    // sequence.
    id := TermRegister(TermMark("", false))
    Assert(id >= 0)
    Assert(TermSetMouse(id, true))
    off := TermRegister(TermMark("TERMO\n", false))
    Assert(off >= 0)
    Assert(TermSetMouse(off, true))
    Assert(TermSetMouse(off, false))
    Exit(9)
}

test "RT8-Term-HeldRestore [exit 7: TERMQ]" {
    // A record another exit path took (Restoring) and never finished, as
    // when exit_group or a second fatal signal ends that thread mid-
    // restore: this exit path restores it again rather than skipping it.
    id := TermRegister(TermMark("TERMQ\n", false))
    Assert(id >= 0)
    Assert(Terms.state[id].Cas(TermReady, TermRestoring))
    Exit(7)
}

test "RT8-Term-Kitty [exit 14: TERMJ]" {
    // Kitty flags left pushed on both screens, with mouse capture
    // and the alternate screen on. The exit path turns capture off, pops
    // the alternate screen's one flag, leaves the screen, then pops the
    // main screen's two (std/rt_term_test.go checks the order).
    id := TermRegister(TermMark("TERMJ\n", false))
    Assert(id >= 0)
    Assert(TermSetMouse(id, true))
    Assert(TermSetAlt(id, true))
    Assert(TermSetKitty(id, 2, 1))
    Assert(Terms.state[id].Load() == TermReady | TermAltBit | TermMouseBit | (2 << TermKittyMainShift) | (1 << TermKittyAltShift))
    // A flag change keeps the counts, and a count change the flags.
    Assert(TermSetAlt(id, false))
    Assert(TermSetAlt(id, true))
    Assert(Terms.state[id].Load() == TermReady | TermAltBit | TermMouseBit | (2 << TermKittyMainShift) | (1 << TermKittyAltShift))
    Exit(14)
}

test "RT8-Term-KittyMain [exit 15: TERMU]" {
    // A record that holds only kitty flags on the main screen (no mode, no
    // alternate screen): its count, clamped to TermKittyMax, is popped,
    // and the alternate screen's count is not, since that screen is not in
    // force. A negative count is 0; an id that is no record is refused.
    id := TermRegister(TermMark("", false))
    Assert(id >= 0)
    Assert(TermSetKitty(id, -3, -1))
    Assert(Terms.state[id].Load() == TermReady)
    Assert(TermSetKitty(id, 1000, 7))
    Assert(Terms.state[id].Load() == TermReady | (255 << TermKittyMainShift) | (7 << TermKittyAltShift))
    Assert(!TermSetKitty(-1, 1, 1))
    Assert(!TermSetKitty(TermSlots, 1, 1))
    mark := TermRegister(TermMark("TERMU\n", false))
    Assert(mark >= 0)
    Exit(15)
}

test "RT8-Term-KittyHeld [exit 16: TERMZ]" {
    // The exit path that took the record (by hand here) writes its pops,
    // then clears the counts in its Restoring word; an exit path that
    // restores it again past TermHoldNs pops nothing more. The mark is
    // written twice, the pop once.
    id := TermRegister(TermMark("TERMZ\n", false))
    Assert(id >= 0)
    Assert(TermSetKitty(id, 1, 0))
    s := Terms.state[id].Load()
    held := (s & ~TermStateMask) | TermRestoring
    Assert(Terms.state[id].Cas(s, held))
    Assert(!TermSetKitty(id, 0, 0))
    TermRestoreRec(id, s, true)
    Assert(Terms.state[id].Load() == TermRestoring)
    Exit(16)
}

test "RT8-Term-Continue [exit 17: TERMC]" {
    // The SIGCONT hook, called as sys's handler calls it: raw mode
    // is set again (TERMRAW, a TermKindTest record's mark) once, on the
    // terminal of the one Ready record whose raw flag is set, and on no
    // other: not a record never raw (TERMB), one whose flag was cleared
    // (TERMD), one removed while raw. Once an exit path latched, a continue
    // sets nothing, and one already past its latch check puts the saved
    // mode back after its raw set (TERMRAW and TERMD once more). The flag
    // shares the word with the other flags, and the exit path restores
    // each record once.
    a := TermRegister(TermMark("TERMC\n", false))
    Assert(a >= 0)
    Assert(TermSetRaw(a, true))
    Assert(TermSetMouse(a, true))
    Assert(Terms.state[a].Load() == TermReady | TermRawBit | TermMouseBit)
    Assert(TermSetMouse(a, false))
    b := TermRegister(TermMark("TERMB\n", false))
    Assert(b >= 0)
    gone := TermRegister(TermMark("", false))
    Assert(gone >= 0)
    Assert(TermSetRaw(gone, true))
    Assert(TermRemove(gone))
    d := TermRegister(TermMark("TERMD\n", false))
    Assert(d >= 0)
    Assert(TermSetRaw(d, true))
    Assert(TermSetRaw(d, false))
    Assert(Terms.state[d].Load() == TermReady)
    TermContinue()
    Assert(TermContinuing.Load() == 0)
    TermExiting.Store(1)
    TermContinue()
    Assert(TermContinuing.Load() == 0)
    // A continue the latch found already past its check (TermAgain, called
    // by hand): it puts the saved mode back after its raw set,
    // so TERMD is written here and again by Exit's restore.
    TermAgain(d)
    Assert(!TermSetRaw(-1, true))
    Assert(!TermSetRaw(TermSlots, true))
    Exit(17)
}

// RT8-Term-ContinueHeld's waiter: the record's id, the step it is in
// (TermCwIn) and the last it finished (TermCwDone), and the step it may
// start (TermCwGo).
var TermCwId: raw.Atomic[u32] = raw.Atomic[u32].New(0)
var TermCwIn: raw.Atomic[u32] = raw.Atomic[u32].New(0)
var TermCwDone: raw.Atomic[u32] = raw.Atomic[u32].New(0)
var TermCwGo: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// TermCwRun is the waiter's thread: step 1 clears the raw flag, step 2
// removes the record, step 3 registers a new one, each when TermCwGo
// allows it; each must wait for the continue the block holds open.
TermCwRun(p: *u8) {
    id := int(TermCwId.Load())
    for n in 1..4 {
        for TermCwGo.Load() < Trunc[u32](n) {
            sys.ThreadYield()
        }
        TermCwIn.Store(Trunc[u32](n))
        if n == 1 {
            Assert(TermSetRaw(id, false))
        } else if n == 2 {
            Assert(TermRemove(id))
        } else {
            Assert(TermRegister(TermMark("TERMH\n", false)) >= 0)
        }
        TermCwDone.Store(Trunc[u32](n))
    }
}

// TermCwStart starts TermCwRun's thread (a test block's sys names its
// System, so the module is reached from here).
TermCwStart() {
    unsafe "the thread reads only globals" {
        Assert(sys.ThreadStart(TermCwRun, raw.Null[u8](), 1 << 20).err == core.SysErr.None)
    }
}

// TermCwStep runs the block's step n: a continue held open, the waiter let
// into step n, still in it 20 ms later, then done once the continue ends.
TermCwStep(n: u32) {
    _ = TermContinuing.Add(1)
    TermCwGo.Store(n)
    for TermCwIn.Load() < n {
        sys.ThreadYield()
    }
    t0 := sys.Now()
    for sys.Now() - t0 < 20_000_000 {
        sys.ThreadYield()
    }
    Assert(TermCwDone.Load() == n - 1)
    _ = TermContinuing.Add(0xFFFF_FFFF)
    for TermCwDone.Load() < n {
        sys.ThreadYield()
    }
}

// TermContSignal sends SIGCONT to this process on Linux and waits up to 5
// s for its count, which the handler moves after the hook ran; elsewhere
// it calls the hook as the handler would.
TermContSignal() {
    if Target.os != core.Os.Linux {
        TermContinue()
        return
    }
    Assert(sys.Raise(4))
    t0 := sys.Now()
    for sys.SignalCount(4) == 0 && sys.Now() - t0 < 5_000_000_000 {
        sys.ThreadYield()
    }
    Assert(sys.SignalCount(4) == 1)
}

test "RT8-Term-ContinueHeld [exit 18: TERMH]" {
    // A continue in progress (its count raised by hand, as if its thread
    // stalled inside TermContinue). Clearing the raw flag, removing
    // the record and claiming a slot each wait for it on another thread,
    // without a bound, and go on once it ends (the Continue invariant); a step
    // that did not wait would be done 20 ms after it started. The exit
    // path waits for one only up to TermHoldNs: Exit ends the process with
    // the count still raised, and restores TERMH once (TERME was removed).
    id := TermRegister(TermMark("TERME\n", false))
    Assert(id >= 0)
    Assert(TermSetRaw(id, true))
    TermCwId.Store(Trunc[u32](id))
    TermCwStart()
    for n in 1..4 {
        TermCwStep(Trunc[u32](n))
    }
    _ = TermContinuing.Add(1)
    Exit(18)
}

test "RT8-Term-ContinueSignal [exit 19: TERMI]" {
    // On Linux a real SIGCONT, sent to this process: the registry's first
    // record installed SIGCONT's handler (sys.TermContinueInstall), which
    // runs the hook before it counts the signal, so once the count moves
    // the raw mark is written. Windows sends no SIGCONT: the block calls
    // the hook as the handler would.
    id := TermRegister(TermMark("TERMI\n", false))
    Assert(id >= 0)
    Assert(TermSetRaw(id, true))
    TermContSignal()
    Exit(19)
}

// RT8-Term-ContinueWait's continue: 1 once its count is raised.
var TermCcUp: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// TermCcRun is RT8-Term-ContinueWait's continue, on its own thread: it
// counts itself in TermContinuing as a running TermContinue does, waits
// until an exit path latched TermExiting, then 5 ms more (well within
// TermHoldNs), writes TERMHOOK to standard error and drops the count.
TermCcRun(p: *u8) {
    _ = TermContinuing.Add(1)
    TermCcUp.Store(1)
    for TermExiting.Load() == 0 {
        sys.ThreadYield()
    }
    t0 := sys.Now()
    for sys.Now() - t0 < 5_000_000 {
        sys.ThreadYield()
    }
    _ = sys.WriteSync(sys.StdHandle(2), "TERMHOOK\n".AsBytes(), -1)
    _ = TermContinuing.Add(0xFFFF_FFFF)
}

// TermCcStart starts TermCcRun's thread and returns once its count is
// raised (a test block's sys names its System, so the module is reached
// from here).
TermCcStart() {
    unsafe "the thread reads only globals" {
        Assert(sys.ThreadStart(TermCcRun, raw.Null[u8](), 1 << 20).err == core.SysErr.None)
    }
    for TermCcUp.Load() == 0 {
        sys.ThreadYield()
    }
}

test "RT8-Term-ContinueWait [exit 20: TERMT]" {
    // The exit path waits for a continue in progress before its restores,
    // up to TermHoldNs: a continue on another thread that ends 5
    // ms after the latch writes its mark before the record's restore
    // writes TERMT (std/rt_term_test.go checks the order). An exit path
    // that did not wait would restore first, or end the process before
    // the continue's mark.
    id := TermRegister(TermMark("TERMT\n", false))
    Assert(id >= 0)
    TermCcStart()
    Exit(20)
}

test "RT8-Term-AltBack TermSetAlt gives the record back Ready, and Remove takes it" {
    id := TermRegister(TermMark("", false))
    Assert(id >= 0)
    Assert(TermSetAlt(id, true))
    Assert(Terms.state[id].Load() == TermReady | TermAltBit)
    Assert(TermSetAlt(id, false))
    Assert(TermSetMouse(id, true))
    Assert(Terms.state[id].Load() == TermReady | TermMouseBit)
    Assert(TermRemove(id))
    Assert(Terms.state[id].Load() == TermFree)
    Assert(!TermSetAlt(id, true))
    Assert(!TermSetMouse(id, true))
}

test "RT8-Term-Full the table holds four records and refuses a fifth" {
    var ids: [4]int = [0; 4]
    for i in 0..4 {
        ids[i] = TermRegister(TermMark("", false))
        Assert(ids[i] >= 0)
    }
    Assert(TermRegister(TermMark("", false)) == -1)
    for i in 0..4 {
        Assert(TermRemove(ids[i]))
    }
}

// TermAllFree asserts every slot but skip (-1 for none) is Free.
TermAllFree(skip: int) {
    for j in 0..TermSlots {
        if j != skip {
            Assert(Terms.state[j].Load() == TermFree)
        }
    }
}

test "RT8-Term-SlowRestore [exit 10: TERMW]" {
    // A slow, live holder: an exit path took the record (its Cas
    // from Ready to Restoring, by hand here) and stalls past TermHoldNs.
    // Another exit path restores the record again and leaves it Restoring;
    // registration is refused from then on (the Latch invariant); the stalled
    // path then finishes, restores once more and frees the slot. Both
    // restores read the flags from the word, mouse capture's set after the
    // registration, so each writes the off sequence and leaves the
    // alternate screen.
    id := TermRegister(TermMark("TERMW\n", true))
    Assert(id >= 0)
    Assert(TermSetMouse(id, true))
    s := Terms.state[id].Load()
    Assert(s == TermReady | TermAltBit | TermMouseBit)
    held := (s & ~TermStateMask) | TermRestoring
    Assert(Terms.state[id].Cas(s, held))
    Assert(!TermSetAlt(id, false))
    Assert(!TermRemove(id))
    TermRestoreAll()
    Assert(Terms.state[id].Load() == held)
    Assert(TermRegister(TermMark("TERMN\n", false)) == -1)
    TermAllFree(id)
    TermRestoreSlot(id, s)
    Assert(Terms.state[id].Load() == TermFree)
    Assert(TermRegister(TermMark("TERMN\n", false)) == -1)
    Assert(!TermSetMouse(id, false))
    Assert(!TermRemove(id))
    TermAllFree(-1)
    Exit(10)
}

test "RT8-Term-StaleFlag [exit 11: TERMF]" {
    // A TermSetAlt that loaded the Ready word and stalls before its Cas,
    // while an exit path restores and frees the record: the slot is not
    // registered again (the Latch invariant), so the stalled Cas misses, and no
    // record is changed by it.
    id := TermRegister(TermMark("TERMF\n", false))
    Assert(id >= 0)
    s := Terms.state[id].Load()
    Assert(s == TermReady)
    TermRestoreAll()
    Assert(Terms.state[id].Load() == TermFree)
    again := TermRegister(TermMark("TERMG\n", false))
    Assert(!TermFlagCas(id, s, TermAltBit, true))
    Assert(again == -1)
    Assert(!TermSetAlt(id, true))
    TermAllFree(-1)
    Exit(11)
}

test "RT8-Term-HeldWrite [exit 12: TERMV]" {
    // A registrant that claimed its slot before the latch and stalls past
    // TermHoldNs (here this very thread, as when a signal interrupts a
    // TermRegister): the exit path waits out the bound and skips the slot,
    // which was never published. Only a later exit path restores it, when
    // there is one (a fatal signal during the panic report, or a console
    // handler's thread beside rt.Exit; SlowRestore models such a pair);
    // Exit is that later path here, and restores it once.
    first := TermRegister(TermMark("", false))
    Assert(first >= 0)
    Assert(TermRemove(first))
    i := TermClaim()
    Assert(i >= 0)
    Assert(Terms.state[i].Load() == TermWriting)
    TermRestoreAll()
    Assert(Terms.state[i].Load() == TermWriting)
    TermFill(i, TermMark("TERMV\n", false))
    Assert(Terms.state[i].Load() == TermReady)
    Exit(12)
}

// TermOneWay is RT8-Term-HeldWriteOneWay's claimant's progress: 1 once it
// claimed its slot, 2 when the claim failed.
var TermOneWay: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// TermOneWayFill claims a slot, says so, then fills it 2 ms later, well
// within TermHoldNs: a registrant on another thread, inside TermRegister
// when the exit path starts.
TermOneWayFill(p: *u8) {
    i := TermClaim()
    if i < 0 {
        TermOneWay.Store(2)
        return
    }
    TermOneWay.Store(1)
    t0 := sys.Now()
    for sys.Now() - t0 < 2_000_000 {
        sys.ThreadYield()
    }
    TermFill(i, TermMark("TERMY\n", false))
}

test "RT8-Term-HeldWriteOneWay [exit 13: TERMY]" {
    // A registrant on another thread claimed its slot before the latch and
    // fills it while the one exit path (Exit's, which ends the process)
    // runs: the exit path waits for the Writing slot within TermHoldNs and
    // restores the record once it is Ready. An exit path that
    // skipped Writing slots would end the process with it unrestored.
    first := TermRegister(TermMark("", false))
    Assert(first >= 0)
    Assert(TermRemove(first))
    TermOneWayStart()
    Assert(TermOneWay.Load() == 1)
    Exit(13)
}

// TermOneWayStart starts TermOneWayFill's thread and waits for its claim
// (a test block's sys names its System, so the module is reached from
// here).
TermOneWayStart() {
    unsafe "the thread reads only globals" {
        Assert(sys.ThreadStart(TermOneWayFill, raw.Null[u8](), 1 << 20).err == core.SysErr.None)
    }
    for TermOneWay.Load() == 0 {
        sys.ThreadYield()
    }
}

test "RT8-Term-Signal [exit 143: TERMS]" {
    Assert(TermRegister(TermMark("TERMS\n", false)) >= 0)
    // Terminate, unwatched: the fatal path restores, then the process ends
    // by the signal (Linux), or as its default handler would (Windows).
    TermDie()
}

// TermDie raises Terminate at this process and waits for its end (a test
// block's sys names its System, so the module is reached from here).
TermDie() {
    Assert(sys.RaiseFatal(1))
    for {
        sys.ThreadYield()
    }
}

// A stress block for the terminal-restore registry under real threads
// (tests/bench-lanes/rt-term74/ keeps an instrumented form, which also
// counts each record's restores). This form needs no change to
// std/sys: it checks only the slots' states, which is enough for the
// latch, the takeover and the Writing wait.
//
// Threads: three registrants, one thread changing the alt flag of every
// slot, two holders (TermRegister, TermSetAlt, TermSetMouse, TermRemove on
// their own records), and three exit-path threads (TermRestoreAll), of
// which a round runs one, two or three. Records are of a kind no target
// restores, so a restore is a few atomics (and a failed write of the
// alternate-screen or mouse exit to handle 0).
//
// Mode 0: every slot Ready when the round starts, so only an exit path
// frees one: a registration that succeeds claimed a slot an exit path
// freed (the Latch invariant broken). Modes 1 and 2: two slots Ready, two Free,
// and in mode 2 the holders: a slot still Ready after the round is a
// record registered before the latch that no exit path restored
// (the All invariant; the Writing wait), and a slot left Restoring or Writing
// is a slot no one freed. Which two slots are Free rotates with the round,
// so in some rounds the Free slots come before the Ready ones in an exit
// path's pass. Mode 3: no slot registered when the round starts, and the
// registrants register and at once remove until an exit-path thread is
// about to call TermRestoreAll (estarted), then keep what they register:
// slot 0 keeps changing between Free and held as the pass reaches it, so
// a pass that loads a slot before it stores the latch lets a claim in
// between and skips it, a record no exit path restores. The test resets
// the latch between rounds, which no real program does.
//
// Its size: 30,000 rounds, or as many as about 3 s allow but at least
// 200; it writes the rounds run to standard error. A round is excused from
// the unrestored count when one of its registrations took longer than
// TermHoldNs from its claim to its fill (a registrant descheduled past the
// exit path's bound, as on a loaded machine with few CPUs: the exit path
// may then rightly skip it); the excused rounds are written too.

// TermStState is the block's shared state.
struct TermStState {
    round: raw.Atomic[u32]
    quit: raw.Atomic[u32]
    started: raw.Atomic[u32]
    edone: raw.Atomic[u32]
    done: raw.Atomic[u32]
    left: raw.Atomic[u32]
    mode: raw.Atomic[u32]
    nexit: raw.Atomic[u32]
    bad: raw.Atomic[u32]
    slow: raw.Atomic[u32]
    estarted: raw.Atomic[u32]
}

var TermSt: TermStState = TermStState(round: raw.Atomic[u32].New(0), quit: raw.Atomic[u32].New(0),
    started: raw.Atomic[u32].New(0), edone: raw.Atomic[u32].New(0), done: raw.Atomic[u32].New(0),
    left: raw.Atomic[u32].New(0), mode: raw.Atomic[u32].New(0), nexit: raw.Atomic[u32].New(1), bad: raw.Atomic[u32].New(0),
    slow: raw.Atomic[u32].New(0), estarted: raw.Atomic[u32].New(0))

// The non-exit threads (three registrants, the flipper, two holders), and
// all of them.
const TermStWorkers: u32 = 6
const TermStThreads: u32 = 9

// TermStRec is a record of a kind no target restores.
TermStRec(): TermSaved {
    return TermSaved(kind: 99, h: 0, out: 0, alt: false, mouse: false, len: 0, mode: [0; 64])
}

// TermStWait waits for a round other than seen; 0 once quit is set.
TermStWait(seen: u32): u32 {
    for {
        if TermSt.quit.Load() != 0 {
            return 0
        }
        r := TermSt.round.Load()
        if r != seen {
            return r
        }
        sys.ThreadYield()
    }
}

// TermStExitsDone is whether the round's exit paths returned.
TermStExitsDone(): bool {
    return TermSt.edone.Load() >= TermSt.nexit.Load()
}

// TermStNext is one xorshift step.
TermStNext(x: u64): u64 {
    var y := x ^ (x << 13)
    y = y ^ (y >> 7)
    return y ^ (y << 17)
}

// TermStRegister is TermRegister's claim and fill (the hook is already
// set), timed as TermOneWayFill's are: a claim-to-fill gap over TermHoldNs
// marks the round slow.
TermStRegister(): int {
    t0 := sys.Now()
    i := TermClaim()
    if i >= 0 {
        TermFill(i, TermStRec())
        if sys.Now() - t0 > TermHoldNs {
            TermSt.slow.Store(1)
        }
    }
    return i
}

// TermStNow is the clock (a test block's sys names its System).
TermStNow(): i64 {
    return sys.Now()
}

// TermStNote writes "rt8-term-stress <what> <n>" and a line feed to
// standard error.
TermStNote(what: str, n: int) {
    var b: [96]u8 = [0; 96]
    var k := 0
    pre := "rt8-term-stress ".AsBytes()
    for i in 0..pre.Len() {
        b[k] = pre[i]
        k += 1
    }
    w := what.AsBytes()
    for i in 0..w.Len() {
        if k < 60 {
            b[k] = w[i]
            k += 1
        }
    }
    b[k] = 32
    k += 1
    var d: [20]u8 = [0; 20]
    var m := 0
    var x := if n < 0 { 0 } else { n }
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

// TermStSpin spins about n atomic loads.
TermStSpin(n: int) {
    for _ in 0..n {
        _ = TermSt.round.Load()
    }
}

TermStReg(p: *u8) {
    var seen: u32 = 0
    for {
        r := TermStWait(seen)
        if r == 0 {
            break
        }
        seen = r
        mode := TermSt.mode.Load()
        _ = TermSt.started.Add(1)
        if mode != 2 {
            // Registers until the exit paths are done, then 64 more times.
            var extra := 0
            for extra < 64 {
                id := TermStRegister()
                if id >= 0 && mode == 0 {
                    _ = TermSt.bad.Add(1)
                }
                if id >= 0 && mode == 3 && TermSt.estarted.Load() == 0 {
                    _ = TermRemove(id)
                }
                if TermStExitsDone() {
                    extra += 1
                }
            }
        }
        _ = TermSt.done.Add(1)
    }
    _ = TermSt.left.Add(1)
}

TermStFlip(p: *u8) {
    var seen: u32 = 0
    for {
        r := TermStWait(seen)
        if r == 0 {
            break
        }
        seen = r
        _ = TermSt.started.Add(1)
        var k := 0
        var extra := 0
        for extra < 64 {
            _ = TermSetAlt(k % 4, (k & 4) != 0)
            k += 1
            if TermStExitsDone() {
                extra += 1
            }
        }
        _ = TermSt.done.Add(1)
    }
    _ = TermSt.left.Add(1)
}

// TermStHoldRun is a holder: in mode 2 it registers a record, changes its
// flags and removes it, seven times, with varying pauses.
TermStHoldRun(me: u64) {
    var seen: u32 = 0
    var x: u64 = 0x1234_5678_9ABC_DEF1 + me * 77
    for {
        r := TermStWait(seen)
        if r == 0 {
            break
        }
        seen = r
        mode := TermSt.mode.Load()
        _ = TermSt.started.Add(1)
        if mode == 2 {
            for _ in 0..7 {
                x = TermStNext(x)
                TermStSpin(Trunc[int](x % 600))
                id := TermStRegister()
                if id >= 0 {
                    _ = TermSetAlt(id, true)
                    _ = TermSetMouse(id, true)
                    x = TermStNext(x)
                    TermStSpin(Trunc[int](x % 300))
                    _ = TermRemove(id)
                }
            }
        }
        _ = TermSt.done.Add(1)
    }
    _ = TermSt.left.Add(1)
}

TermStHold0(p: *u8) {
    TermStHoldRun(0)
}

TermStHold1(p: *u8) {
    TermStHoldRun(1)
}

// TermStExitRun waits until the other threads run the round, spins a
// varying while, then runs the exit path when it is one of the round's.
TermStExitRun(me: u32) {
    var seen: u32 = 0
    var x: u64 = 0x9E37_79B9_7F4A_7C15 + u64(me)
    for {
        r := TermStWait(seen)
        if r == 0 {
            break
        }
        seen = r
        for TermSt.started.Load() < TermStWorkers {
            sys.ThreadYield()
        }
        x = TermStNext(x)
        TermStSpin(Trunc[int](x % 2000))
        if me < TermSt.nexit.Load() {
            TermSt.estarted.Store(1)
            TermRestoreAll()
            _ = TermSt.edone.Add(1)
        }
        _ = TermSt.done.Add(1)
    }
    _ = TermSt.left.Add(1)
}

TermStExit0(p: *u8) {
    TermStExitRun(0)
}

TermStExit1(p: *u8) {
    TermStExitRun(1)
}

TermStExit2(p: *u8) {
    TermStExitRun(2)
}

// TermStUntil yields until c reaches n (a test block's sys names its
// System, so the module is reached from here).
TermStUntil(c: &raw.Atomic[u32], n: u32) {
    for c.Load() < n {
        sys.ThreadYield()
    }
}

TermStStart(f: fn(*u8)) {
    unsafe "the threads read only globals" {
        Assert(sys.ThreadStart(f, raw.Null[u8](), 1 << 20).err == core.SysErr.None)
    }
}

test "RT8-Term-Stress the registry under real threads: no registration after the latch, every record registered before it restored, every slot freed" {
    TermStStart(TermStReg)
    TermStStart(TermStReg)
    TermStStart(TermStReg)
    TermStStart(TermStFlip)
    TermStStart(TermStHold0)
    TermStStart(TermStHold1)
    TermStStart(TermStExit0)
    TermStStart(TermStExit1)
    TermStStart(TermStExit2)
    var unrestored := 0
    var held := 0
    var excused := 0
    var round := 0
    t0 := TermStNow()
    for round < 30000 && (round < 200 || TermStNow() - t0 < 3_000_000_000) {
        round += 1
        mode := Trunc[u32](round % 4)
        // Resets every slot, the latch and the round's counters.
        for j in 0..TermSlots {
            Terms.state[j].Store(TermFree)
        }
        TermExiting.Store(0)
        TermSt.edone.Store(0)
        TermSt.done.Store(0)
        TermSt.started.Store(0)
        TermSt.slow.Store(0)
        TermSt.estarted.Store(0)
        TermSt.mode.Store(mode)
        TermSt.nexit.Store(Trunc[u32](1 + (round / 3) % 3))
        // Every slot registered (ids 0 to 3 from a free table), then in
        // modes 1 and 2 the two slots k and k+1 freed, k rotating, and in
        // mode 3 all four.
        for j in 0..TermSlots {
            Assert(TermRegister(TermStRec()) == j)
        }
        if mode == 1 || mode == 2 {
            k := (round / 4) % TermSlots
            Assert(TermRemove(k))
            Assert(TermRemove((k + 1) % TermSlots))
        } else if mode == 3 {
            for j in 0..TermSlots {
                Assert(TermRemove(j))
            }
        }
        TermSt.round.Store(Trunc[u32](round))
        TermStUntil(&TermSt.done, TermStThreads)
        var ready := 0
        for j in 0..TermSlots {
            st := Terms.state[j].Load() & TermStateMask
            if st == TermReady {
                ready += 1
            } else if st != TermFree {
                held += 1
            }
        }
        if ready > 0 && TermSt.slow.Load() != 0 {
            excused += 1
        } else {
            unrestored += ready
        }
    }
    TermSt.quit.Store(1)
    TermStUntil(&TermSt.left, TermStThreads)
    for j in 0..TermSlots {
        Terms.state[j].Store(TermFree)
    }
    TermStNote("rounds", round)
    TermStNote("excused", excused)
    Assert(TermSt.bad.Load() == 0)
    Assert(unrestored == 0)
    Assert(held == 0)
}
