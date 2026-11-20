// Why: reading a terminal has to wait for input, tell a lone escape from a
// sequence, follow resizes and job-control stops, and still lose nothing
// when a cancel or a deadline ends the wait.
//
// What: Term.Read and Term.ReadSecret, and under them Term.Next, Fill (the
// wait and the signal races), Decode and Take (bytes to events), the
// Windows record path ReadRecords, Record and Unit, and the secret line
// editor SecretLine.
//
// How: input in, events out. A read waits on the terminal with
// nothing consumed (rt.TermWait), then takes what is queued without
// waiting (rt.TermRead) into the Term's own buffer, which input.mk's Parse
// decodes; a key, a paste or a resize leaves the buffer only once its
// Event is built. So a cancel or a deadline, which can end only the wait,
// loses nothing, and the same call again returns the same event
// (repeatable).
//
// A lone escape is told from the start of a sequence by time, as every
// terminal library does (crossterm, termion, vim's ttimeoutlen): when the
// bytes held could still begin a sequence, the wait is bounded by
// EscapeWait, and when it passes they are decoded as they stand.
//
// Windows' console gives records, not bytes: each key-down record's
// UTF-16 unit (virtual-terminal input sends sequences as units) becomes
// UTF-8 here, a surrogate pair joined across records; a window-size
// record is a resize. Linux and macOS send SIGWINCH for a resize and SIGCONT after
// a stop (whose shell may have reset the mode): the runtime's signal entry
// delivers both, and Read races them against the input. The runtime also
// sets a raw Term's raw mode again on SIGCONT itself, for a
// program that never reads again; Read's own set stays, for a Term the
// runtime holds no record of (its table was full), and for a job
// continued in the background (the hook leaves its mode) and then brought
// to the foreground, which a shell may do with no second SIGCONT.

import std.core
import std.raw
import std.rt
import std.simd.kernel

// EscapeWait is how long an escape waits for the rest of its sequence:
// a terminal sends a sequence in one write.
const EscapeWait: i64 = 25_000_000

// The bracketed paste's end marker (DECSET 2004).
const PasteEnd: str = "\x1b[201~"

// Records is how many Windows input records one console read takes (a
// 1,280-byte stack buffer); Fill reads batches until the console has no
// more or the buffer's room is spent.
const Records: int = 64
const RecordBytes: int = 20

// Room is the least free space Fill reads into: the most UTF-8 one Windows
// record can add (U+FFFD for a high surrogate left without its low unit,
// then the unit's own three bytes).
const Room: int = 6

/// Waits for the next input event and returns it: a key, a bracketed
/// paste, a resize, or a mouse or focus report the program turned on.
///
/// Canceled in a cancelled scope or past a deadline, with nothing
/// consumed: the same call again returns what this one would have. Under a
/// simulated Io, also Canceled when nothing is queued and nothing in the
/// run can queue more (a deadlock). OutOfMemory when a cannot hold a
/// paste's text, which stays unread. Device when the terminal fails a
/// read.
///
/// O(bytes read); allocates a paste's text in a, at most 4,096 bytes an
/// event, and nothing for any other event.
export Term.Read(mut self, a: &Arena, io: Io): Error!Event from a {
    return try self.Next(a, io, false)
}

// Next is Read. With secret, a paste's bytes go straight into the secret
// line (SecretAdd) and the Paste event's text is empty: nothing is
// allocated in a (ReadSecret).
Term.Next(mut self, a: &Arena, io: Io, secret: bool): Error!Event from a {
    for {
        if self.resized {
            (cols, rows) := self.Size(io) else {
                (cols: self.cols, rows: self.rows)
            }
            self.resized = false
            if cols > 0 && rows > 0 {
                // No size known (no stream to ask, no record's size): no
                // event.
                self.cols = cols
                self.rows = rows
                return Event.Resize(cols: cols, rows: rows)
            }
        }
        if e := try self.Decode(a, secret, false) {
            return e
        }
        // More input is needed. Bytes held that could begin a sequence
        // wait for the rest until EscapeWait after they came, counted
        // across calls, so a caller's shorter deadlines cannot hold an
        // escape for ever; a paste waits for its end.
        var bounded := false
        var left: i64 = 0
        if !self.pasting && self.head < self.tail {
            now := Now(io)
            if !self.holding {
                self.holding = true
                self.held_at = now
            }
            bounded = true
            left = EscapeWait - (now - self.held_at)
        }
        if bounded && left <= 0 || !(try self.Fill(io, bounded, left)) {
            if e := try self.Decode(a, secret, true) {
                return e
            }
        }
    }
}

// Decode takes events from the bytes held until one is built (some) or
// the bytes left need more input (none): Read's whole state machine but
// the wait and the read, so the fuzz target drives it as Read does. final
// is Parse's for the first step that uses a byte (the escape wait passed).
// O(bytes held); allocates only a paste's text.
Term.Decode(mut self, a: &Arena, secret: bool, final: bool): OutOfMemory!?Event from a {
    var fin := final
    for {
        if self.pasting {
            if e := try self.PasteStep(a, secret) {
                return some(e)
            }
            if self.pasting {
                return none
            }
            continue
        }
        if self.head >= self.tail {
            return none
        }
        p := Step(self.in_buf[self.head..self.tail], fin, self.skip)
        was := self.skip
        self.skip = SkipOf(p.kind)
        if p.n == 0 && self.skip == was {
            return none
        }
        if p.n > 0 {
            fin = false
        }
        if e := self.Take(a, p) {
            return some(e)
        }
    }
}

// Now is io's clock in nanoseconds, 0 when it has none.
Now(io: Io): i64 {
    r := core.IoNow(io)
    return if r.err == core.SysErr.None { r.value } else { 0 }
}

// Use drops the first n bytes held: they were decoded. They are zeroed,
// so no copy of what was typed (a secret above all) stays in the buffer.
Term.Use(mut self, n: int) {
    for i in self.head..self.head + n {
        self.in_buf[i] = 0
    }
    self.head += n
    self.holding = false
    self.scan = if self.scan > n { self.scan - n } else { 0 }
}

// Take uses the step p (n > 0): the event it makes, or none for a dropped
// sequence or a paste's start (which turns pasting on). Nothing is
// allocated in a: it is the source the Event is declared to borrow from.
Term.Take(mut self, a: &Arena, p: Parsed): ?Event from a {
    self.Use(p.n)
    if p.kind == PPaste {
        self.pasting = true
        return none
    }
    if p.kind == PKey {
        return some(Event.Key(key: p.key, ch: p.ch, mods: p.mods))
    }
    if p.kind == PMouse {
        return some(Event.Mouse(button: p.a, row: p.b, col: p.c, mods: p.mods))
    }
    if p.kind == PFocus {
        return some(Event.Focus(on: p.a != 0))
    }
    return none
}

// PasteStep returns the text of the paste up to its end marker, or, when
// the buffer is full with no marker, the bytes that cannot be part of one
// (cut at a UTF-8 boundary); none when it needs more input or the paste
// was empty. The marker is looked for only from scan, where the bytes
// searched before could still begin it, so each byte is looked at a
// bounded number of times however the paste arrives. With secret, the
// text goes into the secret line and the event's text is empty.
Term.PasteStep(mut self, a: &Arena, secret: bool): OutOfMemory!?Event from a {
    end := PasteEnd.AsBytes()
    held := self.tail - self.head
    if k := FindFrom(self.in_buf[self.head..self.tail], self.scan, end) {
        self.pasting = false
        if k == 0 {
            self.Use(end.Len())
            return none
        }
        e := try self.PasteText(a, k, secret)
        self.Use(k + end.Len())
        return some(e)
    }
    self.scan = if held > end.Len() - 1 { held - (end.Len() - 1) } else { 0 }
    if self.head > 0 || InBytes - self.tail >= Room {
        return none
    }
    // Full (less than Room left, which a Windows read needs): keep what
    // could be the start of the marker, and a UTF-8 sequence cut short.
    b := self.in_buf[self.head..self.tail]
    var k := b.Len() - (end.Len() - 1)
    var back := 0
    for back < 3 && k - back > 0 && b[k - back - 1] & 0xC0 == 0x80 {
        back += 1
    }
    if back < 3 && k - back > 0 && b[k - back - 1] >= 0xC0 {
        k = k - back - 1
    }
    e := try self.PasteText(a, k, secret)
    self.Use(k)
    return some(e)
}

// PasteText is the Paste event of the first k bytes held: their text in
// a, or, with secret, appended to the secret line with an empty text.
Term.PasteText(mut self, a: &Arena, k: int, secret: bool): OutOfMemory!Event from a {
    if secret {
        self.secret_len = AppendLossy(mut self.secret[..], self.secret_len, self.in_buf[self.head..self.head + k])
        return Event.Paste(text: "")
    }
    text := try Lossy(a, self.in_buf[self.head..self.head + k])
    return Event.Paste(text: text)
}

// Find is the index of the first n in b, or none. O(len(b) x len(n)).
Find(b: []u8, n: []u8): ?int {
    return FindFrom(b, 0, n)
}

// FindFrom is the index of the first n in b at from or after, or none.
// O((len(b) - from) x len(n)) for a 6-byte n.
FindFrom(b: []u8, from: int, n: []u8): ?int {
    var i := from
    for i + n.Len() <= b.Len() {
        if b[i] == n[0] && Same(b[i..i + n.Len()], n) {
            return some(i)
        }
        i += 1
    }
    return none
}

// Compact moves the bytes held to the buffer's start and zeroes the bytes
// they leave behind.
Term.Compact(mut self) {
    if self.head == 0 {
        return
    }
    n := self.tail - self.head
    for i in 0..n {
        self.in_buf[i] = self.in_buf[self.head + i]
    }
    for i in n..self.tail {
        self.in_buf[i] = 0
    }
    self.head = 0
    self.tail = n
}

// Fill waits for input and appends what is queued to the buffer: true when
// it read something or noted a resize, false when bounded and
// EscapeWait passed with nothing. The buffer is compacted first. On Linux
// and macOS Fill takes both signals' marks (SIGWINCH, SIGCONT: core.IoSignalMark)
// and starts a task for each that waits from its mark, then sets raw mode
// again while the Term is raw and compares the size (three system calls),
// then waits: a signal after the marks is one the tasks see, however late
// a task's wait starts, at any carrier count.
// On an Io that has no marks (a Recorder's Tape over a real terminal) the
// tasks take their baselines in their waits, and Fill compares once both
// are about to: at more than one carrier a few instructions between a
// task's count and its baseline stay uncovered there. A simulated terminal
// (std.sim) has no shell, no SIGWINCH and no SIGCONT, so those
// POSIX steps are skipped for it: a Sim run that reads the terminal makes
// the same Sim operations and tasks on every target, and a wait
// the Sim ends with no cause (main alone, nothing queued, no deadline: a
// deadlock) is Canceled instead of a retry.
Term.Fill(mut self, io: Io, bounded: bool, left: i64): Error!bool {
    self.Compact()
    if InBytes - self.tail < Room {
        // Only a paste fills the buffer, and PasteStep empties a full one.
        fail Device(code: 0)
    }
    native := !rt.IsForeign(self.input)
    posix := (Target.os == core.Os.Linux || Target.os == core.Os.Macos) && native
    var expired := false
    var resize := raw.Atomic[u32].New(0)
    var cont := raw.Atomic[u32].New(0)
    var armed := raw.Atomic[u32].New(0)
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var before := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var sized := false
    scope sc := io.Scope() {
        if bounded {
            sc.CancelAfter(core.DurationOf(left))
        }
        if posix {
            // The marks come first, on this task: a delivery after them
            // ends the task's wait whenever that wait starts. A
            // signal whose mark the Io does not answer gets the plain
            // wait, which counts itself into armed right before it takes
            // its baseline; the yields below return once each such task
            // has. At one carrier a task runs until it parks in that wait,
            // baseline taken; at more, the few instructions between armed
            // and the baseline stay uncovered on that path. A cancel ends
            // the yields early, and the wait below then ends at once.
            late := if SignalLate.Load() == 0 { 0 } else { SignalLate.Swap(0) }
            var plain: u32 = 0
            m3 := core.IoSignalMark(io, 3)
            if m3.err == core.SysErr.None {
                sc.go Marked(&sc, io, 3, Trunc[u64](m3.value), late, &resize)
            } else {
                plain += 1
                sc.go Signalled(&sc, io, 3, &resize, &armed)
            }
            m4 := core.IoSignalMark(io, 4)
            if m4.err == core.SysErr.None {
                sc.go Marked(&sc, io, 4, Trunc[u64](m4.value), late, &cont)
            } else {
                plain += 1
                sc.go Signalled(&sc, io, 4, &cont, &armed)
            }
            for armed.Load() < plain {
                io.Yield() else {
                    break
                }
            }
            before = self.Compare(io, mut sized)
        }
        if before.err == core.SysErr.None && !sized {
            r = rt.TermWait(io, self.input)
        }
        expired = sc.Expired()
        sc.Cancel()
    }
    _ = try Check(before)
    if sized {
        self.resized = true
        return true
    }
    if cont.Load() != 0 && self.is_raw {
        // The shell that stopped this process may have reset the mode.
        self.is_raw = false
        try self.SetMode(io, core.IoTermRaw)
        self.is_raw = true
    }
    if resize.Load() != 0 {
        self.resized = true
        return true
    }
    if r.err == core.SysErr.Canceled {
        // The caller's cancel or deadline, or this wait's own bound: std's
        // one check (core.SyncCancel) tells them apart.
        core.SyncCancel(io) else {
            fail Canceled
        }
        if expired {
            return false
        }
        if !native {
            // The Sim ended the wait with nothing to come: no signal task
            // runs for its terminal, so no wake of this scope's own is
            // left.
            fail Canceled
        }
        return true
    }
    // A mode set during the wait (by another task, or by a shell while
    // this process is stopped) does not end it: rt's wait goes on
    // (std/sys/linux/poll_uring.mk).
    _ = try Check(r)
    if Target.os == core.Os.Windows {
        try self.ReadRecords(io)
    } else {
        n := try Check(rt.TermRead(io, self.input, mut self.in_buf[self.tail..]))
        if n < 0 || n > i64(InBytes - self.tail) {
            fail Device(code: 0)
        }
        if n == 0 {
            // Ready with nothing to read, again and again: the terminal hung
            // up (a read of it is the end).
            self.empties += 1
            if self.empties >= 3 {
                fail Device(code: 5)
            }
        } else {
            self.empties = 0
        }
        self.tail += Trunc[int](n)
    }
    return true
}

// Compare is Fill's POSIX step after the marks: Between on io masked
// (neither of its calls is a cancellation point), then FillPause's test
// sleep on io itself, which a signal task's cancel ends. They are one
// helper so the pause moves with the compare: a compare moved above the
// marks takes the pause with it, and the winchpause block's resize, sent
// inside the pause, then falls before the marks and is lost, so the block
// goes red. sized: as Between's. O(1); allocates nothing.
Term.Compare(mut self, io: Io, sized: mut bool): core.IoResult {
    var changed := false
    r := self.Between(core.IoMasked(io), mut changed)
    *sized = changed
    Paused(io)
    return r
}

// Between is what Fill does on Linux and macOS once the marks are taken (or, on an
// Io with no marks, once the signal tasks are armed):
// raw mode set again while the Term is raw, since a stop and a continue
// that came between two reads reached no wait and the shell may have put
// line mode back (raw over raw sets the same termios: TCGETS, then
// TCSETS, which flushes nothing), and the size compared, since a SIGWINCH
// between two reads reached no wait (sized set when it changed). The
// mode's failure, else a success. On a masked io: neither is a
// cancellation point. O(1); allocates nothing.
Term.Between(mut self, io: Io, sized: mut bool): core.IoResult {
    if self.is_raw {
        var old: [64]u8 = [0; 64]
        var empty: [0]u8 = []
        r := rt.TermMode(io, self.input, self.out, core.IoTermRaw, empty[..], mut old[..])
        if r.err != core.SysErr.None {
            return r
        }
    }
    if self.cols > 0 {
        (cols, rows) := self.Size(io) else {
            (cols: self.cols, rows: self.rows)
        }
        *sized = cols != self.cols || rows != self.rows
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

// FillPause is a test hook (tests_pty.mk's "winchpause" child):
// when set, the next POSIX Fill sleeps that many milliseconds between its
// size compare and its wait (Compare), once the marks are taken (or, on an
// Io with no marks, its signal tasks armed). 0 in every other
// program, where it costs Fill one load.
var FillPause: raw.Atomic[i64] = raw.Atomic[i64].New(0)

// Paused sleeps FillPause's milliseconds once, if set; a signal task's
// cancel ends the sleep.
Paused(io: Io) {
    if FillPause.Load() == 0 {
        return
    }
    ms := FillPause.Swap(0)
    io.Sleep(core.DurationOf(ms * 1_000_000)) else {}
}

// SignalLate is a test hook (tests_pty.mk's "winchlate" child): when set,
// the next POSIX Fill's signal tasks sleep that many milliseconds before
// their waits start, as a task a busy carrier runs late would, so a
// SIGWINCH that comes after the compare and before a wait starts can be
// sent on purpose. 0 in every other program, where it costs Fill one load.
var SignalLate: raw.Atomic[i64] = raw.Atomic[i64].New(0)

// Marked waits for the runtime's signal s (3 resize, 4 continue) from
// mark, Fill's core.IoSignalMark taken before its size compare, and when
// a delivery has come since the mark, sets got and ends the read's wait.
// late is SignalLate's milliseconds (0 none); a cancel during them ends
// the task.
Marked(sc: &Scope, io: Io, s: u32, mark: u64, late: i64, got: &raw.Atomic[u32]) {
    if late > 0 {
        io.Sleep(core.DurationOf(late * 1_000_000)) else {
            return
        }
    }
    r := core.IoSignalFrom(io, s, mark)
    if r.err == core.SysErr.None {
        got.Store(1)
        sc.Cancel()
    }
}

// Signalled waits for the runtime's signal s (3 resize, 4 continue) and,
// when it comes, sets got and ends the read's wait: Fill's path on an Io
// with no marks. It counts itself into armed right before the wait, whose
// baseline the runtime takes at the call: a delivery after that is seen.
Signalled(sc: &Scope, io: Io, s: u32, got: &raw.Atomic[u32], armed: &raw.Atomic[u32]) {
    _ = armed.Add(1)
    r := core.IoSignal(io, s)
    if r.err == core.SysErr.None {
        got.Store(1)
        sc.Cancel()
    }
}

// ReadRecords takes the console's queued input records (Windows) and
// appends their characters to the buffer as UTF-8, in batches of at most
// Records, until a batch comes back short or the room is spent. The
// records' bytes are wiped from the stack after each batch.
Term.ReadRecords(mut self, io: Io): Error!void {
    var recs: [1280]u8 = [0; 1280]
    for {
        room := RecordsFor(InBytes - self.tail, self.high)
        count := if room < Records { room } else { Records }
        if count == 0 {
            return
        }
        n := try Check(rt.TermRead(io, self.input, mut recs[0..count * RecordBytes]))
        if n < 0 || n > i64(count * RecordBytes) || n % i64(RecordBytes) != 0 {
            kernel.Wipe(mut recs[..])
            fail Device(code: 0)
        }
        got := Trunc[int](n) / RecordBytes
        for k in 0..got {
            self.Record(recs[k * RecordBytes..(k + 1) * RecordBytes])
        }
        kernel.Wipe(mut recs[0..got * RecordBytes])
        if got < count {
            return
        }
    }
}

// RecordsFor is how many records may be read into free bytes when high
// (0 none) is a high surrogate left from before: a record adds at most 3
// bytes once the 3 a pending high may add are set aside (a record that
// completes or breaks a pair spends what its high saved), so count
// records add at most 3 x count + 3. At least 1 whenever free >= Room.
RecordsFor(free: int, high: u32): int {
    pending := if high != 0 { 3 } else { 0 }
    left := free - pending
    return if left > 0 { left / 3 } else { 0 }
}

// Record turns one INPUT_RECORD into input: a KEY_EVENT (type 1) with key
// down (offset 4), virtual key (10) and UTF-16 unit (14); a
// WINDOW_BUFFER_SIZE_EVENT (4) is a resize. A key-up record counts only
// for VK_MENU with a unit: the console types a character entered with
// Alt and the keypad, or pasted outside bracketed paste, there. A key-down
// with no unit is dropped (a modifier, a dead key) unless it is Ctrl with
// Space or 2 and no right Alt (control state at 16; AltGr is right Alt with
// left Ctrl): NUL, as a terminal sends Ctrl+Space.
Term.Record(mut self, r: []u8) {
    kind := U16At(r, 0)
    if kind == 4 {
        // The buffer's size, the fallback when the window cannot be asked.
        self.cols = int(U16At(r, 4))
        self.rows = int(U16At(r, 6))
        self.resized = true
        return
    }
    if kind != 1 {
        return
    }
    down := U16At(r, 4) != 0 || U16At(r, 6) != 0
    unit := u32(U16At(r, 14))
    vk := U16At(r, 10)
    ks := U16At(r, 16)
    if unit == 0 && down && (ks & 0x0C) != 0 && (ks & 0x01) == 0 && (vk == 0x20 || vk == 0x32) {
        self.Unit(0)
        return
    }
    if unit == 0 || !down && vk != 0x12 {
        return
    }
    self.Unit(unit)
}

// U16At is the little-endian u16 at r[i].
U16At(r: []u8, i: int): u16 {
    return u16(r[i]) | (u16(r[i + 1]) << 8)
}

// Unit appends one UTF-16 unit as UTF-8: a high surrogate waits in high
// for its low unit; one that never gets it, and a low unit alone, are
// U+FFFD (the Encoding Standard's UTF-16 decoder). It adds at most 6 bytes
// (RecordsFor sizes a read for it).
Term.Unit(mut self, u: u32) {
    if u >= 0xDC00 && u <= 0xDFFF {
        if self.high != 0 {
            self.PutScalar(0x10000 + ((self.high - 0xD800) << 10) + (u - 0xDC00))
            self.high = 0
        } else {
            self.PutScalar(0xFFFD)
        }
        return
    }
    if self.high != 0 {
        self.PutScalar(0xFFFD)
        self.high = 0
    }
    if u >= 0xD800 && u <= 0xDBFF {
        self.high = u
        return
    }
    self.PutScalar(u)
}

// PutScalar appends the scalar u as UTF-8 at tail.
Term.PutScalar(mut self, u: u32) {
    var b := mut self.in_buf[self.tail..]
    if u < 0x80 {
        b[0] = Trunc[u8](u)
        self.tail += 1
    } else if u < 0x800 {
        b[0] = Trunc[u8](0xC0 | (u >> 6))
        b[1] = Trunc[u8](0x80 | (u & 0x3F))
        self.tail += 2
    } else if u < 0x10000 {
        b[0] = Trunc[u8](0xE0 | (u >> 12))
        b[1] = Trunc[u8](0x80 | ((u >> 6) & 0x3F))
        b[2] = Trunc[u8](0x80 | (u & 0x3F))
        self.tail += 3
    } else {
        b[0] = Trunc[u8](0xF0 | (u >> 18))
        b[1] = Trunc[u8](0x80 | ((u >> 12) & 0x3F))
        b[2] = Trunc[u8](0x80 | ((u >> 6) & 0x3F))
        b[3] = Trunc[u8](0x80 | (u & 0x3F))
        self.tail += 4
    }
}

// SecretMax bounds a secret: characters typed past it are ignored. The
// Term's secret line holds it.
const SecretMax: int = 1024

/// Reads one line with nothing echoed, for a password or a key. Enter (or
/// a line feed, Ctrl+J) ends it, Backspace erases a character and Ctrl+U
/// the whole line. A paste is taken as typed; other keys are ignored.
/// Characters past 1,024 bytes are ignored. Raw mode is set for the line
/// when it is not on, and the mode before is put back when the line ends
/// or fails. A line break is written after Enter and Ctrl+C.
///
/// Canceled on Ctrl+C, with what was typed wiped. Canceled in a cancelled
/// scope or past a deadline, with what was typed kept in the terminal's
/// state, so the next call goes on with it, and raw mode left in force, so
/// keys typed before that call are neither echoed nor held for a line. The
/// next call puts the mode back when its line ends, as Restore and Close
/// do. Until then the terminal is raw for every purpose: a Ctrl+C typed
/// meanwhile raises no signal, and the next call reads it and is Canceled.
/// Close wipes the line. Under a simulated Io, also Canceled, with the line
/// kept, in a deadlock, as Read is.
///
/// O(n); allocates the line in a, and nothing for a paste, whose bytes go
/// straight into the line.
export Term.ReadSecret(mut self, a: &Arena, io: Io): Error!str from a {
    // A line a cancel cut keeps raw mode in force for this call: the mode
    // before that line is the one to put back.
    was_raw := self.is_raw && !self.secret_raw
    if !self.is_raw {
        try self.Raw(io)
    }
    self.secret_raw = false
    var ended := false
    self.SecretLine(a, io, mut ended) else e {
        // Ctrl+C ended the line and wiped it; a cancel, a deadline or a
        // failed read keeps it for the next call or Close. A cancel or a
        // deadline also keeps the raw mode this line set.
        canceled := match e {
            Canceled { true }
            _ { false }
        }
        if canceled && !ended && !was_raw {
            self.secret_raw = true
        } else {
            self.EndSecret(io, was_raw, ended)
        }
        fail e
    }
    self.EndSecret(io, was_raw, true)
    // Must: the line holds whole scalars only (Append writes one whole,
    // Back removes one whole), so From cannot fail.
    out := a.Dup(str.From(self.secret[0..self.secret_len]).Must()) else {
        self.WipeSecret()
        fail OutOfMemory
    }
    self.WipeSecret()
    return out
}

// EndSecret puts back the mode ReadSecret found, whatever went wrong, and
// ends the secret's line on the screen when the line ended (Enter or
// Ctrl+C), all of the line break written: a cancelled read leaves the line
// open for the next call.
Term.EndSecret(mut self, io: Io, was_raw: bool, ended: bool) {
    if ended {
        _ = WriteAll(core.IoMasked(io), self.out, "\r\n".AsBytes())
    }
    if !was_raw {
        self.RestoreMode(io) else {}
    }
}

// WipeSecret zeroes the secret line with stores the optimiser keeps.
Term.WipeSecret(mut self) {
    kernel.Wipe(mut self.secret[..])
    self.secret_len = 0
}

// SecretLine reads keys into the secret line until Enter or a line feed
// (ended set). Ctrl+C wipes the line, sets ended and is Canceled.
Term.SecretLine(mut self, a: &Arena, io: Io, ended: mut bool): Error!void {
    for {
        e := try self.Next(a, io, true)
        match e {
            .Key(key, ch, mods) {
                match key {
                    .Enter {
                        *ended = true
                        return
                    }
                    .Backspace {
                        self.secret_len = Back(self.secret[0..self.secret_len])
                    }
                    .Char {
                        if mods.ctrl && ch == 'c' {
                            self.WipeSecret()
                            *ended = true
                            fail Canceled
                        }
                        if mods.ctrl && ch == 'j' {
                            // A line feed: raw mode gives Enter as
                            // CR, but input fed as text lines ("pw\n") ends
                            // them with LF.
                            *ended = true
                            return
                        }
                        if mods.ctrl && ch == 'u' {
                            self.WipeSecret()
                        } else if mods.ctrl && ch == 'h' {
                            self.secret_len = Back(self.secret[0..self.secret_len])
                        } else if !mods.ctrl && !mods.alt {
                            self.secret_len = Append(mut self.secret[..], self.secret_len, ch)
                        }
                    }
                    _ {}
                }
            }
            _ {}
        }
    }
}

// AppendLossy appends the scalars of b, each ill-formed UTF-8 subpart as
// U+FFFD, leaving out CR and LF, at line[n..] while they fit, and returns
// the new length. O(len(b)); allocates nothing.
AppendLossy(line: mut []u8, n: int, b: []u8): int {
    var m := n
    var i := 0
    for i < b.Len() {
        if b[i] < 0x80 {
            if b[i] != b'\r' && b[i] != b'\n' {
                m = Append(line, m, char(b[i]))
            }
            i += 1
            continue
        }
        (u, n: k) := Utf8At(b[i..], true)
        m = Append(line, m, Cast[char](u))
        i += k
    }
    return m
}

// Back is the length of b without its last scalar.
Back(b: []u8): int {
    var k := b.Len()
    for k > 0 {
        k -= 1
        if b[k] & 0xC0 != 0x80 {
            return k
        }
    }
    return 0
}

// Append writes c's UTF-8 at line[n..] when it fits, and returns the new
// length.
Append(line: mut []u8, n: int, c: char): int {
    var b: [4]u8 = [0; 4]
    u := u32(c)
    var k := 1
    if u < 0x80 {
        b[0] = Trunc[u8](u)
    } else if u < 0x800 {
        b[0] = Trunc[u8](0xC0 | (u >> 6))
        b[1] = Trunc[u8](0x80 | (u & 0x3F))
        k = 2
    } else if u < 0x10000 {
        b[0] = Trunc[u8](0xE0 | (u >> 12))
        b[1] = Trunc[u8](0x80 | ((u >> 6) & 0x3F))
        b[2] = Trunc[u8](0x80 | (u & 0x3F))
        k = 3
    } else {
        b[0] = Trunc[u8](0xF0 | (u >> 18))
        b[1] = Trunc[u8](0x80 | ((u >> 12) & 0x3F))
        b[2] = Trunc[u8](0x80 | ((u >> 6) & 0x3F))
        b[3] = Trunc[u8](0x80 | (u & 0x3F))
        k = 4
    }
    if n + k > line.Len() {
        return n
    }
    for i in 0..k {
        line[n + i] = b[i]
    }
    return n + k
}

// Same is whether a and b, of one length, hold the same bytes.
Same(a: []u8, b: []u8): bool {
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}
