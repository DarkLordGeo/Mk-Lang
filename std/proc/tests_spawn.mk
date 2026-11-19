// Why: Spawn, Child, Pipe and Pty must behave the same on both targets,
// including under cancels and deadlines.
// What: test blocks of proc.Spawn, Command, Child, Pipe and Pty, and SpChild,
// the child body whose modes the other proc test files share.
// How: a child is this test executable run again: args[1] is the block's index
// and args[2] the mode, which SpChild acts on (or PtChild, for the modes it has).

import std.core
import std.fs
import std.rt
import std.time

// SpChild is the child half of the blocks below; it never returns.
//   echoargs A...  each argument after the mode on its own line
//   envget NAME    the variable's value, or <none>, then a newline
//   cwd            the working directory, then a newline
//   orphan         start a grandchild (nap) that inherits stdout, detach
//                  it, write "o" and exit 3 at once
//   family         the same, then sleep 60 s instead of exiting
//   ttyline        read one line of stdin, write got:<line>, exit 0
//   fds            (Linux) how many descriptors /proc/self/fd lists,
//                  the listing's own included, then a newline
//   fdsvia         (Linux) the same, then a grandchild in mode fds on
//                  the same stdout; exits with its code
//   handles        proc.Inherited: three handles, a file, a pipe
//                  write end and a listener; copy the file's bytes into the
//                  pipe, accept once and write acc, then print the count,
//                  a second Inherited's count and a grandchild's MK_HANDLES
//   handstream     proc.Inherited: one Stream; write "from child"
//                  to it and close it
//   foreign        proc.Inherited's answer (the count, os22 for
//                  Os(22), error for another), the handles it gave
//                  closed; on Linux fd3 or nofd3, whether descriptor 3 is
//                  open then; then alive after a sleep on the poller and a
//                  temporary file written, read and removed
//   handwrite      proc.Inherited: two files; write "AA" at 0
//                  through the first and "BB" at 5 through the second,
//                  print which writes were refused
//   anything else  PtChild's modes (gen, cat, sleep, nap...)
SpChild(sys: &System): never {
    mode := sys.args[2]
    var out := sys.Stdout()
    if PtSame(mode, "echoargs") {
        for i in 3..sys.args.Len() {
            out.Str(sys.args[i]) else { Exit(80) }
            out.Str("\n") else { Exit(80) }
        }
        out.Flush() else { Exit(80) }
        Exit(0)
    }
    if PtSame(mode, "envget") {
        v := sys.Env(sys.args[3]) else { "<none>" }
        out.Str(v) else { Exit(81) }
        out.Str("\n") else { Exit(81) }
        out.Flush() else { Exit(81) }
        Exit(0)
    }
    if PtSame(mode, "cwd") {
        d := fs.Cwd(sys.arena, sys.io) else { Exit(82) }
        out.Str(d) else { Exit(82) }
        out.Str("\n") else { Exit(82) }
        out.Flush() else { Exit(82) }
        Exit(0)
    }
    if PtSame(mode, "orphan") || PtSame(mode, "family") {
        var g: [3]str = [sys.args[0], sys.args[1], "nap"]
        var c := DefaultCommand
        c.args = g[..]
        (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, &c) else { Exit(83) }
        SpCloseEnds(stdin, stdout, stderr)
        child.Detach()
        out.Str("o") else { Exit(83) }
        out.Flush() else { Exit(83) }
        if PtSame(mode, "orphan") {
            Exit(3)
        }
        sys.io.Sleep(core.DurationOf(60_000_000_000)) else { Exit(5) }
        Exit(3)
    }
    if PtSame(mode, "ttyline") {
        var inp := sys.Stdin()
        var b: [256]u8 = [0; 256]
        var n := 0
        for n < 256 {
            k := inp.Read(mut b[n..n + 1]) else { Exit(84) }
            if k == 0 || b[n] == 10 || b[n] == 13 {
                break
            }
            n += 1
        }
        out.Str("got:") else { Exit(84) }
        out.Bytes(b[0..n]) else { Exit(84) }
        out.Str("\n") else { Exit(84) }
        out.Flush() else { Exit(84) }
        Exit(0)
    }
    if PtSame(mode, "ttyall") {
        // Each standard stream a terminal (1) or not (0), stdout and
        // stderr each marked, and on Linux a line through /dev/tty, which
        // opens only on a controlling terminal; then a line read ends it.
        out.Str("tty:") else { Exit(86) }
        for i in 0..3 {
            t := rt.IsTerminal(sys.io, Trunc[u32](i))
            var one: [1]u8 = [if t.err == core.SysErr.None && t.value == 1 { 49 } else { 48 }]
            out.Bytes(one[..]) else { Exit(86) }
        }
        out.Str("\n") else { Exit(86) }
        out.Flush() else { Exit(86) }
        var err := sys.Stderr()
        err.Str("err:ok\n") else { Exit(86) }
        err.Flush() else { Exit(86) }
        if !SpWin() {
            var f := fs.OpenWith(sys.io, "/dev/tty", fs.Append) else { Exit(87) }
            f.WriteAll(sys.io, "ctl:ok\n".AsBytes()) else { Exit(87) }
            f.Close(sys.io) else { Exit(87) }
        }
        SpDrainLine(sys)
        Exit(0)
    }
    if PtSame(mode, "stayfam") {
        // A grandchild in this group that reads stdin to its end, then
        // "o", then stdin to its end.
        var g: [3]str = [sys.args[0], sys.args[1], "drain"]
        var c := DefaultCommand
        c.args = g[..]
        (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, &c) else { Exit(88) }
        SpCloseEnds(stdin, stdout, stderr)
        child.Detach()
        out.Str("o") else { Exit(88) }
        out.Flush() else { Exit(88) }
        SpDrainStdin(sys)
        Exit(3)
    }
    if PtSame(mode, "drain") {
        SpDrainStdin(sys)
        Exit(0)
    }
    if PtSame(mode, "fds") || PtSame(mode, "fdsvia") {
        var d := fs.Dir.Open(sys.io, "/proc/self/fd") else { Exit(85) }
        var n := 0
        for {
            e := d.Next(sys.arena, sys.io) else { Exit(85) }
            if e.IsNone() {
                break
            }
            n += 1
        }
        d.Close()
        var b: [24]u8 = [0; 24]
        out.Str(PtDigits(mut b, n)) else { Exit(85) }
        out.Str("\n") else { Exit(85) }
        out.Flush() else { Exit(85) }
        if PtSame(mode, "fdsvia") {
            // Then a grandchild in mode fds on this stdout: what this
            // process holds without close-on-exec must not reach it.
            var g: [3]str = [sys.args[0], sys.args[1], "fds"]
            var c := DefaultCommand
            c.args = g[..]
            (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, &c) else { Exit(85) }
            SpCloseEnds(stdin, stdout, stderr)
            Exit(SpCode(child.Wait(sys.io) else { Exit(85) }))
        }
        Exit(0)
    }
    if PtSame(mode, "handles") || PtSame(mode, "handstream") {
        SpHandlesChild(sys, mode)
    }
    if PtSame(mode, "foreign") {
        SpForeignChild(sys)
    }
    if PtSame(mode, "handwrite") {
        SpHandWriteChild(sys)
    }
    PtChild(sys)
}

// SpDrainStdin reads this process's stdin to its end (or a failure).
SpDrainStdin(sys: &System) {
    var inp := sys.Stdin()
    var b: [256]u8 = [0; 256]
    for {
        k := inp.Read(mut b[..]) else {
            return
        }
        if k == 0 {
            return
        }
    }
}

// SpDrainLine reads this process's stdin to the end of a line, or its end.
SpDrainLine(sys: &System) {
    var inp := sys.Stdin()
    var b: [1]u8 = [0]
    for {
        k := inp.Read(mut b[..]) else {
            return
        }
        if k == 0 || b[0] == 10 || b[0] == 13 {
            return
        }
    }
}

// SpCloseEnds closes whichever pipe ends Spawn returned.
SpCloseEnds(i: ?PipeWriter, o: ?PipeReader, e: ?PipeReader) {
    if w := i {
        w.Close()
    }
    if r := o {
        r.Close()
    }
    if r := e {
        r.Close()
    }
}

// SpFeed writes data to w and closes it, so the child sees the end.
SpFeed(io: Io, w: PipeWriter, data: []u8) {
    w.WriteAll(io, data) else {
        w.Close()
        return
    }
    w.Close()
}

// SpDrain reads r into buf until the end (or buf is full); *n the bytes.
SpDrain(io: Io, r: &PipeReader, buf: mut []u8, n: mut int) {
    for *n < buf.Len() {
        k := r.Read(io, mut buf[*n..]) else {
            return
        }
        if k == 0 {
            return
        }
        *n += k
    }
}

// SpCode is st's exit code, -1000 when a signal ended the child.
SpCode(st: Status): int {
    return st.code else { -1000 }
}

// SpIsOs: e is Os(code).
SpIsOs(e: Error, code: i64): bool {
    return match e {
        Os(code: c) { c == code }
        _ { false }
    }
}

// SpErr is the error Spawn of c fails with; a child it starts instead is
// killed, detached, and the block fails.
SpErr(sys: &System, c: &Command): Error {
    (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, c) else e {
        return e
    }
    SpCloseEnds(stdin, stdout, stderr)
    child.Kill() else {}
    child.Detach()
    Panic("Spawn started a child where it should have failed")
}

// SpText: b holds exactly the bytes of s.
SpText(b: []u8, s: str): bool {
    return PtSame(str.From(b) else { "" }, s)
}

// SpHas: b holds s somewhere.
SpHas(b: []u8, s: str): bool {
    n := s.Len()
    if n > b.Len() {
        return false
    }
    for i in 0..(b.Len() - n + 1) {
        if PtSame(str.From(b[i..i + n]) else { "" }, s) {
            return true
        }
    }
    return false
}

// SpWin: the target is Windows.
SpWin(): bool {
    return Target.os == core.Os.Windows
}

// SpOut spawns c, whose stdout must be Pipe, reads stdout to its end into
// buf, and returns the bytes read; the exit code must be 0.
SpOut(sys: &System, c: &Command, buf: mut []u8): int {
    (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, c).Must()
    SpCloseEnds(stdin, none, stderr)
    r := stdout.Must()
    var n := 0
    SpDrain(sys.io, &r, buf, mut n)
    r.Close()
    Assert(SpCode(child.Wait(sys.io).Must()) == 0)
    return n
}

test "Spawn: three pipes: 300 KB fed and closed, stdout and stderr drained in tasks, then Wait gives code 0" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "cat"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdin = Stdio.Pipe
    c.stdout = Stdio.Pipe
    c.stderr = Stdio.Pipe
    n := 300_000
    data := sys.arena.Slice[u8](n).Must()
    PtPattern(mut data[..], 0)
    ob := sys.arena.Slice[u8](n + 1).Must()
    eb := sys.arena.Slice[u8](n + 1).Must()
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    Assert(child.Pid() > 0)
    o := stdout.Must()
    e := stderr.Must()
    var on := 0
    var en := 0
    scope s := io.Scope() {
        s.go SpFeed(io, stdin.Must(), data[..])
        s.go SpDrain(io, &o, mut ob[..], mut on)
        s.go SpDrain(io, &e, mut eb[..], mut en)
    }
    o.Close()
    e.Close()
    st := child.Wait(io).Must()
    Assert(SpCode(st) == 0 && st.signal.IsNone())
    Assert(on == n && en == n && PtIsPattern(ob[0..n]) && PtIsPattern(eb[0..n]))
}

test "Spawn: exit codes 0, 7 and 255 come back as Status.code with no signal" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    for code in ["0", "7", "255"] {
        var a: [6]str = [sys.args[0], sys.args[1], "gen", "0", "0", code]
        var c := DefaultCommand
        c.args = a[..]
        (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, &c).Must()
        Assert(stdin.IsNone() && stdout.IsNone() && stderr.IsNone())
        SpCloseEnds(stdin, stdout, stderr)
        st := child.Wait(sys.io).Must()
        Assert(SpCode(st) == PtInt(code) && st.signal.IsNone())
    }
}

test "Spawn: NotFound for no args, an empty name and a missing program; Os(0) for a stream given a disposition it cannot take" (sys: &System) {
    io := sys.io
    var c := DefaultCommand
    Assert(PtSame(PtName(SpErr(sys, &c)), "NotFound"))
    var empty: [1]str = [""]
    c.args = empty[..]
    Assert(PtSame(PtName(SpErr(sys, &c)), "NotFound"))
    var missing: [1]str = ["mk-proc-no-such-program-4f1c"]
    c.args = missing[..]
    Assert(PtSame(PtName(SpErr(sys, &c)), "NotFound"))
    var a: [3]str = [sys.args[0], sys.args[1], "noread"]
    (r, w) := Pipe(io).Must()
    var bad := DefaultCommand
    bad.args = a[..]
    bad.stdin = Stdio.JoinStdout
    Assert(SpIsOs(SpErr(sys, &bad), 0))
    bad.stdin = Stdio.Inherit
    bad.stdout = Stdio.JoinStdout
    Assert(SpIsOs(SpErr(sys, &bad), 0))
    bad.stdout = Stdio.Reader(r: &r)
    Assert(SpIsOs(SpErr(sys, &bad), 0))
    bad.stdout = Stdio.Inherit
    bad.stdin = Stdio.Writer(w: &w)
    Assert(SpIsOs(SpErr(sys, &bad), 0))
    r.Close()
    w.Close()
    p := Pty.Open(io, 80, 24).Must()
    var t := DefaultCommand
    t.args = a[..]
    t.terminal = some(&p)
    t.stdout = Stdio.Pipe
    Assert(SpIsOs(SpErr(sys, &t), 0))
    p.Close()
}

test "Spawn: stderr joined to stdout arrives on the one pipe, and a Null stdin reads its end at once" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    var a: [6]str = [sys.args[0], sys.args[1], "gen", "5000", "7000", "0"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.Pipe
    c.stderr = Stdio.JoinStdout
    var b: [16384]u8 = [0; 16384]
    Assert(SpOut(sys, &c, mut b[..]) == 12000)
    var k: [3]str = [sys.args[0], sys.args[1], "cat"]
    var d := DefaultCommand
    d.args = k[..]
    d.stdout = Stdio.Pipe
    d.stdin = Stdio.Null
    d.stderr = Stdio.JoinStdout
    Assert(SpOut(sys, &d, mut b[..]) == 0)
}

test "Spawn: arguments with spaces, quotes, backslashes, tabs, an empty one and non-ASCII arrive as the same list" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    var a: [11]str = [sys.args[0], sys.args[1], "echoargs", "a b", "\"q\"", "back\\\\", "", "tab\tx", "\\\"x\\", "\u{fc}n\u{ef}", "end\\"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.Pipe
    var b: [1024]u8 = [0; 1024]
    n := SpOut(sys, &c, mut b[..])
    Assert(SpText(b[0..n], "a b\n\"q\"\nback\\\\\n\ntab\tx\n\\\"x\\\n\u{fc}n\u{ef}\nend\\\n"))
}

test "Spawn: the environment extended, a name given twice taking the later entry, and replaced" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    var b: [1024]u8 = [0; 1024]
    var a: [4]str = [sys.args[0], sys.args[1], "envget", "MK_PROC3_V"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.Pipe
    var one: [1]str = ["MK_PROC3_V=one"]
    c.env = Env.Extend(vars: one[..])
    var n := SpOut(sys, &c, mut b[..])
    Assert(SpText(b[0..n], "one\n"))
    var two: [2]str = ["MK_PROC3_V=one", "MK_PROC3_V=two"]
    c.env = Env.Extend(vars: two[..])
    n = SpOut(sys, &c, mut b[..])
    Assert(SpText(b[0..n], "two\n"))
    c.env = Env.Inherit
    n = SpOut(sys, &c, mut b[..])
    Assert(SpText(b[0..n], "<none>\n"))
    // Replace drops what the test runner put in this process's
    // environment, so the child is the system shell.
    var win: [3]str = ["cmd.exe", "/c", "echo %MK_PROC3_V%:%PATH%:"]
    var lin: [3]str = ["/bin/sh", "-c", "echo \"$MK_PROC3_V:$PATH:\""]
    c.args = if SpWin() { win[..] } else { lin[..] }
    var only: [3]str = ["MK_PROC3_V=x", "MK_PROC3_V=r", "SystemRoot=C:\\Windows"]
    c.env = Env.Replace(vars: only[..])
    n = SpOut(sys, &c, mut b[..])
    // dash gives an unset PATH a default of its own, so Linux checks only
    // the variable.
    Assert(SpHas(b[0..n], if SpWin() { "r:%PATH%:" } else { "r:" }) && !SpHas(b[0..n], "x:"))
}

test "Spawn: a Linux child holds only its three streams, whatever the parent has open (close-on-exec, the rest closed before exec)" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    if SpWin() {
        return
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "fds"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.Pipe
    // The child's own runtime opens descriptors of its own (its poller), so
    // the count with nothing else open in the parent is the baseline.
    var b0: [64]u8 = [0; 64]
    n0 := SpOut(sys, &c, mut b0[..])
    (r, w) := Pipe(io).Must()
    p := Pty.Open(io, 80, 24).Must()
    var b: [64]u8 = [0; 64]
    n := SpOut(sys, &c, mut b[..])
    Assert(n0 > 0 && SpText(b[0..n], str.From(b0[0..n0]) else { "?" }))
    r.Close()
    w.Close()
    p.Close()
}

test "Spawn: the directory given is the child's working directory, and a missing one starts nothing" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    root := if SpWin() { "C:\\" } else { "/" }
    // The program's path is taken in the child's directory, as os/exec
    // takes Cmd.Path relative to Dir, so it is made absolute first.
    exe := fs.Absolute(sys.arena, sys.io, sys.args[0]).Must()
    var a: [3]str = [exe, sys.args[1], "cwd"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.Pipe
    c.dir = some(root)
    var b: [1024]u8 = [0; 1024]
    n := SpOut(sys, &c, mut b[..])
    Assert(n > root.Len() && SpText(b[0..root.Len()], root))
    c.dir = some(if SpWin() { "C:\\mk-proc-no-such-dir-4f1c" } else { "/mk-proc-no-such-dir-4f1c" })
    Assert(!PtSame(PtName(SpErr(sys, &c)), "Unsupported"))
}

// SpFileHas: the file at p holds n pattern bytes, then m pattern bytes.
SpFileHas(sys: &System, p: str, n: int, m: int): bool {
    io := sys.io
    f := fs.Open(io, p).Must()
    got := sys.arena.Slice[u8](n + m + 1).Must()
    var k := 0
    for k < got.Len() {
        r := f.ReadAt(io, i64(k), mut got[k..]).Must()
        if r == 0 {
            break
        }
        k += r
    }
    f.Close(io).Must()
    return k == n + m && PtIsPattern(got[0..n]) && PtIsPattern(got[n..n + m])
}

// SpGenTo runs gen n with stdout lent the file f, and checks the exit code.
SpGenTo(sys: &System, n: str, f: &fs.File) {
    var a: [6]str = [sys.args[0], sys.args[1], "gen", n, "0", "0"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.File(f: f)
    (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, &c).Must()
    Assert(stdin.IsNone() && stdout.IsNone() && stderr.IsNone())
    SpCloseEnds(stdin, stdout, stderr)
    Assert(SpCode(child.Wait(sys.io).Must()) == 0)
}

test "Spawn: a file lent as stdout gets the child's bytes from its start, one opened with Append after its end, and a file lent as stdin feeds it" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    (file: f, path: p) := fs.CreateTemp(sys.arena, io, "", "mk-proc3-file-").Must()
    // On Windows f is overlapped (on the completion port); the runtime
    // lends the child a synchronous reopen of it.
    SpGenTo(sys, "70000", &f)
    f.Close(io).Must()
    Assert(SpFileHas(sys, p, 70000, 0))
    // Append: the child's bytes go after what the file holds.
    ap := fs.OpenWith(io, p, fs.Append).Must()
    SpGenTo(sys, "1000", &ap)
    ap.Close(io).Must()
    Assert(SpFileHas(sys, p, 70000, 1000))
    // stdin: a file opened for reading feeds cat from its start.
    inp := fs.Open(io, p).Must()
    var b: [3]str = [sys.args[0], sys.args[1], "cat"]
    var d := DefaultCommand
    d.args = b[..]
    d.stdin = Stdio.File(f: &inp)
    d.stdout = Stdio.Pipe
    back := sys.arena.Slice[u8](71001).Must()
    Assert(SpOut(sys, &d, mut back[..]) == 71000 && PtIsPattern(back[0..70000]) && PtIsPattern(back[70000..71000]))
    inp.Close(io).Must()
    fs.Remove(io, p).Must()
}

// SpPump writes n pattern bytes to w in pieces of up to 7,000, then closes
// it.
SpPump(io: Io, w: PipeWriter, n: int) {
    var b: [7000]u8 = [0; 7000]
    var off := 0
    for off < n {
        k := if n - off < 7000 { n - off } else { 7000 }
        PtPattern(mut b[0..k], off)
        w.WriteAll(io, b[0..k]) else {
            break
        }
        off += k
    }
    w.Close()
}

test "Pipe: 2 MB arrive in order while a task writes, 0 after the writer closes, and a write after the reader closes fails" (sys: &System) {
    io := sys.io
    (r, w) := Pipe(io).Must()
    n := 2_000_000
    got := sys.arena.Slice[u8](n + 1).Must()
    var k := 0
    scope s := io.Scope() {
        s.go SpPump(io, w, n)
        SpDrain(io, &r, mut got[..], mut k)
    }
    Assert(k == n && PtIsPattern(got[0..n]))
    var one: [1]u8 = [0]
    Assert(r.Read(io, mut one[..]).Must() == 0)
    Assert(r.Read(io, mut one[0..0]).Must() == 0)
    r.Close()
    (r: r2, w: w2) := Pipe(io).Must()
    r2.Close()
    var failed := false
    w2.WriteAll(io, "x".AsBytes()) else e {
        failed = SpIsOs(e, if SpWin() { 232 } else { 32 })
    }
    Assert(failed)
    w2.Close()
}

test "Pipe: the Writer and Reader adapters carry lines through a pipe" (sys: &System) {
    io := sys.io
    (r, w) := Pipe(io).Must()
    var wb: [8]u8 = [0; 8]
    var wr := w.Writer(io, mut wb[..])
    wr.Str("line one\n").Must()
    wr.Str("line two\n").Must()
    wr.Flush().Must()
    w.Close()
    var rb: [16]u8 = [0; 16]
    var rr := r.Reader(io, mut rb[..])
    l1 := rr.Line(sys.arena).Must()
    l2 := rr.Line(sys.arena).Must()
    l3 := rr.Line(sys.arena).Must()
    Assert(PtSame(l1 else { "" }, "line one") && PtSame(l2 else { "" }, "line two") && l3.IsNone())
    r.Close()
}

test "Pipe: a pipeline: gen writes into a lent write end, cat reads the lent read end, and the parent reads cat's stdout" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    (r, w) := Pipe(io).Must()
    var a1: [6]str = [sys.args[0], sys.args[1], "gen", "100000", "0", "0"]
    var c1 := DefaultCommand
    c1.args = a1[..]
    c1.stdout = Stdio.Writer(w: &w)
    (child: p1, stdin: i1, stdout: o1, stderr: e1) := Spawn(sys.arena, io, &c1).Must()
    SpCloseEnds(i1, o1, e1)
    w.Close()
    var a2: [3]str = [sys.args[0], sys.args[1], "cat"]
    var c2 := DefaultCommand
    c2.args = a2[..]
    c2.stdin = Stdio.Reader(r: &r)
    c2.stdout = Stdio.Pipe
    c2.stderr = Stdio.Null
    (child: p2, stdin: i2, stdout: o2, stderr: e2) := Spawn(sys.arena, io, &c2).Must()
    SpCloseEnds(i2, none, e2)
    r.Close()
    out := o2.Must()
    got := sys.arena.Slice[u8](100001).Must()
    var k := 0
    SpDrain(io, &out, mut got[..], mut k)
    out.Close()
    s1 := p1.Wait(io).Must()
    s2 := p2.Wait(io).Must()
    Assert(SpCode(s1) == 0 && SpCode(s2) == 0)
    Assert(k == 100000 && PtIsPattern(got[0..k]))
}

test "Pipe: a pipe end Spawn made is not lent to another child (Os(0)); the parent still reads it" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a1: [6]str = [sys.args[0], sys.args[1], "gen", "70000", "0", "0"]
    var c1 := DefaultCommand
    c1.args = a1[..]
    c1.stdout = Stdio.Pipe
    (child: p1, stdin: i1, stdout: o1, stderr: e1) := Spawn(sys.arena, io, &c1).Must()
    SpCloseEnds(i1, none, e1)
    r := o1.Must()
    var a2: [3]str = [sys.args[0], sys.args[1], "cat"]
    var c2 := DefaultCommand
    c2.args = a2[..]
    c2.stdin = Stdio.Reader(r: &r)
    Assert(SpIsOs(SpErr(sys, &c2), 0))
    got := sys.arena.Slice[u8](70001).Must()
    var k := 0
    SpDrain(io, &r, mut got[..], mut k)
    r.Close()
    Assert(SpCode(p1.Wait(io).Must()) == 0)
    Assert(k == 70000 && PtIsPattern(got[0..k]))
}

test "Status: a Windows exit code past i32 comes back as a code, never as a signal" (sys: &System) {
    if !SpWin() {
        return
    }
    var a: [3]str = ["cmd.exe", "/c", "exit -1"]
    var c := DefaultCommand
    c.args = a[..]
    (child, stdin, stdout, stderr) := Spawn(sys.arena, sys.io, &c).Must()
    SpCloseEnds(stdin, stdout, stderr)
    st := child.Wait(sys.io).Must()
    Assert(SpCode(st) == 4_294_967_295 && st.signal.IsNone())
}

test "Pipe: a Read cut by a deadline consumes nothing, and the same Read then returns the bytes (repeatable)" (sys: &System) {
    io := sys.io
    (r, w) := Pipe(io).Must()
    var b: [8]u8 = [0; 8]
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(50))
        var failed := false
        _ = r.Read(io, mut b[..]) else e {
            failed = PtSame(PtName(e), "Canceled") && s.Expired()
        }
        Assert(failed)
    }
    w.WriteAll(io, "abc".AsBytes()).Must()
    Assert(r.Read(io, mut b[..]).Must() == 3 && SpText(b[0..3], "abc"))
    w.Close()
    r.Close()
}

test "Pipe: a WriteAll cut by a deadline on a full pipe is Canceled, and what reached the reader is a prefix of the data (prefix)" (sys: &System) {
    io := sys.io
    (r, w) := Pipe(io).Must()
    n := 4_000_000
    data := sys.arena.Slice[u8](n).Must()
    PtPattern(mut data[..], 0)
    var failed := false
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(100))
        w.WriteAll(io, data[..]) else e {
            failed = PtSame(PtName(e), "Canceled") && s.Expired()
        }
    }
    Assert(failed)
    w.Close()
    got := sys.arena.Slice[u8](n + 1).Must()
    var k := 0
    SpDrain(io, &r, mut got[..], mut k)
    r.Close()
    // Windows cancels the pending write whole (an empty prefix); Linux's
    // pipe keeps what its buffer took.
    Assert(k < n && PtIsPattern(got[0..k]))
}

test "Child.Wait: a deadline ends it with Canceled and leaves the child running; after Kill the same Wait answers (repeatable)" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "sleep"]
    var c := DefaultCommand
    c.args = a[..]
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(stdin, stdout, stderr)
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(100))
        _ = child.Wait(io) else e {
            Assert(PtSame(PtName(e), "Canceled") && s.Expired())
            child.Kill().Must()
            // A second kill inside the first one's exit is success
            // (std.sys ProcKill on Windows).
            child.Kill().Must()
            st := child.Wait(io.NoCancel()).Must()
            if SpWin() {
                Assert(SpCode(st) == 1)
            } else {
                Assert(st.code.IsNone() && st.signal.IsNone())
            }
            return
        }
        Assert(false)
    }
}

test "Child.Signal: Terminate ends a Linux child with signal Terminate and is Unsupported on Windows" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "sleep"]
    for group in [false, true] {
        var c := DefaultCommand
        c.args = a[..]
        c.group = group
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, stdout, stderr)
        if SpWin() {
            var failed := false
            child.Signal(Signal.Terminate) else e {
                failed = PtSame(PtName(e), "Unsupported")
            }
            Assert(failed)
            child.Kill().Must()
            Assert(SpCode(child.Wait(io).Must()) == 1)
        } else {
            child.Signal(Signal.Terminate).Must()
            st := child.Wait(io).Must()
            sig := st.signal else { Signal.Hangup }
            Assert(st.code.IsNone() && sig == Signal.Terminate)
        }
    }
}

// SpDrainWithin reads r to its end within ms milliseconds: whether the end
// came in time.
SpDrainWithin(io: Io, r: &PipeReader, ms: i64): bool {
    var b: [256]u8 = [0; 256]
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(ms))
        for {
            k := r.Read(io, mut b[..]) else {
                return false
            }
            if k == 0 {
                return true
            }
        }
    }
}

test "Group: when the leader ends, Wait kills the grandchild that holds its stdout, so the pipe ends at once" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "orphan"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdout = Stdio.Pipe
    c.group = true
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(stdin, none, stderr)
    r := stdout.Must()
    Assert(SpCode(child.Wait(io).Must()) == 3)
    // The grandchild sleeps 15 s holding the pipe; only the group's kill
    // ends the stream within 8.
    Assert(SpDrainWithin(io, &r, 8000))
    r.Close()
}

test "Group: Kill, and Signal on Linux, reach every process still in the group before Wait reaps the leader" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "family"]
    for how in 0..2 {
        if how == 1 && SpWin() {
            break
        }
        var c := DefaultCommand
        c.args = a[..]
        c.stdout = Stdio.Pipe
        c.group = true
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, none, stderr)
        r := stdout.Must()
        var b: [1]u8 = [0]
        Assert(r.Read(io, mut b[..]).Must() == 1 && b[0] == 111)
        if how == 0 {
            child.Kill().Must()
        } else {
            child.Signal(Signal.Terminate).Must()
        }
        // The grandchild sleeps 15 s holding stdout, so the stream ends
        // within 8 s, before Wait (which kills what remains), only when
        // the kill or the signal reached it.
        Assert(SpDrainWithin(io, &r, 8000))
        st := child.Wait(io).Must()
        if how == 0 {
            Assert(if SpWin() { SpCode(st) == 1 } else { st.code.IsNone() })
        } else {
            sig := st.signal else { Signal.Hangup }
            Assert(st.code.IsNone() && sig == Signal.Terminate)
        }
        r.Close()
    }
}

test "Child.Detach: a detached child runs on, and the runtime reaps it once it ends" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [6]str = [sys.args[0], sys.args[1], "gen", "0", "0", "0"]
    var c := DefaultCommand
    c.args = a[..]
    for group in [false, true] {
        c.group = group
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, stdout, stderr)
        child.Detach()
    }
    io.Sleep(time.Millis(500)) else {}
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(stdin, stdout, stderr)
    Assert(rt.DetachedLen() == 0)
    Assert(SpCode(child.Wait(io).Must()) == 0)
}

test "Child.Detach: a group leader detached runs on with its group, and both end on their own" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "stayfam"]
    var c := DefaultCommand
    c.args = a[..]
    c.stdin = Stdio.Pipe
    c.stdout = Stdio.Pipe
    c.group = true
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(none, none, stderr)
    w := stdin.Must()
    r := stdout.Must()
    var b: [1]u8 = [0]
    Assert(r.Read(io, mut b[..]).Must() == 1 && b[0] == 111)
    child.Detach()
    // The leader and its grandchild hold stdout and wait for stdin's end:
    // a read cut by a deadline shows neither ended (a group killed at
    // Detach would end the stream at once).
    io.Sleep(time.Millis(300)) else {}
    var cut := false
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(500))
        _ = r.Read(io, mut b[..]) else e {
            cut = PtSame(PtName(e), "Canceled")
        }
    }
    Assert(cut)
    // stdin's end: both exit, and the stream ends.
    w.Close()
    Assert(SpDrainWithin(io, &r, 8000))
    r.Close()
}

// SpPtyAll reads p into buf until it holds every string of wants, or buf
// is full, or the end, or 10 s pass: whether all came.
SpPtyAll(io: Io, p: &Pty, buf: mut []u8, wants: []str): bool {
    var n := 0
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(10000))
        for n < buf.Len() {
            k := p.Read(io, mut buf[n..]) else {
                return false
            }
            if k == 0 {
                return false
            }
            n += k
            var all := true
            for w in wants {
                if !SpHas(buf[0..n], w) {
                    all = false
                }
            }
            if all {
                return true
            }
        }
    }
    return false
}

// SpPtyErr is the error Pty.Open of cols by rows fails with; a terminal it
// opens instead is closed, and the block fails.
SpPtyErr(io: Io, cols: int, rows: int): Error {
    p := Pty.Open(io, cols, rows) else e {
        return e
    }
    p.Close()
    Panic("Pty.Open succeeded where it should have failed")
}

// SpPtyUntil reads p into buf until it holds want, or buf is full, or the
// end: whether want came.
SpPtyUntil(io: Io, p: &Pty, buf: mut []u8, want: str): bool {
    var n := 0
    for n < buf.Len() {
        k := p.Read(io, mut buf[n..]) else {
            return false
        }
        if k == 0 {
            return false
        }
        n += k
        if SpHas(buf[0..n], want) {
            return true
        }
    }
    return false
}

test "Pty: a child on a pseudo-terminal reads the line typed into it and writes back; Resize, a bad size and Close" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    Assert(SpIsOs(SpPtyErr(io, 0, 24), 0))
    Assert(SpIsOs(SpPtyErr(io, 80, 32768), 0))
    Assert(SpIsOs(SpPtyErr(io, -1, 24), 0))
    p := Pty.Open(io, 80, 24).Must()
    p.Resize(io, 100, 30).Must()
    p.Resize(io, 32767, 32767).Must()
    var failed := false
    p.Resize(io, 100, 40000) else e {
        failed = SpIsOs(e, 0)
    }
    Assert(failed)
    p.Resize(io, 80, 24).Must()
    var a: [3]str = [sys.args[0], sys.args[1], "ttyline"]
    var c := DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    Assert(stdin.IsNone() && stdout.IsNone() && stderr.IsNone())
    SpCloseEnds(stdin, stdout, stderr)
    p.WriteAll(io, "hello\r".AsBytes()).Must()
    var b: [8192]u8 = [0; 8192]
    Assert(SpPtyUntil(io, &p, mut b[..], "got:hello"))
    Assert(SpCode(child.Wait(io).Must()) == 0)
    p.Close()
}

test "Pty: Command.terminal makes the pseudo-terminal the child's stdin, stdout and stderr, and on Linux its controlling terminal" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    p := Pty.Open(io, 80, 24).Must()
    var a: [3]str = [sys.args[0], sys.args[1], "ttyall"]
    var c := DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(stdin, stdout, stderr)
    var wants: [3]str = ["tty:111", "err:ok", "ctl:ok"]
    k := if SpWin() { 2 } else { 3 }
    var b: [8192]u8 = [0; 8192]
    Assert(SpPtyAll(io, &p, mut b[..], wants[0..k]))
    p.WriteAll(io, "x\r".AsBytes()).Must()
    Assert(SpCode(child.Wait(io).Must()) == 0)
    p.Close()
}

test "Pty.Read: a deadline ends it with Canceled while the child waits for input, and the same read then returns its output (repeatable)" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    p := Pty.Open(io, 80, 24).Must()
    var a: [3]str = [sys.args[0], sys.args[1], "ttyline"]
    var c := DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
    SpCloseEnds(stdin, stdout, stderr)
    var b: [8192]u8 = [0; 8192]
    // The terminal's own start-up output (ConPTY's) is read first; the
    // child then waits for a line and writes nothing, so a read under a
    // deadline ends with Canceled.
    var cut := false
    for _ in 0..50 {
        scope s := io.Scope() {
            s.CancelAfter(time.Millis(200))
            _ = p.Read(io, mut b[..]) else e {
                cut = PtSame(PtName(e), "Canceled") && s.Expired()
            }
        }
        if cut {
            break
        }
    }
    Assert(cut)
    p.WriteAll(io, "after\r".AsBytes()).Must()
    Assert(SpPtyUntil(io, &p, mut b[..], "got:after"))
    Assert(SpCode(child.Wait(io).Must()) == 0)
    p.Close()
}

test "Releases in a cancelled scope: pipe ends, Kill, Detach and Pty.Close work and leave the handle count as it was" (sys: &System) {
    if sys.args.Len() > 2 {
        SpChild(sys)
    }
    io := sys.io
    var a: [3]str = [sys.args[0], sys.args[1], "sleep"]
    var c := DefaultCommand
    c.args = a[..]
    var e: [6]str = [sys.args[0], sys.args[1], "gen", "0", "0", "0"]
    var d := DefaultCommand
    d.args = e[..]
    // Rounds 0 and 1 warm up; rounds 2 to 17 are counted. A handle leaked
    // per round is 16 or more. The exact equality this replaced fails at
    // dev with 16 carriers by exactly 1 in about a quarter of runs, and
    // still by 1 over 20 counted rounds (measured 2026-11-13):
    // a handle the runtime makes once (a thread's, presumably), not one
    // per round, so the bound is 3.
    var h0 := 0
    for round in 0..18 {
        if round == 2 {
            h0 = PtHandles()
        }
        (r, w) := Pipe(io).Must()
        p := Pty.Open(io, 80, 24).Must()
        (child, stdin, stdout, stderr) := Spawn(sys.arena, io, &c).Must()
        SpCloseEnds(stdin, stdout, stderr)
        scope s := io.Scope() {
            s.Cancel()
            r.Close()
            w.Close()
            child.Kill().Must()
            child.Detach()
            p.Close()
        }
        io.Sleep(time.Millis(200)) else {}
        // The next spawn reaps the detached child on Linux.
        (child: g, stdin: gi, stdout: gout, stderr: ge) := Spawn(sys.arena, io, &d).Must()
        SpCloseEnds(gi, gout, ge)
        Assert(SpCode(g.Wait(io).Must()) == 0)
    }
    Assert(rt.DetachedLen() == 0)
    if PtHandles() >= 0 {
        Assert(PtHandles() - h0 <= 3)
    }
}
