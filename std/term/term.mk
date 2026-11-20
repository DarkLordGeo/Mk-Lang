// Why: a terminal program needs the controlling terminal itself, whatever
// the standard streams are, in raw mode, and put back exactly as it was
// however the program ends.
//
// What: term.Open, term.IsTerminal, term.Std and term.Term with Raw,
// Restore, Size, Writer and Close; under them SetMode, Track and Untrack
// (the runtime's restore record), and the Writer's scanners WatchAlt,
// WatchCut, AltScan and DecScan.
//
// How: built over std.rt's terminal calls: the controlling terminal
// (/dev/tty on Linux, CONIN$ and CONOUT$ on Windows), so a tool reads keys
// while its standard input is a pipe. Raw mode is cfmakeraw's on Linux (as
// Go's x/term and crossterm set it) and, on Windows, the console with echo,
// line and processed input off and virtual-terminal input and output on.
//
// A Term owns its OS handles, the mode it saved and the input it has read
// but not yet decoded, all in its own fixed fields: it owns no allocator
// memory, so it does not implement Free, and Close only
// releases, so it is not cancellable.
//
// Two Terms on one terminal each register the mode they found: when B
// goes raw after A did, B's record holds A's raw mode. The runtime's exit
// paths restore records in slot order (std/rt/term.mk), so the mode a
// crash leaves is A's saved one only when B's record is restored first,
// which depends on the slots the two got. This is a known limit; a program
// that opens one Term per terminal does not see it.

import std.core
import std.rt
import std.simd.kernel

/// A standard stream of this process.
export enum Std {
    Stdin
    Stdout
    Stderr
}

/// The controlling terminal, open: read events with Read, write through
/// Writer, then Close it. Linear: every path must end it with Close. Send
/// and Share; only Size takes &self, while Read and Writer hold it
/// exclusively. It owns one or two OS handles and a fixed input buffer,
/// and no allocator memory.
export linear struct Term {
    // input and out are the runtime handles of the input and the output (one
    // handle on Linux).
    input: uint
    out: uint
    // is_raw: Raw's mode is in force; saved: the bytes of the mode before it
    // (saved_len 0: none saved).
    is_raw: bool
    saved_len: int
    saved: [64]u8
    // alt: the alternate screen is on, as the Writer saw it; alt_at: how
    // much of AltEnter or AltLeave's common prefix the end of the last
    // write matched.
    alt: bool
    alt_at: int
    // The input read but not decoded: in_buf[head..tail].
    in_buf: [4096]u8
    head: int
    tail: int
    // holding: the bytes held may begin a sequence, since held_at (the
    // Io clock, nanoseconds).
    holding: bool
    held_at: i64
    // pasting: inside a bracketed paste, before its end marker.
    pasting: bool
    // high: a Windows high surrogate whose low unit has not come (0 none).
    high: u32
    // resized: a resize is to be reported; cols and rows: the size last
    // reported or read (0 unknown).
    resized: bool
    // empties: reads in a row that found nothing after a ready wait.
    empties: int
    cols: int
    rows: int
    // reg: the id of the record the runtime restores on a panic, an exit
    // or a fatal signal (rt.TermRegisterOpen), -1 none: the saved mode and
    // the alternate screen, kept equal to saved and alt by Track.
    reg: int
    // mouse: mouse capture is on, as the Writer saw it (MouseWatch then h);
    // mouse_at: how much of MouseWatch the end of the last write matched.
    mouse: bool
    mouse_at: int
    // skip: the decoder's state (input.mk's Step), PSkipCsi or PSkipStr
    // while the rest of a long sequence is dropped, else 0.
    skip: u8
    // scan: where, counted from head, the paste's end marker can first
    // begin (the bytes before it were searched).
    scan: int
    // The line a cancelled ReadSecret typed (secret_len bytes), kept for
    // the next call (Read is repeatable) and wiped by Close.
    secret: [1024]u8
    secret_len: int
    // secret_raw: a cancel cut a ReadSecret that set raw mode itself, and
    // the mode before it is still owed: the next ReadSecret puts it
    // back when its line ends, Restore and Close do, Raw takes it over.
    secret_raw: bool
    // kitty: the kitty keyboard flags the Writer saw pushed and not popped,
    // per screen, and its scan's state (input_kitty.mk).
    kitty: KittyCount
}

// InBytes is the input buffer's size: a paste longer than it arrives as
// several Paste events.
const InBytes: int = 4096

/// Opens this process's controlling terminal: the terminal itself, even
/// when the standard streams are pipes or files. NotTerminal when there is
/// none (a service, a daemon, a job with no console); OutOfMemory when the
/// process heap refuses a handle record; Device for another refusal. Under
/// a simulated Io it opens the Sim's one terminal, 80 by 24, whose input
/// Sim.TermInput queues. Not a cancellation point. O(1); allocates one or
/// two runtime handle records from the process heap.
export Open(io: Io): Error!Term {
    var t: [2]uint = [0; 2]
    _ = try OpenCheck(rt.TermOpen(io, mut t))
    var term := Term(input: t[0], out: t[1], is_raw: false, saved_len: 0, saved: [0; 64], alt: false, alt_at: 0,
        in_buf: [0; 4096], head: 0, tail: 0, holding: false, held_at: 0, pasting: false, high: 0, resized: false, empties: 0, cols: 0, rows: 0,
        reg: -1, mouse: false, mouse_at: 0, skip: 0, scan: 0, secret: [0; 1024], secret_len: 0,
        secret_raw: false, kitty: KittyZero())
    // The size at the start, which a Linux or macOS read compares with (a resize
    // between two reads); unknown (0) when no stream gives it.
    (cols, rows) := term.Size(io) else {
        (cols: 0, rows: 0)
    }
    term.cols = cols
    term.rows = rows
    return term
}

/// Whether the standard stream s is a terminal (on Windows, a console).
/// False for a pipe or a file, and under a simulated Io. Not a
/// cancellation point. O(1); allocates nothing.
export IsTerminal(io: Io, s: Std): bool {
    r := rt.IsTerminal(io, StdIndex(s))
    return r.err == core.SysErr.None && r.value == 1
}

// StdIndex is the runtime's number of s.
StdIndex(s: Std): u32 {
    return match s {
        .Stdin { 0 }
        .Stdout { 1 }
        .Stderr { 2 }
    }
}

/// Puts the terminal in raw mode: keys arrive one by one, unechoed, with
/// no line editing and no signal from Ctrl+C or Ctrl+Z (Read returns them
/// as keys). Output is not translated, so write "\r\n" to end a line. The
/// mode in force before is saved for Restore and Close. Calling Raw again
/// while raw does nothing, except that raw mode a cancelled ReadSecret left
/// in force then becomes the program's, and that line's end keeps it.
///
/// Unsupported on a Windows console without virtual-terminal input; Device
/// for another refusal.
///
/// On Linux, a process outside its terminal's foreground group is stopped
/// by SIGTTOU here, as every Unix program is. While raw mode is in force, a
/// SIGCONT that finds the process in the terminal's foreground group sets
/// raw mode again, whether or not the program reads again, since the shell
/// that stopped it may have put line mode back; Read also sets it again
/// before each wait. So call Restore before handing the terminal to
/// another program: a child in this process's group (one spawned with no
/// group and no terminal of its own) would have its mode set raw on such a
/// SIGCONT.
///
/// Not a cancellation point. O(1); allocates nothing.
export Term.Raw(mut self, io: Io): Error!void {
    // The program asks for raw mode: a cut ReadSecret no longer owes the
    // mode before it.
    self.secret_raw = false
    if self.is_raw {
        return
    }
    try self.SetMode(io, core.IoTermRaw)
    self.is_raw = true
    // After the mode is set, never before: a continue in between misses
    // one set of raw mode over raw mode, where one before would set raw
    // mode on a terminal that is not to be raw.
    if self.reg >= 0 {
        _ = rt.TermSetRaw(self.reg, true)
    }
}

// SetMode sets mode (IoTermRaw, IoTermNoEcho), keeping the first mode it
// replaced in saved. The first time, on a native terminal, the mode in
// force is read (IoTermGet) and registered with the runtime before it is
// changed, so an exit path that takes the record after the set
// restores the mode in force. A set that fails drops that registration
// again. After a set that succeeds, SetMode asks whether the record is
// still Ready (TermSetAlt with the flag it has): when it is not, an exit
// path took it between the registration and this check, may have
// restored the mode before the set landed, and is ending the process, so
// SetMode puts the saved mode back itself. One window is left: that exit
// path ends the process after the set and before this put-back. A Term
// whose registration was refused (a full table, or a registry latched by
// an exit path that did not end the process) sets the mode unregistered,
// with no exit-path restore. A simulated terminal (a foreign handle) is never
// registered, so there the mode is set at once, with no extra Sim
// operation.
Term.SetMode(mut self, io: Io, mode: i64): Error!void {
    var old: [64]u8 = [0; 64]
    var empty: [0]u8 = []
    first := self.saved_len == 0 && !rt.IsForeign(self.input)
    if first {
        g := try Check(rt.TermMode(io, self.input, self.out, core.IoTermGet, empty[..], mut old[..]))
        if g <= 0 || g > 64 {
            fail Device(code: 0)
        }
        self.saved = old
        self.saved_len = Trunc[int](g)
        self.Track()
    }
    r := rt.TermMode(io, self.input, self.out, mode, empty[..], mut old[..])
    if first && r.err != core.SysErr.None {
        self.saved_len = 0
        self.Track()
    }
    n := try Check(r)
    if first && self.reg >= 0 && !rt.TermSetAlt(self.reg, self.alt) {
        // The record is no longer Ready: an exit path took it between the
        // registration and the set, restored the mode in force and is
        // ending the process. Put that mode back here too, after the set.
        var back: [64]u8 = [0; 64]
        _ = rt.TermMode(io, self.input, self.out, core.IoTermSaved, self.saved[0..self.saved_len], mut back[..])
    }
    if n <= 0 || n > 64 {
        fail Device(code: 0)
    }
    if self.saved_len == 0 {
        self.saved = old
        self.saved_len = Trunc[int](n)
        self.Track()
    }
}

// Track makes the runtime's record match saved, alt, mouse, the kitty
// counts and raw mode: a new record is registered,
// its mouse and raw flags and kitty counts set, before the old one is
// removed, so no moment has none
// (a crash in between restores both, so the kitty flags are popped twice:
// a pop too many, never one too few); with no mode saved, the alternate
// screen off, mouse capture off and no kitty flag left pushed on the main
// screen there is none. A full table (-1) keeps the old record, its flags
// and counts brought up to date. O(1); allocates nothing.
Term.Track(mut self) {
    if self.saved_len == 0 && !self.alt && !self.mouse && self.kitty.main == 0 {
        self.Untrack()
        return
    }
    id := rt.TermRegisterOpen(self.input, self.out, self.saved[0..self.saved_len], self.alt)
    if id < 0 {
        if self.reg >= 0 {
            _ = rt.TermSetAlt(self.reg, self.alt)
            _ = rt.TermSetMouse(self.reg, self.mouse)
            _ = rt.TermSetKitty(self.reg, self.kitty.main, self.kitty.alt)
            _ = rt.TermSetRaw(self.reg, self.is_raw)
        }
        return
    }
    if self.mouse {
        _ = rt.TermSetMouse(id, true)
    }
    if self.is_raw {
        _ = rt.TermSetRaw(id, true)
    }
    if self.kitty.main > 0 || self.kitty.alt > 0 {
        _ = rt.TermSetKitty(id, self.kitty.main, self.kitty.alt)
    }
    if self.reg >= 0 {
        _ = rt.TermRemove(self.reg)
    }
    self.reg = id
}

// Untrack removes the runtime's record, if any. O(1); allocates nothing.
Term.Untrack(mut self) {
    if self.reg >= 0 {
        _ = rt.TermRemove(self.reg)
        self.reg = -1
    }
}

/// Puts back the mode that was in force before Raw (or ReadSecret). First
/// it turns mouse capture off when a Writer turned it on, on every target,
/// since on Windows the console forwards no mouse-mode change once its
/// virtual-terminal input is off. With no mode saved, there is nothing
/// more to do. Device when the terminal refuses either step: the mode is
/// put back even when turning capture off failed, and a mode the terminal
/// refused stays saved, for Close. Not a cancellation point. O(1);
/// allocates nothing.
export Term.Restore(mut self, io: Io): Error!void {
    var off := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    if self.mouse {
        // Masked, as Restore is no cancellation point: a cancel must not
        // leave capture on at the terminal.
        off = self.MouseOffNow(core.IoMasked(io))
    }
    try self.RestoreMode(io)
    _ = try Check(off)
}

// MouseOffNow writes MouseOff, all of it, while the mode in force is still
// the one that forwards it, then records capture off (a panic in between
// writes the off sequence twice, never not at all). It returns the failed
// write's result, or a success; a refused write leaves capture recorded
// on, for Close and the runtime. O(1); allocates nothing.
Term.MouseOffNow(mut self, io: Io): core.IoResult {
    r := WriteAll(io, self.out, MouseOff.AsBytes())
    if r.err != core.SysErr.None {
        return r
    }
    self.mouse = false
    self.mouse_at = 0
    if self.reg >= 0 {
        _ = rt.TermSetMouse(self.reg, false)
    }
    // Drops a record nothing needs any more (no mode, no alternate screen).
    if self.saved_len == 0 {
        self.Track()
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

// RestoreMode puts back the saved mode, if any (Restore without its mouse
// half: ReadSecret's end and Close, which turned capture off already).
Term.RestoreMode(mut self, io: Io): Error!void {
    if self.saved_len == 0 {
        return
    }
    var old: [64]u8 = [0; 64]
    // The raw flag off first, whatever is_raw says (Fill's set again may
    // have failed with the flag still on): once TermSetRaw returns, no
    // continue sets raw mode again over the mode put back below.
    if self.reg >= 0 {
        _ = rt.TermSetRaw(self.reg, false)
    }
    r := rt.TermMode(io, self.input, self.out, core.IoTermSaved, self.saved[0..self.saved_len], mut old[..])
    if self.is_raw && self.reg >= 0 && r.err != core.SysErr.None {
        // Refused: raw mode stays in force, and stays the continue's.
        _ = rt.TermSetRaw(self.reg, true)
    }
    _ = try Check(r)
    self.saved_len = 0
    self.is_raw = false
    self.secret_raw = false
    // After the mode is back, never before: a restore made twice is
    // harmless, a raw terminal left by an end in between is not.
    self.Track()
}

/// The terminal's size in character cells. NotTerminal when no size can
/// be read; 80 by 24 under a simulated Io. Not a cancellation point.
/// O(1); allocates nothing.
export Term.Size(&self, io: Io): Error!(cols: int, rows: int) {
    // Asked of the handles Open made, whatever the standard streams are.
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    r := rt.TermSizeOf(io, self.input, self.out, mut w)
    if r.err == core.SysErr.None && w.a > 0 && w.b > 0 {
        return (cols: Cast[int](w.a), rows: Cast[int](w.b))
    }
    if r.err == core.SysErr.Unsupported || r.err == core.SysErr.Canceled {
        _ = try Check(r)
    }
    fail NotTerminal
}

const TermWrites = core.WritePortMut[Term].New(TermSend, TermFlush)

/// A Writer to the terminal that buffers in buf. Each flush writes until
/// every byte is out. The Writer passes every byte as it is: send
/// untrusted text through term.Sanitize, never straight to it.
///
/// The Writer records the modes written through it. After term.AltScreen,
/// Close, and the runtime on a panic or an exit, leave the alternate
/// screen. After term.MouseCapture, they and Restore turn capture off.
/// Kitty keyboard flags pushed with term.KittyKeys and left pushed are
/// popped by Close and the runtime; Restore leaves them.
///
/// Errors are WriteError. O(1); allocates nothing.
export Term.Writer(mut self, io: Io, buf: mut []u8): Writer from self, buf {
    return (&TermWrites).Writer(io, self, buf)
}

// TermSend writes all of b to the terminal and notes whether it entered
// or left the alternate screen, or turned mouse capture on or off. When a
// write fails (a cancel or a deadline, after part of it went out), the bytes
// written before it took effect, and the failed write may have put out a
// part of its own: WatchCut records the state after the bytes written,
// with a set of either mode in the rest taken as done, so Close
// and the runtime leave the alternate screen and turn capture off
// whenever they may be on.
TermSend(t: mut Term, io: Io, b: []u8): WriteError!void {
    r := WriteAll(io, t.out, b)
    if r.err != core.SysErr.None {
        t.WatchCut(b, Trunc[int](r.value))
        fail WriteError
    }
    t.WatchAlt(b)
}

// WriteAll writes all of b to h, a write after each short one: a success,
// or the failed write's result (Other for a count of 0 or one past what was
// asked, which would loop for ever or skip bytes); either way value is how
// many bytes of b were written. O(len(b)) plus one write each; allocates
// nothing.
WriteAll(io: Io, h: uint, b: []u8): core.IoResult {
    var off := 0
    for off < b.Len() {
        r := core.IoWrite(io, h, b[off..], -1)
        if r.err != core.SysErr.None {
            return core.IoResult(value: i64(off), err: r.err, os: r.os)
        }
        if r.value <= 0 || r.value > i64(b.Len() - off) {
            return core.IoResult(value: i64(off), err: core.SysErr.Other, os: 0)
        }
        off += Trunc[int](r.value)
    }
    return core.IoResult(value: i64(off), err: core.SysErr.None, os: 0)
}

// The terminal is written at each send: nothing is held.
TermFlush(t: mut Term, io: Io): WriteError!void {
}

// WatchAlt follows b for AltEnter and AltLeave, which share every byte but
// the last ("\x1b[?1049" then h or l), and for MouseWatch's set and reset,
// across writes: alt_at and mouse_at are how much of each prefix the bytes
// so far end with. Each prefix has no border but its first byte, so on a
// mismatch the match restarts at an escape. The runtime's record follows
// both, after the bytes are written: a panic in between, a few
// instructions, leaves capture on or writes its off sequence twice.
Term.WatchAlt(mut self, b: []u8) {
    (on: mouse, at: mouse_at) := DecScan(MouseWatch.AsBytes(), self.mouse, self.mouse_at, b)
    kitty := KittyScan(self.kitty, self.alt, self.alt_at, b, true)
    (alt, at) := AltScan(self.alt, self.alt_at, b)
    self.Note(alt, at, mouse, mouse_at, kitty)
}

// WatchCut is WatchAlt for a failed send of b of which the first n bytes
// were written: the state after those, and either mode on when the
// rest, which the failed write may have put out in part, sets it anywhere.
// A reset in the rest is not taken: a mode that may be on stays recorded
// on. So for the kitty flags: a push in the rest is counted, a pop is not
// O(len(b)); allocates nothing.
Term.WatchCut(mut self, b: []u8, n: int) {
    k := if n < 0 { 0 } else if n > b.Len() { b.Len() } else { n }
    (on: mouse, at: mouse_at) := DecScan(MouseWatch.AsBytes(), self.mouse, self.mouse_at, b[0..k])
    head := KittyScan(self.kitty, self.alt, self.alt_at, b[0..k], true)
    (alt, at) := AltScan(self.alt, self.alt_at, b[0..k])
    rest := b[k..]
    kitty := KittyScan(head, alt, at, rest, false)
    self.Note(alt || DecSets(AltEnter.AsBytes(), at, rest), at, mouse || DecSets(MouseWatch.AsBytes(), mouse_at, rest),
        mouse_at, kitty)
}

// Note sets the Writer's view of the alternate screen, mouse capture and
// the kitty flags, and their match lengths, and brings the runtime's
// record to it.
Term.Note(mut self, alt: bool, alt_at: int, mouse: bool, mouse_at: int, kitty: KittyCount) {
    was_mouse := self.mouse
    was_alt := self.alt
    kitty_moved := self.kitty.main != kitty.main || self.kitty.alt != kitty.alt
    self.mouse = mouse
    self.mouse_at = mouse_at
    self.alt = alt
    self.alt_at = alt_at
    self.kitty = kitty
    if alt == was_alt && mouse == was_mouse && !kitty_moved {
        return
    }
    // Changed in place while the record is still needed (false: an exit
    // path holds it, the process is ending); made or dropped otherwise.
    if self.reg >= 0 && (self.saved_len > 0 || alt || mouse || kitty.main > 0) {
        if alt != was_alt {
            _ = rt.TermSetAlt(self.reg, alt)
        }
        if mouse != was_mouse {
            _ = rt.TermSetMouse(self.reg, mouse)
        }
        if kitty_moved {
            _ = rt.TermSetKitty(self.reg, kitty.main, kitty.alt)
        }
    } else {
        self.Track()
    }
}

// AltScan is WatchAlt's step: the screen state and match length after b,
// from alt and at before it. O(len(b)); allocates nothing.
AltScan(alt: bool, at: int, b: []u8): (alt: bool, at: int) {
    (on, at: m) := DecScan(AltEnter.AsBytes(), alt, at, b)
    return (alt: on, at: m)
}

// MouseWatch is the DEC mode the Writer follows for mouse capture: MouseOn
// starts with it set and MouseOff ends with it reset (input_mouse.mk).
const MouseWatch: str = "\x1b[?1000h"

// MouseOff resets every mode MouseCapture sets, in reverse.
const MouseOff: str = "\x1b[?1004l\x1b[?1006l\x1b[?1002l\x1b[?1000l"

// DecSets is whether b, from a match of at bytes of pattern (DecScan's),
// sets the mode anywhere. O(len(b)); allocates nothing.
DecSets(pattern: []u8, at: int, b: []u8): bool {
    last := pattern.Len() - 1
    var m := at
    for c in b {
        if m == last {
            if c == b'h' {
                return true
            }
            m = 0
        }
        if c == pattern[m] {
            m += 1
        } else {
            m = if c == 0x1B { 1 } else { 0 }
        }
    }
    return false
}

// DecScan follows b for the DEC mode set (h) or reset (l) whose sequence
// is pattern (ending in h; its prefix has no border but its first byte, an
// escape): the state and match length after b, from start and at before
// it.
// O(len(b)); allocates nothing.
DecScan(pattern: []u8, start: bool, at: int, b: []u8): (on: bool, at: int) {
    prefix := pattern
    last := prefix.Len() - 1
    var on := start
    var m := at
    for c in b {
        if m == last {
            if c == b'h' {
                on = true
            } else if c == b'l' {
                on = false
            }
            m = 0
        }
        if c == prefix[m] {
            m += 1
        } else {
            m = if c == 0x1B { 1 } else { 0 }
        }
    }
    return (on, at: m)
}

/// Closes the terminal and puts it back as it was. In order: it turns
/// mouse capture off and leaves the alternate screen if a Writer turned
/// them on, popping first the kitty keyboard flags a Writer left pushed on
/// that screen and then those left on the main screen, each sequence
/// written whole; it puts back a saved mode, wipes a secret a cancelled
/// ReadSecret left, and releases the handles. It cannot fail and cannot be
/// cancelled: what the terminal refuses here is ignored. O(1), with at
/// most 255 kitty pops a screen in at most 16 writes each; frees the
/// handle records.
export Term.Close(self, io: Io) {
    quiet := core.IoMasked(io)
    var t := self
    t.Leave(quiet)
    t.RestoreMode(quiet) else {}
    // Always before the handles close, even when Restore failed: the
    // record names their OS handles, which may be reused after. When the
    // removal fails, an exit path holds the record (it is restoring it
    // with those handles, and the process is ending), so the handles stay
    // open for it. In a process an exit path left alive with the
    // registry latched the two handles are then never closed.
    held := t.reg >= 0 && !rt.TermRemove(t.reg)
    t.reg = -1
    if !held {
        _ = core.IoClose(quiet, t.input)
        if t.out != t.input {
            _ = core.IoClose(quiet, t.out)
        }
    }
    Term(input: _, out: _, is_raw: _, saved_len: _, saved: _, alt: _, alt_at: _, in_buf: _, head: _, tail: _, holding: _, held_at: _, pasting: _,
        high: _, resized: _, empties: _, cols: _, rows: _, reg: _, mouse: _, mouse_at: _, skip: _, scan: _, secret: _, secret_len: _,
        secret_raw: _, kitty: _) := t
}

// Leave is Close's first half: the off sequence of mouse capture; on the
// alternate screen the kitty pops of that screen's count and the screen's
// leave; the kitty pops of the main screen's count; each written
// whole (WriteAll, after a short write too), then the secret and the
// input held wiped. The pops are recorded in the runtime's record once
// written, so an exit before Close's removal does not pop them again. A
// flag the Writer saw pushed on the alternate screen while it is off is
// on that screen's stack, which does not reach the main screen, and is
// left; kitty keeps that stack across alternate-screen sessions (it
// clears it only at a reset), so the next program to enter the alternate
// screen inherits the flag of this program's unpaired push. A refused
// write is ignored, as Close ignores it. On Windows, when the console's
// output mode lacks virtual-terminal processing as Leave starts (Restore
// put back a mode without it), the bit is turned on for these writes and
// put back after (rt.TermVtOnOpen), so they are read, not printed;
// untested, since no block drives such a console. O(1) (at most
// KittyPopMost pops a screen); allocates nothing.
Term.Leave(mut self, io: Io) {
    var vt: u64 = 0
    if self.mouse || self.alt || self.kitty.main > 0 || self.kitty.alt > 0 {
        vt = rt.TermVtOnOpen(self.out)
    }
    if self.mouse {
        _ = self.MouseOffNow(io)
        self.mouse = false
    }
    was := self.kitty
    if self.alt {
        _ = KittyPopAll(io, self.out, self.kitty.alt)
        self.kitty.alt = 0
        _ = WriteAll(io, self.out, AltLeave.AsBytes())
        self.alt = false
    }
    _ = KittyPopAll(io, self.out, self.kitty.main)
    self.kitty.main = 0
    rt.TermVtBackOpen(self.out, vt)
    if self.reg >= 0 && (was.main != self.kitty.main || was.alt != self.kitty.alt) {
        _ = rt.TermSetKitty(self.reg, self.kitty.main, self.kitty.alt)
    }
    self.WipeSecret()
    kernel.Wipe(mut self.in_buf[..])
}
