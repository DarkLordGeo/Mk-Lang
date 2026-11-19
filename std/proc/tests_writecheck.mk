// Why: WriteAll's check between partial writes must count as a cancel only
// when it answers Canceled, as std's one check (core.SyncCancel) does.
// What: PfIo, an Io whose write takes at most 2 bytes and whose check answers
// Other, and a test of PipeWriter.WriteAll and Pty.WriteAll over it.
// How: the native table with write and sleep replaced, so no handle is
// touched; run by mk test --std=std.proc.

import std.core
import std.raw
import std.task

struct PfWords {
    writes: raw.Atomic[i64]
}

var Pf: PfWords = PfWords(writes: raw.Atomic[i64].New(0))

// PfWrite writes at most 2 bytes of data, to no handle.
PfWrite(io: Io, h: uint, data: []u8, pos: i64): core.IoResult {
    _ = Pf.writes.Add(1)
    var n := data.Len()
    if n > 2 {
        n = 2
    }
    return core.IoResult(value: i64(n), err: core.SysErr.None, os: 0)
}

// PfSleep answers Other: the check fails with something other than
// Canceled.
PfSleep(io: Io, nanos: i64): core.IoResult {
    return core.IoResult(value: 0, err: core.SysErr.Other, os: 0)
}

const PfTable = core.IoTable(open: task.NativeTable.open, read: task.NativeTable.read, write: PfWrite,
    size: task.NativeTable.size, sync: task.NativeTable.sync, close: task.NativeTable.close,
    rename: task.NativeTable.rename, remove: task.NativeTable.remove, listen: task.NativeTable.listen,
    accept: task.NativeTable.accept, dial: task.NativeTable.dial, recv: task.NativeTable.recv,
    send: task.NativeTable.send, now: task.NativeTable.now, sleep: PfSleep,
    random: task.NativeTable.random, yield: task.NativeTable.yield, signal: task.NativeTable.signal,
    wait: task.NativeTable.wait, wake: task.NativeTable.wake, mkdir: task.NativeTable.mkdir,
    stat: task.NativeTable.stat, opendir: task.NativeTable.opendir, readdir: task.NativeTable.readdir,
    udp: task.NativeTable.udp, sendto: task.NativeTable.sendto, recvfrom: task.NativeTable.recvfrom,
    resolve: task.NativeTable.resolve, spawn: task.NativeTable.spawn, procwait: task.NativeTable.procwait,
    prockill: task.NativeTable.prockill, inherited: task.NativeTable.inherited,
    enter: core.InertHook, leave: core.InertHook, deadline: task.NativeTable.deadline,
    stdio: task.NativeTable.stdio, call: task.NativeTable.call)

// PfIo is an Io over PfTable; it holds no loan (the table is a const, the
// static pseudo-loan).
PfIo(): Io {
    return core.MakeIo(raw.Of(&PfTable), 0)
}

// PfPipe is 0 when PipeWriter.WriteAll of data over io writes it whole, 1
// when it is Canceled, 2 for another error.
PfPipe(io: Io, data: []u8): int {
    w := PipeWriter(h: 0, lend: false, writes: Mutex[u8].New(0))
    var r := 0
    w.WriteAll(io, data) else e {
        r = match e {
            Canceled { 1 }
            _ { 2 }
        }
    }
    PipeWriter(h: _, lend: _, writes: _) := w
    return r
}

// PfPty is PfPipe for Pty.WriteAll.
PfPty(io: Io, data: []u8): int {
    p := Pty(p: core.IoPty(read: 0, write: 0, term: 0), writes: Mutex[u8].New(0))
    var r := 0
    p.WriteAll(io, data) else e {
        r = match e {
            Canceled { 1 }
            _ { 2 }
        }
    }
    Pty(p: _, writes: _) := p
    return r
}

test "PipeWriter.WriteAll and Pty.WriteAll go on past a check between partial writes that fails with another error than Canceled" (sys: &System) {
    io := PfIo()
    Pf.writes.Store(0)
    Assert(PfPipe(io, "hello".AsBytes()) == 0)
    // 2, 2 and 1 bytes: three writes, two checks between them.
    Assert(Pf.writes.Load() == 3)
    Pf.writes.Store(0)
    Assert(PfPty(io, "hello".AsBytes()) == 0)
    Assert(Pf.writes.Load() == 3)
    Pf.writes.Store(0)
    Assert(PfPipe(io.NoCancel(), "hello".AsBytes()) == 0)
    Assert(PfPty(io.NoCancel(), "hello".AsBytes()) == 0)
    Assert(Pf.writes.Load() == 6)
}
