// Why: a program that puts the terminal in raw mode or on the alternate
// screen must not leave it that way when it panics, calls proc.Exit or dies
// of a signal; only the runtime sees all of those ends.
//
// What: the terminal-restore registry: TermSaved, TermRegister,
// TermRemove, the flag setters (TermSetAlt, TermSetMouse, TermSetKitty,
// TermSetRaw), TermRestoreAll, TermExitHook and TermContinue.
//
// How: std.term registers each terminal mode it changes (raw mode, the
// alternate screen) and removes the record when it puts the mode back
// itself; the runtime restores every record still registered when the
// process ends abnormally: on a panic (before the report, so the report is
// readable), in rt.Exit (which proc.Exit calls), and on a fatal signal it
// handles. On Linux it also sets raw mode again on SIGCONT for the records
// std.term marks raw, since the shell that stopped the program may
// have put line mode back and a program that never reads again has no
// other point where that is undone. std-internal: user code cannot import
// std.rt.
//
// The table is a fixed array of TermSlots records, each with a state word
// that holds the slot's state, the record's alt, mouse and raw flags and
// its two counts of kitty keyboard flags left pushed; every
// transition is one compare-and-swap or a store by the slot's one owner,
// so the restore path takes no lock and allocates nothing (a signal
// handler, or a panic on a task that was inside TermRegister, may run
// it). A flag changes by one Cas of the word from Ready to Ready, so no
// thread holds a record while it changes a flag, and the exit path that
// takes the record reads the flags in the word its own Cas read.
// Invariant Once: a record is restored by the exit path that
// wins its Cas from Ready to Restoring, which alone frees the slot after
// its restore, and never after TermRemove took it from Ready to Free.
// Invariant All: an exit path returns only once every record
// registered before it latched TermExiting has been restored by it or by
// another exit path, unless its holder stalled past TermHoldNs while
// still registering it. A record another exit path holds (Restoring), or
// one still being registered (Writing), is waited for, up to TermHoldNs
// in all. Past the bound a Restoring record (its holder is this very
// thread, interrupted by a signal, or a thread the exit is ending) is
// restored again and left Restoring, for its holder to free if it runs
// again; a Writing record is skipped, since its registrant has not
// published it. The restore is idempotent (the saved termios or console
// modes set once more, the alternate screen left once more), so a record
// may be restored once by each exit path that reaches it while it is
// Ready or Restoring. The kitty pops are the exception: a pop
// written twice pops one entry more, which on a stack the program's own
// pushes emptied changes nothing, and else pops an entry the program's
// parent pushed on that screen. The exit path that took the record clears
// the counts in its word once its pops are written, so a takeover past
// the bound pops again only when that path stalled before or within them:
// pops too many in a rare case, never one too few. How many: a takeover
// after a stall between the leave and the clear reloads the word with the
// alternate screen's bit and both counts, so it writes that screen's pops
// again, which land on the main screen's stack (the terminal has left the
// alternate screen), and the main screen's pops again: up to the two
// counts' sum extra pops on the main stack. Two records of one terminal
// restored in slot order (std.term's Track registers the new record
// before it removes the old one) do the same: the second's
// alternate-screen pops land on the main stack after the first's leave.
// std.term registers a mode before it sets it, so a
// record restored before its mode is set puts back the mode in force.
// Invariant Latch: the first exit path latches TermExiting, and
// every claim of a slot (TermClaim) that loads it set answers -1: no slot
// an exit path freed is registered again, so no exit path that restores a
// record again reads it while it is rewritten, and no stale TermSetAlt or
// TermSetMouse finds the word it loaded in a new record. The latch is
// never cleared. That is sound because an exit path ends the process:
// rt.Exit and the panic path always do. Three ways leave the process
// alive with the registry latched, so every later TermRegister answers -1
// and std.term's later raw sessions are restored only by its own Restore
// and Close: on Windows a console handler that runs after the
// registry's (one added before it) handles the event, or a SignalWatch
// for the event lands between TermCtrlHandler's check and MkCtrlHandler;
// on Linux a SignalWatch lands between SigFatal's reset to the default
// action and its kill, so the signal is counted instead. An exit path
// restores the records in slot order, 0 to TermSlots - 1, so two records of
// one terminal (two std.term Terms, the second's holding the first's
// changed mode) leave the mode of whichever is restored last; std.term's
// header says so.
// Invariant Continue: TermContinue, the SIGCONT hook,
// sets raw mode again on the terminals of the Ready records whose raw flag
// is set (TermSetRaw), and never takes or writes a record. It counts
// itself in TermContinuing before it loads anything, and each path that
// must not meet it stores first and then waits for the count to drop:
// TermClaim before it rewrites a slot (so the hook reads whole records),
// TermRemove before it returns (so std.term closes no handle the hook
// still uses), TermSetRaw clearing the flag (so std.term's restore comes
// after any raw set), each without a bound, since a continue is a few
// system calls; and TermRestoreAll after its latch, up to TermHoldNs. The
// hook does nothing once the latch is set, and a hook the bound let run on
// puts the saved mode back after its raw one, while the process lives: a
// continue on another thread preempted past TermHoldNs just before its
// raw set can land that set between the exit path's restore and the
// process's end, which leaves the terminal raw. A continue on the exit
// path's own thread, interrupted by the fatal signal, never resumes, and
// one whose set landed before the restore is undone by it. Tests:
// "RT8-Term-Exit", "RT8-Term-Panic", "RT8-Term-Removed",
// "RT8-Term-HeldRestore", "RT8-Term-SlowRestore", "RT8-Term-StaleFlag",
// "RT8-Term-HeldWrite", "RT8-Term-HeldWriteOneWay", "RT8-Term-Stress",
// "RT8-Term-Kitty", "RT8-Term-KittyMain", "RT8-Term-KittyHeld",
// "RT8-Term-Continue", "RT8-Term-ContinueHeld", "RT8-Term-ContinueSignal",
// "RT8-Term-ContinueWait"
// (std/rt/tests_term.mk, counted by std/rt_term_test.go).

import std.raw
import std.sys

/// TermSaved is one saved terminal mode: kind (sys.TermKindTermios,
/// TermKindConsole, TermKindTest), the handle the mode belongs to, the
/// output handle the alternate-screen exit and the mouse-capture exit are
/// written to, whether the alternate screen is on, whether mouse capture
/// is on, and the mode's bytes (a termios is 60 bytes on Linux;
/// two console modes are 8 on Windows; none for a record that only leaves
/// the alternate screen or mouse capture). alt and mouse are the flags at
/// registration: TermRegister moves them into the record's state word,
/// which TermSetAlt and TermSetMouse change and the exit paths read. A
/// record starts with no kitty flags pushed; TermSetKitty sets its counts.
export struct TermSaved {
    export kind: u32
    export h: uint
    export out: uint
    export alt: bool
    export mouse: bool
    export len: int
    export mode: [64]u8
}

// The number of records: std.term holds one per open terminal, and a
// program has at most a few.
const TermSlots: int = 4

// Slot states, the low bits of a state word (TermStateMask). Writing:
// TermRegister fills the record, which is not whole yet (an exit path
// waits for its Ready within TermHoldNs, then skips it). Ready:
// registered. Restoring: an exit path restores it.
const TermFree: u32 = 0
const TermWriting: u32 = 1
const TermReady: u32 = 2
const TermRestoring: u32 = 3
const TermStateMask: u32 = 7

// The flag bits of a Ready or Restoring word: the alternate screen is on,
// mouse capture is on (TermSaved's alt and mouse).
const TermAltBit: u32 = 8
const TermMouseBit: u32 = 16

// The kitty keyboard flags a std.term Writer pushed and has not popped,
// one count per screen, since the main and alternate screens keep
// separate stacks: the alternate screen's in bits 8 to 15, the main
// screen's in bits 16 to 23, each at most TermKittyMax.
const TermKittyAltShift: u32 = 8
const TermKittyMainShift: u32 = 16
const TermKittyMax: int = 255
const TermKittyBits: u32 = 0xFF_FF00

// TermRawBit: std.term's Raw mode is in force on the record's terminal
// (TermSetRaw), so a SIGCONT sets raw mode again there.
const TermRawBit: u32 = 32

// MouseOff turns off what std.term's MouseCapture(w, true) turns on: focus
// reports, SGR mouse encoding, button-motion and button tracking, in the
// reverse order (std/term/input_mouse.mk's MouseOff). The exit paths write
// it before they leave the alternate screen.
const MouseOff: str = "\x1b[?1004l\x1b[?1006l\x1b[?1002l\x1b[?1000l"

// KittyPop pops one entry of the kitty keyboard protocol's stack of the
// screen in force (std/term/input_kitty.mk's KittyKeys(w, false)).
const KittyPop: str = "\x1b[<u"

// TermAltLeave leaves the alternate screen (xterm's 1049, as std/sys's
// AltLeave): the exit paths write it themselves, between the two screens'
// kitty pops.
const TermAltLeave: str = "\x1b[?1049l"

// TermHoldNs bounds how long an exit path waits, in all, for records
// another exit path holds (Restoring), which it then restores itself, and
// for records a registrant is writing (Writing), which it then skips: each
// holder's window is one restore's system calls or one record's copy, so
// the bound is reached only when the holder is slow or will not run again.
const TermHoldNs: i64 = 50_000_000

struct TermTable {
    state: [4]raw.Atomic[u32]
    recs: [4]TermSaved
}

var Terms: TermTable = TermTable(state: [raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
    raw.Atomic[u32].New(0)], recs: [TermSaved(kind: 0, h: 0, out: 0, alt: false, mouse: false, len: 0, mode: [0; 64]); 4])

// TermExiting is 1 once an exit path started (TermRestoreAll's first
// step): the Latch invariant.
var TermExiting: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// TermContinuing counts the runs of TermContinue (the SIGCONT hook)
// in progress: the Continue invariant.
var TermContinuing: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// TermHooks is what the exit paths call: restore is TermRestoreAll. It is
// reached only through TermHook, which TermRegister sets, so a binary that
// never registers a terminal mode links neither the table nor the
// restores (hello world pays one load and one branch in rt.Exit and
// the panic path).
struct TermHooks {
    restore: fn()
}

var TermHooksSet: TermHooks = TermHooks(restore: TermRestoreAll)

// TermHook is the address of TermHooksSet once a record was registered, 0
// before.
var TermHook: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// TermInstalled is 1 once the fatal-signal path is installed
// (sys.TermFatalInstall returned).
var TermInstalled: raw.Atomic[u32] = raw.Atomic[u32].New(0)

/// TermExitHook runs TermRestoreAll when any record was ever registered:
/// the call rt.Exit and the panic path make. O(1) when none was; allocates
/// nothing.
export TermExitHook() {
    // Edge: pairs with TermRegister's Store of the hook, which comes before
    // the record's Store of Ready; a record this load misses was
    // registered after the exit path started, by a thread the exit ends.
    p := TermHook.Load()
    if p != 0 {
        unsafe "p is the address of the global TermHooksSet, stored by TermRegister" {
            h := raw.FromAddr[TermHooks](uint(p))
            restore := (*h).restore
            restore()
        }
    }
}

/// TermRegister records rec for the exit paths and returns its id, or -1
/// when the table is full, rec.len is outside 0 to 64, or an exit path
/// latched TermExiting before this call's claim (the Latch invariant: the
/// process is ending, or was kept alive after a restore; std.term then
/// restores only through its own Restore and Close). O(TermSlots);
/// allocates nothing.
export TermRegister(rec: TermSaved): int {
    if rec.len < 0 || rec.len > 64 {
        return -1
    }
    // Edge: published before any record turns Ready (below), so an exit
    // path that reads a Ready record also reads the hook (TermExitHook).
    // The Cas from 0 also picks the one call that installs the fatal-signal
    // path (sys.TermFatalInstall), before any record is Ready: a concurrent
    // registrant that lost the Cas waits for TermInstalled, so no record
    // turns Ready while a fatal signal could still take its default action
    // unrestored. The wait is the installer's few system calls.
    unsafe "the address of a global, never freed" {
        if TermHook.Load() == 0 && TermHook.Cas(0, u64(raw.Addr(raw.Of(&TermHooksSet)))) {
            sys.TermFatalInstall(TermRestoreAll)
            sys.TermContinueInstall(TermContinue)
            // Edge: publishes the installed handlers to the registrants
            // that load 1 below.
            TermInstalled.Store(1)
        }
    }
    for TermInstalled.Load() == 0 {
        sys.ThreadYield()
    }
    i := TermClaim()
    if i < 0 {
        return -1
    }
    TermFill(i, rec)
    return i
}

// TermClaim takes a Free slot for TermRegister and returns it in
// Writing, or -1 when none is Free or an exit path has started (the
// slot it took is then Free again). O(TermSlots); allocates nothing.
TermClaim(): int {
    for i in 0..TermSlots {
        // Edge: the Cas from Free claims the slot and reads the Store of
        // Free that freed it. An exit path's Store of Free (TermRestoreAll,
        // after its restore) comes after that path's Store of TermExiting,
        // and a refused claim's Store of Free after its Load of 1, so a
        // claim of a slot an exit path freed loads 1 below.
        if Terms.state[i].Cas(TermFree, TermWriting) {
            // Edge: TermRestoreAll's first Store (the Latch invariant). Both are
            // seq_cst, and each thread stores before it loads: this claim
            // stores Writing, then loads the latch; an exit path stores the
            // latch, then loads the slot. In the single order of the four,
            // one store comes first, so either this Load reads 1 (the claim
            // is refused) or the exit path's Load reads Writing or later and
            // waits for the Fill (TermRestoreAll). A Load of 0 is also a
            // claim made before any exit path freed this slot.
            if TermExiting.Load() != 0 {
                // Edge: the slot is this call's while it is Writing; the
                // Store of Free gives it back unwritten.
                Terms.state[i].Store(TermFree)
                return -1
            }
            // A continue that may have read this slot's last record waits
            // here before TermFill rewrites it (the Continue invariant).
            TermQuiet()
            return i
        }
    }
    return -1
}

// TermFill writes rec into slot i, which TermClaim returned in Writing,
// and makes it Ready with rec's two flags in its word. O(1); allocates
// nothing.
TermFill(i: int, rec: TermSaved) {
    unsafe "the slot is this call's while it is TermWriting" {
        raw.Write(raw.Of(&Terms.recs[i]), rec)
    }
    var s: u32 = TermReady
    if rec.alt {
        s = s | TermAltBit
    }
    if rec.mouse {
        s = s | TermMouseBit
    }
    // Edge: the record is written before this Store of Ready, which an
    // exit path's Cas from Ready reads, so it reads the whole record and
    // the flags.
    Terms.state[i].Store(s)
}

/// TermSetAlt records whether the alternate screen of record id is on
/// (std.term enters and leaves it while the mode stays saved). False when
/// id is not a registered record, or an exit path is restoring it. O(1);
/// allocates nothing.
export TermSetAlt(id: int, on: bool): bool {
    return TermSetFlag(id, TermAltBit, on)
}

/// TermSetMouse records whether mouse capture is on for record id:
/// std.term turns it on and off while the mode stays saved, and the exit
/// paths write the capture's off sequence to the record's out before they
/// leave the alternate screen. False when id is not a registered record,
/// or an exit path is restoring it. O(1); allocates nothing.
export TermSetMouse(id: int, on: bool): bool {
    return TermSetFlag(id, TermMouseBit, on)
}

/// TermSetKitty records how many kitty keyboard flags a std.term Writer
/// pushed and has not popped for record id: main on the main
/// screen, alt on the alternate screen, each clamped to 0 through
/// TermKittyMax. The exit paths write one pop for each, the alternate
/// screen's before they leave it and the main screen's after. False when
/// id is not a registered record, or an exit path is restoring it. O(1);
/// allocates nothing.
export TermSetKitty(id: int, main: int, alt: int): bool {
    m := Trunc[u32](TermClampKitty(main))
    a := Trunc[u32](TermClampKitty(alt))
    return TermSetBits(id, TermKittyBits, (m << TermKittyMainShift) | (a << TermKittyAltShift))
}

/// TermSetRaw records whether std.term's raw mode is in force on record
/// id's terminal: while it is, a SIGCONT sets raw mode again there
/// (TermContinue), whether or not the program reads again. std.term sets it
/// after its raw mode is set and clears it before it puts the saved mode
/// back. Clearing waits for every continue in progress, so once it returns
/// no continue sets raw mode on that terminal again until it is set: the
/// mode std.term restores next is not overwritten. False when id is not a
/// registered record, or an exit path is restoring it. O(1), plus the
/// wait: a continue's few system calls; allocates nothing.
export TermSetRaw(id: int, on: bool): bool {
    ok := TermSetFlag(id, TermRawBit, on)
    if !on {
        TermQuiet()
    }
    return ok
}

// TermQuiet waits until no continue is in progress (TermContinuing is 0).
// For registrants and owners only, never an exit path, which bounds its
// wait (TermRestoreAll): a continue runs on another thread, or on this one
// before this resumes, and is a few system calls that wait for nothing, so
// the wait ends; no signal handler that may interrupt a continue calls
// this. O(1) when none is in progress; allocates nothing.
TermQuiet() {
    // Edge: (store-buffering, as TermClaim's) the caller stored to a state
    // word first (a Cas to Writing, to Free, or clearing TermRawBit); a
    // continue adds to TermContinuing first, then loads the state words.
    // Both are seq_cst, so either this Load reads its Add (and the loop
    // waits for its subtraction, the continue's last step) or the
    // continue's Load reads the caller's store, and skips the record.
    for TermContinuing.Load() != 0 {
        sys.ThreadYield()
    }
}

/// TermContinue sets raw mode again on the terminal of every Ready record
/// whose raw flag is set (TermSetRaw): sys's SIGCONT handler calls it
/// (sys.TermContinueInstall), so a raw program that was stopped and
/// continued, which a shell may have put back in line mode meanwhile, is
/// raw again whether or not it reads. sys.TermRawAgain sets the mode only
/// when this process is the terminal's foreground group and the mode is not
/// raw already. Nothing once an exit path latched TermExiting. It never
/// takes or writes a record: the Continue invariant, the count in
/// TermContinuing, keeps each record it reads whole and its handles open
/// (TermClaim and TermRemove wait for it), keeps a restore std.term makes
/// after clearing the flag last (TermSetRaw), and keeps an exit path's
/// restore after it (TermRestoreAll waits for it up to TermHoldNs; past the
/// bound this puts the record's saved mode back itself). No lock, no
/// allocation; async-signal-safe. O(TermSlots) plus the system calls.
export TermContinue() {
    // Edge: comes before the Loads below (both seq_cst): TermQuiet's and
    // TermRestoreAll's store-buffering pairs.
    _ = TermContinuing.Add(1)
    if TermExiting.Load() == 0 {
        for i in 0..TermSlots {
            // Edge: reads the Store of Ready or a flag Cas; a record read
            // Ready here is not rewritten before the Add of 0xFFFF_FFFF
            // below, since a claim of its slot waits for it (TermClaim).
            s := Terms.state[i].Load()
            if (s & TermStateMask) == TermReady && (s & TermRawBit) != 0 {
                TermAgain(i)
            }
        }
    }
    // Edge: the continue's last step, which TermQuiet and TermRestoreAll
    // wait for.
    _ = TermContinuing.Add(0xFFFF_FFFF)
}

// TermAgain sets raw mode again on record i's terminal (TermContinue). An
// exit path that latched meanwhile waits for this continue only up to
// TermHoldNs, so it may have restored the record already: then the saved
// mode is put back after the raw one (the Latch invariant keeps the record
// unwritten after the latch). O(1) system calls; allocates nothing.
TermAgain(i: int) {
    r := Terms.recs[i]
    sys.TermRawAgain(r.kind, r.h)
    // Edge: TermRestoreAll's Store of the latch.
    if TermExiting.Load() != 0 {
        sys.TermRestoreMode(r.kind, r.h, r.out, r.mode[0..r.len], false)
    }
}

// TermClampKitty is n within 0 to TermKittyMax.
TermClampKitty(n: int): int {
    return if n < 0 { 0 } else if n > TermKittyMax { TermKittyMax } else { n }
}

// TermSetFlag sets (on) or clears flag bit in record id's state word while
// the record is Ready (TermSetBits). O(1) with only the record's owner
// changing its flags; allocates nothing.
TermSetFlag(id: int, bit: u32, on: bool): bool {
    return TermSetBits(id, bit, if on { bit } else { 0 })
}

// TermSetBits sets the bits of mask in record id's state word to value's
// (value has no bit outside mask, and mask none of TermStateMask) while
// the record is Ready: false when id is out of range or the record is not
// Ready. Lock-free, not wait-free: each retry follows a change of the word
// between the Load and the Cas by another thread (a flag Cas, TermRemove,
// an exit path's Cas to Restoring), so with only the record's owner
// changing its flags it is O(1); allocates nothing.
TermSetBits(id: int, mask: u32, value: u32): bool {
    if id < 0 || id >= TermSlots {
        return false
    }
    for {
        // Edge: the Load reads the word TermBitsCas expects: the Store of
        // Ready, or the last flag Cas.
        s := Terms.state[id].Load()
        if (s & TermStateMask) != TermReady {
            return false
        }
        if TermBitsCas(id, s, mask, value) {
            return true
        }
    }
}

// TermFlagCas is TermSetFlag's step from s, a Ready word a Load of record
// id's state returned: true when the word was still s and now has bit set
// (on) or cleared. O(1); allocates nothing.
TermFlagCas(id: int, s: u32, bit: u32, on: bool): bool {
    return TermBitsCas(id, s, bit, if on { bit } else { 0 })
}

// TermBitsCas is TermSetBits' step from s, a Ready word a Load of record
// id's state returned: true when the word was still s and now has value in
// the bits of mask. O(1); allocates nothing.
TermBitsCas(id: int, s: u32, mask: u32, value: u32): bool {
    n := (s & ~mask) | value
    // Edge: the Cas from s publishes the flag in the word an exit path's
    // Cas from Ready reads, so no write of the record is left to order. It
    // fails when the word changed since the Load: another flag Cas changed
    // a flag (TermSetFlag loads again), TermRemove freed the record, or an
    // exit path took it (Restoring) and maybe freed it. The Latch invariant
    // keeps a slot an exit path freed from holding s again; a slot
    // TermRemove freed can hold a new record only by the id owner's own
    // Remove and Register, before any exit path latched.
    return Terms.state[id].Cas(s, n)
}

/// TermRemove drops record id: std.term put the mode back itself. False
/// when id is not a registered record, or an exit path took it first (the
/// process is ending). O(1); allocates nothing.
export TermRemove(id: int): bool {
    if id < 0 || id >= TermSlots {
        return false
    }
    for {
        // Edge: the Load reads the Ready word the Cas expects; the Cas from
        // it is the record's last use here, and a later TermRegister reuses
        // the slot. It fails when a flag Cas or an exit path changed the
        // word; the next Load says which.
        s := Terms.state[id].Load()
        if (s & TermStateMask) != TermReady {
            return false
        }
        if Terms.state[id].Cas(s, TermFree) {
            // No continue uses the record's handles once this returns, so
            // std.term may close them (the Continue invariant).
            TermQuiet()
            return true
        }
    }
}

/// TermRestoreAll restores every registered record and drops each one it
/// took from Ready: the exit paths' call (panic, Exit, a fatal signal).
/// It first latches TermExiting, so a TermRegister that claims a slot
/// after it answers -1 (the Latch invariant). A record another exit path holds
/// (Restoring) or a registrant is still writing (Writing) is waited for,
/// up to TermHoldNs in all. Past the bound a Restoring record is restored
/// here as well and left Restoring, and a Writing one is skipped: its
/// registrant stalled before publishing it (the All invariant). No lock, no
/// allocation; only async-signal-safe system calls (the clock, a yield,
/// the restores). O(TermSlots) plus the restores' system calls, plus at
/// most TermHoldNs of yields.
export TermRestoreAll() {
    // Edge: comes before this path's Stores of Free below, so a TermClaim
    // whose Cas reads one of them loads 1 (TermClaim).
    TermExiting.Store(1)
    var start: i64 = -1
    // A continue in progress sets raw mode before this path's
    // restores, not after: waited for within the same TermHoldNs. Past it
    // (a continue on this very thread, interrupted by the fatal signal, or
    // a thread stalled within one) the restores go ahead, and a continue
    // that resumes puts each saved mode back after its raw one (TermAgain).
    // Edge: (store-buffering) this path stored the latch before this Load;
    // a continue adds to TermContinuing before it loads the latch. Either
    // this Load reads its Add, or its Load reads the latch and it restores
    // nothing.
    for TermContinuing.Load() != 0 {
        now := sys.Now()
        if start < 0 {
            start = now
        }
        if now - start >= TermHoldNs {
            break
        }
        sys.ThreadYield()
    }
    for i in 0..TermSlots {
        for {
            // Edge: the Load reads a registrant's Store of Ready, a flag Cas,
            // or a Restoring word another exit path's Cas stored; that Cas
            // read the Store of Ready, so a Restoring word read here orders
            // the record's write before the restore below.
            s := Terms.state[i].Load()
            st := s & TermStateMask
            if st == TermFree {
                break
            }
            if st == TermReady {
                // Edge: the Cas from s wins the record against TermRemove,
                // a flag Cas and other exit paths, and reads the record its
                // Store of Ready published; the flags are s's.
                if Terms.state[i].Cas(s, (s & ~TermStateMask) | TermRestoring) {
                    TermRestoreSlot(i, s)
                    break
                }
                continue
            }
            // Restoring: held by another exit path, or by this thread under
            // a signal. Writing: a claim that loaded the latch as 0 (by
            // TermClaim's store-buffering argument, every claim this path
            // must restore is one of these), whose Fill is a copy and a
            // Store of Ready; or a claim that has not loaded the latch yet,
            // which will load 1 and give the slot back Free. Either holder's
            // next Store (Free, or Ready) is read by the Load above; a
            // holder still there after the bound is slow or will not run
            // again.
            now := sys.Now()
            if start < 0 {
                start = now
            }
            if now - start < TermHoldNs {
                sys.ThreadYield()
                continue
            }
            if st == TermWriting {
                // Past the bound, still Writing: the record was never
                // published, so there is nothing whole to restore.
                break
            }
            // Past the bound: restored again (idempotent but for the kitty
            // pops still in the word), and left Restoring: only the
            // exit path whose Cas took the record frees the slot, so this
            // one never frees a slot it did not take.
            TermRestoreRec(i, s, false)
            break
        }
    }
}

// TermRestoreSlot restores record i, which this exit path took from Ready
// to Restoring with flags s, and frees it.
TermRestoreSlot(i: int, s: u32) {
    TermRestoreRec(i, s, true)
    // Edge: the Store of Free follows this path's reads of the record and
    // its Store of TermExiting (TermRestoreAll), so a TermClaim whose Cas
    // reads it gives the slot back unwritten; exit paths waiting for the
    // slot read it and skip it. Only this path stores to a Restoring word
    // it took, so the Store frees no other record.
    Terms.state[i].Store(TermFree)
}

// TermRestoreRec restores record i with the flags of s (a Ready or
// Restoring word of slot i), mouse capture off first, so no report follows
// the mode's restore or lands on the main screen. Then, on the alternate
// screen, the kitty flags pushed there are popped and the screen is left;
// then the main screen's kitty flags are popped (each screen has
// its own stack, so a flag pushed on the main screen before the alternate
// screen was entered is popped only after it is left). All of it is
// written before the mode's restore, while a Windows console still
// processes virtual-terminal output. own: this exit path took the record
// from Ready (TermRestoreSlot); its pops written, it clears the counts in
// the word, so an exit path that restores the record again past
// TermHoldNs pops nothing more. The record was written before the Store
// of Ready that s's Load or Cas read, and the Latch invariant keeps it from
// being written again. On Windows, a record whose console output mode
// lacks virtual-terminal processing when these writes come (Restore put
// its mode back, or it saved none) has the bit turned on for them and put
// back after (sys.TermVtOn), so the sequences are read, not
// printed; untested, since no block drives such a console.
TermRestoreRec(i: int, s: u32, own: bool) {
    r := Terms.recs[i]
    var vt: u64 = 0
    if (s & (TermMouseBit | TermAltBit | TermKittyBits)) != 0 {
        vt = sys.TermVtOn(r.kind, r.out)
    }
    if (s & TermMouseBit) != 0 {
        _ = sys.WriteSync(r.out, MouseOff.AsBytes(), -1)
    }
    if (s & TermAltBit) != 0 {
        TermPops(r.out, int((s >> TermKittyAltShift) & 0xFF))
        _ = sys.WriteSync(r.out, TermAltLeave.AsBytes(), -1)
    }
    TermPops(r.out, int((s >> TermKittyMainShift) & 0xFF))
    if own && (s & TermKittyBits) != 0 {
        // Edge: only the exit path whose Cas took the record from Ready
        // stores to its Restoring word (TermRestoreSlot), so this Store
        // changes no other record; an exit path past the bound that loads
        // it afterwards pops nothing. One that loaded the word before it
        // pops again: a pop too many, never one too few.
        Terms.state[i].Store((s & ~(TermStateMask | TermKittyBits)) | TermRestoring)
    }
    sys.TermVtBack(r.out, vt)
    // The alternate screen was left above.
    sys.TermRestoreMode(r.kind, r.h, r.out, r.mode[0..r.len], false)
}

// TermPops writes n kitty pops (KittyPop) to out, at most 16 to a write.
// O(n); allocates nothing.
TermPops(out: uint, n: int) {
    if n <= 0 {
        return
    }
    var b: [64]u8 = [0; 64]
    pop := KittyPop.AsBytes()
    for j in 0..64 {
        b[j] = pop[j % 4]
    }
    var left := n
    for left > 0 {
        k := if left > 16 { 16 } else { left }
        _ = sys.WriteSync(out, b[0..k * 4], -1)
        left -= k
    }
}
