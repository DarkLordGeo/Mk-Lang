// Why: another task or a shell can set the terminal's mode while a Read
// waits, and that must neither end the wait nor fail it.
// What: pseudo-terminal tests of a Read whose wait sees mode changes.
// How: a child sets the mode during its own Read and checks it still reads
// keys and is still cut by its deadline; Linux only.

// A terminal mode set during a Read's
// wait does not end the wait. On io_uring the kernel answered such a wait
// with EINVAL (TCSETS wakes the tty's wait queues with no event mask, and
// the one-shot POLL_ADD's re-arm fails); std.term used to count that as a
// spurious wake, three in a row giving Device(22). The poller now submits
// the wait again (std/sys/linux/poll_uring.mk). Linux only: the Windows
// console's mode changes do not touch its wait. The child also says which
// backend it runs on, so a run with MK_IO set checks that it got it.

import std.core
import std.fs
import std.proc
import std.raw
import std.rt

// TmBackend is the poller this process runs on, from its descriptors'
// links: "uring" when one is an io_uring instance, else "epoll" when one
// is an epoll instance, else "none".
TmBackend(a: &Arena, io: Io): str {
    var epoll := false
    for fd in 3..256 {
        p := a.Fmt("/proc/self/fd/{fd}", (fd)).Must()
        l := fs.ReadLink(a, io, p) else { "" }
        if l == "anon_inode:[io_uring]" {
            return "uring"
        }
        if l == "anon_inode:[eventpoll]" {
            epoll = true
        }
    }
    return if epoll { "epoll" } else { "none" }
}

// TmModeFlips sets the terminal (input h, output out) to line mode and
// back to raw mode 20 times, 15 ms apart, starting 150 ms from now (so
// inside the Read's wait), and then writes "flipped 40" to path. failed
// is set when the terminal refuses a mode.
TmModeFlips(io: Io, h: uint, out: uint, path: str, failed: &raw.Atomic[u32]) {
    io.Sleep(TmMs(150)) else {
        failed.Store(1)
        return
    }
    var old: [64]u8 = [0; 64]
    var none_: [0]u8 = []
    for _ in 0..20 {
        TmLineMode(io, h, out, failed)
        io.Sleep(TmMs(15)) else {
            failed.Store(1)
            return
        }
        if rt.TermMode(io, h, out, core.IoTermRaw, none_[..], mut old[..]).err != core.SysErr.None {
            failed.Store(1)
        }
        io.Sleep(TmMs(15)) else {
            failed.Store(1)
            return
        }
    }
    fs.WriteAll(io, path, "flipped 40\n".AsBytes()) else {
        failed.Store(1)
    }
}

// TmModeWaitChild is the child of mode "modewait", on t in raw mode; it
// never returns. It writes its backend to path.io, logs "reading", then
// reads keys, logging each, until q, while a second task sets the mode 40
// times during the first wait (TmModeFlips, which writes path.flips).
// Exit codes: 81 a mode refused, 74 a transcript, TmReadOrExit's (122:
// the wait answered EINVAL, Device(22)).
TmModeWaitChild(sys: &System, t: Term, path: str): never {
    io := sys.io
    var term := t
    io_path := sys.arena.Fmt("{path}.io", (path)).Must()
    flips_path := sys.arena.Fmt("{path}.flips", (path)).Must()
    fs.WriteAll(io, io_path, TmBackend(sys.arena, io).AsBytes()) else { proc.Exit(74) }
    var log := Text.New(sys.arena)
    log.Push("reading\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    h := term.input
    out := term.out
    var failed := raw.Atomic[u32].New(0)
    scope s := io.Scope() {
        s.go TmModeFlips(io, h, out, flips_path, &failed)
        for {
            e := TmReadOrExit(mut term, sys.arena, io)
            if TmIsStartSize(e) {
                continue
            }
            log.Push(TmDescribe(sys.arena, e)).Must()
            log.Push("\n").Must()
            fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
            if TmIsQuit(e) {
                break
            }
        }
    }
    if failed.Load() != 0 {
        proc.Exit(81)
    }
    term.Close(io)
    proc.Exit(0)
}

// TmModeCancelChild is the child of mode "modecancel", on t in raw mode;
// it never returns. It writes its backend to path.io and logs "reading";
// then, while TmModeFlips sets the mode 40 times, it reads under a 300 ms
// deadline, which lands among the flips, and logs "cut true" when the
// Read was Canceled within 2 s of its start ("cut false" when later),
// then "done" once the flips ended. A key or a size cannot come: none is
// typed. Exit codes: 81 a mode refused, 74 a transcript, 92 an event
// came, 73 another Read failure (TmReadCut's).
TmModeCancelChild(sys: &System, t: Term, path: str): never {
    io := sys.io
    var term := t
    io_path := sys.arena.Fmt("{path}.io", (path)).Must()
    flips_path := sys.arena.Fmt("{path}.flips", (path)).Must()
    fs.WriteAll(io, io_path, TmBackend(sys.arena, io).AsBytes()) else { proc.Exit(74) }
    var log := Text.New(sys.arena)
    log.Push("reading\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    h := term.input
    out := term.out
    var failed := raw.Atomic[u32].New(0)
    var cuts := 0
    var soon := false
    scope s := io.Scope() {
        s.go TmModeFlips(io, h, out, flips_path, &failed)
        t0 := Now(io)
        var got: ?Event = none
        scope c := io.Scope() {
            c.CancelAfter(TmMs(300))
            got = TmReadCut(mut term, sys.arena, io, mut cuts)
        }
        if got.IsSome() {
            proc.Exit(92)
        }
        soon = Now(io) - t0 < 2_000_000_000
    }
    if failed.Load() != 0 {
        proc.Exit(81)
    }
    log.Push(sys.arena.Fmt("cut {c}\n", (c: cuts == 1 && soon)).Must()).Must()
    log.Push("done\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    term.Close(io)
    proc.Exit(0)
}

test "on Linux, a Read whose wait sees the terminal's mode set during it is still cut by its deadline, within 2 s (a resubmitted wait keeps its cancel)" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        return
    }
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-modecancel-{tag}.txt", (dir, tag)).Must()
    io_path := sys.arena.Fmt("{path}.io", (path)).Must()
    flips_path := sys.arena.Fmt("{path}.flips", (path)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], "modecancel", path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var ready := raw.Atomic[u32].New(0)
    var done := false
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        done = TmAwait(sys, path, "reading\ncut true\ndone\n")
        if !done {
            child.Kill() else {}
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    fs.Remove(io, io_path) else {}
    fs.Remove(io, flips_path) else {}
    Assert(code != 81 && code != 74 && code != 92 && code != 73)
    Assert(TmSame(sys, text, "reading\ncut true\ndone\n"))
    Assert(done && code == 0)
}

test "on Linux, a Read whose wait sees the terminal's mode set 40 times by another task goes on waiting and reads the next keys alone" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        // The console's mode changes do not touch its wait.
        return
    }
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-modewait-{tag}.txt", (dir, tag)).Must()
    io_path := sys.arena.Fmt("{path}.io", (path)).Must()
    flips_path := sys.arena.Fmt("{path}.flips", (path)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], "modewait", path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var ready := raw.Atomic[u32].New(0)
    var reading := false
    var flipped := false
    var quit := false
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        reading = TmAwait(sys, path, "reading\n")
        flipped = TmAwait(sys, flips_path, "flipped 40\n")
        // The last mode set was raw: "a" arrives alone.
        p.WriteAll(io, "a".AsBytes()).Must()
        _ = TmAwait(sys, path, "reading\nkey Char a\n")
        p.WriteAll(io, "q".AsBytes()).Must()
        quit = TmAwait(sys, path, "reading\nkey Char a\nkey Char q\n")
        if !quit {
            child.Kill() else {}
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    backend := fs.ReadText(sys.arena, io, io_path) else { "" }
    fs.Remove(io, path) else {}
    fs.Remove(io, io_path) else {}
    fs.Remove(io, flips_path) else {}
    want := sys.Env("MK_IO") else { "" }
    // The child's own failures first: 81 a mode refused, 74 a transcript;
    // 122 the wait answered EINVAL (Device(22)); 90 to 149 its other Read
    // failures (TmReadOrExit).
    Assert(code != 81 && code != 74)
    Assert(code != 122)
    Assert(code < 90 || code >= 150)
    Assert(reading && flipped)
    // MK_IO=epoll must give epoll, and MK_IO=uring io_uring (a kernel that
    // refuses io_uring fails this leg rather than passing it on epoll).
    if want == "epoll" {
        Assert(backend == "epoll")
    } else if want == "uring" {
        Assert(backend == "uring")
    }
    Assert(TmSame(sys, text, "reading\nkey Char a\nkey Char q\n"))
    Assert(quit && code == 0)
}
