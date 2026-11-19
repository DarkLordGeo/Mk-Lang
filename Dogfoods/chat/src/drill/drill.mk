// Why: Cancellation must end every kind of wait the server uses; the drill
// checks each one on its own.
//
// What: Outcome, Result, and one small task per wait: semaphore, mutex, read
// and write lock, notify, barrier, channel send and receive, select, sleep,
// accept and read.
//
// How: Each task blocks on its wait and records in `out` how the wait ended, so
// run.mk can cancel the scope and read the result.

import std.net
import std.sync
import std.time

/// How a wait ended in a drill.
export enum Outcome {
    /// The wait returned `Canceled`, or its task returned because of it.
    Canceled
    /// The wait finished without seeing the cancel.
    Done
    /// The drill did not get as far as the wait.
    Skipped
}

/// One drill: the wait it cancels and how that wait ended.
export struct Result {
    export point: str
    export outcome: Outcome
}

AcquireOne(io: Io, s: &sync.Semaphore, out: mut Outcome) {
    s.Acquire(io, 1) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

LockMutex(io: Io, m: &Mutex[int], out: mut Outcome) {
    g := m.Lock(io) else {
        *out = Outcome.Canceled
        return
    }
    g.Unlock()
    *out = Outcome.Done
}

LockWrite(io: Io, rw: &sync.RwLock[int], out: mut Outcome) {
    g := rw.Lock(io) else {
        *out = Outcome.Canceled
        return
    }
    g.Unlock()
    *out = Outcome.Done
}

LockRead(io: Io, rw: &sync.RwLock[int], out: mut Outcome) {
    g := rw.LockRead(io) else {
        *out = Outcome.Canceled
        return
    }
    g.Unlock()
    *out = Outcome.Done
}

WaitNotify(io: Io, n: &sync.Notify, out: mut Outcome) {
    n.Wait(io) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

WaitBarrier(io: Io, b: &sync.Barrier, out: mut Outcome) {
    _ = b.Wait(io) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

RecvOne(io: Io, c: &Chan[int], out: mut Outcome) {
    _ = c.Recv(io) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

SendOne(io: Io, c: &Chan[int], out: mut Outcome) {
    c.Send(io, 7) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

SelectTwo(io: Io, a: &Chan[int], b: &Chan[int], out: mut Outcome) {
    _ = sync.Select(io, (a: a, b: b)) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

Sleep(io: Io, out: mut Outcome) {
    io.Sleep(time.Millis(60_000)) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}

AcceptOne(io: Io, l: &net.Listener, out: mut Outcome) {
    c := l.Accept(io) else {
        *out = Outcome.Canceled
        return
    }
    c.Close()
    *out = Outcome.Done
}

ReadOne(io: Io, c: net.Stream, out: mut Outcome) {
    defer c.Close()
    var buf: [16]u8 = [0; 16]
    _ = c.Read(io, mut buf[..]) else {
        *out = Outcome.Canceled
        return
    }
    *out = Outcome.Done
}
