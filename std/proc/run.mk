// Why: most programs only want to run a child to completion and collect what
// it wrote; doing that by hand over Spawn deadlocks when a child fills one
// pipe while the parent waits on the other.
//
// What: proc.Output and proc.Run, over Spawn and the runtime's procwait and
// prockill entries on the child's handle; Pump, Feed, Stop and Reap are
// Run's tasks and exit paths.
//
// How: Run starts a child with its output on pipes, feeds it input, drains
// both output pipes at once (as Go's exec.Cmd and Rust's read2 do), then
// waits for its exit. One task per pipe, none of them ever interrupted to
// grow a buffer: each reader owns an arena of its own on the process heap (a
// core.HeapArena, never a sub-arena of a, whose chunk source may be
// confined to the caller's task) and fills segments in it in order, with no
// copy (std.core's ReadToEnd scheme); the body copies each stream once into
// a after the join. The two readers share one byte budget, so the reader
// that passes limit, or the task that meets an error, kills the child and
// cancels the others at once.
//
// Invariants (checked by the test blocks of tests_run.mk): every exit
// path after the spawn kills and waits for the child (Reap) or waits for
// its exit, so none is left running or a zombie; exactly one task's Stop
// wins the Cas, so the child is killed and the scope cancelled once; each
// output byte is copied once, after the join, which orders every task's
// writes before the body's reads.

import std.core
import std.raw
import std.rt

/// What a child wrote and how it ended. code is the exit code: 0 to 255 on
/// Linux, or minus the signal when a signal ended the child; on Windows,
/// the exit code as a u32. stdout and stderr hold everything the child
/// wrote to each stream.
export struct Output {
    /// The exit code: 0 to 255 on Linux, or minus the signal that ended the
    /// child; on Windows the exit code as a u32.
    export code: int
    /// Everything the child wrote to standard output.
    export stdout: []u8
    /// Everything the child wrote to standard error.
    export stderr: []u8
}

// FirstBuf is the size of each stream's first segment, in Run's frame.
const FirstBuf: int = 16384

// GrowMin is the smallest segment after the first.
const GrowMin: int = 65536

// MaxSegs bounds a stream's segments. They double (each at least the
// stream's bytes so far), so 48 of them hold more than 2^62 bytes: the
// heap fails long before the bound is met, which is OutOfMemory too.
const MaxSegs: int = 48

// MaxInt is the largest int.
const MaxInt: int = 9_223_372_036_854_775_807

// The failures a task of Run reports through Ctl.failed (0: none).
const FailTooLarge: u32 = 2
const FailMemory: u32 = 3
const FailOs: u32 = 4

// Ctl is what Run's tasks share: the child, the limit, the bytes both
// readers have read, and the first failure with its OS code. Only the
// atomics change while the tasks run.
struct Ctl {
    ph: uint
    limit: int
    total: raw.Atomic[int]
    failed: raw.Atomic[u32]
    os: raw.Atomic[i64]
}

// Stop records failure code (with the OS's code os) unless another task
// recorded one first; the first to record it kills the child and cancels
// the scope s, so every other task of Run stops at once. A cancel does not
// reach a task on a NoCancel Io; the kill does, since the child's death
// closes its ends of the pipes, unless a grandchild still holds one.
//
// Ordering: the Cas is the one edge between the tasks. Exactly one task
// wins it, so the child is killed and the scope cancelled once, and its
// os Store happens before the scope's join, which orders it before the
// body's Load of both words.
Stop(s: &Scope, io: Io, c: &Ctl, code: u32, os: i64) {
    if c.failed.Cas(0, code) {
        c.os.Store(os)
        _ = core.IoProcKill(io.NoCancel(), c.ph)
        s.Cancel()
    }
}

// In is the child's standard input as Feed sees it: the parent's end h (0
// once closed), the bytes written, and whether a cancel stopped it.
struct In {
    h: uint
    n: int
    canceled: bool
}

// Out is one output pipe as its reader sees it: the parent's end h, the
// bytes read, whether it reached its end or a cancel stopped it, and the
// segments that hold the bytes, in order (segs[0..nseg]).
struct Out {
    h: uint
    n: int
    done: bool
    canceled: bool
    segs: [48][]u8
    nseg: int
}

OutOf(h: uint): Out {
    return Out(h: h, n: 0, done: h == 0, canceled: false, segs: [b""; 48], nseg: 0)
}

// NextSeg is the size of a stream's next segment: the bytes it holds so far
// (so the capacity doubles), at least GrowMin, and at most one byte past
// what the shared budget has left (rem: limit minus every byte read), so
// the reader that passes the limit sees it with one byte more and the
// buffers never hold more than limit + 1 bytes for one stream.
NextSeg(n: int, rem: int): int {
    m := if n < GrowMin { GrowMin } else { n }
    return if rem < m { rem + 1 } else { m }
}

// Pump reads the stream st to its end into first, then into segments of
// x, each filled before the next is made: no byte is copied. Each read is
// bounded by the shared budget, and the read that takes the total past
// c.limit stops the run with TooLarge. A read of 0 or Closed is the end;
// Canceled stops the reader (the caller's cancel, or another task's
// Stop); any other failure, or a count outside the buffer (the runtime's
// contract, 2.3.2: Os(0), as std.net's CheckCount), stops the run with Os.
// Every exit keeps the segment in use, so st.segs holds st.n bytes.
Pump(s: &Scope, io: Io, x: mut core.HeapArena into st, first: mut []u8 into st, st: mut Out, c: &Ctl) {
    var cur: mut []u8 = first
    var used := 0
    for !st.done {
        // No edge is needed: a stale total only lets this read ask for
        // more than rem, and the Add below sees every earlier Add. rem < 0:
        // the other reader passed the limit and stops the run.
        rem := c.limit - c.total.Load()
        if rem < 0 {
            break
        }
        if used == cur.Len() {
            if st.nseg == MaxSegs - 1 {
                Stop(s, io, c, FailMemory, 0)
                break
            }
            st.segs[st.nseg] = cur[0..used]
            st.nseg += 1
            nb := x.Arena().Slice[u8](NextSeg(st.n, rem)) else {
                // cur is stored already: the last store below adds an
                // empty segment, and a failed run reads none of them.
                Stop(s, io, c, FailMemory, 0)
                used = 0
                break
            }
            cur = nb
            used = 0
        }
        free := cur.Len() - used
        w := if rem < free { rem + 1 } else { free }
        r := core.IoRead(io, st.h, mut cur[used..used + w], -1)
        if r.err == core.SysErr.Canceled {
            st.canceled = true
            break
        }
        if r.err == core.SysErr.Closed || r.err == core.SysErr.None && r.value == 0 {
            st.done = true
            break
        }
        if r.err != core.SysErr.None || r.value < 0 || r.value > i64(w) {
            os: i64 = if r.err != core.SysErr.None { r.os } else { 0 }
            Stop(s, io, c, FailOs, os)
            break
        }
        k := Trunc[int](r.value)
        used += k
        st.n += k
        // Edge: the Adds of the two readers are totally ordered (SeqCst),
        // so the read that first takes the total past the limit sees an
        // old value of more than limit - k, whichever reader made it.
        old := c.total.Add(k)
        if old > c.limit - k {
            Stop(s, io, c, FailTooLarge, 0)
            break
        }
    }
    st.segs[st.nseg] = cur[0..used]
    st.nseg += 1
}

// Feed writes data[st.n..] to the child's stdin, then closes it so the
// child sees the end of its input. A child that exits or closes stdin
// first ends the feed with Closed, which is not an error (Go's
// skipStdinCopyError); a cancel leaves st.n where it got to. Any other
// failure, or a count of 0 or past what was asked, stops the run with Os.
Feed(s: &Scope, io: Io, data: []u8, st: mut In, c: &Ctl) {
    for st.n < data.Len() {
        r := core.IoWrite(io, st.h, data[st.n..], -1)
        if r.err == core.SysErr.Canceled {
            st.canceled = true
            return
        }
        if r.err == core.SysErr.Closed {
            break
        }
        if r.err != core.SysErr.None || r.value <= 0 || r.value > i64(data.Len() - st.n) {
            os: i64 = if r.err != core.SysErr.None { r.os } else { 0 }
            Stop(s, io, c, FailOs, os)
            return
        }
        st.n += Trunc[int](r.value)
    }
    _ = core.IoClose(io, st.h)
    st.h = 0
}

// WriterHandle and ReaderHandle take the handle out of a pipe end Spawn
// returned (0 for none); the caller closes it.
WriterHandle(w: ?PipeWriter): uint {
    if p := w {
        PipeWriter(h, lend: _, writes: _) := p
        return h
    }
    return 0
}

ReaderHandle(r: ?PipeReader): uint {
    if p := r {
        PipeReader(h, lend: _) := p
        return h
    }
    return 0
}

// Close closes h if it is still open, and clears it.
Close(io: Io, h: mut uint) {
    if *h != 0 {
        _ = core.IoClose(io, *h)
        *h = 0
    }
}

// Reap ends the child ph and waits for it on a NoCancel Io, so it is
// never left running or a zombie, then closes its handle. Killing a
// child that already ended is success (the runtime's prockill).
Reap(io: Io, ph: uint) {
    q := io.NoCancel()
    _ = core.IoProcKill(q, ph)
    _ = core.IoProcWait(q, ph)
    _ = core.IoClose(q, ph)
}

// Joined copies the segments of st, in order, into one slice of a of
// exactly st.n bytes: each byte's one copy.
Joined(a: &Arena, st: &Out): OutOfMemory![]u8 from a {
    var r := try a.Slice[u8](st.n)
    var off := 0
    for i in 0..st.nseg {
        g := st.segs[i]
        (mut r[off..off + g.Len()]).CopyFrom(g)
        off += g.Len()
    }
    return r[..]
}

/// Runs a program to the end: args[0] with the arguments args[1..], with
/// input written to its standard input, which is then closed. The child
/// gets this process's environment and working directory. Once the child
/// has exited, returns
/// its exit code and everything it wrote to standard output and standard
/// error. args[0] is a path, or a name searched in PATH. An empty input
/// gives the child the null device as standard input.
///
/// Errors: NotFound for an empty args or args[0]; TooLarge when the output
/// passes limit bytes, both streams together, which kills the child at
/// once; otherwise Spawn's errors and the OS's. A negative limit panics
/// (code 8). On Canceled, TooLarge or any other failure after the start,
/// the child is killed and waited for before Run returns. Under
/// io.NoCancel(), a failure still ends Run once the dead child's
/// pipes close; only a grandchild that holds one open can delay it.
///
/// O(output) time; allocates the output in a, copying each byte once.
/// While the child runs, each stream also holds up to limit + 1 bytes in
/// arenas on the process heap, not from a, freed before Run returns. On
/// Windows a name searched in PATH takes Spawn's search memory as well
/// (see Spawn), freed once the child has started.
export Run(a: &Arena, io: Io, args: []str, input: []u8, limit: int): Error!Output from a {
    if limit < 0 {
        raw.Fail(8, "proc.Run: limit is negative")
    }
    if args.Len() == 0 || args[0].Len() == 0 {
        fail NotFound
    }
    var cmd := DefaultCommand
    cmd.args = args
    cmd.stdin = if input.Len() == 0 { Stdio.Null } else { Stdio.Pipe }
    cmd.stdout = Stdio.Pipe
    cmd.stderr = Stdio.Pipe
    (child, stdin, stdout, stderr) := try Spawn(a, io, &cmd)
    // From here Run holds the handles itself: every path below closes
    // each end and reaps or waits for the child.
    Child(h: ph, ospid: _, job: _, group: _) := child
    var si := In(h: WriterHandle(stdin), n: 0, canceled: false)
    var so := OutOf(ReaderHandle(stdout))
    var se := OutOf(ReaderHandle(stderr))
    // The first segment: one byte past the limit is enough to see it.
    first := if limit < FirstBuf { limit + 1 } else { FirstBuf }
    var ob0: [16384]u8 = [0; 16384]
    var eb0: [16384]u8 = [0; 16384]
    var ox := core.HeapArena.New(rt.ProcessHeap()) else e {
        Close(io, mut si.h)
        Close(io, mut so.h)
        Close(io, mut se.h)
        Reap(io, ph)
        fail e
    }
    defer ox.Free()
    var ex := core.HeapArena.New(rt.ProcessHeap()) else e {
        Close(io, mut si.h)
        Close(io, mut so.h)
        Close(io, mut se.h)
        Reap(io, ph)
        fail e
    }
    defer ex.Free()
    ctl := Ctl(ph: ph, limit: limit, total: raw.Atomic[int].New(0), failed: raw.Atomic[u32].New(0), os: raw.Atomic[i64].New(0))
    scope s := io.Scope() {
        if si.h != 0 {
            s.go Feed(&s, io, input, mut si, &ctl)
        }
        if !so.done {
            s.go Pump(&s, io, mut ox, mut ob0[0..first], mut so, &ctl)
        }
        if !se.done {
            s.go Pump(&s, io, mut ex, mut eb0[0..first], mut se, &ctl)
        }
    }
    Close(io, mut si.h)
    Close(io, mut so.h)
    Close(io, mut se.h)
    // The join orders every task's Stop before these loads.
    f := ctl.failed.Load()
    if f != 0 {
        Reap(io, ph)
        if f == FailTooLarge {
            fail TooLarge
        }
        if f == FailMemory {
            fail OutOfMemory
        }
        fail Os(code: ctl.os.Load())
    }
    if si.canceled || so.canceled || se.canceled {
        Reap(io, ph)
        fail Canceled
    }
    code := Check(core.IoProcWait(io, ph)) else e {
        Reap(io, ph)
        fail e
    }
    _ = core.IoClose(io, ph)
    out := try Joined(a, &so)
    err := try Joined(a, &se)
    return Output(code: Trunc[int](code), stdout: out, stderr: err)
}
