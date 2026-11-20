// Why: proc.Run and Supervise rest on the runtime's process entries, whose
// pipes, waits and kills must neither deadlock nor leak handles.
// What: the RT-Os-Spawn-* blocks, RT-Os-Inherit and RT-Os-Inherit-Close:
// NativeSpawn with every stdio mode, exit codes, both pipes drained at once
// past the pipe buffer, stdin fed and closed, a missing program, a
// cancelled procwait, prockill, an inherited listener, the parent's accept
// beside a child on it and one holder's close of it, and equal handle counts
// over many spawns.
// How: the child is the system shell (cmd.exe /c on Windows, /bin/sh -c on
// Linux); run by the scheduler harness at 1 to 8 carriers, dev and release.

import std.core
import std.raw
import std.rt
import std.sys

// PHandles is the process's open handle count (the test blocks' parameter
// is named sys, so the module is reached from here).
PHandles(): int {
    // On Windows each started carrier and pool thread keeps its thread's
    // handle for good, and the runtime starts them on demand: a carrier
    // when a ready finds none spinning or idle, a pool thread when queued
    // jobs outnumber the waiting ones. When one starts inside a measured
    // window is timing, not a leak (the pool's spins can move a second
    // carrier's start into the window under the stress run's jitter), so the count leaves them out. A thread is
    // counted before it exists (StartCarrier, Enqueue), so a start in flight
    // reads one low: the count is taken until two samples 2 ms apart agree
    // (at most 20 tries). Linux threads hold no descriptor.
    var last := PHandlesNow()
    for _ in 0..20 {
        t0 := BenchNow()
        for BenchNow() - t0 < 2_000_000 {
            sys.ThreadYield()
        }
        n := PHandlesNow()
        if n == last {
            return n
        }
        last = n
    }
    return last
}

// PHandlesNow is one sample of PHandles' count.
PHandlesNow(): int {
    var n := sys.HandleCount()
    if PWin() {
        st := TS()
        unsafe "the TaskState page; pthreads under plock" {
            rt.Lock(&(*st).plock)
            threads := int((*st).pthreads)
            rt.Unlock(&(*st).plock)
            // Edge: none; a test count (a carrier is counted before its
            // thread starts, so a start in flight reads one low).
            n -= threads + int((*st).ncarriers.Load())
        }
    }
    return n
}

// PWin: the iocp backend, so Windows.
PWin(): bool {
    return PollBackend() == 1
}

// TermModeLen is the bytes of a saved terminal mode: Linux's 36-byte
// termios, the first 64 bytes of macOS's 72-byte one.
TermModeLen(): int {
    return if Target.os == Os.Macos { 64 } else { 36 }
}

// PStdio is a child's request with the three modes given.
PStdio(i: u32, o: u32, e: u32): core.IoChild {
    return core.IoChild(stdio: [i, o, e], inherit: 0, pipes: [0; 3], pid: 0)
}

// PShell starts the shell with command c; the result and the child.
PShell(io: Io, c: str, child: mut core.IoChild): core.IoResult {
    if PWin() {
        var a: [3]str = ["cmd.exe", "/c", c]
        return NativeSpawn(io, a[..], child)
    }
    var a: [3]str = ["/bin/sh", "-c", c]
    return NativeSpawn(io, a[..], child)
}

// PWait is procwait that must succeed: the exit code.
PWait(io: Io, h: uint): i64 {
    r := NativeProcWait(io, h)
    Assert(r.err == core.SysErr.None)
    return r.value
}

PClose(io: Io, h: uint) {
    if h != 0 {
        r := NativeClose(io, h)
        Assert(r.err == core.SysErr.None)
    }
}

// PRead reads h to its end into buf: the bytes read (the rest of a longer
// stream is read and dropped, so the child never blocks on a full pipe).
PRead(io: Io, h: uint, buf: mut []u8): int {
    var n := 0
    var sink: [4096]u8 = [0; 4096]
    for {
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        if n < buf.Len() {
            r = NativeRead(io, h, mut buf[n..], -1)
        } else {
            r = NativeRead(io, h, mut sink[..], -1)
        }
        Assert(r.err == core.SysErr.None)
        if r.value == 0 {
            return n
        }
        if n < buf.Len() {
            n += Trunc[int](r.value)
        }
    }
}

// PCount reads h to its end and stores the byte count in got.
PCount(io: Io, h: uint, got: &raw.Atomic[u64]) {
    var buf: [8192]u8 = [0; 8192]
    var n: u64 = 0
    for {
        r := NativeRead(io, h, mut buf[..], -1)
        if r.err != core.SysErr.None || r.value == 0 {
            break
        }
        n += Trunc[u64](r.value)
    }
    got.Store(n)
}

// PHas: b holds the bytes of s somewhere.
PHas(b: []u8, s: str): bool {
    t := s.AsBytes()
    if t.Len() > b.Len() {
        return false
    }
    for i in 0..b.Len() - t.Len() + 1 {
        var same := true
        for j in 0..t.Len() {
            if b[i + j] != t[j] {
                same = false
                break
            }
        }
        if same {
            return true
        }
    }
    return false
}

test "RT-Os-Spawn-Exit exit codes 0, 7 and 255 with every stream null" (sys: &System) {
    var codes: [3]i64 = [0, 7, 255]
    var cmds: [3]str = ["exit 0", "exit 7", "exit 255"]
    for k in 0..3 {
        var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        r := PShell(sys.io, cmds[k], mut c)
        Assert(r.err == core.SysErr.None && r.value != 0 && c.pid > 0)
        Assert(c.pipes[0] == 0 && c.pipes[1] == 0 && c.pipes[2] == 0)
        h := Trunc[uint](r.value)
        Assert(PWait(sys.io, h) == codes[k])
        // The answer is kept: a second wait gives it again.
        Assert(PWait(sys.io, h) == codes[k])
        PClose(sys.io, h)
    }
}

test "RT-Os-Spawn-Out stdout and stderr through pipes" (sys: &System) {
    var c := PStdio(core.IoStdNull, core.IoStdPipe, core.IoStdPipe)
    cmd := if PWin() { "echo hello& echo oops 1>&2" } else { "echo hello; echo oops 1>&2" }
    r := PShell(sys.io, cmd, mut c)
    Assert(r.err == core.SysErr.None && c.pipes[0] == 0 && c.pipes[1] != 0 && c.pipes[2] != 0)
    var out: [64]u8 = [0; 64]
    var err: [64]u8 = [0; 64]
    no := PRead(sys.io, c.pipes[1], mut out[..])
    ne := PRead(sys.io, c.pipes[2], mut err[..])
    Assert(no >= 6 && PHas(out[0..no], "hello") && !PHas(out[0..no], "oops"))
    Assert(ne >= 5 && PHas(err[0..ne], "oops") && !PHas(err[0..ne], "hello"))
    Assert(PWait(sys.io, Trunc[uint](r.value)) == 0)
    PClose(sys.io, c.pipes[1])
    PClose(sys.io, c.pipes[2])
    PClose(sys.io, Trunc[uint](r.value))
}

// PFeed writes data to h and closes it; got 1 when every byte went.
PFeed(io: Io, h: uint, data: []u8, got: &raw.Atomic[u32]) {
    var n := 0
    for n < data.Len() {
        r := NativeWrite(io, h, data[n..], -1)
        if r.err != core.SysErr.None || r.value <= 0 {
            break
        }
        n += Trunc[int](r.value)
    }
    _ = NativeClose(io, h)
    if n == data.Len() {
        got.Store(1)
    }
}

// PFeedRaw is PFeed over the n bytes at address p (an address: a pointer
// is not Send).
PFeedRaw(io: Io, h: uint, p: uint, n: int, got: &raw.Atomic[u32]) {
    unsafe "p holds n bytes for the caller's scope" {
        PFeed(io, h, raw.Slice(raw.FromAddr[u8](p), n), got)
    }
}

test "RT-Os-Spawn-In stdin fed through a pipe and closed, the child's answer read back" (sys: &System) {
    var c := PStdio(core.IoStdPipe, core.IoStdPipe, core.IoStdNull)
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    if PWin() {
        // By its full path: PATH may name another sort.exe first (Git's
        // usr/bin does in Git for Windows shells).
        var a: [1]str = ["C:\\Windows\\System32\\sort.exe"]
        r = NativeSpawn(sys.io, a[..], mut c)
    } else {
        var a: [1]str = ["cat"]
        r = NativeSpawn(sys.io, a[..], mut c)
    }
    Assert(r.err == core.SysErr.None && c.pipes[0] != 0 && c.pipes[1] != 0 && c.pipes[2] == 0)
    // 200 KiB of input, past every pipe buffer, while the answer is read:
    // a feeder task and this one, or both pipes would fill.
    p := rt.HeapAlloc(204800, 16)
    Assert(!raw.IsNull(p))
    unsafe "p holds 204,800 bytes until the HeapFree below" {
        raw.Fill(p, 120, 204800)
        for i in 0..3200 {
            raw.Write(raw.Offset(p, i * 64 + 63), 10)
        }
    }
    g := raw.Atomic[u32].New(0)
    n := raw.Atomic[u64].New(0)
    scope s := sys.io.Scope() {
        s.go PFeedRaw(sys.io, c.pipes[0], raw.Addr(p), 204800, &g)
        s.go PCount(sys.io, c.pipes[1], &n)
    }
    rt.HeapFree(p, 204800, 16)
    Assert(g.Load() == 1)
    // cat gives back the 204,800 bytes it was fed. System32's sort.exe ends
    // each of the 3,200 lines with CR LF: 208,000 bytes (measured on a
    // development machine, 2026-11-09; a GNU sort first in PATH kept LF,
    // which is why the name is a full path).
    if PWin() {
        Assert(n.Load() == 208000)
    } else {
        Assert(n.Load() == 204800)
    }
    Assert(PWait(sys.io, Trunc[uint](r.value)) == 0)
    PClose(sys.io, c.pipes[1])
    PClose(sys.io, Trunc[uint](r.value))
}

test "RT-Os-Spawn-Both stdout and stderr past the pipe buffer, drained at once" (sys: &System) {
    var c := PStdio(core.IoStdNull, core.IoStdPipe, core.IoStdPipe)
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var want: u64 = 0
    if PWin() {
        // 2,000 lines of 64 bytes and CR LF on each stream (132 KB each).
        r = PShell(sys.io, "for /l %i in (1,1,2000) do @(echo 0123456789012345678901234567890123456789012345678901234567890123& 1>&2 echo 0123456789012345678901234567890123456789012345678901234567890123)", mut c)
        want = 2000 * 66
    } else {
        r = PShell(sys.io, "head -c 1048576 /dev/zero; head -c 1048576 /dev/zero 1>&2", mut c)
        want = 1048576
    }
    Assert(r.err == core.SysErr.None)
    o := raw.Atomic[u64].New(0)
    e := raw.Atomic[u64].New(0)
    scope s := sys.io.Scope() {
        s.go PCount(sys.io, c.pipes[1], &o)
        s.go PCount(sys.io, c.pipes[2], &e)
    }
    Assert(o.Load() == want && e.Load() == want)
    Assert(PWait(sys.io, Trunc[uint](r.value)) == 0)
    PClose(sys.io, c.pipes[1])
    PClose(sys.io, c.pipes[2])
    PClose(sys.io, Trunc[uint](r.value))
}

test "RT-Os-Spawn-Missing a program that is not there is NotFound, and nothing leaks" (sys: &System) {
    // One round first, so the pool threads exist already (a new thread is
    // a new handle) and so does what the first CreateProcessW loads (45
    // handles on a development machine, once per process, measured).
    var before := 0
    for k in 0..6 {
        if k == 3 {
            before = PHandles()
        }
        var c := PStdio(core.IoStdPipe, core.IoStdPipe, core.IoStdPipe)
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        if k % 3 == 0 {
            var a: [2]str = ["mkx-no-such-program", "x"]
            r = NativeSpawn(sys.io, a[..], mut c)
        } else if PWin() {
            var a: [1]str = ["C:\\mkx-nope\\x.exe"]
            r = NativeSpawn(sys.io, a[..], mut c)
        } else {
            var a: [1]str = ["/mkx-nope/x"]
            r = NativeSpawn(sys.io, a[..], mut c)
        }
        Assert(r.err == core.SysErr.NotFound)
        Assert(c.pipes[0] == 0 && c.pipes[1] == 0 && c.pipes[2] == 0)
    }
    var noargs: [0]str = []
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    Assert(NativeSpawn(sys.io, noargs[..], mut c).err == core.SysErr.InvalidPath)
    var nul: [2]str = ["a\u{0}b", "x"]
    Assert(NativeSpawn(sys.io, nul[..], mut c).err == core.SysErr.InvalidPath)
    Assert(PHandles() == before)
}

// PWaitCancel waits for h; got: 1 Canceled, 2 another answer.
PWaitCancel(io: Io, h: uint, got: &raw.Atomic[u32]) {
    r := NativeProcWait(io, h)
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else {
        got.Store(2)
    }
}

test "RT-Os-Spawn-Kill a cancelled procwait, then prockill, then the exit code" (sys: &System) {
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    if PWin() {
        var a: [4]str = ["ping.exe", "-n", "60", "127.0.0.1"]
        r = NativeSpawn(sys.io, a[..], mut c)
    } else {
        var a: [2]str = ["sleep", "60"]
        r = NativeSpawn(sys.io, a[..], mut c)
    }
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    for round in 0..5 {
        g := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go PWaitCancel(sys.io, h, &g)
            if round % 2 == 0 {
                CAwaitBlocked(&s, 1)
            }
            s.Cancel()
        }
        Assert(g.Load() == 1)
    }
    k := NativeProcKill(sys.io, h)
    Assert(k.err == core.SysErr.None)
    code := PWait(sys.io, h)
    if PWin() {
        Assert(code == 1)
    } else {
        Assert(code == -9)
    }
    // Killing a child that ended is success.
    Assert(NativeProcKill(sys.io, h).err == core.SysErr.None)
    PClose(sys.io, h)
}

test "RT-Os-Spawn-Leak 100 spawns with pipes leave the handle count as it was" (sys: &System) {
    // One spawn first, so the pool threads the loop uses exist already.
    for round in 0..2 {
        before := PHandles()
        times := if round == 1 { 100 } else { 1 }
        for _ in 0..times {
            var c := PStdio(core.IoStdPipe, core.IoStdPipe, core.IoStdNull)
            r := PShell(sys.io, "exit 3", mut c)
            Assert(r.err == core.SysErr.None)
            PClose(sys.io, c.pipes[0])
            var buf: [16]u8 = [0; 16]
            Assert(PRead(sys.io, c.pipes[1], mut buf[..]) == 0)
            Assert(PWait(sys.io, Trunc[uint](r.value)) == 3)
            PClose(sys.io, c.pipes[1])
            PClose(sys.io, Trunc[uint](r.value))
        }
        if round == 1 {
            Assert(PHandles() == before)
        }
    }
}

// PExit ends this process at once with code.
PExit(code: i32): never {
    sys.Exit(code)
}

// PServe is an inherited listener's child (the Inherit block): each
// connection gets three bytes back, the child's tag and the port its
// inherited entry answered in os, until the parent kills it. A
// failed accept ends the child with 3 (the parent asserts a kill's code,
// so this would show).
PServe(io: Io, l: uint, tag: u8, port: i64) {
    for {
        a := NativeAccept(io, l)
        if a.err != core.SysErr.None {
            sys.Exit(3)
        }
        s := Trunc[uint](a.value)
        var buf: [16]u8 = [0; 16]
        _ = NativeRecv(io, s, mut buf[..])
        var t: [3]u8 = [tag, Trunc[u8](port >> 8), Trunc[u8](port)]
        _ = NSendAll(io, s, t[..])
        _ = NativeClose(io, s)
    }
}

// PAsk dials port, sends a byte and returns the tag it gets back, 0 on any
// failure or when the child's inherited entry did not answer port.
PAsk(io: Io, port: i64): u8 {
    var ab: [32]u8 = [0; 32]
    d := NativeDial(io, NAddr(mut ab, port))
    if d.err != core.SysErr.None {
        return 0
    }
    s := Trunc[uint](d.value)
    var q: [1]u8 = [63]
    var got: [3]u8 = [0; 3]
    var n := 0
    if NSendAll(io, s, q[..]) {
        for n < 3 {
            r := NativeRecv(io, s, mut got[n..])
            if r.err != core.SysErr.None || r.value <= 0 {
                break
            }
            n += Trunc[int](r.value)
        }
    }
    NAbort(io, s)
    return if n != 3 || ((i64(got[1]) << 8) | i64(got[2])) != port { 0 } else { got[0] }
}

// PChild spawns this program again (an empty args[0]: Supervise's
// re-exec) with the same block index and the tag, the listener l
// inherited, standard error inherited.
PChild(sys: &System, l: uint, tag: str): core.IoResult {
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdInherit)
    c.inherit = l
    var a: [3]str = ["", sys.args[1], tag]
    r := NativeSpawn(sys.io, a[..], mut c)
    Assert(r.err == core.SysErr.None && c.pid > 0)
    return r
}

test "RT-Os-Inherit a listener inherited by two children, one killed, the other still serving" (sys: &System) {
    if sys.args.Len() == 3 && !PWin() {
        // A program the child starts before it asks for the
        // listener (or if it never asks) does not get descriptor 3.
        var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        var a: [3]str = ["/bin/sh", "-c", "test -e /proc/self/fd/3"]
        r := NativeSpawn(sys.io, a[..], mut c)
        Assert(r.err == core.SysErr.None)
        Assert(PWait(sys.io, Trunc[uint](r.value)) == 1)
        PClose(sys.io, Trunc[uint](r.value))
    }
    i := NativeInherited(sys.io)
    if i.err == core.SysErr.None {
        // The child: serve the inherited listener until killed. A second
        // ask is NotFound (at most once); args[0] is this program's path.
        Assert(NativeInherited(sys.io).err == core.SysErr.NotFound)
        Assert(sys.args.Len() == 3 && sys.args[0].Len() > 0)
        // A child whose parent failed is never killed: it ends itself
        // after 60 s (exit 4), so a red run leaves no server behind.
        scope s := sys.io.Scope() {
            s.go PServe(sys.io, Trunc[uint](i.value), sys.args[2].AsBytes()[0], i.os)
            _ = NativeSleep(sys.io, 60_000_000_000)
            PExit(4)
        }
        return
    }
    Assert(i.err == core.SysErr.NotFound)
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    a := PChild(sys, lh, "A")
    b := PChild(sys, lh, "B")
    var na := 0
    var nb := 0
    for _ in 0..40 {
        t := PAsk(sys.io, l.os)
        Assert(t == 65 || t == 66)
        if t == 65 {
            na += 1
        } else {
            nb += 1
        }
    }
    Assert(na + nb == 40)
    // A killed: B serves every later connection, and the parent's own
    // poller (dialing here) is untouched by the children's accepts.
    Assert(NativeProcKill(sys.io, Trunc[uint](a.value)).err == core.SysErr.None)
    ca := PWait(sys.io, Trunc[uint](a.value))
    Assert(ca == 1 || ca == -9)
    for _ in 0..20 {
        Assert(PAsk(sys.io, l.os) == 66)
    }
    Assert(NativeProcKill(sys.io, Trunc[uint](b.value)).err == core.SysErr.None)
    cb := PWait(sys.io, Trunc[uint](b.value))
    Assert(cb == 1 || cb == -9)
    PClose(sys.io, Trunc[uint](a.value))
    PClose(sys.io, Trunc[uint](b.value))
    PClose(sys.io, lh)
}

test "RT-Os-Inherit-Close a holder's close of an inherited listener leaves the other holders serving" (sys: &System) {
    i := NativeInherited(sys.io)
    if i.err == core.SysErr.None {
        // Child C closes its copy and ends; child B serves until killed
        // (or for 60 s, as in the Inherit block).
        h := Trunc[uint](i.value)
        tag := sys.args[2].AsBytes()[0]
        if tag == 67 {
            PExit(if NativeClose(sys.io, h).err == core.SysErr.None { 0 } else { 5 })
        }
        scope s := sys.io.Scope() {
            s.go PServe(sys.io, h, tag, i.os)
            _ = NativeSleep(sys.io, 60_000_000_000)
            PExit(4)
        }
        return
    }
    Assert(i.err == core.SysErr.NotFound)
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    c := PChild(sys, lh, "C")
    Assert(PWait(sys.io, Trunc[uint](c.value)) == 0)
    PClose(sys.io, Trunc[uint](c.value))
    // C's close left the listener open: B, started after it, serves.
    b := PChild(sys, lh, "B")
    Assert(PAsk(sys.io, l.os) == 66)
    // The parent accepts beside B on the shared listener: some dial
    // reaches it (tag 80), every other one B.
    got := raw.Atomic[u32].New(0)
    var np := 0
    scope s := sys.io.Scope() {
        s.go PServeOne(sys.io, lh, 80, l.os, &got)
        for _ in 0..400 {
            t := PAsk(sys.io, l.os)
            Assert(t == 66 || t == 80)
            if t == 80 {
                np += 1
                break
            }
        }
        s.Cancel()
    }
    Assert(np == 1 && got.Load() == 1)
    // So does the parent's own close of its copy.
    PClose(sys.io, lh)
    for _ in 0..5 {
        Assert(PAsk(sys.io, l.os) == 66)
    }
    Assert(NativeProcKill(sys.io, Trunc[uint](b.value)).err == core.SysErr.None)
    cb := PWait(sys.io, Trunc[uint](b.value))
    Assert(cb == 1 || cb == -9)
    PClose(sys.io, Trunc[uint](b.value))
}

// PServeOne answers one connection on l as PServe does, with tag; got is 1
// when it did, 2 when the accept failed (the block's cancel).
PServeOne(io: Io, l: uint, tag: u8, port: i64, got: &raw.Atomic[u32]) {
    a := NativeAccept(io, l)
    if a.err != core.SysErr.None {
        got.Store(2)
        return
    }
    s := Trunc[uint](a.value)
    var buf: [16]u8 = [0; 16]
    _ = NativeRecv(io, s, mut buf[..])
    var t: [3]u8 = [tag, Trunc[u8](port >> 8), Trunc[u8](port)]
    _ = NSendAll(io, s, t[..])
    _ = NativeClose(io, s)
    got.Store(1)
}

// PWaitOne waits for h and counts it in got when it was killed.
PWaitOne(io: Io, h: uint, got: &raw.Atomic[u32]) {
    r := NativeProcWait(io, h)
    if r.err == core.SysErr.None && (r.value == 1 || r.value == -9) {
        _ = got.Add(1)
    }
}

// PSleeper starts a child that sleeps for a minute.
PSleeper(io: Io): uint {
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    if PWin() {
        var a: [4]str = ["ping.exe", "-n", "60", "127.0.0.1"]
        r = NativeSpawn(io, a[..], mut c)
    } else {
        var a: [2]str = ["sleep", "60"]
        r = NativeSpawn(io, a[..], mut c)
    }
    Assert(r.err == core.SysErr.None)
    return Trunc[uint](r.value)
}

test "RT-Os-Spawn-Many 80 children waited for at once, more than the pool's 64 threads, then one more spawn" (sys: &System) {
    // A child wait holds no pool thread: 80 waits in flight, and a spawn
    // and a stat (both pool jobs) still run. Then every child is killed.
    var hs: [80]uint = [0; 80]
    for k in 0..80 {
        hs[k] = PSleeper(sys.io)
    }
    got := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..80 {
            s.go PWaitOne(sys.io, hs[k], &got)
        }
        CAwaitBlocked(&s, 80)
        var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        r := PShell(sys.io, "exit 5", mut c)
        Assert(r.err == core.SysErr.None)
        Assert(PWait(sys.io, Trunc[uint](r.value)) == 5)
        PClose(sys.io, Trunc[uint](r.value))
        for k in 0..80 {
            Assert(NativeProcKill(sys.io, hs[k]).err == core.SysErr.None)
        }
    }
    Assert(got.Load() == 80)
    for k in 0..80 {
        PClose(sys.io, hs[k])
    }
}
