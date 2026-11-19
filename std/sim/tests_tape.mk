// Why: a replay is only faithful if deadlines and the call ops replay as
// recorded, with no OS behind the replay.
// What: the SIM-Tape-* blocks: a run recorded by a Recorder and replayed by
// its Replayer observes the same Expired, Remaining and cancellations, and
// the same answers of the file system's, runtime and network call ops;
// helpers TapeTimed, TapeChan, TapeSimRun, TapeFs, TapeRt and TapeNet.
// How: each block records a run over the native Io or a Sim, replays the
// log and compares. Run by mk test --std=std.sim.

import std.core
import std.fs
import std.net
import std.raw
import std.rt
import std.time

// TapeTimed runs five TimedOut-style scopes on io and returns what each
// observed: whether its call was cut, Expired, and Remaining in
// nanoseconds (-1 for none).
// 1: a 20 ms deadline cuts a 10 s sleep.
// 2: a deadline of 0 fires at once, before any call; a sleep then is cut.
// 3: a scope cancelled before its deadline is never Expired.
// 4: an outer deadline cuts a sleep in an inner scope, which is not Expired.
// 5: a 10 s deadline does not cut a 1 ms sleep; Remaining is read twice.
TapeTimed(io: Io): [16]i64 {
    var o: [16]i64 = [0; 16]
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        io.Sleep(time.Seconds(10)) else {
            o[0] = 1
        }
        o[1] = if s.Expired() { 1 } else { 0 }
        o[2] = TapeLeft(s.Remaining())
    }
    scope s := io.Scope() {
        s.CancelAfter(time.Nanos(0))
        o[3] = if s.Expired() { 1 } else { 0 }
        o[4] = TapeLeft(s.Remaining())
        io.Sleep(time.Millis(1)) else {
            o[5] = 1
        }
    }
    scope s := io.Scope() {
        s.Cancel()
        s.CancelAfter(time.Millis(1))
        io.Sleep(time.Millis(50)) else {
            o[6] = 1
        }
        o[7] = if s.Expired() { 1 } else { 0 }
    }
    scope outer := io.Scope() {
        outer.CancelAfter(time.Millis(20))
        scope inner := io.Scope() {
            io.Sleep(time.Seconds(10)) else {
                o[8] = 1
            }
            o[9] = if inner.Expired() { 1 } else { 0 }
        }
        o[10] = if outer.Expired() { 1 } else { 0 }
    }
    scope s := io.Scope() {
        s.CancelAfter(time.Seconds(10))
        o[13] = TapeLeft(s.Remaining())
        io.Sleep(time.Millis(1)) else {
            o[11] = 1
        }
        o[12] = if s.Expired() { 1 } else { 0 }
        o[14] = TapeLeft(s.Remaining())
    }
    o[15] = 7
    return o
}

// TapeLeft is a Remaining in nanoseconds, -1 for none.
TapeLeft(r: ?core.Duration): i64 {
    if d := r {
        return d.Nanos()
    }
    return -1
}

// TapeTimedOk: o is what TapeTimed observes when each deadline fires as
// armed (or, for 5, does not).
TapeTimedOk(o: [16]i64): bool {
    return o[0] == 1 && o[1] == 1 && o[2] == 0 && o[3] == 1 && o[4] == 0 && o[5] == 1 && o[6] == 1 && o[7] == 0 &&
        o[8] == 1 && o[9] == 0 && o[10] == 1 && o[11] == 0 && o[12] == 0 && o[13] > 0 && o[14] > 0 &&
        o[14] <= o[13] && o[15] == 7
}

// TapeSame: a and b hold the same observations.
TapeSame(a: [16]i64, b: [16]i64): bool {
    for i in 0..16 {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

test "SIM-Tape-Expired a TimedOut-style run recorded over the native Io replays with the same cancellations, Expired and Remaining" (sys: &System) {
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    o1 := TapeTimed(rec.Io())
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    Assert(TapeTimedOk(o1))
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    o2 := TapeTimed(rp.Io())
    Assert(TapeSame(o1, o2))
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TapeChan waits on an empty channel through io under a 20 ms deadline: 1
// in [0] when the wait was Canceled, Expired in [1]. The wait is a futex
// wait (TWait), which the log holds no record of: the fire is logged
// around it.
TapeChan(io: Io, ch: &Chan[int]): [2]i64 {
    var o: [2]i64 = [0; 2]
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        _ = ch.Recv(io) else {
            o[0] = 1
        }
        o[1] = if s.Expired() { 1 } else { 0 }
    }
    return o
}

test "SIM-Tape-ExpiredWait a deadline that cuts a channel wait under a Recorder cuts it at once on replay, with the scope Expired, and the replay does not hang" (sys: &System) {
    a := sys.heap.Alloc()
    ch := Chan[int].New(a, 1) else {
        Assert(false)
        return
    }
    defer ch.Free(a)
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    o1 := TapeChan(rec.Io(), &ch)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    Assert(o1[0] == 1 && o1[1] == 1)
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    t0 := time.Now(sys.io)
    o2 := TapeChan(rp.Io(), &ch)
    // No clock behind the replay: the fire is replayed before the wait,
    // which ends at once.
    Assert(time.Now(sys.io).Since(t0).Nanos() < 5_000_000_000)
    Assert(o2[0] == 1 && o2[1] == 1)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TapeSimRun records TapeTimed under a Recorder over the Sim's io, then
// replays it; out 1 when the recording saw every deadline as armed and
// the replay saw the same.
TapeSimRun(io: Io, h: &Heap, out: &raw.Atomic[u32]) {
    var a := Arena.New(h) else {
        return
    }
    defer a.Free()
    var rec := Recorder.New(io) else {
        return
    }
    o1 := TapeTimed(rec.Io())
    log := rec.Log(&a) else {
        rec.Free()
        return
    }
    rec.Free()
    var rp := Replayer.New(log) else {
        return
    }
    o2 := TapeTimed(rp.Io())
    ok := TapeTimedOk(o1) && TapeSame(o1, o2) && !rp.Diverged() && rp.Done()
    rp.Free()
    if ok {
        out.Store(1)
    }
}

test "SIM-Tape-SimExpired a TimedOut-style run recorded over a Sim replays with the same cancellations, Expired and Remaining, on 4 seeds" (sys: &System) {
    for seed in 1..5 {
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            Assert(false)
            return
        }
        io := s.Io()
        out := raw.Atomic[u32].New(0)
        scope r := io.Scope() {
            r.go TapeSimRun(io, sys.heap, &out)
        }
        s.Free()
        Assert(out.Load() == 1)
    }
}

// TapeFire is a log of one OFire record naming arm number num, then
// nothing: a fire of a scope the replay never armed.
TapeFire(num: u64): [41]u8 {
    var b: [41]u8 = [0; 41]
    b[0] = OFire
    PutAt(mut b[..], 1, FireDigest)
    PutAt(mut b[..], 9, num)
    return b
}

test "SIM-Tape-FireUnknown a logged fire of a scope the replay has not armed diverges the replay, and the call that meets it answers Canceled" (sys: &System) {
    b := TapeFire(7)
    var rp := Replayer.New(b[..]) else {
        Assert(false)
        return
    }
    io := rp.Io()
    var cut := false
    io.Sleep(time.Millis(1)) else {
        cut = true
    }
    Assert(cut)
    Assert(rp.Diverged())
    rp.Free()
}

// TapeErr is e's name.
TapeErr(a: &Arena, e: fs.Error | OutOfMemory): str from a {
    return a.Fmt("{e}", (e)).Must()
}

// TapeFs makes the file system's call ops on io in the directory root
// (made and removed here) and returns what each answered, one field per
// call: Cwd, Absolute, Stat, SetModified, SetPermissions, Copy, Symlink,
// ReadLink and StatLink, Seek, Truncate and DataSync, a Walk
// (IoOpDirOpenAt), RemoveAll (IoOpRemoveAt) and a Stat after it.
TapeFs(a: &Arena, h: &Heap, io: Io, root: str): str from a {
    var s := "fs"
    f := a.Fmt("{root}/f", (root)).Must()
    g := a.Fmt("{root}/g", (root)).Must()
    l := a.Fmt("{root}/l", (root)).Must()
    sub := a.Fmt("{root}/sub", (root)).Must()
    cwd := fs.Cwd(a, io) else e {
        TapeErr(a, e)
    }
    abs := fs.Absolute(a, io, "x") else e {
        TapeErr(a, e)
    }
    s = a.Fmt("{s} cwd={cwd} abs={abs}", (s, cwd, abs)).Must()
    fs.MakeDirAll(io, sub) else e {
        s = a.Fmt("{s} mkdir={e}", (s, e)).Must()
    }
    fs.WriteAll(io, f, "hello".AsBytes()) else e {
        s = a.Fmt("{s} write={e}", (s, e)).Must()
    }
    s = TapeStat(a, io, s, f, false)
    fs.SetModified(io, f, time.FromUnix(1_700_000_000, 0)) else e {
        s = a.Fmt("{s} setmod={e}", (s, e)).Must()
    }
    fs.SetPermissions(io, f, fs.Permissions(mode: 0o444)) else e {
        s = a.Fmt("{s} setperm={e}", (s, e)).Must()
    }
    s = TapeStat(a, io, s, f, false)
    fs.SetPermissions(io, f, fs.Permissions(mode: 0o666)) else e {
        s = a.Fmt("{s} setperm={e}", (s, e)).Must()
    }
    n := fs.Copy(io, f, g) else e {
        s = a.Fmt("{s} copy={e}", (s, e)).Must()
        -1
    }
    s = a.Fmt("{s} copied={n}", (s, n)).Must()
    var linked := true
    fs.Symlink(io, "f", l) else e {
        // A host without the link privilege (Windows without Developer
        // Mode) refuses it, as natively; the replay answers alike, by the
        // same error's name (an Unsupported replayed as Other would show).
        // Windows answers Unsupported there (std/sys/windows/call.mk's
        // Symlink), spelled apart so the no-Unsupported assert below holds.
        linked = false
        en := a.Fmt("{e}", (e)).Must()
        k := if en == "Unsupported" { "unprivileged" } else { en }
        s = a.Fmt("{s} symlink={k}", (s, k)).Must()
    }
    if linked {
        t := fs.ReadLink(a, io, l) else e {
            TapeErr(a, e)
        }
        s = a.Fmt("{s} target={t}", (s, t)).Must()
        s = TapeStat(a, io, s, l, true)
    }
    s = TapeFile(a, io, s, g)
    var listed := 0
    var w := fs.Walk(h, io, root) else e {
        s = a.Fmt("{s} walk={e}", (s, e)).Must()
        return s
    }
    for {
        x := w.Next(a, io) else e {
            s = a.Fmt("{s} next={e}", (s, e)).Must()
            break
        }
        if x.IsNone() {
            break
        }
        listed += 1
    }
    w.Close()
    s = a.Fmt("{s} listed={listed}", (s, listed)).Must()
    fs.RemoveAll(io, root) else e {
        s = a.Fmt("{s} removeall={e}", (s, e)).Must()
    }
    s = TapeStat(a, io, s, root, false)
    return s
}

// TapeStat adds to s what Stat (or StatLink) of p answered.
TapeStat(a: &Arena, io: Io, s: str, p: str, link: bool): str from a {
    if link {
        i := fs.StatLink(io, p) else e {
            return a.Fmt("{s} stat={e}", (s, e)).Must()
        }
        return TapeInfo(a, s, i)
    }
    i := fs.Stat(io, p) else e {
        return a.Fmt("{s} stat={e}", (s, e)).Must()
    }
    return TapeInfo(a, s, i)
}

// TapeInfo adds i's kind, size, mode bits and modification time to s.
TapeInfo(a: &Arena, s: str, i: fs.Info): str from a {
    k := match i.kind {
        .File { "file" }
        .Dir { "dir" }
        .Link { "link" }
        _ { "other" }
    }
    return a.Fmt("{s} stat={k},{z},{m},{t}", (s, k, z: i.size, m: i.permissions.mode, t: i.modified.UnixNanos())).Must()
}

// TapeFile adds to s what a file's Seek, Truncate and Size answered
// (IoOpSeek, IoOpTruncate).
TapeFile(a: &Arena, io: Io, s: str, p: str): str from a {
    var o := fs.DefaultOptions
    o.write = true
    var file := fs.OpenWith(io, p, o) else e {
        return a.Fmt("{s} open={e}", (s, e)).Must()
    }
    var r := s
    end := file.Seek(io, 0, fs.Whence.End) else e {
        r = a.Fmt("{r} seek={e}", (r, e)).Must()
        -1
    }
    file.Truncate(io, 3) else e {
        r = a.Fmt("{r} truncate={e}", (r, e)).Must()
    }
    size := file.Size(io) else e {
        r = a.Fmt("{r} size={e}", (r, e)).Must()
        -1
    }
    file.Close(io).Must()
    return a.Fmt("{r} end={end} size={size}", (r, end, size)).Must()
}

// TapeRt makes the call ops std.rt names directly on io: DataSync and
// Seek of a handle the open entry made, IsTerminal and TermSize of
// stdout, and TimeZone (Windows' record; Unsupported on Linux). Each
// field is the answer's error code, value and the words or bytes it
// filled.
TapeRt(a: &Arena, io: Io, p: str): str from a {
    var s := "rt"
    ho := core.IoOpen(io, p, 2)
    if ho.err == core.SysErr.None {
        h := Trunc[uint](ho.value)
        ds := rt.FileDataSync(io, h)
        sk := rt.FileSeek(io, h, 0, 2)
        _ = core.IoClose(io, h)
        s = a.Fmt("{s} datasync={e} seek={k},{v}", (s, e: u32(ds.err), k: u32(sk.err), v: sk.value)).Must()
    } else {
        s = a.Fmt("{s} open={e}", (s, e: u32(ho.err))).Must()
    }
    it := rt.IsTerminal(io, 1)
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    ts := rt.TermSize(io, 1, mut w)
    s = a.Fmt("{s} isterm={e},{v} termsize={f},{c},{r}", (s, e: u32(it.err), v: it.value, f: u32(ts.err), c: w.a, r: w.b)).Must()
    var tz: [432]u8 = [0; 432]
    z := rt.TimeZone(io, 0, mut tz[..])
    var sum: u64 = 0
    for i in 0..tz.Len() {
        sum = sum *% 31 +% u64(tz[i])
    }
    return a.Fmt("{s} tz={e},{v},{sum}", (s, e: u32(z.err), v: z.value, sum)).Must()
}

// TapeNet listens with options (IoOpListen), sets and reads back no_delay
// on a dialled socket (IoOpSockOpt, both ways: an accept's own no_delay
// discards its answer), dials, accepts, half-closes the dialled stream
// (IoOpShutdown) and reads the end of the stream on the accepted one.
TapeNet(a: &Arena, io: Io): str from a {
    var o := net.DefaultListenOptions
    o.backlog = 4
    o.no_delay = true
    l := net.ListenWith(io, "127.0.0.1:0", o) else e {
        return a.Fmt("listen={e}", (e)).Must()
    }
    var s := "net"
    addr := a.Fmt("127.0.0.1:{p}", (p: l.Port())).Must()
    hd := core.IoDial(io, addr)
    if hd.err == core.SysErr.None {
        h := Trunc[uint](hd.value)
        so := rt.SockOption(io, h, core.IoSockNoDelay, 1, 0)
        sg := rt.SockOptionGet(io, h, core.IoSockNoDelay)
        s = a.Fmt("{s} opt={e} get={f},{v}", (s, e: u32(so.err), f: u32(sg.err), v: sg.value)).Must()
        d0 := l.Accept(io) else e {
            _ = core.IoClose(io, h)
            l.Close()
            return a.Fmt("{s} accept0={e}", (s, e)).Must()
        }
        d0.Close()
        _ = core.IoClose(io, h)
    } else {
        s = a.Fmt("{s} dial0={e}", (s, e: u32(hd.err))).Must()
    }
    c := net.Dial(io, addr) else e {
        l.Close()
        return a.Fmt("{s} dial={e}", (s, e)).Must()
    }
    d := l.Accept(io) else e {
        c.Close()
        l.Close()
        return a.Fmt("{s} accept={e}", (s, e)).Must()
    }
    c.WriteAll(io, "hi".AsBytes()) else e {
        s = a.Fmt("{s} write={e}", (s, e)).Must()
    }
    c.Shutdown(io, net.Shutdown.Write) else e {
        s = a.Fmt("{s} shutdown={e}", (s, e)).Must()
    }
    var buf: [16]u8 = [0; 16]
    var got := 0
    for {
        n := d.Read(io, mut buf[got..]) else e {
            s = a.Fmt("{s} read={e}", (s, e)).Must()
            break
        }
        if n == 0 {
            break
        }
        got += n
    }
    d.Close()
    c.Close()
    l.Close()
    return a.Fmt("{s} got={got}", (s, got)).Must()
}

test "SIM-Tape-CallFs the file system's call ops recorded over the native Io replay with the same answers and no OS behind the replay" (sys: &System) {
    // On Linux under TempDir, not the working directory, which the harness
    // mounts over 9p for Linux blocks (as tests_edges.mk's tree).
    t := fs.TempDir(sys.arena, sys.io) else {
        Assert(false)
        return
    }
    root := if Target.os == core.Os.Linux { sys.arena.Fmt("{t}/mk_tape_callfs", (t)).Must() } else { "mk_tape_callfs" }
    fs.RemoveAll(sys.io, root) else {
    }
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    s1 := TapeFs(sys.arena, sys.heap, rec.Io(), root)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    // Every op answered as natively: none Unsupported, the tree listed and
    // removed, and Stat after the removal NotFound.
    Assert(!s1.Has("Unsupported"))
    Assert(s1.Has(" listed="))
    Assert(!s1.Has("removeall="))
    Assert(s1.EndsWith(" stat=NotFound"))
    Assert(s1.Has("stat=file,5,"))
    Assert(s1.Has(",1700000000000000000"))
    Assert(s1.Has("copied=5"))
    Assert(s1.Has("end=5 size=3"))
    if !s1.Has("symlink=") {
        Assert(s1.Has("target=f stat=link,0,"))
    }
    // The tree is gone: a replay that reached the OS would not find it.
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    s2 := TapeFs(sys.arena, sys.heap, rp.Io(), root)
    Assert(s1 == s2)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

test "SIM-Tape-CallRt DataSync, Seek, IsTerminal, TermSize and TimeZone recorded over the native Io replay with the same results, words and bytes" (sys: &System) {
    p := "mk_tape_callrt"
    fs.WriteAll(sys.io, p, "hello".AsBytes()) else {
        Assert(false)
        return
    }
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    s1 := TapeRt(sys.arena, rec.Io(), p)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    fs.Remove(sys.io, p) else {
    }
    // Each op answered as the host does, not Unsupported (16), which a
    // tape that stopped forwarding it would give on both sides alike.
    Assert(s1.Has(" datasync=0 seek=0,5"))
    Assert(s1.Has(" isterm=0,"))
    Assert(!s1.Has(" termsize=16,"))
    if Target.os == core.Os.Windows {
        // The zone record is Windows' (Unsupported on Linux, as natively).
        Assert(s1.Has(" tz=0,"))
    } else {
        Assert(s1.Has(" tz=16,"))
    }
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    s2 := TapeRt(sys.arena, rp.Io(), p)
    Assert(s1 == s2)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

test "SIM-Tape-CallNet a listener with options, a socket option set and read back, and a half-close recorded over the native Io replay with the same answers" (sys: &System) {
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    s1 := TapeNet(sys.arena, rec.Io())
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    Assert(s1 == "net opt=0 get=0,1 got=2")
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    s2 := TapeNet(sys.arena, rp.Io())
    Assert(s1 == s2)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}
