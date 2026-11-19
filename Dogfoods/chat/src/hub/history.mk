// Why: Recent messages must be listable without blocking senders, and the
// history file must be written without slowing them.
//
// What: Entry, Ring (the last 64 messages), History, LogError and the writer
// task HistoryWriter.
//
// How: Senders append to a pending list under a lock and wake the writer; the
// writer moves pending messages into the fixed ring and appends them to the
// file. A write error stops the file but keeps the ring current.

import std.fs
import std.sync
import src.proto

/// One message as history keeps it.
export struct Entry {
    export room: proto.Name
    export nick: proto.Name
    len: int
    body: [200]u8
}

/// The message text, cut to 200 bytes on a scalar boundary when it was made.
export Entry.Text(&self): str from self {
    return str.From(self.body[..self.len]).Must()
}

/// The last 64 messages of the whole server, oldest first, in a fixed ring:
/// plain, so a lock can hand out a copy.
export struct Ring {
    entries: [64]Entry
    next: int
    count: int
}

/// An empty ring. O(1).
export Ring.Empty(): Ring {
    e := Entry(room: proto.Name.Of(""), nick: proto.Name.Of(""), len: 0, body: [0; 200])
    return Ring(entries: [e; 64], next: 0, count: 0)
}

/// Adds a message, overwriting the oldest when full. O(text).
export Ring.Add(mut self, room: str, nick: str, text: str) {
    var e := Entry(room: proto.Name.Of(room), nick: proto.Name.Of(nick), len: 0, body: [0; 200])
    var n := text.Len()
    if n > 200 {
        n = 200
        for n > 0 && text.Byte(n) & 0xC0 == 0x80 {
            n -= 1
        }
    }
    for i in 0..n {
        e.body[i] = text.Byte(i)
    }
    e.len = n
    self.entries[self.next] = e
    self.next = (self.next + 1) % 64
    if self.count < 64 {
        self.count += 1
    }
}

/// How many messages the ring holds.
export Ring.Len(&self): int {
    return self.count
}

/// The `i`th oldest message, `i` below `Len()`.
export Ring.At(&self, i: int): &Entry from self {
    first := (self.next - self.count + 64) % 64
    return &self.entries[(first + i) % 64]
}

/// Why the history file could not be written.
export type LogError = fs.Error | WriteError

/// The messages waiting for the history writer, and the history itself.
export struct History {
    /// Messages not yet written by the writer task.
    export pending: Mutex[Ring]
    /// What `HISTORY` answers from.
    export recent: Mutex[Ring]
    /// Wakes the writer when `pending` gains a message or shutdown begins.
    export wake: sync.Notify
}

/// An empty history. O(1).
export History.New(): History {
    return History(pending: Mutex.New(Ring.Empty()), recent: Mutex.New(Ring.Empty()), wake: sync.Notify.New())
}

/// Hands a message to the writer. Fails with `Canceled` when the scope is
/// cancelled before the lock is taken; the message is then not recorded.
export History.Record(&self, io: Io, room: str, nick: str, text: str): Canceled!void {
    var g := try self.pending.Lock(io)
    g.Mut().Add(room, nick, text)
    g.Unlock()
    self.wake.NotifyOne()
}

/// Moves pending messages into `recent` and writes each as a line to `log`
/// when there is one; returns how many moved. Fails with `Canceled` when a
/// lock is cancelled, and with the file's error when a write fails, the
/// messages then being in `recent` but not in the file.
export History.Flush(&self, io: Io, log: mut ?fs.File): LogError!int {
    var g := try self.pending.Lock(io)
    batch := *g.Ref()
    g.Store(Ring.Empty())
    g.Unlock()
    if batch.Len() == 0 {
        return 0
    }
    var r := try self.recent.Lock(io)
    for i in 0..batch.Len() {
        e := batch.At(i)
        r.Mut().Add(e.room.Str(), e.nick.Str(), e.Text())
    }
    r.Unlock()
    if f := mut *log {
        var buf: [4096]u8 = [0; 4096]
        var w := f.Writer(io, mut buf[..])
        for i in 0..batch.Len() {
            e := batch.At(i)
            try Print(mut w, "{room} <{nick}> {text}\n", (room: e.room.Str(), nick: e.nick.Str(), text: e.Text()))
        }
        try w.Flush()
        try f.Sync(io)
    }
    return batch.Len()
}

/// The history writer task: waits to be woken, then flushes, until shutdown
/// cancels its wait. A write error is kept in `failed`, and the writer stops
/// writing the file but keeps `recent` current. The file stays open for the
/// caller to close.
export HistoryWriter(io: Io, h: &History, log: mut ?fs.File, failed: mut ?LogError, written: mut int) {
    for {
        h.wake.Wait(io) else {
            break
        }
        n := h.Flush(io, log) else e {
            match e {
                Canceled {
                    break
                }
                _ {
                    *failed = some(e)
                    if f := Replace(log, none) {
                        f.Abandon()
                    }
                    0
                }
            }
        }
        *written += n
    }
    // a last flush after cancellation, so nothing recorded is lost
    *written += h.Flush(io.NoCancel(), log) else e {
        *failed = some(e)
        0
    }
}
