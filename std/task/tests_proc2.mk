// Why: each block pins a process or directory bug that was found and fixed,
// so it cannot come back.
// What: the RT-Os-Proc-* blocks (SIGPIPE, descriptor limit, closed pipe, args,
// batch files, PATH entries), RT-Os-Dir-NoSearch and RT-Os-Spawn-ExitCancel.
// How: children are the system shell or this program re-executed; run by the
// scheduler harness at 1 to 8 carriers, and a one-target block returns at
// once on the other target.

import std.core
import std.raw
import std.sys

// PSame: a and b are the same bytes.
PSame(a: str, b: str): bool {
    x := a.AsBytes()
    y := b.AsBytes()
    if x.Len() != y.Len() {
        return false
    }
    for i in 0..x.Len() {
        if x[i] != y[i] {
            return false
        }
    }
    return true
}

// PExePath is sys.ExePath (the blocks' parameter is named sys).
PExePath(out: mut []u8): int {
    return sys.ExePath(out)
}

// PSh runs the shell command c with every stream null and waits: its exit
// code (Linux /bin/sh -c, Windows cmd.exe /c).
PSh(io: Io, c: str): i64 {
    var ch := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    r := PShell(io, c, mut ch)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    code := PWait(io, h)
    PClose(io, h)
    return code
}

test "RT-Os-Proc-SigPipe a child starts with SIGPIPE at its default, not the runtime's ignore" (sys: &System) {
    if PWin() {
        return
    }
    // The runtime ignores SIGPIPE (FaultHook); an ignored signal would
    // survive execve, and a non-interactive shell could not undo it.
    Assert(PSh(sys.io, "kill -PIPE $$; exit 0") == -13)
    // SigIgn in /proc: bit 12 (signal 13) clear in the child.
    Assert(PSh(sys.io, "m=$(sed -n 's/^SigIgn:[[:space:]]*//p' /proc/self/status); [ $(( 0x$m & 0x1000 )) -eq 0 ]") == 0)
}

test "RT-Os-Proc-FdLimit a child starts with the descriptor limit this process inherited, not the raised one" (sys: &System) {
    if PWin() {
        return
    }
    // PollInit raises this process's soft RLIMIT_NOFILE to its hard limit,
    // and a child must get back the soft limit this process started with.
    // The block starts itself again under a known soft limit, 512, set by
    // a shell in between, so the check needs nothing of whoever started the
    // test (mk test's runner is a Go program, which raises its own limit and
    // gives its children the original). In the child: the raise happened
    // (its own soft limit, read by its shell child from /proc, is not 512),
    // and its shell child starts with 512.
    if sys.args.Len() >= 3 && PSame(sys.args[2], "fdlimit-child") {
        if PSh(sys.io, "[ $(awk '/^Max open files/ {print $4}' /proc/$PPID/limits) != 512 ]") != 0 {
            PExit(8)
        }
        if PSh(sys.io, "[ $(ulimit -Sn) = 512 ]") != 0 {
            PExit(9)
        }
        PExit(0)
    }
    // The hard limit must be above 512, or the raise could not be seen.
    Assert(PSh(sys.io, "h=$(ulimit -Hn); [ \"$h\" = unlimited ] || [ \"$h\" -gt 512 ]") == 0)
    var self: [4096]u8 = [0; 4096]
    sn := PExePath(mut self[..])
    Assert(sn > 0)
    var me := ""
    unsafe "self holds sn bytes of the path" {
        me = raw.Str(raw.Data(self[..]), sn)
    }
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdInherit)
    var a: [5]str = ["/bin/sh", "-c", "ulimit -Sn 512 && exec \"$0\" \"$1\" fdlimit-child", me, sys.args[1]]
    r := NativeSpawn(sys.io, a[..], mut c)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    Assert(PWait(sys.io, h) == 0)
    PClose(sys.io, h)
}

// PWriteUntil writes 8 KiB at a time to h until a write fails or 1 MiB
// went: the failing write's error (None when none failed).
PWriteUntil(io: Io, h: uint): core.SysErr {
    var b: [8192]u8 = [120; 8192]
    for _ in 0..128 {
        r := NativeWrite(io, h, b[..], -1)
        if r.err != core.SysErr.None {
            return r.err
        }
    }
    return core.SysErr.None
}

test "RT-Os-Proc-PipeClosed a write to a child's stdin after the child exited is Closed on both targets" (sys: &System) {
    for _ in 0..3 {
        var c := PStdio(core.IoStdPipe, core.IoStdNull, core.IoStdNull)
        r := PShell(sys.io, "exit 0", mut c)
        Assert(r.err == core.SysErr.None && c.pipes[0] != 0)
        h := Trunc[uint](r.value)
        Assert(PWait(sys.io, h) == 0)
        Assert(PWriteUntil(sys.io, c.pipes[0]) == core.SysErr.Closed)
        PClose(sys.io, c.pipes[0])
        PClose(sys.io, h)
    }
    // A child that exits with input unread (Linux: ECONNRESET on the
    // parent's socket end, before any EPIPE) is Closed too.
    var c := PStdio(core.IoStdPipe, core.IoStdNull, core.IoStdNull)
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    if PWin() {
        r = PShell(sys.io, "ping -n 2 127.0.0.1 >nul & exit 0", mut c)
    } else {
        r = PShell(sys.io, "sleep 0.2; exit 0", mut c)
    }
    Assert(r.err == core.SysErr.None)
    var b: [100]u8 = [65; 100]
    w := NativeWrite(sys.io, c.pipes[0], b[..], -1)
    Assert(w.err == core.SysErr.None && w.value == 100)
    h := Trunc[uint](r.value)
    Assert(PWait(sys.io, h) == 0)
    Assert(PWriteUntil(sys.io, c.pipes[0]) == core.SysErr.Closed)
    PClose(sys.io, c.pipes[0])
    PClose(sys.io, h)
}

// The arguments the re-exec'd child must see: spaces, an
// empty one, quotes, backslash runs before a quote and at the end, a tab,
// non-ASCII, and cmd's specials (which CommandLineToArgvW passes as text).
const PArgCount: int = 12

PArg(i: int): str {
    var a: [12]str = ["a b", "", "q\"x", "tr\\", "\\\\\"", "tab\tx", "\u{FC} \u{E9}", "\\\\a\\\\\"b\\", "\"", "%PATH%",
        "^&|<>!", "  lead and trail  "]
    return a[i]
}

test "RT-Os-Proc-Args arguments round-tripped through a spawn of this program" (sys: &System) {
    if sys.args.Len() >= 3 && PSame(sys.args[2], "args-child") {
        // The child: every argument as the parent gave it.
        var ok := sys.args.Len() == 3 + PArgCount
        if ok {
            for i in 0..PArgCount {
                if !PSame(sys.args[3 + i], PArg(i)) {
                    ok = false
                }
            }
        }
        if ok {
            PExit(0)
        }
        PExit(9)
    }
    var a: [15]str = [""; 15]
    a[1] = sys.args[1]
    a[2] = "args-child"
    for i in 0..PArgCount {
        a[3 + i] = PArg(i)
    }
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdInherit)
    r := NativeSpawn(sys.io, a[..], mut c)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    Assert(PWait(sys.io, h) == 0)
    PClose(sys.io, h)
}

// PWriteFile writes s to a new file name.
PWriteFile(io: Io, name: str, s: str) {
    o := NativeOpen(io, name, 1)
    Assert(o.err == core.SysErr.None)
    h := Trunc[uint](o.value)
    w := NativeWrite(io, h, s.AsBytes(), 0)
    Assert(w.err == core.SysErr.None && w.value == i64(s.Len()))
    Assert(NativeClose(io, h).err == core.SysErr.None)
}

// PBat runs the batch file prog with one argument arg, stdout piped: the
// exit code, and stdout in out with its length.
PBat(io: Io, prog: str, arg: str, out: mut [512]u8): (code: i64, n: int) {
    var c := PStdio(core.IoStdNull, core.IoStdPipe, core.IoStdNull)
    var a: [2]str = [prog, arg]
    r := NativeSpawn(io, a[..], mut c)
    Assert(r.err == core.SysErr.None)
    n := PRead(io, c.pipes[1], mut out[..])
    h := Trunc[uint](r.value)
    code := PWait(io, h)
    PClose(io, c.pipes[1])
    PClose(io, h)
    return (code: code, n: n)
}

test "RT-Os-Proc-Bat a batch file's arguments cannot end cmd.exe's quoting" (sys: &System) {
    if !PWin() {
        return
    }
    // The script echoes %1 as cmd passed it (quoted where needed), so the
    // argument comes back whole and nothing it holds runs.
    PWriteFile(sys.io, "mkx-bat-t.bat", "@echo {%1}\r\n")
    var out: [512]u8 = [0; 512]
    // CVE-2024-24576's shape: a quote, then a command. An unescaped quote
    // would let cmd.exe run "exit 66".
    (code: c1, n: n1) := PBat(sys.io, ".\\mkx-bat-t.bat", "\"&exit 66", mut out)
    Assert(c1 == 0 && PHas(out[0..n1], "&exit 66"))
    // %VAR% is not expanded; ! is plain (delayed expansion off).
    (code: c2, n: n2) := PBat(sys.io, ".\\mkx-bat-t.bat", "%PATH%!x!", mut out)
    Assert(c2 == 0 && PHas(out[0..n2], "{\"%PATH%!x!\"}"))
    // The same file by an upper-case extension and a trailing dot (what
    // CreateProcessW judges is the full path, which drops the dot).
    (code: c3, n: n3) := PBat(sys.io, ".\\mkx-bat-t.BAT.", "a&b|c", mut out)
    Assert(c3 == 0 && PHas(out[0..n3], "{\"a&b|c\"}"))
    // A plain argument is passed as it is.
    (code: c4, n: n4) := PBat(sys.io, ".\\mkx-bat-t.bat", "plain-1", mut out)
    Assert(c4 == 0 && PHas(out[0..n4], "{plain-1}"))
    // A line break would cut cmd's line: refused, as Rust refuses it.
    var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    var bad: [2]str = [".\\mkx-bat-t.bat", "a\nb"]
    Assert(NativeSpawn(sys.io, bad[..], mut c).err == core.SysErr.InvalidPath)
    Assert(NativeRemove(sys.io, "mkx-bat-t.bat").err == core.SysErr.None)
}

test "RT-Os-Proc-PathDot an empty or relative PATH entry never finds a program in the current directory" (sys: &System) {
    if PWin() {
        return
    }
    if sys.args.Len() >= 3 && PSame(sys.args[2], "path-child") {
        // The child runs under the PATH env gave it: mkx-pathdot is in the
        // current directory only.
        var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        var a: [1]str = ["mkx-pathdot"]
        r := NativeSpawn(sys.io, a[..], mut c)
        if r.err == core.SysErr.NotFound {
            PExit(0)
        }
        PExit(9)
    }
    Assert(PSh(sys.io, "cp /bin/true ./mkx-pathdot && chmod +x ./mkx-pathdot") == 0)
    var self: [4096]u8 = [0; 4096]
    sn := PExePath(mut self[..])
    Assert(sn > 0)
    var me := ""
    unsafe "self holds sn bytes of the path" {
        me = raw.Str(raw.Data(self[..]), sn)
    }
    // A trailing colon (an empty entry), a relative entry, an empty PATH,
    // and no PATH at all.
    var paths: [3]str = ["PATH=/nonexistent:", "PATH=/nonexistent:.:bin", "PATH="]
    for k in 0..4 {
        var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdInherit)
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        if k < 3 {
            var a: [5]str = ["/usr/bin/env", paths[k], me, sys.args[1], "path-child"]
            r = NativeSpawn(sys.io, a[..], mut c)
        } else {
            var a: [6]str = ["/usr/bin/env", "-u", "PATH", me, sys.args[1], "path-child"]
            r = NativeSpawn(sys.io, a[..], mut c)
        }
        Assert(r.err == core.SysErr.None)
        h := Trunc[uint](r.value)
        Assert(PWait(sys.io, h) == 0)
        PClose(sys.io, h)
    }
    Assert(PSh(sys.io, "rm -f ./mkx-pathdot") == 0)
}

test "RT-Os-Dir-NoSearch a directory that can be read but not searched still lists its entries" (sys: &System) {
    if PWin() {
        return
    }
    d := "/tmp/mkx-nosearch"
    Assert(PSh(sys.io, "d=/tmp/mkx-nosearch; chmod 755 $d 2>/dev/null; rm -rf $d && mkdir $d && touch $d/a $d/b && mkdir $d/c && chmod 444 $d") == 0)
    o := NativeOpenDir(sys.io, d)
    Assert(o.err == core.SysErr.None)
    h := Trunc[uint](o.value)
    var buf: [256]u8 = [0; 256]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    var files := 0
    var dirs := 0
    for _ in 0..10 {
        r := NativeReadDir(sys.io, h, mut buf[..], mut st)
        Assert(r.err == core.SysErr.None)
        if r.value == 0 {
            break
        }
        if st.kind == core.IoKindFile {
            files += 1
        } else if st.kind == core.IoKindDir {
            dirs += 1
        }
    }
    Assert(files == 2 && dirs == 1)
    PClose(sys.io, h)
    Assert(PSh(sys.io, "chmod 755 /tmp/mkx-nosearch && rm -rf /tmp/mkx-nosearch") == 0)
}

// PWaitRace waits for h; got: 1 Canceled, 2 the exit code 7, 3 anything
// else.
PWaitRace(io: Io, h: uint, got: &raw.Atomic[u32]) {
    r := NativeProcWait(io, h)
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else if r.err == core.SysErr.None && r.value == 7 {
        got.Store(2)
    } else {
        got.Store(3)
    }
}

// PWatch fails the block when its rounds stop moving. progress is the round
// times 4 plus its phase (1 spawned, 2 its scope entered, 3 joined, 4 the
// second wait answered; a 4 that stays is the PClose or the next round's
// spawn), read once a second; 20 reads with no change print
// the stuck value and fail, so a hang says where it is instead of ending as
// the harness's 60 s child timeout (status -1), which a slow but moving run
// also gives. It ends when its scope is cancelled.
PWatch(io: Io, progress: &raw.Atomic[u32]) {
    var last := progress.Load()
    var still := 0
    for {
        r := NativeSleep(io, 1_000_000_000)
        if r.err != core.SysErr.None {
            return
        }
        now := progress.Load()
        if now != last {
            last = now
            still = 0
        } else {
            still += 1
            if still >= 20 {
                FNote("exit-cancel stuck at round*4+phase", i64(now))
                Assert(false)
            }
        }
    }
}

test "RT-Os-Spawn-ExitCancel a child's exit races a cancel of its procwait, 100 rounds" (sys: &System) {
    // The cancel lands after a delay drawn from 0 to twice the time a child
    // takes to exit (measured first), so across rounds it comes before the
    // exit, as the exit's completion is in flight (Windows: ProcFired's Cas
    // against ProcCancel's; Linux: the pidfd's edge against the poller's
    // cancel), and after it. Each wait answers Canceled or the exit code,
    // never anything else and never hangs; after a Canceled, the next wait
    // gives the code.
    // The block's time is bounded: if every round's delay scaled one
    // measured lifetime, a first child slowed by the parallel suite (alone
    // the block takes 2 to 3 s) would make each of the 100 rounds sleep up
    // to twice that. The lifetime is the shortest of three children, at
    // most 50 ms; the rounds stop after 30 s once 20 have run; and PWatch
    // turns a stall into a failure that names its round and phase. Measured
    // (2026-11-09, Windows, a development desktop in use and the std.task suite running
    // beside it, 90 runs at the default, 2 and 8 carriers, with per-round
    // timing and the watchdog at 7 s): no stall; a child that takes 7 to
    // 16 ms alone took up to 1.36 s to exit in stretches of consecutive
    // rounds, so rounds that slept on one slow first child would each wait
    // about a second.
    var life: i64 = 0
    for k in 0..3 {
        var c0 := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        t0 := NativeNow(sys.io).value
        r0 := PShell(sys.io, "exit 7", mut c0)
        Assert(r0.err == core.SysErr.None)
        Assert(PWait(sys.io, Trunc[uint](r0.value)) == 7)
        d := NativeNow(sys.io).value - t0
        PClose(sys.io, Trunc[uint](r0.value))
        if k == 0 || d < life {
            life = d
        }
    }
    FNote("exit-cancel child lifetime ns", life)
    if life > 50_000_000 {
        life = 50_000_000
    }
    var rnd: [100]u8 = [0; 100]
    _ = NativeRandom(sys.io, mut rnd[..])
    var cancelled := 0
    var rounds := 0
    progress := raw.Atomic[u32].New(0)
    start := NativeNow(sys.io).value
    scope w := sys.io.Scope() {
        w.go PWatch(sys.io, &progress)
        for round in 0..100 {
            if round >= 20 && NativeNow(sys.io).value - start > 30_000_000_000 {
                break
            }
            var c := PStdio(core.IoStdNull, core.IoStdNull, core.IoStdNull)
            r := PShell(sys.io, "exit 7", mut c)
            Assert(r.err == core.SysErr.None)
            h := Trunc[uint](r.value)
            got := raw.Atomic[u32].New(0)
            progress.Store(Trunc[u32](round * 4 + 1))
            scope s := sys.io.Scope() {
                progress.Store(Trunc[u32](round * 4 + 2))
                s.go PWaitRace(sys.io, h, &got)
                _ = NativeSleep(sys.io, life * i64(rnd[round]) / 128)
                s.Cancel()
            }
            progress.Store(Trunc[u32](round * 4 + 3))
            g := got.Load()
            Assert(g == 1 || g == 2)
            if g == 1 {
                cancelled += 1
            }
            Assert(PWait(sys.io, h) == 7)
            progress.Store(Trunc[u32](round * 4 + 4))
            PClose(sys.io, h)
            rounds += 1
        }
        w.Cancel()
    }
    // How many rounds ran and how many the cancel won (stderr, shown with
    // -v).
    FNote("exit-cancel rounds run", i64(rounds))
    FNote("exit-cancel rounds cancelled", i64(cancelled))
}
