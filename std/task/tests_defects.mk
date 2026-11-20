// Why: each block pins a defect found in the runtime's file, socket and
// process entries, so that it cannot come back.
// What: the SD8-* blocks and the sys.UnixUnbind block, listed below.
// How: run by the scheduler harness (TestStdDefects_ListenBlocks, and its
// Linux leaf) and by mk test; each block builds its case on loopback, a
// pipe, a child process or a temporary file.

// The blocks: rt.ListenWith
// (std/rt/listen.mk), with the backlog asked for, the
// reuse choice against a held port and one in TIME_WAIT, the address
// checks and a cancelled scope; and a pool write cut by a deadline
// (JobWrite's WriteOnce); and rt.CopyFile's
// refusal of a source that is not a regular file before dst is opened;
// and a file std.task opened lent to a child as
// stdout and stdin (on Windows the file is
// overlapped, and the child gets a synchronous handle on it); and a
// detached child reaped by the reaper thread with no later spawn,
// alone or leading a group; and a child's piped streams, pipes
// it can open again by name, carrying more than a pipe's buffer;
// and 80 waiters of one file lock, more than the
// pool's threads, cut by a deadline and then served in turn (on
// Windows each waits on the port); and a seek of a socket on the Linux
// poller; and a directory rt.DirOpenAt opened,
// its first fill made in the open's job on Windows,
// listed whole; and sys.UnixUnbind, which a ListenUnix that
// fails after its bind calls. std.sim's answer to
// ListenWith is SD8-Listen-Sim in std/sim/tests_call.mk.

import std.core
import std.raw
import std.rt
import std.sys

test "SD8-Listen-Backlog a backlog of 4 holds 4 dialled connections before any accept, and each is accepted" (sys: &System) {
    l := rt.ListenWith(sys.io, "127.0.0.1:0", 4, true)
    Assert(l.err == core.SysErr.None && l.value != 0 && l.os > 0 && l.os < 65536)
    lh := Trunc[uint](l.value)
    var ab: [32]u8 = [0; 32]
    addr := NAddr(mut ab, l.os)
    var cs: [4]uint = [0; 4]
    for i in 0..4 {
        d := NativeDial(sys.io, addr)
        Assert(d.err == core.SysErr.None)
        cs[i] = Trunc[uint](d.value)
    }
    for _ in 0..4 {
        a := NativeAccept(sys.io, lh)
        Assert(a.err == core.SysErr.None)
        Assert(rt.CloseRuntimeHandle(Trunc[uint](a.value)).err == core.SysErr.None)
    }
    for i in 0..4 {
        Assert(rt.CloseRuntimeHandle(cs[i]).err == core.SysErr.None)
    }
    Assert(rt.CloseRuntimeHandle(lh).err == core.SysErr.None)
}

// SdTimeWait listens on a port the OS picks, with or without reuse, makes
// one connection whose accepted end closes first, and closes it all, so
// the port keeps one TIME_WAIT entry from a socket made with that choice;
// it returns the port.
SdTimeWait(io: Io, reuse: bool): i64 {
    l := rt.ListenWith(io, "127.0.0.1:0", 0, reuse)
    Assert(l.err == core.SysErr.None)
    lh := Trunc[uint](l.value)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(io, NAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    c := Trunc[uint](d.value)
    a := NativeAccept(io, lh)
    Assert(a.err == core.SysErr.None)
    Assert(rt.CloseRuntimeHandle(Trunc[uint](a.value)).err == core.SysErr.None)
    var buf: [8]u8 = [0; 8]
    e := NativeRecv(io, c, mut buf[..])
    Assert(e.err == core.SysErr.None && e.value == 0)
    Assert(rt.CloseRuntimeHandle(c).err == core.SysErr.None)
    Assert(rt.CloseRuntimeHandle(lh).err == core.SysErr.None)
    return l.os
}

test "SD8-Listen-Reuse a held port is AddrInUse either way; on Linux a TIME_WAIT port rebinds only when both sockets chose reuse, on macOS when the new one does; Windows rebinds it either way" (sys: &System) {
    l := rt.ListenWith(sys.io, "127.0.0.1:0", 0, false)
    Assert(l.err == core.SysErr.None)
    var ab: [32]u8 = [0; 32]
    addr := NAddr(mut ab, l.os)
    // A second listener on a held port, either choice: AddrInUse.
    Assert(rt.ListenWith(sys.io, addr, 0, false).err == core.SysErr.AddrInUse)
    Assert(rt.ListenWith(sys.io, addr, 0, true).err == core.SysErr.AddrInUse)
    Assert(rt.CloseRuntimeHandle(Trunc[uint](l.value)).err == core.SysErr.None)
    // TIME_WAIT left by a socket made without reuse.
    var ab2: [32]u8 = [0; 32]
    at := NAddr(mut ab2, SdTimeWait(sys.io, false))
    again := rt.ListenWith(sys.io, at, 0, false)
    if Target.os == Os.Linux {
        // Linux lets a bind share a TIME_WAIT port only when both sockets
        // set SO_REUSEADDR, so neither choice takes this one.
        Assert(again.err == core.SysErr.AddrInUse)
        Assert(rt.ListenWith(sys.io, at, 0, true).err == core.SysErr.AddrInUse)
        // TIME_WAIT left by a socket made with reuse: reuse rebinds it,
        // and no reuse is still AddrInUse.
        var ab3: [32]u8 = [0; 32]
        port := SdTimeWait(sys.io, true)
        at2 := NAddr(mut ab3, port)
        Assert(rt.ListenWith(sys.io, at2, 0, false).err == core.SysErr.AddrInUse)
        r := rt.ListenWith(sys.io, at2, 0, true)
        Assert(r.err == core.SysErr.None && r.os == port)
        Assert(rt.CloseRuntimeHandle(Trunc[uint](r.value)).err == core.SysErr.None)
    } else if Target.os == Os.Macos {
        // Darwin binds a TIME_WAIT port when the new socket sets
        // SO_REUSEADDR, whatever the old one chose; without it the bind
        // finds the TIME_WAIT entry and is AddrInUse.
        Assert(again.err == core.SysErr.AddrInUse)
        r := rt.ListenWith(sys.io, at, 0, true)
        Assert(r.err == core.SysErr.None)
        Assert(rt.CloseRuntimeHandle(Trunc[uint](r.value)).err == core.SysErr.None)
    } else {
        // Windows binds over the TIME_WAIT entry even with
        // SO_EXCLUSIVEADDRUSE (std/rt/listen.mk's doc): measured here.
        Assert(again.err == core.SysErr.None)
        Assert(rt.CloseRuntimeHandle(Trunc[uint](again.value)).err == core.SysErr.None)
    }
}

test "SD8-Listen-Errors an address the parser refuses is InvalidAddress, and a cancelled scope's ListenWith is Canceled with nothing held" (sys: &System) {
    Assert(rt.ListenWith(sys.io, "example.com:80", 0, true).err == core.SysErr.InvalidAddress)
    Assert(rt.ListenWith(sys.io, "127.0.0.1", 0, false).err == core.SysErr.InvalidAddress)
    Assert(rt.ListenWith(sys.io, "127.0.0.1:65536", 8, true).err == core.SysErr.InvalidAddress)
    var c := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    scope s := sys.io.Scope() {
        s.Cancel()
        c = rt.ListenWith(sys.io, "127.0.0.1:0", 4, false)
    }
    Assert(c.err == core.SysErr.Canceled && c.value == 0)
    // IPv6 loopback, where the host has it: the same call path.
    six := rt.ListenWith(sys.io, "[::1]:0", 2, false)
    if six.err == core.SysErr.None {
        Assert(six.os > 0)
        Assert(rt.CloseRuntimeHandle(Trunc[uint](six.value)).err == core.SysErr.None)
    }
}

// SdFlood writes to the pipe end h until a write fails; out 1 when that
// failure was Canceled.
SdFlood(io: Io, h: uint, out: &raw.Atomic[u32]) {
    var buf: [8192]u8 = [0; 8192]
    for {
        w := NativeWrite(io, h, buf[..], -1)
        if w.err != core.SysErr.None {
            if w.err == core.SysErr.Canceled {
                out.Store(1)
            }
            return
        }
    }
}

test "SD8-PoolWrite-Deadline a pool write to a pipe nobody reads ends at the scope's deadline with Canceled, from a task and from the body" (sys: &System) {
    io := sys.io
    var e: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut e).err == core.SysErr.None)
    o0 := raw.Atomic[u32].New(0)
    o1 := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    var exp := false
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(40 * DlMs))
        s.go SdFlood(io, e[1], &o0)
        SdFlood(io, e[1], &o1)
        exp = s.Expired()
    }
    Assert(BenchNow() - t0 < DlBound)
    Assert(exp && o0.Load() == 1 && o1.Load() == 1)
    Assert(core.CancelQuiet())
    PClose(io, e[0])
    PClose(io, e[1])
}

test "SD8-Copy-NotRegular rt.CopyFile of a directory source fails before dst is opened, IsDirectory on Linux" (sys: &System) {
    io := sys.io
    dst := "sd8-copy-not-regular-dst"
    r := rt.CopyFile(io, ".".AsBytes(), dst.AsBytes())
    Assert(r.err != core.SysErr.None)
    if !PWin() {
        Assert(r.err == core.SysErr.IsDirectory)
    }
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    Assert(NativeStat(io, dst, mut st).err == core.SysErr.NotFound)
}

test "SD8-Spawn-LendFile a file std.task opened is lent as a child's stdout and as its stdin, overlapped on Windows or not" (sys: &System) {
    io := sys.io
    path := "sd8-spawn-lend-file.txt"
    var noenv: [1]str = [""]
    // A fresh file lent as stdout: the child's output lands in it.
    o := NativeOpen(io, path, 1)
    Assert(o.err == core.SysErr.None)
    fh := Trunc[uint](o.value)
    var s := CSpec(core.IoStdNull, core.IoStdHandle, core.IoStdInherit)
    s.src[1] = fh
    r := CSh(io, "echo lent-file", "echo lent-file", noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    Assert(rt.ChildWait(io, h, s.job).value == 0)
    PClose(io, h)
    var b: [64]u8 = [0; 64]
    n := NativeRead(io, fh, mut b[..], 0)
    Assert(n.err == core.SysErr.None && CHas(b[0..Trunc[int](n.value)], "lent-file"))
    PClose(io, fh)
    // The same file opened for reading, lent as stdin: the child reads it
    // from its start and echoes the line it finds.
    i := NativeOpen(io, path, 0)
    Assert(i.err == core.SysErr.None)
    ih := Trunc[uint](i.value)
    var t := CSpec(core.IoStdHandle, core.IoStdPipe, core.IoStdJoin)
    t.src[0] = ih
    // findstr by its full path: the harness's PATH may not name System32.
    c := CSh(io, "%SystemRoot%\\System32\\findstr.exe lent", "grep lent", noenv[0..0], "", mut t)
    Assert(c.err == core.SysErr.None)
    PClose(io, ih)
    var out: [64]u8 = [0; 64]
    m := PRead(io, t.pipes[1], mut out[..])
    Assert(CHas(out[0..m], "lent-file"))
    ch := Trunc[uint](c.value)
    Assert(rt.ChildWait(io, ch, t.job).value == 0)
    PClose(io, t.pipes[1])
    PClose(io, ch)
    Assert(NativeRemove(io, path).err == core.SysErr.None)
}

// SdProcPath writes "/proc/<pid>/stat" into buf and returns it.
SdProcPath(buf: mut [40]u8, pid: i64): str {
    var digits: [20]u8 = [0; 20]
    var n := 0
    var v := pid
    for {
        digits[n] = Trunc[u8](48 + v % 10)
        n += 1
        v = v / 10
        if v == 0 {
            break
        }
    }
    head := "/proc/".AsBytes()
    tail := "/stat".AsBytes()
    var k := 0
    for b in head {
        buf[k] = b
        k += 1
    }
    for i in 0..n {
        buf[k] = digits[n - 1 - i]
        k += 1
    }
    for b in tail {
        buf[k] = b
        k += 1
    }
    return str.From(buf[0..k]) else { "" }
}

test "SD8-Detach-Reaped a detached child that ends is reaped with no later spawn or detach, alone or leading a group" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    var pids: [2]i64 = [0; 2]
    for group in 0..2 {
        var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        s.group = group == 1
        r := CSh(io, "exit 0", "exit 0", noenv[0..0], "", mut s)
        Assert(r.err == core.SysErr.None && s.pid > 0)
        pids[group] = s.pid
        Assert(rt.ChildDetach(io, Trunc[uint](r.value), s.job).err == core.SysErr.None)
    }
    // No spawn and no detach from here on: only the reaper can reap them.
    t0 := BenchNow()
    for rt.DetachedLen() != 0 && BenchNow() - t0 < DlBound {
        io.Sleep(core.DurationOf(10 * DlMs)) else {}
    }
    Assert(rt.DetachedLen() == 0)
    if !PWin() {
        // A zombie keeps its /proc entry; a reaped child has none.
        for pid in pids {
            var b: [40]u8 = [0; 40]
            var st := core.IoStat(kind: 0, size: 0, modified: 0)
            Assert(NativeStat(io, SdProcPath(mut b, pid), mut st).err == core.SysErr.NotFound)
        }
    }
}

test "SD8-SockOptGet rt.SockOptionGet reads back what rt.SockOption set: no-delay on and off, keep-alive with its idle seconds, linger" (sys: &System) {
    io := sys.io
    l := NListen(io)
    lh := Trunc[uint](l.value)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(io, NAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    c := Trunc[uint](d.value)
    a := NativeAccept(io, lh)
    Assert(a.err == core.SysErr.None)
    p := Trunc[uint](a.value)
    for on in 0..2 {
        Assert(rt.SockOption(io, c, core.IoSockNoDelay, i64(on), 0).err == core.SysErr.None)
        g := rt.SockOptionGet(io, c, core.IoSockNoDelay)
        Assert(g.err == core.SysErr.None && g.value == i64(on))
    }
    Assert(rt.SockOption(io, p, core.IoSockKeepAlive, 1, 30).err == core.SysErr.None)
    k := rt.SockOptionGet(io, p, core.IoSockKeepAlive)
    Assert(k.err == core.SysErr.None && k.value == 1 && (k.os == 30 || (PWin() && k.os == 0)))
    Assert(rt.SockOption(io, p, core.IoSockKeepAlive, 0, 0).err == core.SysErr.None)
    Assert(rt.SockOptionGet(io, p, core.IoSockKeepAlive).value == 0)
    Assert(rt.SockOption(io, c, core.IoSockLinger, 1, 0).err == core.SysErr.None)
    g := rt.SockOptionGet(io, c, core.IoSockLinger)
    Assert(g.err == core.SysErr.None && g.value == 1 && g.os == 0)
    Assert(rt.SockOptionGet(io, c, 99).err == core.SysErr.Other)
    NAbort(io, c)
    PClose(io, p)
    PClose(io, lh)
}

// SdWriteAll writes all of data to h, one NativeWrite after another (a
// pipe's write may take part of it).
SdWriteAll(io: Io, h: uint, data: []u8) {
    var at := 0
    for at < data.Len() {
        w := NativeWrite(io, h, data[at..], -1)
        Assert(w.err == core.SysErr.None && w.value > 0)
        at += Trunc[int](w.value)
    }
}

test "SD8-Spawn-PipeReopen a child's piped streams are pipes it can open again by name, and the parent's ends carry more than a pipe's buffer" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    // Linux: the child opens its stdin, stdout and stderr again by name,
    // which a socket's /proc link refuses with ENXIO. macOS has no /proc:
    // its /dev/fd/0 is the same name there (an open of it duplicates the
    // descriptor).
    var s := CSpec(core.IoStdPipe, core.IoStdPipe, core.IoStdPipe)
    fd0 := if Target.os == core.Os.Macos { "/dev/fd/0" } else { "/proc/self/fd/0" }
    lin := sys.arena.Fmt("cat /dev/stdin > /dev/stdout && cat {fd0} && echo reopened-err > /dev/stderr", (fd0)).Must()
    r := CSh(io, "%SystemRoot%\\System32\\findstr.exe reopen", lin, noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    SdWriteAll(io, s.pipes[0], "reopen-in\n".AsBytes())
    PClose(io, s.pipes[0])
    var out: [64]u8 = [0; 64]
    n := PRead(io, s.pipes[1], mut out[..])
    Assert(CHas(out[0..n], "reopen-in"))
    var err: [64]u8 = [0; 64]
    m := PRead(io, s.pipes[2], mut err[..])
    Assert(PWin() || CHas(err[0..m], "reopened-err"))
    h := Trunc[uint](r.value)
    Assert(rt.ChildWait(io, h, s.job).value == 0)
    PClose(io, s.pipes[1])
    PClose(io, s.pipes[2])
    PClose(io, h)
    // 300 KiB through stdin, past a pipe's 64 KiB buffer: the parent's
    // writes wait for room on the poller, and the child reads every byte.
    // Linux's wc counts them; Windows' findstr reads them all and, finding
    // no y, prints nothing and exits 1.
    var t := CSpec(core.IoStdPipe, core.IoStdPipe, core.IoStdInherit)
    c := CSh(io, "%SystemRoot%\\System32\\findstr.exe y", "wc -c", noenv[0..0], "", mut t)
    Assert(c.err == core.SysErr.None)
    var line: [1024]u8 = [120; 1024]
    line[1023] = 10
    for _ in 0..300 {
        SdWriteAll(io, t.pipes[0], line[..])
    }
    PClose(io, t.pipes[0])
    var count: [64]u8 = [0; 64]
    k := PRead(io, t.pipes[1], mut count[..])
    Assert(if PWin() { k == 0 } else { CHas(count[0..k], "307200") })
    ch := Trunc[uint](c.value)
    want: i64 = if PWin() { 1 } else { 0 }
    Assert(rt.ChildWait(io, ch, t.job).value == want)
    PClose(io, t.pipes[1])
    PClose(io, ch)
}

// SdPtyRun starts cmd (win's on Windows, lin's elsewhere) on the Pty p,
// waits for it, and only then reads p into buf for at most 10 s: the
// bytes read and whether the read reached the end. On macOS it reads
// first and waits after the end: there the last close of a pty's peer
// waits at most 60 ticks for the master to read and then discards the
// output left (XNU's ptsclose and ttyclose), so a read after the wait
// would find only the end.
SdPtyRun(io: Io, p: &core.IoPty, win: str, lin: str, buf: mut []u8): (n: int, end: bool) {
    var noenv: [1]str = [""]
    var s := CSpec(core.IoStdInherit, core.IoStdInherit, core.IoStdInherit)
    s.pty = p.term
    r := CSh(io, win, lin, noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None && s.pty == p.term)
    h := Trunc[uint](r.value)
    mac := Target.os == core.Os.Macos
    if !mac {
        Assert(rt.ChildWait(io, h, 0).value == 0)
        PClose(io, h)
    }
    var n := 0
    var end := false
    scope d := io.Scope() {
        d.CancelAfter(core.DurationOf(10_000 * DlMs))
        for n < buf.Len() {
            g := rt.PtyRead(io, p, mut buf[n..])
            if g.err != core.SysErr.None {
                break
            }
            if g.value == 0 {
                end = true
                break
            }
            n += Trunc[int](g.value)
        }
    }
    if mac {
        Assert(rt.ChildWait(io, h, 0).value == 0)
        PClose(io, h)
    }
    return (n: n, end: end)
}

test "SD8-Pty-EndAfterWait a pseudo-terminal read after its only child was waited for gives the child's output, then the end, on both targets, and the Pty takes a second child that ends the same way, at the size a resize set" (sys: &System) {
    io := sys.io
    var p := core.IoPty(read: 0, write: 0, term: 0)
    Assert(rt.PtyOpen(io, 80, 24, mut p).err == core.SysErr.None)
    var b: [8192]u8 = [0; 8192]
    (n, end) := SdPtyRun(io, &p, "echo pty-end", "echo pty-end", mut b[..])
    Assert(end && CHas(b[0..n], "pty-end"))
    Assert(rt.PtyResize(io, &p, 100, 30).err == core.SysErr.None)
    Assert(rt.PtyResize(io, &p, 0, 30).err == core.SysErr.Other)
    // Windows opens a new pseudo-console behind the Pty for this child.
    second := SdPtyRun(io, &p, "echo pty-again", "stty size; echo pty-again", mut b[..])
    Assert(second.end && CHas(b[0..second.n], "pty-again"))
    if !PWin() {
        Assert(CHas(b[0..second.n], "30 100"))
    }
    Assert(rt.PtyClose(io, p).err == core.SysErr.None)
}

// SdCutFlood writes 5,000-byte pieces to h until a write fails (65,536,
// the pipe's size, is no multiple of it, so the last write finds part of
// the room it needs), counting into sent every byte a write reports, a
// cancelled one's included.
SdCutFlood(io: Io, h: uint, sent: mut i64) {
    var piece: [5000]u8 = [120; 5000]
    for {
        w := NativeWrite(io, h, piece[..], -1)
        *sent += w.value
        if w.err != core.SysErr.None {
            return
        }
    }
}

test "SD8-Spawn-PipeCut a write to a child's stdin pipe cut by a deadline leaves in the pipe exactly the bytes the writes reported (under io_uring)" (sys: &System) {
    if PWin() {
        return
    }
    io := sys.io
    var noenv: [1]str = [""]
    var s := CSpec(core.IoStdPipe, core.IoStdPipe, core.IoStdInherit)
    // The child reads nothing until the parent's writes have been cut.
    r := CSh(io, "", "sleep 1; wc -c", noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    var sent: i64 = 0
    scope d := io.Scope() {
        d.CancelAfter(core.DurationOf(50 * DlMs))
        SdCutFlood(io, s.pipes[0], mut sent)
    }
    PClose(io, s.pipes[0])
    var out: [64]u8 = [0; 64]
    n := PRead(io, s.pipes[1], mut out[..])
    var counted: i64 = 0
    for b in out[0..n] {
        if b >= 48 && b <= 57 {
            counted = counted * 10 + i64(b - 48)
        }
    }
    Assert(sent > 0 && counted == sent)
    h := Trunc[uint](r.value)
    Assert(rt.ChildWait(io, h, s.job).value == 0)
    PClose(io, s.pipes[1])
    PClose(io, h)
}

// SdLockWait waits for h's exclusive lock (on its own handle), counts it
// in got, holds it 1 ms, and gives it up; a Canceled wait counts in cut.
SdLockWait(io: Io, h: uint, got: &raw.Atomic[u32], cut: &raw.Atomic[u32]) {
    r := rt.FileLock(io, h, true, false)
    if r.err == core.SysErr.Canceled {
        _ = cut.Add(1)
        return
    }
    if r.value == 1 {
        _ = got.Add(1)
        io.NoCancel().Sleep(core.DurationOf(DlMs)) else {}
        _ = rt.FileUnlock(io, h)
    }
}

// SdLockers is how many tasks wait for one lock at once: more than the
// blocking pool's 64 threads, so a wait that held one would starve the
// rest.
const SdLockers: int = 80

test "SD8-Lock-Waiters 80 tasks wait for one file's lock on their own handles: a deadline ends every wait with nothing taken, then each takes the lock in turn after the holder frees it" (sys: &System) {
    io := sys.io
    var nb: [256]u8 = [0; 256]
    name := FlName(mut nb, "sdlock", 0)
    CaWrite(io, name, 10)
    holder := Trunc[uint](NativeOpen(io, name, 2).value)
    var hs: [80]uint = [0; 80]
    for i in 0..SdLockers {
        hs[i] = Trunc[uint](NativeOpen(io, name, 2).value)
        Assert(hs[i] != 0)
    }
    Assert(rt.FileLock(io, holder, true, true).value == 1)
    got := raw.Atomic[u32].New(0)
    cut := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(50 * DlMs))
        for i in 0..SdLockers {
            s.go SdLockWait(io, hs[i], &got, &cut)
        }
    }
    Assert(got.Load() == 0 && cut.Load() == u32(SdLockers))
    // Nothing was taken: the holder still holds it alone.
    Assert(rt.FileLock(io, hs[0], true, true).value == 0)
    scope s := io.Scope() {
        for i in 0..SdLockers {
            s.go SdLockWait(io, hs[i], &got, &cut)
        }
        io.Sleep(core.DurationOf(30 * DlMs)) else {}
        Assert(got.Load() == 0)
        Assert(rt.FileUnlock(io, holder).err == core.SysErr.None)
    }
    Assert(got.Load() == u32(SdLockers))
    for i in 0..SdLockers {
        Assert(NativeClose(io, hs[i]).err == core.SysErr.None)
    }
    Assert(NativeClose(io, holder).err == core.SysErr.None)
    Assert(NativeRemove(io, name).err == core.SysErr.None)
}

test "SD8-Seek-Stream rt.FileSeek of a socket on the Linux poller is the OS's ESPIPE, never a position kept in its record" (sys: &System) {
    if Target.os != core.Os.Linux {
        return
    }
    l := rt.ListenWith(sys.io, "127.0.0.1:0", 0, true)
    Assert(l.err == core.SysErr.None)
    var ab: [32]u8 = [0; 32]
    d := core.IoDial(sys.io, NAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    for whence in 0..3 {
        r := rt.FileSeek(sys.io, Trunc[uint](d.value), 0, Trunc[u32](whence))
        Assert(r.err != core.SysErr.None && r.os == 29)
    }
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](l.value))
}

// SdEntry is "e" and i's decimal digits, in b.
SdEntry(b: mut [16]u8, i: int): str from b {
    b[0] = 101
    var n := 1
    var d := 1
    for d * 10 <= i {
        d *= 10
    }
    var v := i
    for d > 0 {
        b[n] = Trunc[u8](48 + v / d)
        v = v % d
        n += 1
        d /= 10
    }
    return str.From(b[0..n]).Must()
}

// SdListCount reads the directory stream d to its end: the entries, or -1
// on an error.
SdListCount(io: Io, d: uint): int {
    var n := 0
    var name: [512]u8 = [0; 512]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    for {
        r := NativeReadDir(io, d, mut name[..], mut st)
        if r.err != core.SysErr.None {
            return -1
        }
        if r.value == 0 {
            return n
        }
        n += 1
    }
}

test "SD8-DirOpenAt-Fill a directory rt.DirOpenAt opens (its first fill made in the open's own job on Windows) lists each of its 400 entries once, more than one block, then ends; an empty one ends at once; a second open lists the same" (sys: &System) {
    io := sys.io
    var rb: [256]u8 = [0; 256]
    root := FlName(mut rb, "sdr8", 0)
    Assert(NativeMkdir(io, root).err == core.SysErr.None)
    var nb: [16]u8 = [0; 16]
    for i in 0..399 {
        RaFile(io, root, SdEntry(mut nb, i), 1)
    }
    RaMkdir(io, root, "empty")
    o := rt.DirOpenAt(io, 0, root.AsBytes())
    Assert(o.err == core.SysErr.None)
    d := Trunc[uint](o.value)
    Assert(SdListCount(io, d) == 400)
    // At the end, the end again.
    Assert(SdListCount(io, d) == 0)
    e := rt.DirOpenAt(io, d, "empty".AsBytes())
    Assert(e.err == core.SysErr.None)
    Assert(SdListCount(io, Trunc[uint](e.value)) == 0)
    Assert(NativeClose(io, Trunc[uint](e.value)).err == core.SysErr.None)
    Assert(NativeClose(io, d).err == core.SysErr.None)
    o2 := rt.DirOpenAt(io, 0, root.AsBytes())
    Assert(o2.err == core.SysErr.None)
    d2 := Trunc[uint](o2.value)
    Assert(RaRemoveTree(io, d2, 0) == 400)
    Assert(NativeClose(io, d2).err == core.SysErr.None)
    Assert(rt.RemoveAt(io, 0, root.AsBytes(), true).err == core.SysErr.None)
}

test "sys.UnixUnbind removes the socket file a bind left behind a closed listener, so the next UnixListen at that path binds again" (system: &System) {
    io := system.io
    // Linux's sockets need Linux's own file system (WSL's Windows drives
    // refuse a bind).
    path := if Target.os == Os.Linux { "/tmp/mk-sd8-unbind.sock" } else { "mk-sd8-unbind.sock" }
    sys.UnixUnbind(path.AsBytes())
    l := rt.UnixListen(io, path.AsBytes(), 0)
    Assert(l.err == core.SysErr.None)
    Assert(NativeClose(io, Trunc[uint](l.value)).err == core.SysErr.None)
    // The file stays after the close, so a listen there is AddrInUse.
    again := rt.UnixListen(io, path.AsBytes(), 0)
    Assert(again.err == core.SysErr.AddrInUse)
    sys.UnixUnbind(path.AsBytes())
    l2 := rt.UnixListen(io, path.AsBytes(), 0)
    Assert(l2.err == core.SysErr.None)
    Assert(NativeClose(io, Trunc[uint](l2.value)).err == core.SysErr.None)
    sys.UnixUnbind(path.AsBytes())
    // A second call finds no file and does no harm.
    sys.UnixUnbind(path.AsBytes())
}

// SdWholeWait is a whole call's job: it waits up to 10 s on the semaphore
// whose address is w.a, then answers 7 (-1 when the wait timed out).
SdWholeWait(a: *core.IoCallArgs) {
    unsafe "w.a is the block's semaphore, live until the call returns" {
        ok := sys.SemWait(&(*raw.FromAddr[sys.Sem](Trunc[uint]((*a).w.a))), 10_000_000_000)
        (*a).res = core.IoResult(value: if ok { 7 } else { -1 }, err: core.SysErr.None, os: 0)
    }
}

// SdWholeMark is a whole call's job: it stores 1 in the word at w.a.
SdWholeMark(a: *core.IoCallArgs) {
    unsafe "w.a is the block's word, live until the call returns" {
        (*raw.FromAddr[raw.Atomic[u32]](Trunc[uint]((*a).w.a))).Store(1)
        (*a).res = core.IoResult(value: 5, err: core.SysErr.None, os: 0)
    }
}

// SdWhole runs job through io's call entry on the whole route, with w in
// w.a.
SdWhole(io: Io, job: fn(*core.IoCallArgs), w: uint): core.IoResult {
    var a := core.IoCallArgs(op: core.IoOpSpawn, route: core.IoRouteWhole, h: 0, os: 0, p: raw.Null[u8](), pn: 0,
        q: raw.Null[u8](), qn: 0, out: raw.Null[u8](), outn: 0, w: core.IoWords(a: Trunc[i64](w), b: 0, c: 0, d: 0),
        res: core.IoResult(value: 0, err: core.SysErr.None, os: 0), job: job, stop: raw.Null[u32]())
    unsafe "a is in this frame until IoCall returns" {
        return core.IoCall(io, raw.OfMut(mut a))
    }
}

// SdWholeIn runs SdWholeWait on the whole route over the semaphore at sem
// and stores its value in out, or -2 when it was Canceled.
SdWholeIn(io: Io, sem: uint, out: &raw.Atomic[i64]) {
    r := SdWhole(io, SdWholeWait, sem)
    out.Store(if r.err == core.SysErr.None { r.value } else { -2 })
}

// SdRelease posts n to the semaphore at sem after 3 s, so holders that a
// fixed route would outlive end late rather than never.
SdRelease(io: Io, sem: uint, n: u32) {
    io.NoCancel().Sleep(core.DurationOf(3_000_000_000)) else {}
    unsafe "sem lives in the block's frame until the scope ends" {
        sys.SemPost(&(*PSem(sem)), n)
    }
}

test "SD8-Whole-Queued a whole-route call queued behind busy pool threads ends at its scope's deadline unrun, Canceled; one in its call runs to its end through a cancel" (sys: &System) {
    var sem := PNewSem()
    var max: u32 = 0
    unsafe "the TaskState page Start mapped, which lives until exit" {
        max = (*TS()).pmax
    }
    done := raw.Atomic[u32].New(0)
    ran := raw.Atomic[u32].New(0)
    var got := core.SysErr.None
    var took: i64 = 0
    scope outer := sys.io.Scope() {
        // Every pool thread blocked in a job, outside the cut scope.
        for _ in 0..max {
            outer.go PBlocker(sys.io, PAddr(&sem), &done)
        }
        outer.go SdRelease(sys.io, PAddr(&sem), max)
        Assert(PPoolIs(0))
        t0 := BenchNow()
        scope s := sys.io.Scope() {
            s.CancelAfter(core.DurationOf(100_000_000))
            unsafe "ran lives in this frame until the call returns" {
                got = SdWhole(sys.io, SdWholeMark, raw.Addr(raw.Of(&ran))).err
            }
        }
        took = BenchNow() - t0
    }
    // Cut while queued: Canceled, the job never ran, long before a holder
    // left its thread.
    Assert(got == core.SysErr.Canceled && ran.Load() == 0 && took < 2_000_000_000)
    Assert(done.Load() == max)
    // In its call: a cancel neither marks nor interrupts it.
    var sem2 := PNewSem()
    res := raw.Atomic[i64].New(0)
    scope s2 := sys.io.Scope() {
        s2.go SdWholeIn(sys.io, PAddr(&sem2), &res)
        unsafe "the TaskState page Start mapped, which lives until exit" {
            st := TS()
            t1 := BenchNow()
            for BenchNow() - t1 < 2_000_000_000 {
                rt.Lock(&(*st).plock)
                busy := (*st).pqueued == 0 && (*st).pwaiting + 1 <= (*st).pthreads
                rt.Unlock(&(*st).plock)
                if busy && CBlocked(RecOf(&s2)) == 1 {
                    break
                }
                YieldNow()
            }
        }
        s2.Cancel()
        PPost(&sem2, 1)
    }
    Assert(res.Load() == 7)
}
