// Why: The request log is shared by every connection's task.
//
// What: LogSink and AccessLog, with InMemory, ToFile, Write and Close.
//
// How: Writes take the log's lock; a file log stamps each line with the time,
// and an in-memory log keeps lines without times for the caller to print after
// the server stops.

import std.fs
import std.log

/// Where the request log goes: a file, with the time on each line, or a heap
/// text without times that the caller prints when the server has stopped.
export enum LogSink {
    File(f: fs.File)
    Memory(text: HeapText)
}

/// The request log, shared by every connection's task.
export struct AccessLog {
    export sink: Mutex[LogSink]
    export level: log.Level
}

/// A log that keeps its lines in memory, for `Lines`.
export AccessLog.InMemory(a: &Alloc, level: log.Level): AccessLog from a {
    return AccessLog(sink: Mutex.New(LogSink.Memory(text: HeapText.New(a))), level: level)
}

/// A log that appends timed lines to `f`.
export AccessLog.ToFile(f: fs.File, level: log.Level): AccessLog {
    return AccessLog(sink: Mutex.New(LogSink.File(f: f)), level: level)
}

/// Writes one line at `level`, dropped when it is below the log's level. A
/// file line starts with the time. Waits for the log's lock; allocates the
/// line in `a`, and in the heap text through `al` for an in-memory log.
export AccessLog.Write(&self, a: &Arena, io: Io, al: &Alloc, level: log.Level, line: str): Canceled|OutOfMemory|fs.Error!void {
    var g := try self.sink.Lock(io)
    defer g.Unlock()
    match g.Mut() {
        .File(f) {
            var buf: [512]u8 = [0; 512]
            var w := f.Writer(io, mut buf[..])
            var l := log.Logger.NewTimed(io, mut w, self.level)
            l.Log(level, "{line}", (line)) else { return }
            w.Flush() else { return }
        }
        .Memory(text) {
            var t := Text.New(a)
            var w := t.Writer()
            var l := log.Logger.New(mut w, self.level)
            l.Log(level, "{line}", (line)) else { fail OutOfMemory }
            try text.Push(al, t.Str())
        }
    }
}

/// Releases the log and gives back the lines of an in-memory log, copied into
/// `a` ("" when they do not fit), or "" for a file log, which it closes.
/// `closed` is false when closing the file failed, so its last lines may be lost.
export AccessLog.Close(self, a: &Arena, io: Io, al: &Alloc): (lines: str, closed: bool) from a {
    AccessLog(sink, level) := self
    _ = level
    match sink.Into() {
        .File(f) {
            var closed := true
            f.Close(io) else {
                closed = false
                f.Abandon()
            }
            return (lines: "", closed: closed)
        }
        .Memory(text) {
            defer text.Free(al)
            lines := a.Dup(text.Str()) else { "" }
            return (lines: lines, closed: true)
        }
    }
}
