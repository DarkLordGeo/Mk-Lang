// Why: many tasks report to one stream or file, and their records must
// never interleave, even while their scopes are being cancelled.
//
// What: Sink, a report sink that tasks share, made by Stderr, Stdout or
// ToFile, with Log, Debug, Info, Warn and Error.
//
// How: a Sink holds no Writer: it names a process stream, or borrows a
// Mutex[fs.File], and each call formats one record into a buffer on the
// caller's stack, then writes it with one write under the stream's or the
// file's own lock. A call is not a cancellation point: a report made while
// its scope is being cancelled is still written.

import std.core
import std.fs

/// The most bytes one record takes, its newline included. A longer record
/// is cut on a UTF-8 boundary and ends with the mark ` [cut N bytes]`,
/// where N counts the bytes of the record that were not written.
const RecordMax = 4096

/// A log destination that many tasks share: a process stream, or a file
/// under a mutex, and the least level it writes. Each call writes its
/// record with one write, so the records of different tasks never
/// interleave. Plain, Send and Share, and it borrows no Writer: copy it
/// into each task, or lend it with &. Make one with Stderr, Stdout or
/// ToFile.
export struct Sink {
    /// The mutex a ToFile sink writes under, none for a process stream.
    /// Exported because a plain struct's reference field must be (E0410);
    /// the sink only locks it.
    export file: ?&Mutex[fs.File]
    stream: u32
    least: Level
    // emit is EmitStream or EmitFile, chosen by the constructor, so a
    // program links the file path only when it names ToFile.
    emit: fn(&Sink, Io, []u8): WriteError!void
}

/// A sink that writes records at level or above to standard error, in
/// order with System.PrintErr's bytes. O(1); allocates nothing.
export Stderr(level: Level): Sink {
    return Sink(file: none, stream: 1, least: level, emit: EmitStream)
}

/// A sink that writes records at level or above to standard output, after
/// the bytes System.Stdout's writers have buffered. O(1); allocates nothing.
export Stdout(level: Level): Sink {
    return Sink(file: none, stream: 0, least: level, emit: EmitStream)
}

/// A sink that writes records at level or above to the file f guards. The
/// program owns the file and closes it after the tasks that log to it have
/// joined. O(1); allocates nothing.
export ToFile(f: &Mutex[fs.File], level: Level): Sink from f {
    return Sink(file: some(f), stream: 0, least: level, emit: EmitFile)
}

/// Writes one record, `LEVEL message` and a newline, unless level is below
/// the sink's. The record is formatted on the stack, at most 4,096 bytes
/// with its newline: a longer one is cut on a UTF-8 boundary and ends with
/// ` [cut N bytes]`, N counting the bytes not written. Then it is written
/// whole with one write: to the process stream under its lock, or to the
/// file under its mutex, taken through io.NoCancel(), so it is not a
/// cancellation point and a record made while its scope is cancelled is
/// still written. f and args are Print's format and record, checked at the
/// call (E0706).
///
/// Fails with WriteError when the write fails, on an Io that reaches no
/// process or file, and when a field's Format fails, in which case nothing
/// is written. O(record bytes): one 4 KiB stack buffer; no allocation.
export Sink.Log[A: Fields[Format] + Plain](&self, io: Io, level: Level, const f: str, args: A): WriteError!void {
    if Rank(level) < Rank(self.least) {
        return
    }
    var buf: [RecordMax]u8 = [0; RecordMax]
    var c := core.Capped.New(mut buf[..RecordMax - 1])
    var w := c.Writer()
    try w.Str(Name(level))
    try Print(mut w, f, args)
    n := Finish(mut buf[..], c.Bytes().Len(), c.Dropped())
    e := self.emit
    try e(self, io, buf[..n])
}

/// Log at Debug: dropped unless the sink's level is Debug. Fails as Log.
/// O(record bytes); no allocation.
export Sink.Debug[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void {
    try self.Log(io, Level.Debug, f, args)
}

/// Log at Info: dropped when the sink's level is Warn or Error. Fails as
/// Log. O(record bytes); no allocation.
export Sink.Info[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void {
    try self.Log(io, Level.Info, f, args)
}

/// Log at Warn: dropped when the sink's level is Error. Fails as Log.
/// O(record bytes); no allocation.
export Sink.Warn[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void {
    try self.Log(io, Level.Warn, f, args)
}

/// Log at Error, which no sink drops. Fails as Log. O(record bytes); no
/// allocation.
export Sink.Error[A: Fields[Format] + Plain](&self, io: Io, const f: str, args: A): WriteError!void {
    try self.Log(io, Level.Error, f, args)
}

// Finish ends the record in buf, whose first kept bytes the Capped writer
// filled (it had buf's first RecordMax - 1 bytes) with dropped more not
// kept, and returns the record's length. A record that fits gets its
// newline. One that does not is cut back to a UTF-8 boundary with room for
// the mark, ` [cut N bytes]` and the newline, N counting every formatted
// byte not written. The mark's room is reserved for N's largest digit
// count, so the record never passes RecordMax bytes.
Finish(buf: mut []u8, kept: int, dropped: int): int {
    if dropped == 0 {
        buf[kept] = 0x0A
        return kept + 1
    }
    total := if dropped > IntMax - kept { IntMax } else { kept + dropped }
    var keep := buf.Len() - MarkHead.Len() - Digits(total) - MarkTail.Len()
    // A continuation byte, 10xxxxxx, never starts a scalar, and a scalar
    // has at most three: back over three at most, so text that is not
    // UTF-8 costs no more than text that is.
    low := keep - 3
    for keep > low && buf[keep] & 0xC0 == 0x80 {
        keep -= 1
    }
    var n := keep
    n = Put(buf, n, MarkHead)
    n = PutInt(buf, n, total - keep)
    n = Put(buf, n, MarkTail)
    return n
}

const IntMax = 9_223_372_036_854_775_807
const MarkHead = " [cut "
const MarkTail = " bytes]\n"

// Digits is the count of decimal digits of n, which is at least 0.
Digits(n: int): int {
    var d := 1
    var v := n
    for v >= 10 {
        v = v / 10
        d += 1
    }
    return d
}

// Put copies s into buf at n and returns the end; the caller reserved room.
Put(buf: mut []u8, n: int, s: str): int {
    b := s.AsBytes()
    (mut buf[n..n + b.Len()]).CopyFrom(b)
    return n + b.Len()
}

// PutInt writes v, at least 0, in decimal into buf at n and returns the
// end; the caller reserved Digits(v) bytes.
PutInt(buf: mut []u8, n: int, v: int): int {
    end := n + Digits(v)
    var k := end
    var x := v
    for k > n {
        k -= 1
        buf[k] = Trunc[u8](x % 10) + 0x30
        x = x / 10
    }
    return end
}

// EmitStream writes rec to the sink's process stream with one write,
// under the stream's lock and after its buffered bytes (core.IoStdio, not a
// cancellation point). An Io that reaches no process stream, and a failed
// write, are WriteError.
EmitStream(s: &Sink, io: Io, rec: []u8): WriteError!void {
    r := core.IoStdio(io, s.stream, rec)
    if r.err != core.SysErr.None {
        fail WriteError
    }
}

// EmitFile writes rec to the sink's file with one WriteAll under its
// mutex, both through io.NoCancel(), which no scope's cancel or deadline
// interrupts: the mask is taken here, so no scope opened from a masked Io
// lies past it. The lock fails only on an Io whose wait answers
// Canceled though masked, a std.sim Io whose run has ended, and only while
// another holds the mutex (the check of a free one goes on in a dead run
// and under a Recorder or Replayer that is freed or cannot replay it);
// that and a failed write are WriteError. The mutex is
// unlocked on every path.
EmitFile(s: &Sink, io: Io, rec: []u8): WriteError!void {
    m := s.file else {
        fail WriteError
    }
    q := io.NoCancel()
    var g := m.Lock(q) else {
        fail WriteError
    }
    g.Mut().WriteAll(q, rec) else {
        g.Unlock()
        fail WriteError
    }
    g.Unlock()
}
