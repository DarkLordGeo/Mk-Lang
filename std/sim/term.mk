// Why: terminal programs need tests in which a deadline or a cancel
// interrupts Term.Read and a seed replays a key stream the same on every
// run and every target, with no real console behind it.
//
// What: the Sim's one terminal: TermCall and its ops (CallTermOpen,
// CallTermMode, CallTermSize, TermReady, CallTermRead), Sim.TermInput,
// which queues input, and the Windows key-record encoding (AppendKeys,
// PutKey, KeyScalar).
//
// How: input queued by Sim.TermInput arrives on the simulated clock; a
// wait for it sleeps as SimSleep does, firing deadlines on the way. The
// model in detail follows.
//
// Model. term.Open under a Sim's Io opens the Sim's one terminal, 80 by
// 24: two handles, its input and its output, as natively (on Linux both
// sides are one descriptor; here they are two handles on every target).
// What a program writes to the output is discarded (table.mk's SimWrite).
// Raw and Restore record a mode and change nothing: the old mode a
// TermMode gives is the target's length of bytes, byte 0 the mode in force.
// The input is what Sim.TermInput queued: each call's bytes arrive
// together after its delay on the simulated clock, never before earlier
// input, in the target's own form (the bytes on Linux; on Windows one
// key-down KEY_EVENT record per UTF-16 unit of their text). rt.TermWait
// (IoOpReady) waits on the simulated clock until input has arrived,
// consuming none, and a deadline or a cancel ends the wait with Canceled
// (a holder of the baton blocks until the arrival; main with tasks of the
// run sleeps until it; main alone moves the clock to the arrival or to the
// first deadline before it, which fires); rt.TermRead (IoOpTermRead)
// takes what has arrived without waiting. Nothing here draws from the
// random stream, so a run that uses the terminal keeps every seed's
// schedule. The runtime's restore registry takes no simulated terminal
// (rt.TermRegisterOpen answers -1 for a foreign handle).

import std.core
import std.raw
import std.time

// The simulated terminal's size in character cells.
const TermCols: i64 = 80
const TermRows: i64 = 24

// KeyRecord is the size of one Windows INPUT_RECORD.
const KeyRecord: int = 20

// LastClock is the latest clock an arrival may be given (SimDeadline's).
const LastClock: i64 = 0x7FFF_FFFF_FFFF_FFFE

// TermCall answers the call a when its op is one of the terminal's, else
// none. IoOpTermSize asked of a standard stream (w.a 0 to 2) is not: no
// standard stream of a Sim is a terminal (IoOpIsTerminal is Unsupported).
TermCall(io: Io, a: *core.IoCallArgs): ?core.IoResult {
    unsafe "a is the caller's record, live until the call entry returns; p and out are its caller's slices" {
        op := (*a).op
        if op == core.IoOpTermOpen {
            return some(CallTermOpen(io))
        }
        if op == core.IoOpReady {
            return some(TermReady(io, (*a).h))
        }
        if op == core.IoOpTermRead {
            return some(CallTermRead(io, (*a).h, raw.SliceMut((*a).out, (*a).outn)))
        }
        if op == core.IoOpTermMode {
            return some(CallTermMode(io, (*a).h, (*a).w.b, raw.Slice((*a).p, (*a).pn), raw.SliceMut((*a).out, (*a).outn)))
        }
        if op == core.IoOpTermSize && (*a).w.a == -1 {
            return some(CallTermSize(io, a))
        }
    }
    return none
}

// TermSide is h's side of the terminal (0 input, 1 output), or -1 when h
// is no terminal handle of this run.
TermSide(w: &World, h: uint): int {
    i := FindHand(&*w, h, HTerm)
    return if i >= 0 { w.handles.Get(i).obj } else { -1 }
}

// TermClose drops the terminal handle h; false when h is none. The
// terminal stays, with its queued input, for a later Open.
TermClose(w: mut World, h: uint): bool {
    i := FindHand(&*w, h, HTerm)
    if i < 0 {
        return false
    }
    DropHand(w, i)
    return true
}

// CallTermOpen is rt.TermOpen under a Sim: value the input's handle, os
// the output's. Other when memory runs out, with neither made.
CallTermOpen(io: Io): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    var r := Fail(core.SysErr.Other)
    hi := NewHand(w, HTerm, 0, false)
    if hi.err == core.SysErr.None {
        ho := NewHand(w, HTerm, 1, true)
        if ho.err == core.SysErr.None {
            r = core.IoResult(value: hi.value, err: core.SysErr.None, os: ho.value)
        } else {
            _ = TermClose(w, Cast[uint](hi.value))
        }
    }
    Leave(&e)
    return r
}

// ModeBytes is the length of a saved mode on this target: a Linux termios
// (36) or Windows' two console modes (8).
ModeBytes(): int {
    return if Target.os == core.Os.Windows { 8 } else { 36 }
}

// CallTermMode is rt.TermMode under a Sim, on the input handle h: old gets
// the mode in force (ModeBytes bytes, byte 0 the mode, the rest 0) and the
// terminal records mode (core.IoTermRaw, IoTermNoEcho, or IoTermSaved with
// saved an earlier old; IoTermGet keeps the mode). Other for another
// mode, a saved mode that is not one of these, or an old too short;
// Closed when h is not the input.
// Inline, no cancellation point, as natively.
CallTermMode(io: Io, h: uint, mode: i64, saved: []u8, old: mut []u8): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    r := if TermSide(&*w, h) == 0 { SetTermMode(w, mode, saved, old) } else { Fail(core.SysErr.Closed) }
    Leave(&e)
    return r
}

// SetTermMode is CallTermMode's step on the world.
SetTermMode(w: mut World, mode: i64, saved: []u8, old: mut []u8): core.IoResult {
    n := ModeBytes()
    var next: u8 = 0
    if mode == core.IoTermSaved {
        if saved.Len() != n || saved[0] > 2 {
            return Fail(core.SysErr.Other)
        }
        next = saved[0]
    } else if mode == core.IoTermRaw || mode == core.IoTermNoEcho {
        next = Trunc[u8](mode)
    } else if mode == core.IoTermGet {
        next = w.tmode
    } else {
        return Fail(core.SysErr.Other)
    }
    if old.Len() < n {
        return Fail(core.SysErr.Other)
    }
    old[0] = w.tmode
    for i in 1..n {
        old[i] = 0
    }
    w.tmode = next
    return Ok(i64(n))
}

// CallTermSize is rt.TermSizeOf under a Sim: w.a 80 columns and w.b 24
// rows for either terminal handle; Closed for any other handle.
CallTermSize(io: Io, a: *core.IoCallArgs): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    var r := Fail(core.SysErr.Closed)
    unsafe "a is the caller's live record" {
        if TermSide(&*WorldOf(&e), (*a).h) >= 0 {
            (*a).w.a = TermCols
            (*a).w.b = TermRows
            r = Ok(0)
        }
    }
    Leave(&e)
    return r
}

// TermDue is when the terminal's input next has something to read: 0
// when input has arrived (the clock never reads 0), the clock of the next
// arrival, or -1 when nothing is queued.
TermDue(w: &World): i64 {
    if Arrived(&w.term, w.clock) > 0 {
        return 0
    }
    // Compact keeps only the marks of bytes not yet taken, in order.
    for k in 0..w.term.marks.Len() {
        m := w.term.marks.Get(k)
        if m.end > w.term.head && m.at > w.clock {
            return m.at
        }
    }
    return -1
}

// TermReady is rt.TermWait under a Sim: Ok(0) once the input h has input
// that has arrived, nothing consumed. The baton's holder blocks until the
// arrival or a new TermInput; main, while the run has tasks, sleeps until
// the arrival (or waits for a TermInput); main alone moves the clock to
// the arrival, firing each deadline on the way, as a sleep does
// (table.mk's SimSleep). A cancelled scope ends the wait with Canceled
// before it starts and while it waits, nothing consumed (so the
// wait can be made again); with nothing queued and nothing in the run to queue more,
// the wait is a deadlock, Canceled (main alone: at the earliest armed
// deadline, which fires, MainAloneCut). Closed when h is not the input.
TermReady(io: Io, h: uint): core.IoResult {
    if Halted(io) {
        return Fail(core.SysErr.Canceled)
    }
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    if TermSide(&*WorldOf(&e), h) != 0 {
        Leave(&e)
        return Fail(core.SysErr.Closed)
    }
    for {
        w := WorldOf(&e)
        at := TermDue(&*w)
        if at == 0 {
            Leave(&e)
            return Ok(0)
        }
        if IsHolder(&*w) {
            if BlockAs(io, mut e, BNet, 0, at) != core.SysErr.None {
                if e.ok {
                    Leave(&e)
                }
                return Dead()
            }
            continue
        }
        if MainWaits(&*w) {
            // Asked again from the top when it returns: the run's last task
            // may have ended meanwhile, leaving main alone.
            st := MainWait(io, mut e, at)
            if !e.ok {
                return Dead()
            }
            if Halted(io) || st != core.SysErr.None {
                MainAloneCut(WorldOf(&e))
                Leave(&e)
                return Fail(core.SysErr.Canceled)
            }
            continue
        }
        if at < 0 {
            MainAloneCut(w)
            Leave(&e)
            return Fail(core.SysErr.Canceled)
        }
        // Main alone: a deadline at or before the arrival fires first, as
        // a sleep's does.
        if MainAloneUntil(io, w, at) {
            Leave(&e)
            return Fail(core.SysErr.Canceled)
        }
        w.clock = at
    }
}

// CallTermRead is rt.TermRead under a Sim: what has arrived of the input
// h, without waiting (TakeInput). Inline, no cancellation point, as
// natively; Closed when h is not the input.
CallTermRead(io: Io, h: uint, out: mut []u8): core.IoResult {
    var e := NoEntry()
    g := Begin(io, FNone, mut e)
    if g.err != core.SysErr.None {
        return g
    }
    w := WorldOf(&e)
    r := if TermSide(&*w, h) == 0 { TakeInput(w, out, Target.os == core.Os.Windows) } else { Fail(core.SysErr.Closed) }
    Leave(&e)
    return r
}

// TakeInput moves the input that has arrived into out, as much as fits:
// value the bytes, 0 when none. records: the queue holds KEY_EVENT records
// and only whole ones are taken; an out shorter than one is Other, as
// natively.
TakeInput(w: mut World, out: mut []u8, records: bool): core.IoResult {
    if records && out.Len() < KeyRecord {
        return Fail(core.SysErr.Other)
    }
    var n := Arrived(&w.term, w.clock)
    if n > out.Len() {
        n = out.Len()
    }
    if records {
        n -= n % KeyRecord
    }
    unsafe "data[head..head + n] has arrived and is in bounds" {
        raw.Copy(raw.DataMut(mut out[0..n]), raw.Offset(w.term.data.p, w.term.head), n)
    }
    w.term.head += n
    Compact(mut w.term)
    return Ok(i64(n))
}

/// Queues data as input of the Sim's one terminal, the one term.Open
/// opens under the Sim's Io. The bytes arrive together once after has
/// passed on the simulated clock (at once for a zero or negative after),
/// never before input queued earlier. Term.Read takes them in the target's
/// own form: the bytes on Linux; on Windows one key-down KEY_EVENT record
/// per UTF-16 unit of their text, each call's bytes decoded on their own
/// (an ill-formed or cut-short sequence is one U+FFFD per maximal
/// subpart). Input still queued when Restart ends the run is lost. Fails
/// with OutOfMemory when the heap is exhausted, with nothing queued. It
/// draws nothing from the random stream. O(len(data)); allocates the
/// queue's growth from the process heap.
export Sim.TermInput(mut self, data: []u8, after: time.Duration): OutOfMemory!void {
    io := self.Io()
    var e := Enter(io)
    if !e.ok {
        fail OutOfMemory
    }
    w := WorldOf(&e)
    ok := QueueInput(w, data, after.Nanos(), Target.os == core.Os.Windows)
    // A reader waiting with nothing queued waits for this.
    WakeNet(w)
    Settle(io, mut e)
    Leave(&e)
    if !ok {
        fail OutOfMemory
    }
}

// QueueInput appends data to the terminal's input, arriving nanos from
// now and never before earlier input; as KEY_EVENT records when records.
// False when memory runs out, with nothing queued.
QueueInput(w: mut World, data: []u8, nanos: i64, records: bool): bool {
    if data.Len() == 0 {
        return true
    }
    base := w.term.data.Len()
    if !w.term.marks.Reserve(w.term.marks.Len() + 1) {
        return false
    }
    ok := if records { AppendKeys(mut w.term.data, data) } else { AppendBytes(mut w.term.data, data) }
    if !ok {
        return false
    }
    if w.term.data.Len() == base {
        return true
    }
    clock := w.clock
    var at := if nanos <= 0 { clock } else if nanos > LastClock - clock { LastClock } else { clock + nanos }
    if at < w.term.last {
        at = w.term.last
    }
    _ = w.term.marks.Push(Mark(end: w.term.data.Len(), at: at))
    w.term.last = at
    return true
}

// AppendKeys appends one key-down KEY_EVENT record per UTF-16 unit of
// data's text (KeyScalar's decoding); false when memory runs out, with
// nothing appended. A byte makes at most one unit, so len(data) records
// are room enough.
AppendKeys(v: mut Vec[u8], data: []u8): bool {
    if data.Len() > MaxVecBytes / KeyRecord || !v.Reserve(v.Len() + data.Len() * KeyRecord) {
        return false
    }
    var i := 0
    for i < data.Len() {
        (cp, n) := KeyScalar(data, i)
        if cp >= 0x10000 {
            PutKey(v, 0xD800 + ((cp - 0x10000) >> 10))
            PutKey(v, 0xDC00 + ((cp - 0x10000) & 0x3FF))
        } else {
            PutKey(v, cp)
        }
        i += n
    }
    return true
}

// PutKey appends one key-down KEY_EVENT INPUT_RECORD carrying the UTF-16
// unit u, into room the caller reserved: EventType 1 at 0, bKeyDown 1 at
// 4, wRepeatCount 1 at 8, uChar at 14; no virtual key, scan code or
// modifier, except for U+0000, which is Ctrl+Space as a console gives it:
// VK_SPACE at 10, LEFT_CTRL_PRESSED at 16, uChar 0.
PutKey(v: mut Vec[u8], u: u32) {
    var r: [20]u8 = [0; 20]
    r[0] = 1
    r[4] = 1
    r[8] = 1
    if u == 0 {
        r[10] = 0x20
        r[16] = 0x08
    }
    r[14] = Trunc[u8](u)
    r[15] = Trunc[u8](u >> 8)
    _ = AppendBytes(v, r[..])
}

// KeyScalar decodes the UTF-8 sequence at b[i]: its scalar and length, or
// U+FFFD and the length of its maximal subpart (at least 1) when it is
// ill-formed or cut short, by table 3-7 of Unicode §3.9 (the Encoding
// Standard's decoder).
KeyScalar(b: []u8, i: int): (cp: u32, n: int) {
    c := b[i]
    if c < 0x80 {
        return (cp: u32(c), n: 1)
    }
    var need := 0
    var lo: u8 = 0x80
    var hi: u8 = 0xBF
    if c >= 0xC2 && c <= 0xDF {
        need = 1
    } else if c == 0xE0 {
        need = 2
        lo = 0xA0
    } else if c >= 0xE1 && c <= 0xEC || c == 0xEE || c == 0xEF {
        need = 2
    } else if c == 0xED {
        need = 2
        hi = 0x9F
    } else if c == 0xF0 {
        need = 3
        lo = 0x90
    } else if c >= 0xF1 && c <= 0xF3 {
        need = 3
    } else if c == 0xF4 {
        need = 3
        hi = 0x8F
    } else {
        return (cp: 0xFFFD, n: 1)
    }
    // The lead's payload bits: 5, 4 or 3 by the sequence's length.
    var cp := u32(c) & (0x7F >> Trunc[u32](need + 1))
    for k in 1..need + 1 {
        if i + k >= b.Len() {
            return (cp: 0xFFFD, n: k)
        }
        d := b[i + k]
        if d < lo || d > hi {
            return (cp: 0xFFFD, n: k)
        }
        // Only the second byte has a narrowed range.
        lo = 0x80
        hi = 0xBF
        cp = (cp << 6) | u32(d & 0x3F)
    }
    return (cp, n: need + 1)
}
