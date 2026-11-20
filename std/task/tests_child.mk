// Why: child processes and terminals behave differently on each OS, and
// std's process and terminal calls must hide that.
// What: the RT8-Child-* and RT8-Con-Console blocks for std/rt/proc.mk and
// each target's sys/child.mk, with helpers CSpec, CSh and CConsoleChild.
// How: they cover pipes, a lent pipe end, the environment modes, the
// directory, stderr joined to stdout, a group killed when its leader's wait
// answers, signals, detach and its reaping, the pseudo-terminal and terminal
// modes. The child is the system shell (cmd.exe /c on Windows, /bin/sh -c on
// Linux). Run by the scheduler harness, and on both targets at 1 and 16
// carriers by std/rt_deadline_test.go (its RT8- blocks).

import std.core
import std.rt
import std.sys

// CSpec is a spawn request with the three modes given and nothing else.
CSpec(i: u32, o: u32, e: u32): core.IoSpawnReq {
    return core.IoSpawnReq(stdio: [i, o, e], src: [0; 3], env_mode: core.IoEnvInherit, group: false, pty: 0,
        pipes: [0; 3], pid: 0, job: 0)
}

// CSh starts the shell with win's command on Windows and lin's on Linux.
CSh(io: Io, win: str, lin: str, env: []str, dir: str, s: mut core.IoSpawnReq): core.IoResult {
    if PWin() {
        var a: [3]str = ["cmd.exe", "/c", win]
        return rt.Spawn(io, a[..], env, dir, s)
    }
    var a: [3]str = ["/bin/sh", "-c", lin]
    return rt.Spawn(io, a[..], env, dir, s)
}

// CLong starts a child that runs for 100 s: ping itself on Windows (through
// cmd.exe a kill would end only the shell and orphan ping), sleep on Linux.
CLong(io: Io, s: mut core.IoSpawnReq): core.IoResult {
    var noenv: [1]str = [""]
    if PWin() {
        var a: [4]str = ["ping.exe", "-n", "100", "127.0.0.1"]
        return rt.Spawn(io, a[..], noenv[0..0], "", s)
    }
    var a: [3]str = ["/bin/sh", "-c", "sleep 100"]
    return rt.Spawn(io, a[..], noenv[0..0], "", s)
}

// CHas: b holds s.
CHas(b: []u8, s: str): bool {
    n := s.Len()
    if n > b.Len() {
        return false
    }
    for i in 0..(b.Len() - n + 1) {
        var same := true
        for j in 0..n {
            if b[i + j] != s.AsBytes()[j] {
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

// COut runs the shell with stdout on a pipe and stderr joined to it, reads
// it to its end into buf, and returns the bytes read; the exit code must
// be code.
COut(io: Io, win: str, lin: str, env: []str, mode: u32, dir: str, buf: mut []u8, code: i64): int {
    var s := CSpec(core.IoStdNull, core.IoStdPipe, core.IoStdJoin)
    s.env_mode = mode
    r := CSh(io, win, lin, env, dir, mut s)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    n := PRead(io, s.pipes[1], buf)
    Assert(rt.ChildWait(io, h, s.job).value == code)
    PClose(io, s.pipes[1])
    PClose(io, h)
    return n
}

test "RT8-Child-Pipe a pipe carries bytes, reads 0 after its writer closes, and a write is Closed after its reader closes" (sys: &System) {
    io := sys.io
    var e: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut e).err == core.SysErr.None)
    Assert(NativeWrite(io, e[1], "hello".AsBytes(), -1).value == 5)
    var b: [16]u8 = [0; 16]
    Assert(NativeRead(io, e[0], mut b[..], -1).value == 5)
    Assert(b[0] == 104 && b[4] == 111)
    PClose(io, e[1])
    r := NativeRead(io, e[0], mut b[..], -1)
    Assert(r.err == core.SysErr.None && r.value == 0)
    PClose(io, e[0])
    Assert(rt.PipeOpen(io, mut e).err == core.SysErr.None)
    PClose(io, e[0])
    Assert(NativeWrite(io, e[1], "x".AsBytes(), -1).err == core.SysErr.Closed)
    PClose(io, e[1])
}

test "RT8-Child-Lend a pipe end lent as a child's stdout: the parent reads what it wrote, and the end after it exits" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    var e: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut e).err == core.SysErr.None)
    var s := CSpec(core.IoStdNull, core.IoStdHandle, core.IoStdInherit)
    s.src[1] = e[1]
    r := CSh(io, "echo lent", "echo lent", noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    Assert(r.os == s.pid && s.pid > 0)
    PClose(io, e[1])
    var b: [64]u8 = [0; 64]
    n := PRead(io, e[0], mut b[..])
    Assert(CHas(b[0..n], "lent"))
    h := Trunc[uint](r.value)
    Assert(rt.ChildWait(io, h, s.job).value == 0)
    PClose(io, h)
    PClose(io, e[0])
    // A lent stream with no handle is refused before any child.
    var bad := CSpec(core.IoStdNull, core.IoStdHandle, core.IoStdInherit)
    Assert(CSh(io, "echo x", "echo x", noenv[0..0], "", mut bad).err == core.SysErr.Other)
}

test "RT8-Child-Env the environment inherited, extended (an override ignores case on Windows) and replaced" (sys: &System) {
    io := sys.io
    var b: [512]u8 = [0; 512]
    var add: [1]str = ["MK_RT8_A=one"]
    var n := COut(io, "echo %MK_RT8_A%", "echo $MK_RT8_A", add[..], core.IoEnvExtend, "", mut b[..], 0)
    Assert(CHas(b[0..n], "one"))
    var path: [1]str = ["PATH=/rt8-none"]
    if PWin() {
        path[0] = "Path=C:\\rt8none"
    }
    n = COut(io, "echo %PATH%", "echo $PATH", path[..], core.IoEnvExtend, "", mut b[..], 0)
    Assert(CHas(b[0..n], "rt8") && !CHas(b[0..n], ";"))
    if PWin() {
        var only: [2]str = ["MK_RT8_B=two", "SystemRoot=C:\\Windows"]
        n = COut(io, "echo %MK_RT8_B%:%USERNAME%", "", only[..], core.IoEnvReplace, "", mut b[..], 0)
        Assert(CHas(b[0..n], "two:%USERNAME%"))
    } else {
        var only: [1]str = ["MK_RT8_B=two"]
        n = COut(io, "", "echo \"$MK_RT8_B:$HOME:\"", only[..], core.IoEnvReplace, "", mut b[..], 0)
        Assert(CHas(b[0..n], "two::"))
    }
    // Inherited: this process's own variable reaches the child.
    var noenv: [1]str = [""]
    n = COut(io, "echo %PATH%", "echo $PATH", noenv[0..0], core.IoEnvInherit, "", mut b[..], 0)
    Assert(n > 4 && !CHas(b[0..n], "rt8"))
    // A NUL in an entry is refused.
    var nul: [1]str = ["MK_RT8_C=a\u{0}b"]
    var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    s.env_mode = core.IoEnvExtend
    Assert(CSh(io, "echo x", "echo x", nul[..], "", mut s).err == core.SysErr.InvalidPath)
}

test "RT8-Child-Dir the child runs in the directory given; a missing one fails with no child" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    var b: [256]u8 = [0; 256]
    root := if PWin() { "C:\\" } else { "/" }
    n := COut(io, "cd", "pwd", noenv[0..0], core.IoEnvInherit, root, mut b[..], 0)
    Assert(n >= root.Len() && b[0] == root.AsBytes()[0])
    if PWin() {
        Assert(b[1] == 58 && b[2] == 92 && (b[3] == 13 || b[3] == 10))
    } else {
        Assert(b[1] == 10)
    }
    var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    missing := if PWin() { "C:\\rt8-no-such-dir" } else { "/rt8-no-such-dir" }
    r := CSh(io, "cd", "pwd", noenv[0..0], missing, mut s)
    Assert(r.err != core.SysErr.None)
    if !PWin() {
        Assert(r.err == core.SysErr.NotFound)
    }
}

test "RT8-Child-Join stderr joined to stdout: both streams arrive on the one pipe" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    var b: [256]u8 = [0; 256]
    n := COut(io, "echo out& echo err 1>&2", "echo out; echo err 1>&2", noenv[0..0], core.IoEnvInherit, "", mut b[..], 0)
    Assert(CHas(b[0..n], "out") && CHas(b[0..n], "err"))
}

test "RT8-Child-Group a group leader's wait kills what remains of its group: a grandchild holding the pipe is gone" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    var s := CSpec(core.IoStdNull, core.IoStdPipe, core.IoStdInherit)
    s.group = true
    // The grandchild holds stdout for 100 s, longer than the harness's
    // watchdog; only the group's kill ends the stream in time.
    r := CSh(io, "start /b ping -n 100 127.0.0.1 & exit 3", "sleep 100 & exit 3", noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    if PWin() {
        Assert(s.job != 0)
    } else {
        Assert(s.job == 0)
    }
    Assert(rt.ChildWait(io, h, s.job).value == 3)
    var b: [4096]u8 = [0; 4096]
    _ = PRead(io, s.pipes[1], mut b[..])
    // A signal after the wait answered reaches nothing and succeeds.
    Assert(rt.ChildSignal(io, h, true, s.pid, 0, 9).err == core.SysErr.None)
    PClose(io, s.pipes[1])
    PClose(io, h)
}

test "RT8-Child-Signal kill and terminate a child and a group; Windows delivers only a kill" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    for group in 0..2 {
        var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        s.group = group == 1
        r := CLong(io, mut s)
        Assert(r.err == core.SysErr.None)
        h := Trunc[uint](r.value)
        if PWin() {
            Assert(rt.ChildSignal(io, h, s.group, s.pid, s.job, 15).err == core.SysErr.Unsupported)
            Assert(rt.ChildSignal(io, h, s.group, s.pid, s.job, 9).err == core.SysErr.None)
            Assert(rt.ChildWait(io, h, s.job).value == 1)
        } else {
            sig: u32 = if group == 1 { 9 } else { 15 }
            Assert(rt.ChildSignal(io, h, s.group, s.pid, s.job, sig).err == core.SysErr.None)
            v := rt.ChildWait(io, h, s.job).value
            // sh -c with one command may exec it, so the signal ends sleep
            // or the shell; either way the leader ends by it.
            Assert(v == -i64(sig))
        }
        Assert(rt.ChildSignal(io, h, s.group, s.pid, s.job, 9).err == core.SysErr.None)
        PClose(io, h)
    }
    var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    r := CSh(io, "exit 0", "exit 0", noenv[0..0], "", mut s)
    h := Trunc[uint](r.value)
    Assert(rt.ChildSignal(io, h, false, s.pid, 0, 0).err == core.SysErr.Other || PWin())
    Assert(rt.ChildWait(io, h, 0).value == 0)
    PClose(io, h)
}

test "RT8-Child-KillTwice a second kill sent while the first one's termination runs succeeds, and the wait answers the kill" (sys: &System) {
    io := sys.io
    // Windows' TerminateProcess fails with 5 on a process whose termination
    // is under way but not yet signalled; 20 rounds reach that window.
    for _ in 0..20 {
        var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        r := CLong(io, mut s)
        Assert(r.err == core.SysErr.None)
        h := Trunc[uint](r.value)
        Assert(rt.ChildSignal(io, h, false, s.pid, s.job, 9).err == core.SysErr.None)
        Assert(rt.ChildSignal(io, h, false, s.pid, s.job, 9).err == core.SysErr.None)
        Assert(rt.ChildSignal(io, h, false, s.pid, s.job, 9).err == core.SysErr.None)
        v := rt.ChildWait(io, h, s.job).value
        Assert(v == 1 || (!PWin() && v == -9))
        Assert(rt.ChildSignal(io, h, false, s.pid, s.job, 9).err == core.SysErr.None)
        PClose(io, h)
    }
}

test "RT8-Child-Detach a detached child runs on and is reaped by the next spawn after it ends" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    for group in 0..2 {
        var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
        s.group = group == 1
        r := CSh(io, "exit 0", "exit 0", noenv[0..0], "", mut s)
        Assert(r.err == core.SysErr.None)
        Assert(rt.ChildDetach(io, Trunc[uint](r.value), s.job).err == core.SysErr.None)
    }
    io.Sleep(core.DurationOf(200 * DlMs)) else {}
    var s := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    r := CSh(io, "exit 0", "exit 0", noenv[0..0], "", mut s)
    h := Trunc[uint](r.value)
    Assert(rt.DetachedLen() == 0)
    Assert(rt.ChildWait(io, h, 0).value == 0)
    PClose(io, h)
}

// CPtyRead reads p into buf until it holds want (or buf is full, or the
// end): the bytes read.
CPtyRead(io: Io, p: &core.IoPty, buf: mut []u8, want: str): int {
    var n := 0
    for n < buf.Len() && !CHas(buf[0..n], want) {
        r := rt.PtyRead(io, p, mut buf[n..])
        if r.err != core.SysErr.None || r.value == 0 {
            return n
        }
        n += Trunc[int](r.value)
    }
    return n
}

test "RT8-Child-Pty a child on a pseudo-terminal: its size, a resize, its output, and the terminal's modes" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    var p := core.IoPty(read: 0, write: 0, term: 0)
    Assert(rt.PtyOpen(io, 0, 24, mut p).err == core.SysErr.Other)
    Assert(rt.PtyOpen(io, 80, 24, mut p).err == core.SysErr.None)
    Assert(rt.PtyResize(io, &p, 100, 30).err == core.SysErr.None)
    var s := CSpec(core.IoStdInherit, core.IoStdInherit, core.IoStdInherit)
    s.pty = p.term
    r := CSh(io, "echo pty-ok", "stty size; echo pty-ok", noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    var b: [4096]u8 = [0; 4096]
    n := CPtyRead(io, &p, mut b[..], "pty-ok")
    Assert(CHas(b[0..n], "pty-ok"))
    if !PWin() {
        Assert(CHas(b[0..n], "30 100"))
    }
    Assert(rt.ChildWait(io, h, 0).value == 0)
    PClose(io, h)
    var old: [64]u8 = [0; 64]
    var again: [64]u8 = [0; 64]
    var none8: [1]u8 = [0]
    if PWin() {
        // The pseudo-console's pipes are not consoles.
        Assert(rt.TermMode(io, p.read, p.write, core.IoTermRaw, none8[0..0], mut old[..]).err != core.SysErr.None)
    } else {
        // The master's mode is its terminal's: raw, then the saved mode back.
        m := rt.TermMode(io, p.read, p.write, core.IoTermRaw, none8[0..0], mut old[..])
        Assert(m.err == core.SysErr.None && m.value == i64(TermModeLen()))
        Assert(rt.TermMode(io, p.read, p.write, core.IoTermSaved, old[0..TermModeLen()], mut again[..]).err == core.SysErr.None)
        Assert(rt.TermMode(io, p.read, p.write, core.IoTermNoEcho, none8[0..0], mut again[..]).err == core.SysErr.None)
        for i in 0..TermModeLen() {
            Assert(again[i] == old[i])
        }
        // IoTermGet reads the mode in force (echo off now) and sets nothing:
        // two reads agree, and differ from the mode saved first.
        var g1: [64]u8 = [0; 64]
        var g2: [64]u8 = [0; 64]
        Assert(rt.TermMode(io, p.read, p.write, core.IoTermGet, none8[0..0], mut g1[..]).value == i64(TermModeLen()))
        Assert(rt.TermMode(io, p.read, p.write, core.IoTermGet, none8[0..0], mut g2[..]).value == i64(TermModeLen()))
        var same := true
        var differs := false
        for i in 0..TermModeLen() {
            same = same && g1[i] == g2[i]
            differs = differs || g1[i] != old[i]
        }
        Assert(same && differs)
        Assert(rt.TermMode(io, p.read, p.write, core.IoTermSaved, old[0..3], mut again[..]).err == core.SysErr.Other)
    }
    Assert(rt.PtyClose(io, p).err == core.SysErr.None)
    // A pipe is not a terminal.
    var e: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut e).err == core.SysErr.None)
    Assert(rt.TermMode(io, e[0], e[0], core.IoTermRaw, none8[0..0], mut old[..]).err != core.SysErr.None)
    PClose(io, e[0])
    PClose(io, e[1])
}

test "RT8-Child-TermOpen the controlling terminal opens, or is NotFound when the process has none" (sys: &System) {
    io := sys.io
    var t: [2]uint = [0; 2]
    r := rt.TermOpen(io, mut t)
    Assert(r.err == core.SysErr.None || r.err == core.SysErr.NotFound)
    // Linux sets no mode here: a process outside its terminal's
    // foreground group that sets one is stopped by SIGTTOU (the harness
    // runs blocks so); the pty block tests the modes.
    if r.err == core.SysErr.None && PWin() {
        var old: [64]u8 = [0; 64]
        var cur: [64]u8 = [0; 64]
        var none8: [1]u8 = [0]
        // Read the mode by setting it to itself: nothing changes.
        m := rt.TermMode(io, t[0], t[1], core.IoTermNoEcho, none8[0..0], mut old[..])
        if m.err == core.SysErr.None {
            k := Trunc[int](m.value)
            Assert(rt.TermMode(io, t[0], t[1], core.IoTermSaved, old[0..k], mut cur[..]).err == core.SysErr.None)
        }
    }
    if r.err == core.SysErr.None {
        PClose(io, t[0])
        if t[1] != t[0] {
            PClose(io, t[1])
        }
    }
}

test "RT8-Child-Ready a terminal wait consumes nothing: a deadline ends it with Canceled, and input then ends it at once" (sys: &System) {
    io := sys.io
    var noenv: [1]str = [""]
    if !PWin() {
        // A pty's master: no output until the child writes.
        var p := core.IoPty(read: 0, write: 0, term: 0)
        Assert(rt.PtyOpen(io, 80, 24, mut p).err == core.SysErr.None)
        var s := CSpec(core.IoStdInherit, core.IoStdInherit, core.IoStdInherit)
        s.pty = p.term
        r := CSh(io, "", "sleep 0.3; echo rdy", noenv[0..0], "", mut s)
        Assert(r.err == core.SysErr.None)
        var w := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        scope d := io.Scope() {
            d.CancelAfter(core.DurationOf(50 * DlMs))
            w = rt.TermWait(io, p.read)
        }
        Assert(w.err == core.SysErr.Canceled)
        // Repeatable: the same wait again ends when the bytes come,
        // and the read that follows has every one of them.
        Assert(rt.TermWait(io, p.read).err == core.SysErr.None)
        // What is there now reads without a wait; the rest by PtyRead.
        var b: [256]u8 = [0; 256]
        k := rt.TermRead(io, p.read, mut b[..])
        Assert(k.err == core.SysErr.None && k.value > 0)
        n := Trunc[int](k.value) + CPtyRead(io, &p, mut b[Trunc[int](k.value)..], "rdy")
        Assert(CHas(b[0..n], "rdy"))
        h := Trunc[uint](r.value)
        Assert(rt.ChildWait(io, h, 0).value == 0)
        PClose(io, h)
        Assert(rt.PtyClose(io, p).err == core.SysErr.None)
        return
    }
    // Windows: the console's input handle, when this process has a console.
    var t: [2]uint = [0; 2]
    if rt.TermOpen(io, mut t).err != core.SysErr.None {
        return
    }
    var w := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    scope d := io.Scope() {
        d.CancelAfter(core.DurationOf(50 * DlMs))
        w = rt.TermWait(io, t[0])
    }
    // Records already queued (focus, a size) end it at once.
    Assert(w.err == core.SysErr.Canceled || w.err == core.SysErr.None)
    var ab: [2]u16 = [97, 98]
    if CType(ab[..]) >= 0 {
        Assert(rt.TermWait(io, t[0]).err == core.SysErr.None)
        // Each typed unit is a key-down and a key-up record.
        var rec: [400]u8 = [0; 400]
        k := rt.TermRead(io, t[0], mut rec[..])
        Assert(k.err == core.SysErr.None && k.value >= 40 && k.value % 20 == 0)
    }
    var tiny: [8]u8 = [0; 8]
    Assert(rt.TermRead(io, t[0], mut tiny[..]).err == core.SysErr.Other)
    PClose(io, t[0])
    PClose(io, t[1])
}

// CType types units into this process's console (sys.ConsoleTestType; the
// test blocks' parameter is named sys): -1 when it may not.
CType(units: []u16): int {
    return sys.ConsoleTestType(units)
}

// CKeys reads the console records in rec[0..n] and sets bit 0 of seen for
// an 'a' key-down and bit 1 for a 'b' (INPUT_RECORD: EventType at 0, 1 a
// KEY_EVENT; bKeyDown at 4; uChar at 14).
CKeys(rec: []u8, seen: u32): u32 {
    var s := seen
    var i := 0
    for i + 20 <= rec.Len() {
        kind := u32(rec[i]) | (u32(rec[i + 1]) << 8)
        unit := u32(rec[i + 14]) | (u32(rec[i + 15]) << 8)
        if kind == 1 && rec[i + 4] != 0 {
            if unit == 97 {
                s |= 1
            } else if unit == 98 {
                s |= 2
            }
        }
        i += 20
    }
    return s
}

// CConsoleChild is RT8-Con-Console's child, attached to the parent's
// pseudo-console: a real console's raw mode is set and read back, the
// records queued at start are read until a wait under a deadline is
// Canceled (nothing pending), then it says ready, and the wait that follows
// is ended by the parent's typing; the records are its two keys; the saved
// mode goes back. Exit 0 when all of it held (the block passes), else the
// assert's 101.
CConsoleChild(io: Io) {
    var t: [2]uint = [0; 2]
    Assert(rt.TermOpen(io, mut t).err == core.SysErr.None)
    var old: [64]u8 = [0; 64]
    var cur: [64]u8 = [0; 64]
    var none8: [1]u8 = [0]
    m := rt.TermMode(io, t[0], t[1], core.IoTermRaw, none8[0..0], mut old[..])
    Assert(m.err == core.SysErr.None && m.value > 0)
    k := Trunc[int](m.value)
    var rec: [800]u8 = [0; 800]
    var seen: u32 = 0
    // Drain what the console queued at start; a Canceled wait consumed
    // nothing (the wait is repeatable).
    var quiet := false
    for _ in 0..50 {
        var w := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        scope d := io.Scope() {
            d.CancelAfter(core.DurationOf(200 * DlMs))
            w = rt.TermWait(io, t[0])
        }
        if w.err == core.SysErr.Canceled {
            quiet = true
            break
        }
        Assert(w.err == core.SysErr.None)
        r := rt.TermRead(io, t[0], mut rec[..])
        Assert(r.err == core.SysErr.None && r.value % 20 == 0)
        seen = CKeys(rec[0..Trunc[int](r.value)], seen)
    }
    Assert(quiet && seen == 0)
    Assert(core.IoStdio(io, 0, "rt8-ready\r\n".AsBytes()).err == core.SysErr.None)
    var waits := 0
    scope d := io.Scope() {
        d.CancelAfter(core.DurationOf(10_000 * DlMs))
        for seen != 3 {
            w := rt.TermWait(io, t[0])
            if w.err != core.SysErr.None {
                break
            }
            waits += 1
            r := rt.TermRead(io, t[0], mut rec[..])
            Assert(r.err == core.SysErr.None && r.value % 20 == 0)
            seen = CKeys(rec[0..Trunc[int](r.value)], seen)
        }
    }
    Assert(rt.TermMode(io, t[0], t[1], core.IoTermSaved, old[0..k], mut cur[..]).err == core.SysErr.None)
    Assert(seen == 3 && waits >= 1)
    Assert(core.IoStdio(io, 0, "rt8-done\r\n".AsBytes()).err == core.SysErr.None)
}

test "RT8-Con-Console a child on a pseudo-console: raw mode on a real console, a wait ended by typed input, its key records, the mode restored" (sys: &System) {
    io := sys.io
    if !PWin() || sys.args.Len() < 2 {
        // Linux runs the terminal calls on a pty (RT8-Child-Pty, -Ready).
        return
    }
    if sys.args.Len() == 3 && PSame(sys.args[2], "rt8-console-child") {
        CConsoleChild(io)
        return
    }
    var p := core.IoPty(read: 0, write: 0, term: 0)
    if rt.PtyOpen(io, 80, 24, mut p).err != core.SysErr.None {
        // Before Windows 10 1809: no pseudo-console.
        return
    }
    var s := CSpec(core.IoStdInherit, core.IoStdInherit, core.IoStdInherit)
    s.pty = p.term
    var noenv: [1]str = [""]
    var a: [3]str = [sys.args[0], sys.args[1], "rt8-console-child"]
    r := rt.Spawn(io, a[..], noenv[0..0], "", mut s)
    Assert(r.err == core.SysErr.None)
    h := Trunc[uint](r.value)
    var b: [16384]u8 = [0; 16384]
    var n := 0
    var typed := false
    scope d := io.Scope() {
        d.CancelAfter(core.DurationOf(30_000 * DlMs))
        n = CPtyRead(io, &p, mut b[..], "rt8-ready")
        if CHas(b[0..n], "rt8-ready") {
            typed = NativeWrite(io, p.write, "ab".AsBytes(), -1).value == 2
            var b2: [16384]u8 = [0; 16384]
            m := CPtyRead(io, &p, mut b2[..], "rt8-done")
            typed = typed && CHas(b2[0..m], "rt8-done")
        }
    }
    code := rt.ChildWait(io, h, 0).value
    PClose(io, h)
    Assert(rt.PtyClose(io, p).err == core.SysErr.None)
    if code != 0 || !typed {
        // The child's report went to the pseudo-console: show its printable
        // bytes.
        for i in 0..n {
            if b[i] < 32 || b[i] > 126 {
                b[i] = 32
            }
        }
        out := str.From(b[0..n]) else { "?" }
        sys.PrintErr("console child: exit {code}, typed and done {typed}; it wrote: {out}\n",
            (code: code, typed: typed, out: out)) else {}
    }
    Assert(code == 0 && typed)
}
