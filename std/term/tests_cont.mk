// Why: a stopped raw program comes back to a terminal the shell may have
// put in line mode, and must be raw again whether it reads or not.
// What: pseudo-terminal tests of the SIGCONT hook and of Read's own re-set.
// How: children stop and continue under a pseudo-terminal and report the
// mode with TCGETS; Linux only.

// The runtime's SIGCONT hook on a pseudo-terminal: a raw program
// that never reads again is raw again after a stop and a continue, since
// the registry's record of its Term carries the raw flag, and after
// Restore a continue leaves the mode. Linux only: Windows sends no
// SIGSTOP or SIGCONT. The child reads the mode in force with TCGETS alone
// (TmModeNow), so nothing but the runtime sets it.

import std.core
import std.fs
import std.proc
import std.raw
import std.rt

// TmModeNow is the mode in force on the terminal (input h, output out), as
// hex, read without setting anything (IoTermGet); "" when the terminal
// refuses.
TmModeNow(a: &Arena, io: Io, h: uint, out: uint): str from a {
    var old: [64]u8 = [0; 64]
    var none_: [0]u8 = []
    r := rt.TermMode(io, h, out, core.IoTermGet, none_[..], mut old[..])
    if r.err != core.SysErr.None || r.value <= 0 || r.value > 64 {
        return ""
    }
    n := Trunc[int](r.value)
    var hex := Text.New(a)
    digits := "0123456789abcdef".AsBytes()
    for i in 0..n {
        hex.PushChar(char(digits[int(old[i] >> 4)])).Must()
        hex.PushChar(char(digits[int(old[i] & 15)])).Must()
    }
    return hex.Done()
}

// TmModeBecomes waits up to 10 s, reading the mode every 10 ms, for the
// terminal's mode to be want, and says whether it was.
TmModeBecomes(sys: &System, h: uint, out: uint, want: str): bool {
    for _ in 0..1000 {
        if TmModeNow(sys.arena, sys.io, h, out) == want {
            return true
        }
        sys.io.Sleep(TmMs(10)).Must()
    }
    return false
}

// TmContChild is the child of mode "cont", on t in raw mode; it never
// returns and never calls Read. It puts the terminal in line mode behind
// the Term's back (as a shell may while the job is stopped) and logs
// "line"; then waits for raw mode, which only the runtime's SIGCONT hook
// sets here, and logs "raw again"; then Restore, "restored"; then, once the
// block wrote path.go after a second stop and continue, checks the mode is
// still the restored one and logs "kept". Exit codes: 81 the line mode
// refused, 82 the mode unreadable, 83 line mode is raw, 84 never raw
// again, 85 Restore failed, 86 a continue changed the restored mode, 87 no
// go, 74 the transcript.
TmContChild(sys: &System, t: Term, path: str): never {
    io := sys.io
    var term := t
    raw_mode := TmModeNow(sys.arena, io, term.input, term.out)
    if raw_mode == "" {
        proc.Exit(82)
    }
    var failed := raw.Atomic[u32].New(0)
    TmLineMode(io, term.input, term.out, &failed)
    if failed.Load() != 0 {
        proc.Exit(81)
    }
    if TmModeNow(sys.arena, io, term.input, term.out) == raw_mode {
        proc.Exit(83)
    }
    var log := Text.New(sys.arena)
    log.Push("line\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    if !TmModeBecomes(sys, term.input, term.out, raw_mode) {
        proc.Exit(84)
    }
    log.Push("raw again\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    term.Restore(io) else {
        proc.Exit(85)
    }
    cooked := TmModeNow(sys.arena, io, term.input, term.out)
    log.Push("restored\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    go_path := sys.arena.Fmt("{path}.go", (path)).Must()
    if !TmAwait(sys, go_path, "go") {
        proc.Exit(87)
    }
    if TmModeNow(sys.arena, io, term.input, term.out) != cooked {
        proc.Exit(86)
    }
    log.Push("kept\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
    term.Close(io)
    proc.Exit(0)
}

// TmContReadChild is the child of mode "contread", on t in raw mode; it
// never returns. It logs "reading", then reads keys, logging each, until
// q. TmReadOrExit's codes: 122 is Device(22), rt's wait answering EINVAL
// when the mode changed under it (the poller now resubmits such a wait).
TmContReadChild(sys: &System, t: Term, path: str): never {
    io := sys.io
    var term := t
    var log := Text.New(sys.arena)
    log.Push("reading\n").Must()
    fs.WriteAll(io, path, log.Str().AsBytes()) else { proc.Exit(74) }
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
    term.Close(io)
    proc.Exit(0)
}

// TmFillRegistry fills the runtime's restore table (four records) with
// test records that write nothing at exit, so a Term opened after it has
// no record (Track's id is -1) and the SIGCONT hook never sets its mode;
// exits 89 when a record is refused. Kind 3 is sys.TermKindTest on every
// target (std.term does not import std.sys).
TmFillRegistry() {
    for _ in 0..4 {
        rec := rt.TermSaved(kind: 3, h: 2, out: 2, alt: false, mouse: false, len: 0, mode: [0; 64])
        if rt.TermRegister(rec) < 0 {
            proc.Exit(89)
        }
    }
}

// TmContFullChild is the child of mode "stopfull", on t in raw mode with
// the registry full (TmFillRegistry before Open): as stopbusy's child, it
// reads keys, logging each, until q, and after s puts the terminal in
// line mode behind the Term's back and works a second outside Read, in
// which the block stops and continues it. Only Read's own raw set (Fill's
// Between) can make the next key arrive alone. Exit codes: 88 the Term has
// a record, 81 the line mode refused, 72 the ready line, 74 the
// transcript, TmReadOrExit's.
TmContFullChild(sys: &System, t: Term, path: str): never {
    io := sys.io
    var term := t
    if term.reg >= 0 {
        proc.Exit(88)
    }
    var buf: [64]u8 = [0; 64]
    var w := term.Writer(io, mut buf[..])
    w.Str("ready\r\n") else { proc.Exit(72) }
    w.Flush() else { proc.Exit(72) }
    var log := Text.New(sys.arena)
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
        if TmIsChar(e, 's') {
            var failed := raw.Atomic[u32].New(0)
            TmLineMode(io, term.input, term.out, &failed)
            if failed.Load() != 0 {
                proc.Exit(81)
            }
            io.Sleep(TmMs(1000)).Must()
        }
    }
    term.Close(io)
    proc.Exit(0)
}

test "on Linux, with the restore table full, Read sets raw mode again after a stop and a continue that came while the program was not reading (Read's own set, for a Term with no record)" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        // No SIGSTOP or SIGCONT on Windows.
        return
    }
    // The child's Term has no record, so the runtime's SIGCONT hook sets
    // nothing: the key after the continue arrives alone only because Read
    // sets raw mode before its wait.
    TmStopCont(sys, "stopfull", 1200)
}

// TmStopAndContinue stops process pid, waits for it to be stopped, and
// continues it: whether both happened.
TmStopAndContinue(sys: &System, pid: i64): bool {
    _ = TmSh(sys, sys.arena.Fmt("kill -STOP {pid}", (pid)).Must())
    stopped := TmStopped(sys, pid)
    cont := TmSh(sys, sys.arena.Fmt("kill -CONT {pid}", (pid)).Must())
    return stopped && cont
}

test "on Linux, a raw program that never reads again is raw again after a stop and a continue, and after Restore a continue leaves the mode" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    if Target.os != core.Os.Linux {
        // No SIGSTOP or SIGCONT on Windows.
        return
    }
    io := sys.io
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-cont-{tag}.txt", (dir, tag)).Must()
    go_path := sys.arena.Fmt("{path}.go", (path)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], "cont", path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    pid := child.Pid()
    var ready := raw.Atomic[u32].New(0)
    var line := false
    var still := false
    var first := false
    var again := false
    var second := false
    var kept := false
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        line = TmAwait(sys, path, "line\n")
        // No continue yet: the mode stays as the child left it.
        io.Sleep(TmMs(300)).Must()
        still = (fs.ReadText(sys.arena, io, path) else { "" }) == "line\n"
        first = TmStopAndContinue(sys, pid)
        again = TmAwait(sys, path, "line\nraw again\nrestored\n")
        second = TmStopAndContinue(sys, pid)
        io.Sleep(TmMs(300)).Must()
        fs.WriteAll(io, go_path, "go".AsBytes()).Must()
        kept = TmAwait(sys, path, "line\nraw again\nrestored\nkept\n")
        if !kept {
            child.Kill() else {}
        }
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        s.Cancel()
    }
    p.Close()
    text := fs.ReadText(sys.arena, io, path) else { "" }
    fs.Remove(io, path) else {}
    fs.Remove(io, go_path) else {}
    // The child's own failures first (TmContChild's codes).
    Assert(code != 81 && code != 82 && code != 83 && code != 85 && code != 87 && code != 74)
    Assert(line && still)
    Assert(first)
    Assert(code != 84)
    Assert(again)
    Assert(second)
    Assert(code != 86)
    Assert(TmSame(sys, text, "line\nraw again\nrestored\nkept\n"))
    Assert(kept && code == 0)
}

test "on Linux, a raw program waiting in Read through five stops and continues reads the next keys alone (the hook sets nothing over raw mode)" (sys: &System) {
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
    path := sys.arena.Fmt("{dir}/mkterm-contread-{tag}.txt", (dir, tag)).Must()
    p := proc.Pty.Open(io, 80, 24).Must()
    var a: [4]str = [sys.args[0], sys.args[1], "contread", path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    pid := child.Pid()
    var ready := raw.Atomic[u32].New(0)
    var reading := false
    var rounds := 0
    var quit := false
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrain(io, &p, &ready)
        reading = TmAwait(sys, path, "reading\n")
        io.Sleep(TmMs(300)).Must()
        for _ in 0..5 {
            if TmStopAndContinue(sys, pid) {
                rounds += 1
            }
            io.Sleep(TmMs(100)).Must()
        }
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
    fs.Remove(io, path) else {}
    // 122: a wait that answered EINVAL (Device(22)); 90 to 149 the child's other
    // failures (TmReadOrExit).
    Assert(code != 122)
    Assert(code < 90 || code >= 150)
    Assert(reading && rounds == 5)
    Assert(TmSame(sys, text, "reading\nkey Char a\nkey Char q\n"))
    Assert(quit && code == 0)
}
