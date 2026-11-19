// Why: stdout, stderr and stdin are shared by every task of the process, so
// their writes must not interleave mid-record and a read must flush pending
// output first.
//
// What: the process-stream tables StreamWriteTable and StreamReadTable with
// StreamWrite, StreamFlush, StreamRead and StreamLine, the lock StreamLock
// and StreamUnlock, and the helpers StreamPut and WriteHandle.
//
// How: a stream writer has no inline buffer (cap 0) and its ctx is the
// stream's BufState, so every write is one table call: lock, copy into the
// shared process buffer, write the buffer at pos -1 when it fills, unlock.
// The lock is std's three-state lock word. The BufState is reached through a
// raw pointer and never as a mut reference: other tasks touch its lock word
// while this one holds it, which a mut loan (noalias) would forbid.

import std.raw

const StreamWriteTable = WriterTable(write: StreamWrite, flush: StreamFlush)
const StreamReadTable = ReaderTable(read: StreamRead, line: StreamLine)

// StreamLock takes a stream's lock word: the one three-state lock word of
// std (0 free, 1 held, 2 held with waiters; Drepper's "Futexes Are Tricky"
// in Rust std's futex form), sync_mutex.mk's LockWordSlow and ReleaseWord
// (this is their one caller). The uncontended lock is one Cas here; the
// contended path is
// LockWordSlow (a bounded read-only spin, MutexSpins, then Swap(2) and
// IoTable.wait) with a NoCancel Io, because a stream write is not a
// cancellation point, so it never fails. Acquire edge: a Cas(0, 1) or a Swap(2) that returns 0 reads
// the 0 the last StreamUnlock's Swap(0) wrote, so the previous holder's
// buffer writes happen before this holder's reads (seq_cst). Lock
// order (no cycle): stdin's lock may be held while stdout's is taken
// (StreamTake flushes stdout before a read), never the reverse. Only the
// process's last flush (FlushStd) holds stdout's, sealed, while it takes
// stderr's, and no path holding stderr's takes stdout's, so no cycle
// (TestSTDIO_ConcurrentStdinReaders drives the stdin-then-stdout order
// at 1 to 16 carriers).
StreamLock(a: &raw.Atomic[u32], io: Io) {
    if !a.Cas(0, 1) {
        LockWordSlow(a, IoMasked(io)) else {}
    }
}

// StreamUnlock releases the word (ReleaseWord: Swap(0), and one wake when
// the old value was 2; the wake never fails).
StreamUnlock(a: &raw.Atomic[u32], io: Io) {
    ReleaseWord(a, io)
}

// WriteHandle writes every byte of data to the handle at its stream
// position, looping over partial writes; false on an error or a write that
// makes no progress (which would otherwise loop forever). The runtime's
// entry retries EINTR and EAGAIN itself.
WriteHandle(io: Io, h: uint, data: []u8): bool {
    var off := 0
    for off < data.Len() {
        r := IoWrite(io, h, data[off..], -1)
        if r.err != SysErr.None || r.value <= 0 || r.value > i64(data.Len() - off) {
            return false
        }
        off += int(r.value)
    }
    return true
}

// StreamPut appends b to the stream's buffer under its lock, by Go
// bufio.Writer's rule (as Writer.BytesSlow): when b does not fit, the
// buffer is topped up and written out full, and a rest that still exceeds
// the whole buffer is written directly; drain writes the buffer out at the
// end. A drain never splits b (one write per record): when b does
// not fit behind the buffered bytes, they are written first and b whole
// after them. With wait false the lock is only tried, and a held stream is skipped
// (a panic report must not wait for the task that panicked holding it). The
// buffer is emptied even when a write fails, so a broken stream does not
// fail every later write on the same full buffer, and once a write fails
// the rest of b is dropped, not written out of order. With seal true the
// lock is kept: no write that starts later reaches the stream (SealStream).
// rel is the Io the lock is released with: io itself, but for StdWriteHere.
// False on a failed write. At most two writes per call, plus the drain.
//
// A stream write is not a cancellation point, like the lock wait: every
// write of the handle goes through io.NoCancel(). The buffer is
// shared by every Writer from Stdout() or Stderr() on every task,
// so a cancellable write would let one cancelled writer, or a cancelled
// reader's flush (StreamTake), throw away bytes other writes had
// already buffered and been told were written. Go's os.Stdout and Rust's
// stdout are not cancellable either, and Writer's error set has no
// Canceled. A cancelled scope that wants to stop writing checks its own
// cancellation (any Io call that is a cancellation point, Io.Sleep(0)).
StreamPut(st: *BufState, io: Io, rel: Io, b: []u8, drain: bool, wait: bool, seal: bool): bool {
    // A task of a Sim holds the Sim's turn from before it
    // may wait for the lock until it released it, its drain on the pool
    // included (HookMuteHere). A task that left the turn while holding the
    // lock would hang the Sim's next writer, which waits for the lock
    // holding the turn the holder needs back.
    m := if wait { HookMuteHere() } else { false }
    ok := StreamPutIn(st, io, rel, b, drain, wait, seal)
    HookUnmuteHere(m)
    return ok
}

// StreamPutIn is StreamPut with the scheduler hooks as the caller left
// them.
StreamPutIn(st: *BufState, io: Io, rel: Io, b: []u8, drain: bool, wait: bool, seal: bool): bool {
    unsafe "st is a live process stream; its buffer holds cap bytes, read and written only under the lock" {
        lock := &(*st).lock
        if wait {
            // Locked through rel, not io: under StdWriteHere io is the
            // DirectIo, whose contended wait yields and never parks, so at
            // one carrier a native task parked in its write while holding
            // the lock never got the carrier back.
            // Uncontended it is one Cas either way; for every other caller
            // rel is io.
            StreamLock(lock, rel)
        } else if !lock.Cas(0, 1) {
            return true
        }
        n := b.Len()
        cap := (*st).cap
        used := (*st).len
        if !drain && n <= cap - used {
            // The common case: the bytes fit; one copy under the lock.
            raw.Copy(raw.Offset((*st).buf, used), raw.Data(b), n)
            (*st).len = used + n
            StreamUnlock(lock, rel)
            return true
        }
        q := IoMasked(io)
        var ok := true
        var off := 0
        if drain && (*st).len > 0 && n > cap - (*st).len {
            // A drain never splits b across two writes (a record
            // is one write): the buffered bytes go out first, then b
            // whole, from the caller's memory.
            pending := (*st).len
            (*st).len = 0
            ok = WriteHandle(q, (*st).h, raw.Slice((*st).buf, pending))
            off = n
            if ok {
                ok = WriteHandle(q, (*st).h, b)
            }
        } else if n > cap - (*st).len && (*st).len > 0 {
            k := cap - (*st).len
            raw.Copy(raw.Offset((*st).buf, (*st).len), raw.Data(b), k)
            off = k
            (*st).len = 0
            ok = WriteHandle(q, (*st).h, raw.Slice((*st).buf, cap))
        }
        rest := n - off
        if !ok {
        } else if rest > cap {
            ok = WriteHandle(q, (*st).h, b[off..])
        } else if rest > 0 {
            raw.Copy(raw.Offset((*st).buf, (*st).len), raw.Offset(raw.Data(b), off), rest)
            (*st).len += rest
        }
        if drain && (*st).len > 0 {
            pending := (*st).len
            (*st).len = 0
            ok = WriteHandle(q, (*st).h, raw.Slice((*st).buf, pending)) && ok
        }
        if !seal {
            StreamUnlock(lock, rel)
        }
        return ok
    }
}

/// Writes data to process stream i (0 stdout, 1 stderr) after the bytes
/// already buffered, and writes the buffer out: the body of the native
/// stdio entry. Std-internal. The stream's lock is held throughout, so
/// another task's write never splits data, and data has left the process
/// when this returns. It is not a cancellation point. False on a failed
/// write, or for an i other than 0 and 1. O(buffered bytes + data.Len()):
/// at most three writes; it allocates nothing.
export StdWrite(p: *Process, i: u32, io: Io, data: []u8): bool {
    if i > 1 {
        return false
    }
    return StreamPut(ProcStream(p, int(i)), io, io, data, true, true, false)
}

/// StdWrite for a caller that must not suspend: std.sim's stdio entry,
/// whose task would otherwise give up its turn while a pool thread writes,
/// making the simulated schedule depend on real time. Std-internal.
///
/// here takes the lock and writes on this thread (rt.DirectIo: waiting for
/// the lock yields the thread and never parks). rel releases the lock: the
/// native Io, so a native task parked on the lock is woken. Memory
/// ordering is as for every other write of the stream. O(buffered bytes +
/// data.Len()), plus a spin while another thread holds the stream; it
/// allocates nothing.
export StdWriteHere(p: *Process, i: u32, here: Io, rel: Io, data: []u8): bool {
    if i > 1 {
        return false
    }
    return StreamPut(ProcStream(p, int(i)), here, rel, data, true, true, false)
}

// StreamOf is the BufState of a stream writer's ctx (System.Stdout).
StreamOf(ctx: *u8): *BufState {
    unsafe "a stream writer's ctx is its BufState; nothing is dereferenced here" {
        return raw.PtrCast[u8, BufState](ctx)
    }
}

StreamWrite(w: mut Writer, b: []u8): WriteError!void {
    if !StreamPut(StreamOf(w.ctx), w.io, w.io, b, false, true, false) {
        fail WriteError
    }
}

StreamFlush(w: mut Writer): WriteError!void {
    if !StreamPut(StreamOf(w.ctx), w.io, w.io, "".AsBytes(), true, true, false) {
        fail WriteError
    }
}

// FlushStream writes a stream's buffered bytes out, and ignores a failure
// (StreamTake, before a read that may block, has nowhere to report it).
// Not cancellable (StreamPut): a cancelled stdin read fails at its own
// read, after the prompt it flushed has been written.
FlushStream(st: *BufState, io: Io) {
    _ = StreamPut(st, io, io, "".AsBytes(), true, true, false)
}

// SealStream is the last flush of a process that is ending (FlushStd: Main
// returned, or proc.Exit): it waits for the lock, writes the buffered bytes
// out, reports whether that worked (FlushStdCode reports a failure),
// and keeps the lock. A task on another carrier that writes later parks on the lock
// until the process ends, instead of starting a write the exit would cut:
// the stream ends with whole writes, never a torn last record (each
// Writer.Bytes or Str call on a process stream is one StreamPut, under the
// lock from its first byte to its last).
SealStream(st: *BufState, io: Io): bool {
    return StreamPut(st, io, io, "".AsBytes(), true, true, true)
}

// TrySealStream is SealStream for a panic report: the lock is only tried,
// and a stream whose lock is held (by the task that panicked, perhaps) is
// skipped, not waited for.
TrySealStream(st: *BufState, io: Io) {
    _ = StreamPut(st, io, io, "".AsBytes(), true, false, true)
}

// StreamRead serves stdin: buffered bytes first; else, when buf is at
// least as large as the 64 KiB process buffer, one read straight into buf
// (Go bufio.Reader's rule: no copy), or one read into the process buffer to
// serve from. A stdin reader's ctx is the Process, so standard output and
// standard error are flushed before a read that may block (stderr
// too, so a prompt written there shows before the program waits). start is
// the first unread byte and len the end of the read ones. 0 at end of
// input.
StreamRead(r: mut Reader, buf: mut []u8): ReadError!int {
    return try StreamTake(r, buf, false)
}

// StreamLine is StreamRead that stops after the first \n it copies: one
// lock and one table call per line for Reader.Line on stdin.
StreamLine(r: mut Reader, buf: mut []u8): ReadError!int {
    return try StreamTake(r, buf, true)
}

// StreamTake serves both; nl stops the copy after a \n. The stdin lock is
// held across the read, so readers on several tasks see whole, ordered
// pieces; it is taken before stdout's and stderr's (StreamLock's order).
StreamTake(r: mut Reader, buf: mut []u8, nl: bool): ReadError!int {
    want := buf.Len()
    if want == 0 {
        return 0
    }
    io := r.io
    unsafe "a stdin reader's ctx is the live Process; its buffer holds cap bytes, used only under the lock" {
        p := raw.PtrCast[u8, Process](r.ctx)
        st := ProcStream(p, 2)
        lock := &(*st).lock
        StreamLock(lock, io)
        if (*st).start == (*st).len {
            FlushStream(ProcStream(p, 0), io)
            FlushStream(ProcStream(p, 1), io)
            (*st).start = 0
            (*st).len = 0
            if !nl && want >= (*st).cap {
                res := IoRead(io, (*st).h, buf, -1)
                StreamUnlock(lock, io)
                if res.err != SysErr.None || res.value < 0 || res.value > i64(want) {
                    fail ReadError
                }
                return int(res.value)
            }
            res := IoRead(io, (*st).h, raw.SliceMut((*st).buf, (*st).cap), -1)
            if res.err != SysErr.None || res.value < 0 || res.value > i64((*st).cap) {
                StreamUnlock(lock, io)
                fail ReadError
            }
            (*st).len = int(res.value)
        }
        var n := (*st).len - (*st).start
        if n > want {
            n = want
        }
        if nl {
            // Up to and including the first newline of the n bytes: a SWAR
            // memchr (as musl's), eight bytes per step through an align-1
            // load (raw.ReadUnaligned) and the exact zero-byte test
            // (x - 0x01..01) & ~x & 0x80..80 on x = word ^ 0x0A..0A. Its
            // lowest set bit is the first newline (little-endian); a
            // false positive can only sit above a true one. The tail of
            // fewer than eight bytes goes one at a time. O(n).
            at := raw.Offset((*st).buf, (*st).start)
            var i := 0
            var hit := -1
            for hit < 0 && i + 8 <= n {
                x := raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(at, i))) ^ 0x0A0A_0A0A_0A0A_0A0A
                m := (x -% 0x0101_0101_0101_0101) & ~x & 0x8080_8080_8080_8080
                if m != 0 {
                    hit = i + raw.Ctz64(m) / 8
                }
                i += 8
            }
            for hit < 0 && i < n {
                if raw.Read(raw.Offset(at, i)) == 0x0A {
                    hit = i
                }
                i += 1
            }
            if hit >= 0 {
                n = hit + 1
            }
        }
        raw.Copy(raw.DataMut(buf), raw.Offset((*st).buf, (*st).start), n)
        (*st).start += n
        StreamUnlock(lock, io)
        return n
    }
}
