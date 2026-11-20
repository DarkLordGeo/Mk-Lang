// Why: Read races input against signals, stops and deadlines, and only a
// real pseudo-terminal shows those races.
// What: pseudo-terminal blocks for Read and ReadSecret across SIGWINCH,
// stops, deadline cuts and mode changes.
// How: the tests_pty.mk harness; each block asserts on the child's
// transcript and exit code.

// End-to-end blocks for Term.Read and Term.ReadSecret on a pseudo-terminal
// across signals, stops, deadline cuts and the terminal's modes, with the
// harness of tests_pty.mk: the block runs this test executable again on a
// proc.Pty in one of TmChild's modes and asserts on the transcript and the
// exit code.

import std.core
import std.fs
import std.proc
import std.raw
import std.rt

// TmReadCut is t.Read, or none when it was cut (Canceled), counted in
// cancels; any other failure ends the child.
TmReadCut(t: mut Term, a: &Arena, io: Io, cancels: mut int): ?Event from a {
    e := t.Read(a, io) else err {
        match err {
            Canceled {
                *cancels += 1
                return none
            }
            _ { proc.Exit(73) }
        }
    }
    return some(e)
}

// TmSame is whether got is want, printing both to stderr when not.
TmSame(sys: &System, got: str, want: str): bool {
    if got == want {
        return true
    }
    sys.PrintErr("transcript:\n{got}\nwant:\n{want}\n", (got, want)) else {}
    return false
}

// TmIsStartSize: e is a resize to the pseudo-terminal's first size.
TmIsStartSize(e: Event): bool {
    return match e {
        .Resize(cols, rows) { cols == 80 && rows == 24 }
        _ { false }
    }
}

test "Term.Read returns a 10 KiB paste with multi-byte text in pieces whose texts join to it" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var big := Text.New(sys.arena)
    for _ in 0..640 {
        big.Push("ab\u{e9}\u{20ac}\u{1F600}xyz ").Must()
    }
    body := big.Str()
    paste := sys.arena.Fmt("\x1b[200~{body}\x1b[201~", (body)).Must()
    var keys: [2]str = [paste, "q"]
    (code, text) := TmRun(sys, "bigpaste", keys[..], -1)
    Assert(code == 0)
    Assert(text.Len() == body.Len() && text == body)
}

test "A program whose standard streams are a pipe and the null device reads keys from the terminal it opens" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [3]str = ["a", "\x1b[A", "q"]
    (code, text) := TmRun(sys, "piped", keys[..], 2)
    Assert(code == 0)
    Assert(TmSame(sys, text, "size 80 24\nkey Char a\nkey Up\nresize 100 30\nkey Char q\n"))
}

// TmAwait waits up to 10 s for the transcript at path to be want, and
// says whether it was.
TmAwait(sys: &System, path: str, want: str): bool {
    for _ in 0..1000 {
        got := fs.ReadText(sys.arena, sys.io, path) else { "" }
        if got == want {
            return true
        }
        sys.io.Sleep(TmMs(10)).Must()
    }
    return false
}

// TmStopped waits up to 10 s for Linux process pid to be stopped (state T
// in /proc/pid/stat) and says whether it was.
TmStopped(sys: &System, pid: i64): bool {
    stat := sys.arena.Fmt("/proc/{pid}/stat", (pid)).Must()
    for _ in 0..1000 {
        text := fs.ReadText(sys.arena, sys.io, stat) else { "" }
        // The state follows the command's closing parenthesis.
        b := text.AsBytes()
        var k := b.Len() - 1
        for k > 0 && b[k] != b')' {
            k -= 1
        }
        if k > 0 && k + 2 < b.Len() && b[k + 2] == b'T' {
            return true
        }
        sys.io.Sleep(TmMs(10)).Must()
    }
    return false
}

// TmSh runs the shell command cmd and says whether it exited 0.
TmSh(sys: &System, cmd: str): bool {
    var a: [3]str = ["/bin/sh", "-c", cmd]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.stdin = proc.Stdio.Null
    c.stdout = proc.Stdio.Null
    c.stderr = proc.Stdio.Null
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, sys.io, &c) else {
        return false
    }
    TmCloseEnds(stdin, stdout, stderr)
    st := child.Wait(sys.io).Must()
    code := st.code else { -1 }
    return code == 0
}

test "On Linux, Read sets raw mode again after SIGCONT, when the terminal was put back in line mode while the program was stopped" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        // No SIGSTOP or SIGCONT on Windows.
        return
    }
    TmStopCont(sys, "stopcont", 300)
}

test "on Linux, Read sets raw mode again after a stop and a continue that came while the program was not reading" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        return
    }
    // The child works a second outside Read after its "s" line: the stop
    // and the continue land there, and it reads again 1.2 s later.
    TmStopCont(sys, "stopbusy", 1200)
}

// TmStopCont runs mode (stopcont or stopbusy) on a pseudo-terminal: it
// types s, stops and continues the child 300 ms after its line, waits
// settle ms, then types a and q, which must each arrive alone (raw mode).
TmStopCont(sys: &System, mode: str, settle: i64) {
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    pid := child.Pid()
    var ready := raw.Atomic[u32].New(0)
    var stopped := false
    var cont := false
    var quit := false
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        var waited := 0
        for ready.Load() == 0 && waited < 1000 {
            io.Sleep(TmMs(10)).Must()
            waited += 1
        }
        // "s" makes the child put the terminal in line mode (stopcont: 150
        // ms into its next wait; stopbusy: at once, then a second's work).
        TmType(sys, &p, "s")
        _ = TmAwait(sys, path, "key Char s\n")
        io.Sleep(TmMs(300)).Must()
        _ = TmSh(sys, sys.arena.Fmt("kill -STOP {pid}", (pid)).Must())
        stopped = TmStopped(sys, pid)
        cont = TmSh(sys, sys.arena.Fmt("kill -CONT {pid}", (pid)).Must())
        io.Sleep(TmMs(settle)).Must()
        // In raw mode "a" arrives alone; in line mode it waits for Enter.
        TmType(sys, &p, "a")
        _ = TmAwait(sys, path, "key Char s\nkey Char a\n")
        TmType(sys, &p, "q")
        quit = TmAwait(sys, path, "key Char s\nkey Char a\nkey Char q\n")
        if !quit {
            child.Kill() else {}
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    // The child's own failures first (TmReadOrExit's codes: 90 Canceled,
    // 100 + the OS code for Device, 91 another; 81 the mode change
    // failed). 122: rt's wait answered EINVAL when the mode changed under
    // it (Device(22)), which the io_uring poller now waits through.
    Assert(code != 122)
    Assert(code < 90 || code >= 150)
    Assert(code != 81)
    Assert(stopped && cont)
    Assert(TmSame(sys, text, "key Char s\nkey Char a\nkey Char q\n"))
    Assert(quit && code == 0)
}

test "UseColor on a terminal is true with NO_COLOR unset or empty and false with NO_COLOR=1, and the streams are terminals" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    io := sys.io
    // Unset: only what a Windows process needs of its environment.
    var base: [1]str = [""]
    var nb := 0
    if root := sys.Env("SystemRoot") {
        base[0] = sys.arena.Fmt("SystemRoot={root}", (root)).Must()
        nb = 1
    }
    var empty: [1]str = ["NO_COLOR="]
    var one: [1]str = ["NO_COLOR=1"]
    p := proc.Pty.Open(io, 80, 24).Must()
    (code: c0, text: unset) := TmOnPtyEnv(sys, &p, "color", proc.Env.Replace(vars: base[0..nb]))
    (code: c1, text: blank) := TmOnPtyEnv(sys, &p, "color", proc.Env.Extend(vars: empty[..]))
    (code: c2, text: set) := TmOnPtyEnv(sys, &p, "color", proc.Env.Extend(vars: one[..]))
    p.Close()
    Assert(c0 == 0 && c1 == 0 && c2 == 0)
    Assert(TmSame(sys, unset, "color true out true in true\n"))
    Assert(TmSame(sys, blank, "color true out true in true\n"))
    Assert(TmSame(sys, set, "color false out true in true\n"))
}

// TmCutOnce runs mode "cutonce" on a new pseudo-terminal: it types q once
// the transcript ends in "cut", waits up to 5 s for the key's line, and
// kills the child when it does not come. The child's exit code (-1000 when
// killed) and transcript.
TmCutOnce(sys: &System, mode: str): (code: int, text: str) from sys {
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var ready := raw.Atomic[u32].New(0)
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        var cut := false
        for _ in 0..1000 {
            got := fs.ReadText(sys.arena, io, path) else { "" }
            if got.EndsWith("cut\n") {
                cut = true
                break
            }
            io.Sleep(TmMs(10)).Must()
        }
        if cut {
            TmType(sys, &p, "q")
            for _ in 0..500 {
                got := fs.ReadText(sys.arena, io, path) else { "" }
                if got.EndsWith("key Char q\n") {
                    break
                }
                io.Sleep(TmMs(10)).Must()
            }
        }
        got := fs.ReadText(sys.arena, io, path) else { "" }
        if !got.EndsWith("key Char q\n") {
            child.Kill() else {}
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    return (code, text)
}

test "a Term.Read cut once by a 300 ms deadline, then a plain Read after its scope, reads the key typed after the cut" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (code, text) := TmCutOnce(sys, "cutonce")
    Assert(TmSame(sys, text, "cut\nkey Char q\n"))
    Assert(code == 0)
}

test "Read cut once right after Raw, nothing drained or written first" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (code, text) := TmCutOnce(sys, "cutbare")
    Assert(TmSame(sys, text, "cut\nkey Char q\n"))
    Assert(code == 0)
}

// TmModeOf is the mode in force on the terminal (input h, output out), as
// hex, read as the probe reads it (raw mode set, then the mode read put
// back); "" when the terminal refuses.
TmModeOf(a: &Arena, io: Io, h: uint, out: uint): str from a {
    var old: [64]u8 = [0; 64]
    var none_: [0]u8 = []
    r := rt.TermMode(io, h, out, core.IoTermRaw, none_[..], mut old[..])
    if r.err != core.SysErr.None || r.value <= 0 || r.value > 64 {
        return ""
    }
    n := Trunc[int](r.value)
    var back: [64]u8 = [0; 64]
    _ = rt.TermMode(io, h, out, core.IoTermSaved, old[0..n], mut back[..])
    var hex := Text.New(a)
    digits := "0123456789abcdef".AsBytes()
    for i in 0..n {
        hex.PushChar(char(digits[int(old[i] >> 4)])).Must()
        hex.PushChar(char(digits[int(old[i] & 15)])).Must()
    }
    return hex.Done()
}

// TmSecretCooked is the child of mode "secretcooked": on a Term
// never made raw, ReadSecret cut by 300 ms deadlines until a line ends; the
// mode is read at the start, after each cut and at the end. kept: no cut
// left the mode found (the echoing one) in force; back: the end's mode is
// the start's.
TmSecretCooked(sys: &System, term: Term, path: str): never {
    var t := term
    io := sys.io
    a := sys.arena
    start := TmModeOf(a, io, t.input, t.out)
    var buf: [64]u8 = [0; 64]
    var w := t.Writer(io, mut buf[..])
    w.Str("ready\r\n") else { proc.Exit(72) }
    w.Flush() else { proc.Exit(72) }
    var cuts := 0
    var kept := start.Len() > 0
    var line := ""
    for {
        var got: ?str = none
        scope s := io.Scope() {
            s.CancelAfter(TmMs(300))
            got = TmSecretCut(mut t, a, io, mut cuts)
        }
        if l := got {
            line = l
            break
        }
        if TmModeOf(a, io, t.input, t.out) == start {
            kept = false
        }
    }
    back := TmModeOf(a, io, t.input, t.out) == start
    many := cuts >= 2
    fs.WriteAll(io, path, a.Fmt("secret {line} cuts {many} kept {kept} back {back}\n", (line, many, kept, back)).Must().AsBytes()) else {
        proc.Exit(74)
    }
    t.Close(io)
    proc.Exit(0)
}

test "Term.ReadSecret on a Term that was not raw, cut by deadlines, keeps the echoing mode off between the cuts, and puts it back when the line ends" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [4]str = ["a", "b", "", "c\r"]
    (code, text) := TmRun(sys, "secretcooked", keys[..], -1)
    Assert(code == 0)
    Assert(TmSame(sys, text, "secret abc cuts true kept true back true\n"))
}

// TmWinch runs mode ("winchpause" or "winchlate") on a new
// pseudo-terminal: 300 ms after the child's "pre" line (inside Fill's
// pause, or its signal tasks' late start) it resizes the terminal to 100
// by 30, waits up to 10 s for the child's resize line, then types q. The
// child's exit code (-1000 when killed) and transcript.
TmWinch(sys: &System, mode: str): (code: int, text: str) from sys {
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var ready := raw.Atomic[u32].New(0)
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        if TmAwait(sys, path, "pre\n") {
            io.Sleep(TmMs(300)).Must()
            p.Resize(io, 100, 30).Must()
            _ = TmAwait(sys, path, "pre\nresize 100 30\n")
            TmType(sys, &p, "q")
            for _ in 0..500 {
                got := fs.ReadText(sys.arena, io, path) else { "" }
                if got.EndsWith("key Char q\n") {
                    break
                }
                io.Sleep(TmMs(10)).Must()
            }
        }
        got := fs.ReadText(sys.arena, io, path) else { "" }
        if !got.EndsWith("key Char q\n") {
            child.Kill() else {}
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    return (code, text)
}

test "on Linux, a SIGWINCH that comes after Read compared the size and before it waits is a Resize with no key typed" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        // Windows reports a resize as an input record, which no wait can
        // miss.
        return
    }
    (code, text) := TmWinch(sys, "winchpause")
    Assert(TmSame(sys, text, "pre\nresize 100 30\nkey Char q\n"))
    Assert(code == 0)
}

test "on Linux, a SIGWINCH that comes after Read compared the size and before its signal task's wait starts is a Resize with no key typed" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        return
    }
    // The signal tasks start their waits 1 s late (SignalLate), as tasks
    // a busy carrier runs late would; the resize comes 300 ms after "pre",
    // inside that second. A task whose baseline is the count at its wait
    // would miss it, and the transcript would be "pre", then the key.
    (code, text) := TmWinch(sys, "winchlate")
    Assert(TmSame(sys, text, "pre\nresize 100 30\nkey Char q\n"))
    Assert(code == 0)
}
