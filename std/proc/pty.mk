// Why: terminal programs (shells, editors) behave differently when their
// streams are a terminal, so running one under a parent needs a
// pseudo-terminal, which neither target offers in the same shape.
//
// What: proc.Pty (Open, Resize, Read, WriteAll, Close), over std.rt's
// PtyOpen, PtyResize, PtyRead and PtyClose. Command.terminal lends a Pty to
// a child.
//
// How: /dev/ptmx on Linux (its peer, found by TIOCGPTPEER at Spawn, becomes
// the child's terminal) and a pseudo-console on Windows (CreatePseudoConsole
// over two anonymous pipes, an attribute of process creation). The Pty is
// the controlling side: what its children write is read here, and what is
// written here is their input. SizeOk checks the size here, so both targets
// refuse the same sizes.

import std.core
import std.rt

/// The controlling side of a pseudo-terminal: what children on the
/// terminal write is read here, and what is written here is their input.
/// Give it to a child with Command.terminal. Linear: Close it. Send and
/// Share: two tasks' WriteAlls never interleave, since each holds the
/// Pty's write lock for all of its data. Owns OS handles and no memory.
///
/// On Windows, a Spawn on the Pty after every earlier child was waited for
/// opens a new pseudo-console and closes the old handles: no task may Read
/// or WriteAll the Pty while such a Spawn runs.
///
/// Provisional.
export linear struct Pty {
    p: core.IoPty
    // writes serialises WriteAll, as PipeWriter's.
    writes: Mutex[u8]
}

// SizeOk: cols and rows are each 1 to 32,767 (a Windows COORD's i16, and
// the bound on both targets), else Os(0). Checked here, before the
// runtime, so the targets agree (Linux's ioctl takes any u16).
SizeOk(cols: int, rows: int): Error!void {
    if cols < 1 || cols > 32_767 || rows < 1 || rows > 32_767 {
        fail Os(code: 0)
    }
}

/// A new pseudo-terminal of cols columns by rows rows.
///
/// Os(0) for a size outside 1 to 32,767. Unsupported before Windows 10
/// 1809 and under a Recorder or Replayer. OutOfMemory when the process
/// heap cannot give a handle record; the OS's error otherwise. Not a
/// cancellation point. O(1); allocates one or two handle records from the
/// process heap.
export Pty.Open(io: Io, cols: int, rows: int): Error!Pty {
    try SizeOk(cols, rows)
    var p := core.IoPty(read: 0, write: 0, term: 0)
    // Other with no OS code is the runtime's missing record.
    _ = try SpawnCheck(rt.PtyOpen(io, cols, rows, mut p))
    return Pty(p: p, writes: Mutex[u8].New(0))
}

/// Sets the terminal's size. A child on it sees SIGWINCH on Linux and a
/// resize event on Windows. Os(0) for a size outside 1 to 32,767. Not a
/// cancellation point; O(1); allocates nothing.
export Pty.Resize(&self, io: Io, cols: int, rows: int): Error!void {
    try SizeOk(cols, rows)
    _ = try Check(rt.PtyResize(io, &self.p, cols, rows))
}

/// Reads what the children on this terminal wrote into buf, waiting until
/// there is something, and returns the number of bytes read: at least 1,
/// or 0 at the end. One read, which may fill less than buf. O(n);
/// allocates nothing.
///
/// On Linux the end comes once every child has closed its side of the
/// terminal. On Windows it comes once every child started on this terminal
/// has been waited for and its last output read, so a child that is
/// detached or never waited for holds the end off until Close.
///
/// Canceled when the task's scope is cancelled; nothing is consumed, so
/// the same call again reads every byte.
///
/// On Windows the bytes are the pseudo-console's rendering of the screen,
/// not the child's own bytes, and no Read may run beside a Spawn that
/// opens a new pseudo-console (see Pty). A waiting Read holds a thread of
/// its own, outside the 64 that run blocking calls; waiting Reads hold up
/// the process's other blocking calls only past about 1,024 threads in
/// all.
export Pty.Read(&self, io: Io, buf: mut []u8): Error!int {
    if buf.Len() == 0 {
        return 0
    }
    n := try Check(rt.PtyRead(io, &self.p, buf))
    if n < 0 || n > i64(buf.Len()) {
        fail Os(code: 0)
    }
    return Trunc[int](n)
}

/// Writes every byte of data as the terminal's input. It holds the Pty's
/// write lock throughout, so another task's WriteAll comes wholly before
/// or after it.
///
/// Canceled when the task's scope is cancelled, either while waiting for
/// the lock, with nothing written, or after an unknown prefix of data was
/// written. Os(0) when a write makes no progress; the OS's error
/// otherwise. O(n) plus the wait for the lock; allocates nothing. A
/// waiting write holds a thread as a waiting Read does.
export Pty.WriteAll(&self, io: Io, data: []u8): Error!void {
    g := try self.writes.Lock(io)
    defer g.Unlock()
    var off := 0
    for off < data.Len() {
        if off > 0 {
            // A write cut by a cancel returns its partial count; the next
            // one must see the cancel before it blocks (as PipeWriter's,
            // core.SyncCancel).
            core.SyncCancel(io) else {
                fail Canceled
            }
        }
        n := try Check(core.IoWrite(io, self.p.write, data[off..], -1))
        if n <= 0 || n > i64(data.Len() - off) {
            fail Os(code: 0)
        }
        off += Trunc[int](n)
    }
}

/// Closes the terminal. On Linux the processes still on it see a hang-up;
/// on Windows the pseudo-console is closed, which ends them. Infallible
/// and not cancellable. O(1); allocates nothing and frees the handle
/// records.
///
/// On Windows the pseudo-console's close runs on a thread of its own, so
/// Close does not wait for a free thread among those that run blocking
/// calls; only when that thread cannot start does the close wait for one.
export Pty.Close(self) {
    _ = rt.PtyClose(core.IoMasked(rt.NativeIo()), self.p)
    Pty(p: _, writes: _) := self
}
