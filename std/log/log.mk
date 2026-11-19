// Why: programs need levelled log lines with a checked format, without a
// logger that allocates or holds bytes between calls.
//
// What: Logger (New, NewTimed, Log, Debug, Info, Warn, Error) and Level:
// one line per call with the level first, and the time before it when the
// logger was made with NewTimed.
//
// How: a Logger holds the caller's Writer, so it is `from w`, allocates
// nothing and holds no bytes between calls: the Writer buffers (Stdout,
// Text). A call below the logger's level returns before it touches the
// Writer.
//
// Each line is gathered in a LineMax-byte buffer on the caller's stack and
// handed to the Writer with one Bytes call, so a Writer with no buffer of
// its own (a Text, a process stream) sees one write per line, not one per
// piece of it. A longer line is handed on in order as the buffer fills: a
// run each time it is full, a rest past LineMax straight from the
// formatter, and what is left at the end, each run cut back to the end of
// its last whole character, the cut-off bytes going first in the next run
// (one more write then, for that character), so a Writer that judges each
// run as UTF-8, a Text's, takes every valid line; the Writer is handed
// exactly the line's bytes, in order. A Logger stays on its task (its
// Writer is not Send), but other tasks reach a process stream through a
// log.Sink, whose records are one write each; the stream takes each write
// whole under its lock, so no record lands inside a line up to LineMax
// bytes, and inside a longer one only between its runs. A line that fails
// stops there, and the Writer has been handed only the runs before the
// failure: none for a line up to LineMax bytes whose Format fails, panics
// or traps (so a panic report shows no part of it), and for one whose
// write fails, what that Writer keeps of a failed write. The Writer judges
// what a Format wrote together with the rest of the run, not each write on
// its own: a Format that ignores the failure of a write it made into the
// line's buffer cannot fail there, and the Writer judges those bytes with
// the line (into a Text, bytes that are not UTF-8 fail the whole line), and
// a character a Format writes in two pieces reaches the Writer whole. A
// Format that flushes its Writer mid-line hands the line so far on to the
// logger's Writer, and does not flush that Writer: a Logger never does.
//
// The format is Print's: every method passes its `const f` on to
// Line, which passes it to Print, so a format that does not match its
// record is E0706 at the Logger call that wrote it, and the line is written
// from Print's piece table.

import std.core

/// The four levels, in increasing order of severity.
export enum Level {
    Debug
    Info
    Warn
    Error
}

/// A writer of log lines over a Writer it borrows, with the least level it
/// writes. It buffers nothing itself; the Writer does. No exported field:
/// make one with New or NewTimed.
export view struct Logger {
    w: mut Writer
    level: Level
    clock: ?Io
    start: i64
}

/// A logger that writes `LEVEL message` lines to w, such as
/// `INFO listening on 8080`, and drops calls below level. O(1); allocates
/// nothing.
export Logger.New(w: mut Writer, level: Level): Logger from w {
    return Logger(w: w, level: level, clock: none, start: 0)
}

/// A logger whose lines start with the time since it was made, in seconds
/// and microseconds: `S.UUUUUU LEVEL message`, such as
/// `0.000412 INFO ready`. The time is read through io on the monotonic
/// clock, so it is relative, never the time of day. O(1); allocates
/// nothing.
export Logger.NewTimed(io: Io, w: mut Writer, level: Level): Logger from w {
    return Logger(w: w, level: level, clock: some(io), start: core.IoNow(io).value)
}

/// Writes one line at level, unless level is below the logger's: the
/// stamp of a NewTimed logger, the level's word (DEBUG, INFO, WARN or
/// ERROR), a space, the message and a newline. f and args are Print's
/// format and record, checked at the call (E0706); a message with no holes
/// needs no record. Fails with the Writer's error, or when a field's
/// Format fails. O(length of the line); allocates nothing.
export Logger.Log[A: Fields[Format] + Plain](mut self, level: Level, const f: str, args: A): WriteError!void {
    if Rank(level) < Rank(self.level) {
        return
    }
    try Line(mut *self.w, self.clock, self.start, level, f, args)
}

/// Log at Debug: dropped unless the logger's level is Debug. Fails with
/// the Writer's error. O(length of the line); allocates nothing.
export Logger.Debug[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try self.Log(Level.Debug, f, args)
}

/// Log at Info: dropped when the logger's level is Warn or Error. Fails
/// with the Writer's error. O(length of the line); allocates nothing.
export Logger.Info[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try self.Log(Level.Info, f, args)
}

/// Log at Warn: dropped when the logger's level is Error. Fails with the
/// Writer's error. O(length of the line); allocates nothing.
export Logger.Warn[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try self.Log(Level.Warn, f, args)
}

/// Log at Error, which no logger drops. Fails with the Writer's error.
/// O(length of the line); allocates nothing.
export Logger.Error[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try self.Log(Level.Error, f, args)
}

// Rank orders the levels: Debug 0 to Error 3.
Rank(l: Level): int {
    return match l {
        .Debug { 0 }
        .Info { 1 }
        .Warn { 2 }
        .Error { 3 }
    }
}

// Name is the level's word in a line, as Go's slog writes it.
Name(l: Level): str {
    return match l {
        .Debug { "DEBUG " }
        .Info { "INFO " }
        .Warn { "WARN " }
        .Error { "ERROR " }
    }
}

// Line writes one whole line: the time since start when clock is some, the
// level, the message, and one newline. f is Print's format, a const
// parameter passed on unchanged from the public method, so it is checked
// against args at that method's call. The line is formatted into a
// Writer over LinePort with lb as its inline buffer, so it reaches w as
// runs, in order, by Writer.Bytes's rule, each cut back to a character's
// end by LineWrite (the module's header); the line Writer carries w's Io,
// so a Format sees w's Io. What LineWrite still holds at the
// end, the start of a character the line's bytes never finished, goes to w
// last.
Line[A: Fields[Format] + Plain](w: mut Writer, clock: ?Io, start: i64, level: Level, const f: str, args: A): WriteError!void {
    var lb: [LineMax]u8 = [0; LineMax]
    wio := core.WriterIo(&*w)
    var out := LineOut(w: w, held: [0; 3], n: 0)
    var lw := (&LinePort).Writer(wio, mut out, mut lb[..])
    if io := clock {
        try Stamp(mut lw, core.IoNow(io).value - start)
    }
    try lw.Str(Name(level))
    try Print(mut lw, f, args)
    try lw.Str("\n")
    // Hands the rest to w; LineFlush flushes nothing.
    try lw.Flush()
    if out.n > 0 {
        rest := out.held
        try out.w.Bytes(rest[..out.n])
    }
}

/// The bytes of a line a Logger gathers before it hands them to its Writer
/// (the module's header): a line up to this long is one write.
const LineMax = 256

// LineOut is LinePort's state: the logger's Writer, and the first n bytes
// of a character that the last run ended inside, which LineWrite holds
// back, so a Writer that judges each run as UTF-8 (a Text's) is handed
// whole characters; a character has at most 4 bytes, so at most 3 are held.
view struct LineOut {
    w: mut Writer
    held: [3]u8
    n: int
}

// LinePort's entries: LineWrite hands a run to the logger's Writer, and
// flush forces nothing, since a Logger never flushed its Writer.
const LinePort = core.WritePortMut[LineOut].New(LineWrite, LineFlush)

// LineWrite hands b on after the bytes held from the last run: first the
// held character, completed by b's leading continuation bytes, as one write
// (if b ends before it is complete, they are held together); then b's
// remaining bytes up to the end of its last whole character, as one write,
// holding back the start of a character b ends inside (Incomplete). Every
// byte goes on once, in order: what the line holds at its end Line hands
// on after the last run. A run of bytes that are not UTF-8 is handed on as
// it is, and a Writer that judges runs refuses it.
LineWrite(s: mut LineOut, io: Io, b: []u8): WriteError!void {
    _ = io
    n := b.Len()
    if s.n == 0 && (n == 0 || b[n - 1] < 0x80) {
        // Nothing held and b ends in ASCII, so it ends a character: the
        // common run, every line's last (it ends in a newline).
        try s.w.Bytes(b)
        return
    }
    var i := 0
    if s.n > 0 {
        var c: [4]u8 = [0; 4]
        for k in 0..s.n {
            c[k] = s.held[k]
        }
        var m := s.n
        want := LeadLen(c[0])
        for i < b.Len() && m < want && b[i] & 0xC0 == 0x80 {
            c[m] = b[i]
            m += 1
            i += 1
        }
        if m < want && i == b.Len() {
            // b ended before the character did: hold all of it.
            for k in 0..m {
                s.held[k] = c[k]
            }
            s.n = m
            return
        }
        s.n = 0
        try s.w.Bytes(c[..m])
    }
    rest := b[i..]
    k := Incomplete(rest)
    try s.w.Bytes(rest[..rest.Len() - k])
    for j in 0..k {
        s.held[j] = rest[rest.Len() - k + j]
    }
    s.n = k
}

LineFlush(s: mut LineOut, io: Io): WriteError!void {
    _ = s
    _ = io
}

// LeadLen is the length of the character whose first byte is c by UTF-8's
// leads (2 from C2 to DF, 3 from E0 to EF, 4 from F0 to F4), 1 for any
// other byte, which needs nothing after it to be judged.
LeadLen(c: u8): int {
    if c >= 0xC2 && c <= 0xDF {
        return 2
    }
    if c >= 0xE0 && c <= 0xEF {
        return 3
    }
    if c >= 0xF0 && c <= 0xF4 {
        return 4
    }
    return 1
}

// Incomplete is the count of b's last bytes that start a character b ends
// inside: back over at most three continuation bytes to a lead, as sink.mk's
// Finish backs, and that lead's character longer than what follows it;
// else 0 (b ends a character, or its end is not UTF-8 and is handed on).
Incomplete(b: []u8): int {
    n := b.Len()
    var j := n - 1
    low := n - 4
    for j >= 0 && j > low && b[j] & 0xC0 == 0x80 {
        j -= 1
    }
    if j < 0 || j == low || b[j] & 0xC0 == 0x80 {
        return 0
    }
    return if LeadLen(b[j]) > n - j { n - j } else { 0 }
}

// Stamp writes nanos as `S.UUUUUU ` (seconds, then six digits of
// microseconds). A negative difference, which a monotonic clock never
// gives, and an Io whose clock fails (IoNow's value 0), are written as 0.
Stamp(w: mut Writer, nanos: i64): WriteError!void {
    t: i64 = if nanos < 0 { 0 } else { nanos }
    secs := t / 1_000_000_000
    var micros := (t % 1_000_000_000) / 1000
    // d is ".UUUUUU ": the digits are written from the last one back
    var d: [8]u8 = [0; 8]
    d[0] = 0x2E
    var k := 6
    for k > 0 {
        d[k] = Trunc[u8](micros % 10) + 0x30
        micros = micros / 10
        k -= 1
    }
    d[7] = 0x20
    try secs.Format(w)
    try w.Bytes(d[..])
}
