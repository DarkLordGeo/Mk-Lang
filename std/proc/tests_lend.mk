// Why: lending a proc.Pipe end to a child while a parent task waits on it
// must lose no byte and leave the parent's own ends working.
// What: three test blocks of lent ends, and SpReadCount.
// How: a child is this test executable run again with SpChild's modes
// (tests_spawn.mk), and the blocks run as tests_spawn.mk's are.

import std.raw
import std.time

// SpReadCount reads r until the end, or until want bytes when want is
// above 0, adding each Read's count to n; bad counts a failed Read and,
// when pattern, a Read whose bytes are off gen's pattern.
SpReadCount(io: Io, r: &PipeReader, want: int, pattern: bool, n: &raw.Atomic[u32], bad: &raw.Atomic[u32]) {
    var b: [4096]u8 = [0; 4096]
    var off := 0
    for want == 0 || off < want {
        k := r.Read(io, mut b[..]) else {
            _ = bad.Add(1)
            return
        }
        if k == 0 {
            return
        }
        if pattern {
            for i in 0..k {
                if b[i] != PtByte(off + i) {
                    _ = bad.Add(1)
                    break
                }
            }
        }
        off += k
        _ = n.Add(Trunc[u32](k))
    }
}

test "Pipe: lend: a Read already waiting on the read end when the write end is lent to a gen child gets its 200,000 bytes, more than the pipe holds; then the parent's own ends still write, read and end" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    (r, w) := Pipe(io).Must()
    n := raw.Atomic[u32].New(0)
    bad := raw.Atomic[u32].New(0)
    var code := -1
    scope g := io.Scope() {
        g.go SpReadCount(io, &r, 200000, true, &n, &bad)
        // The Read waits on the poller (Linux) before the lend: the
        // child's end is a blocking end of the same pipe, the parent's
        // stays as it was.
        io.Sleep(time.Millis(100)) else {}
        var a: [6]str = [sys.args[0], sys.args[1], "gen", "200000", "0", "0"]
        var c := DefaultCommand
        c.args = a[..]
        c.stdout = Stdio.Writer(w: &w)
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, stdout, stderr)
        code = SpCode(child.Wait(io).Must())
    }
    Assert(code == 0)
    Assert(n.Load() == 200000 && bad.Load() == 0)
    w.WriteAll(io, "after".AsBytes()).Must()
    var b: [8]u8 = [0; 8]
    k := r.Read(io, mut b[..]).Must()
    Assert(k == 5 && SpText(b[0..k], "after"))
    w.Close()
    Assert(r.Read(io, mut b[..]).Must() == 0)
    r.Close()
}

test "Pipe: lend with descriptor 0 free (Linux): the end opened again for the child takes descriptor 0 and is still closed after the Spawn, so the parent's Read sees the end once its own write end closes" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    if SpWin() {
        return
    }
    io := sys.io
    (r, w) := Pipe(io).Must()
    // Descriptor 0 free: under epoll PipeLend's open of /proc/self/fd/N
    // takes the lowest free descriptor, 0. SpawnJob once marked an end
    // it made by a non-zero value, so a descriptor 0 stayed open in the
    // parent, a write end of the pipe, and the Read below never ended.
    SpCloseFd0()
    var a: [6]str = [sys.args[0], sys.args[1], "gen", "1000", "0", "0"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdin = Stdio.Null
    c.stdout = Stdio.Writer(w: &w)
    c.stderr = Stdio.Null
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(stdin, stdout, stderr)
    Assert(SpCode(child.Wait(io).Must()) == 0)
    w.Close()
    var b: [4096]u8 = [0; 4096]
    var total := 0
    var end := false
    scope d := io.Scope() {
        d.CancelAfter(time.Seconds(10))
        for !end {
            k := r.Read(io, mut b[..]) else {
                break
            }
            if k == 0 {
                end = true
            }
            total += k
        }
    }
    r.Close()
    Assert(end)
    Assert(total == 1000)
}

test "Pipe: lend: a read end lent to a cat child while a task of the parent waits in Read on it: each of 100,000 bytes written reaches the waiting Read or the child, once, and both see the end" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    (r, w) := Pipe(io).Must()
    mine := raw.Atomic[u32].New(0)
    echoed := raw.Atomic[u32].New(0)
    bad := raw.Atomic[u32].New(0)
    var code := -1
    scope g := io.Scope() {
        g.go SpReadCount(io, &r, 0, false, &mine, &bad)
        io.Sleep(time.Millis(100)) else {}
        var a: [3]str = [sys.args[0], sys.args[1], "cat"]
        var c := DefaultCommand
        c.args = a[..]
        c.stdin = Stdio.Reader(r: &r)
        c.stdout = Stdio.Pipe
        c.stderr = Stdio.Null
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, none, stderr)
        out := stdout.Must()
        scope h := io.Scope() {
            h.go SpReadCount(io, &out, 0, false, &echoed, &bad)
            data := sys.arena.Slice[u8](100000).Must()
            PtPattern(mut data[..], 0)
            w.WriteAll(io, data[..]).Must()
            w.Close()
        }
        out.Close()
        code = SpCode(child.Wait(io).Must())
    }
    Assert(code == 0 && bad.Load() == 0)
    Assert(mine.Load() + echoed.Load() == 100000)
    r.Close()
}
