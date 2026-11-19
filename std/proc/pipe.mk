// Why: a parent needs pipes of its own to wire children together and to
// talk to them, made apart from any child and lent to one only on request.
//
// What: proc.Pipe, PipeReader (Read, Reader, Close) and PipeWriter
// (WriteAll, Writer, Close), over std.rt's PipeOpen.
//
// How: an anonymous pipe, never inherited by a child until Spawn lends an
// end (Stdio.Reader, Stdio.Writer, Pass.Reader, Pass.Writer), which
// duplicates it. On Linux both ends are on the runtime's poller, as the ends
// Spawn makes for a Pipe disposition are, and a lent end reaches the child
// blocking (under epoll the runtime opens the pipe again for the child,
// since O_NONBLOCK belongs to the open file every duplicate shares). On
// Windows the ends are synchronous handles the runtime reads and writes on
// its blocking pool (a child cannot use an overlapped end, and none can be
// opened again as a synchronous handle on the same pipe). The ends Spawn
// makes are the same two types, on the poller on both targets.
//
// Each end is linear and owns one runtime handle and nothing else: no
// allocator memory, so neither implements Free; its Close only releases, so
// it takes no Io and is not cancellable. Read is repeatable under a cancel
// (nothing consumed), WriteAll is prefix (an unknown prefix reached the
// reader).
//
// A waiting read or write of a Pipe end holds none of the blocking pool's
// 64 threads (std/task/pool.mk). On Linux it waits on the poller and holds
// no thread (where /proc/self/fd can open the pipe again for a lend; else
// it is a wait job, as on Windows). On Windows it is a wait job, which
// holds a pool thread beyond the 64. The pool has at most 1,024 threads in
// all (task.PoolSlots) and keeps each one it starts: past that many waiting
// ends, less the threads other blocking calls hold, every other blocking
// call of the process queues behind them again (BlockingWait in
// std/task/pool.mk).

import std.core
import std.rt

/// The read end of a pipe. Read from it, or wrap it in a Reader, then
/// Close it. Linear. Send and Share: two tasks may read one end, each read
/// taking what is there. Owns one OS handle and no memory.
export linear struct PipeReader {
    h: uint
    // lend: Pipe made it, so a child can take it (as a blocking end);
    // an end Spawn made is on this process's poller and is not
    // lent.
    lend: bool
}

/// The write end of a pipe. Write to it with WriteAll, or wrap it in a
/// Writer, then Close it; the reader sees the close as the end of input.
/// Linear. Send and Share: two tasks' WriteAlls on one end never
/// interleave, since each holds the end's write lock for all of its data,
/// as net.Conn does. Owns one OS handle and no memory.
export linear struct PipeWriter {
    h: uint
    lend: bool
    // writes serialises WriteAll: its loop of partial writes, with
    // a yield between them, would otherwise interleave with another
    // task's on Linux, where a pipe write may be short.
    writes: Mutex[u8]
}

/// A new pipe: what is written to w is read from r, in order. No child
/// inherits either end unless a Command lends it (Stdio.Reader,
/// Stdio.Writer, Pass.Reader, Pass.Writer).
///
/// OutOfMemory when the process heap cannot give a handle record;
/// otherwise the OS's error as its proc.Error member (reaching the
/// process's handle limit is Os). Not a cancellation point. O(1);
/// allocates two handle records from the process heap, nothing in an
/// arena.
export Pipe(io: Io): Error!(r: PipeReader, w: PipeWriter) {
    var e: [2]uint = [0; 2]
    // The runtime answers Other with no OS code only when its record
    // cannot be had, as for a spawn.
    _ = try SpawnCheck(rt.PipeOpen(io, mut e))
    return (r: PipeReader(h: e[0], lend: true), w: PipeWriter(h: e[1], lend: true, writes: Mutex[u8].New(0)))
}

/// Reads what the pipe holds into buf, waiting until there is something,
/// and returns the number of bytes read: at least 1, or 0 at the end of
/// input (once every write end, the parent's and each child's, is closed)
/// and for an empty buf. One read, which may fill less than buf. O(n);
/// allocates nothing.
///
/// Canceled when the task's scope is cancelled; nothing is consumed, so
/// the same call again reads every byte. Os(0) when the runtime reports a
/// count outside buf.
///
/// A waiting Read holds no thread on Linux, nor on Windows for an end
/// Spawn returned. On Windows an end made by Pipe holds a thread of its
/// own, outside the 64 that run blocking calls, and up to about
/// 1,024 may wait at once, less the threads other blocking calls hold;
/// past that, the process's other blocking calls queue behind them.
export PipeReader.Read(&self, io: Io, buf: mut []u8): Error!int {
    if buf.Len() == 0 {
        return 0
    }
    r := core.IoRead(io, self.h, buf, -1)
    if r.err == core.SysErr.Closed {
        return 0
    }
    n := try Check(r)
    if n < 0 || n > i64(buf.Len()) {
        fail Os(code: 0)
    }
    return Trunc[int](n)
}

const PipeReads = core.ReadPort[PipeReader].New(PipeFill)
const PipeWrites = core.WritePort[PipeWriter].New(PipeSend, PipeFlush)

// The Reader's fill and the Writer's write: any proc.Error is the
// Reader's ReadError or the Writer's WriteError.
PipeFill(p: &PipeReader, io: Io, dst: mut []u8): ReadError!int {
    return p.Read(io, dst) else {
        fail ReadError
    }
}

PipeSend(p: &PipeWriter, io: Io, b: []u8): WriteError!void {
    p.WriteAll(io, b) else {
        fail WriteError
    }
}

// A pipe has nothing of its own to flush: WriteAll has written every byte.
PipeFlush(p: &PipeWriter, io: Io): WriteError!void {
}

/// A Reader for this end that buffers in buf. Each fill is one Read, and
/// any error becomes the Reader's ReadError. Bytes it buffered that the
/// caller did not read are lost when it is dropped. O(1); allocates
/// nothing.
export PipeReader.Reader(&self, io: Io, buf: mut []u8): Reader from self, buf {
    return (&PipeReads).Reader(io, self, buf)
}

/// Closes this end: once every read end is closed, a write to the pipe
/// fails. Infallible and not cancellable. O(1); allocates nothing.
export PipeReader.Close(self) {
    rt.Abandon(self.h)
    PipeReader(h: _, lend: _) := self
}

/// Writes every byte of data, waiting while the pipe is full. It holds the
/// end's write lock throughout, so another task's WriteAll on the same end
/// comes wholly before or after it.
///
/// Once every read end is closed: Os(32) on Linux (EPIPE), Os(232) on
/// Windows (ERROR_NO_DATA). Canceled when the task's scope is cancelled,
/// either while waiting for the lock, with nothing written, or after an
/// unknown prefix of data reached the pipe. Os(0) when a write makes no
/// progress or the runtime reports a count past data.
///
/// O(n) plus the wait for the lock; allocates nothing. A waiting write
/// holds threads as a waiting Read does.
export PipeWriter.WriteAll(&self, io: Io, data: []u8): Error!void {
    g := try self.writes.Lock(io)
    defer g.Unlock()
    var off := 0
    for off < data.Len() {
        if off > 0 {
            // A write interrupted by a cancel returns its partial count;
            // the next one must see the cancel before it blocks on a full
            // pipe (Linux's pool starts it otherwise). std's one check
            // (core.SyncCancel): only Canceled is a cancellation.
            core.SyncCancel(io) else {
                fail Canceled
            }
        }
        r := core.IoWrite(io, self.h, data[off..], -1)
        if r.err == core.SysErr.Closed {
            fail Os(code: if Target.os == core.Os.Windows { 232 } else { 32 })
        }
        n := try Check(r)
        if n <= 0 || n > i64(data.Len() - off) {
            fail Os(code: 0)
        }
        off += Trunc[int](n)
    }
}

/// A Writer for this end that buffers in buf. Each flush of the buffer is
/// one WriteAll, so another task's WriteAll falls between two flushes,
/// never inside one. Any error becomes the Writer's WriteError. Flush it
/// before dropping it. O(1); allocates nothing.
export PipeWriter.Writer(&self, io: Io, buf: mut []u8): Writer from self, buf {
    return (&PipeWrites).Writer(io, self, buf)
}

/// Closes this end: once every write end is closed, a Read of the pipe
/// returns 0. Infallible and not cancellable. O(1); allocates nothing.
export PipeWriter.Close(self) {
    rt.Abandon(self.h)
    PipeWriter(h: _, lend: _, writes: _) := self
}
