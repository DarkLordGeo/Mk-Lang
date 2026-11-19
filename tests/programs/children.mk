// std.proc: the program spawns
// itself, by its absolute path, as its own child, so no other program is needed. A child with three pipes reads its
// input to the end and answers in upper case with an exit code; a child in another directory
// with an extended environment; a child whose output goes into a Pipe the parent made; a child
// in its own process group, with a grandchild in that group, killed while both sleep, the
// grandchild's end seen on a pipe before any Wait; a Wait cut by a deadline and called again; a
// kill that failed, which detaches the child; proc.Run; and a program that does not exist. A
// status is printed as whether the child ended on its own, since a kill reads as a signal on
// Linux and as exit code 1 on Windows.
import std.fs
import std.proc
import std.time

// Upper is the child's "upper" mode: it reads its input to the end, writes it in upper case,
// reports on standard error, and exits with code 3.
Upper(sys: &System): WriteError!void {
    var buf: [512]u8 = [0; 512]
    var input := sys.Stdin()
    n := input.ReadAll(mut buf[..]) else {
        proc.Exit(4)
    }
    for i in 0..n {
        if buf[i] >= b'a' && buf[i] <= b'z' {
            buf[i] -= 32
        }
    }
    var out := sys.Stdout()
    try out.Bytes(buf[..n])
    try out.Flush()
    try sys.PrintErr("read {n} bytes\n", (n: n))
    proc.Exit(3)
}

// Where is the child's "env" mode: it prints the greeting its parent set and whether it runs
// in a directory named work.
Where(sys: &System): WriteError!void {
    greeting := sys.Env("MK_CHILD_GREETING") else {
        "(unset)"
    }
    cwd := fs.Cwd(sys.arena, sys.io) else {
        "?"
    }
    try sys.Print("greeting {greeting}, in work {w}\n", (greeting: greeting, w: cwd.EndsWith("work")))
}

// Ended says how a child ended: its code, or that a signal or a kill ended it.
Ended(s: proc.Status): str {
    c := s.code else {
        return "by a signal"
    }
    return if c == 0 { "with code 0" } else if c == 3 { "with code 3" } else { "with another code" }
}

// Drain reads r to its end into buf and returns how many bytes it read.
Drain(io: Io, r: &proc.PipeReader, buf: mut []u8): proc.Error!int {
    var n := 0
    for n < buf.Len() {
        got := try r.Read(io, mut buf[n..])
        if got == 0 {
            break
        }
        n += got
    }
    return n
}

// Describe formats what failed and why, in a.
Describe(a: &Arena, what: str, e: proc.Error): str from a {
    return a.Fmt("{what}: {e}", (what: what, e: e)) else {
        "(out of memory)"
    }
}

// Text is the bytes of buf as text.
Text(buf: []u8): str from buf {
    return str.From(buf) else {
        "(not UTF-8)"
    }
}

// Finish waits for child and says how it ended; a Wait that fails detaches it.
Finish(a: &Arena, io: Io, child: proc.Child): str from a {
    status := child.Wait(io) else e {
        child.Detach()
        return Describe(a, "wait", e)
    }
    return Ended(status)
}

// AfterKill waits for a killed child and says whether it ended on its own: a kill reads as a
// signal on Linux and as exit code 1 on Windows, so any other ending is "not on its own".
AfterKill(a: &Arena, io: Io, child: proc.Child): str from a {
    status := child.Wait(io) else e {
        child.Detach()
        return Describe(a, "wait", e)
    }
    c := status.code else {
        return "not on its own"
    }
    return if c == 0 { "on its own" } else { "not on its own" }
}

// Piped runs the "upper" child with three pipes: it writes the child's input and closes it,
// reads both outputs to their ends, and waits.
Piped(sys: &System, a: &Arena, exe: str): str from a {
    io := sys.io
    args := [exe, "upper"]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.stdin = proc.Stdio.Pipe
    c.stdout = proc.Stdio.Pipe
    c.stderr = proc.Stdio.Pipe
    (child, stdin, stdout, stderr) := proc.Spawn(a, io, &c) else e {
        return Describe(a, "spawn", e)
    }
    var trouble := ""
    if w := stdin {
        w.WriteAll(io, "piped through a child\n"[..].AsBytes()) else e {
            trouble = Describe(a, "write", e)
        }
        w.Close()
    }
    var out_buf: [512]u8 = [0; 512]
    var n := 0
    if r := stdout {
        n = Drain(io, &r, mut out_buf[..]) else e {
            trouble = Describe(a, "read stdout", e)
            0
        }
        r.Close()
    }
    var err_buf: [128]u8 = [0; 128]
    var m := 0
    if r := stderr {
        m = Drain(io, &r, mut err_buf[..]) else e {
            trouble = Describe(a, "read stderr", e)
            0
        }
        r.Close()
    }
    ended := Finish(a, io, child)
    return a.Fmt("pipes: stdout {o}, stderr {r}, ended {ended}{trouble}", (o: Text(out_buf[..n]).Trim(), r: Text(err_buf[..m]).Trim(), ended: ended, trouble: trouble)) else {
        "(out of memory)"
    }
}

// Moved runs the "env" child in the directory work with an extended environment, its output
// into a Pipe the parent made.
Moved(sys: &System, a: &Arena, exe: str): str from a {
    io := sys.io
    fs.MakeDirAll(io, "work") else e {
        return a.Fmt("make work: {e}", (e: e)) else {
            "(out of memory)"
        }
    }
    (r, w) := proc.Pipe(io) else e {
        return Describe(a, "pipe", e)
    }
    args := [exe, "env"]
    vars := ["MK_CHILD_GREETING=hello"]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.env = proc.Env.Extend(vars: vars[..])
    c.dir = some("work")
    c.stdout = proc.Stdio.Writer(w: &w)
    match proc.Spawn(a, io, &c) {
        ok(spawned) {
            (child, stdin, stdout, stderr) := spawned
            CloseReader(stdout)
            CloseReader(stderr)
            if x := stdin {
                x.Close()
            }
            w.Close()
            var buf: [256]u8 = [0; 256]
            n := Drain(io, &r, mut buf[..]) else e {
                r.Close()
                _ = Finish(a, io, child)
                return Describe(a, "read", e)
            }
            r.Close()
            ended := Finish(a, io, child)
            return a.Fmt("environment, directory and a lent pipe: {t}, ended {ended}", (t: Text(buf[..n]).Trim(), ended: ended)) else {
                "(out of memory)"
            }
        }
        err(e) {
            w.Close()
            r.Close()
            return Describe(a, "spawn", e)
        }
    }
}

// CloseReader closes r when there is one.
CloseReader(r: ?proc.PipeReader) {
    if x := r {
        x.Close()
    }
}

// WaitBriefly waits for child under a 20 ms deadline. It returns the child when the wait was
// cut, with what the wait said and whether the deadline expired.
WaitBriefly(a: &Arena, io: Io, child: proc.Child): (child: ?proc.Child, cut: str, expired: bool) from a {
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        status := child.Wait(io) else e {
            return (child: some(child), cut: Describe(a, "wait", e), expired: s.Expired())
        }
        return (child: none, cut: Ended(status), expired: s.Expired())
    }
}

// Leaf is the child's "sleep" mode: it says ready on its standard output, then sleeps 120 s, longer
// than the case's timeout and short enough that a child no kill reached does not outlive the
// run by long.
Leaf(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try out.Bytes("ready\n"[..].AsBytes())
    try out.Flush()
    sys.io.Sleep(time.Seconds(120)) else {
    }
}

// Nest is the child's "nest" mode: it starts its own "sleep" child, which inherits its standard
// streams and its process group, and waits for it.
Nest(sys: &System): WriteError!void {
    io := sys.io
    args := [sys.args[0], "sleep"]
    var c := proc.DefaultCommand
    c.args = args[..]
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else e {
        try sys.Print("nest spawn: {e}\n", (e: e))
        proc.Exit(5)
    }
    if x := stdin {
        x.Close()
    }
    CloseReader(stdout)
    CloseReader(stderr)
    _ = Finish(sys.arena, io, child)
}

// FirstLine reads r into buf until a newline, the end, an error or 10 s, and returns how many
// bytes it read.
FirstLine(io: Io, r: &proc.PipeReader, buf: mut []u8): int {
    var n := 0
    scope s := io.Scope() {
        s.CancelAfter(time.Seconds(10))
        for n < buf.Len() {
            got := r.Read(io, mut buf[n..]) else {
                break
            }
            if got == 0 {
                break
            }
            n += got
            if buf[n - 1] == b'\n' {
                break
            }
        }
    }
    return n
}

// Closed reads r until its end, under a 5 s deadline, and says whether the end came: it comes
// only once every process holding the pipe's write end has ended.
Closed(a: &Arena, io: Io, r: &proc.PipeReader): str from a {
    var buf: [64]u8 = [0; 64]
    scope s := io.Scope() {
        s.CancelAfter(time.Seconds(5))
        for {
            got := r.Read(io, mut buf[..]) else e {
                return Describe(a, "read", e)
            }
            if got == 0 {
                return "true"
            }
        }
    }
}

// Settle ends a kill. When the kill failed, the child is detached and the failure returned,
// since a Wait would wait for a child no kill reached (C8-3); otherwise it waits for the child.
Settle(a: &Arena, io: Io, child: proc.Child, failed: ?proc.Error): str from a {
    if e := failed {
        child.Detach()
        return Describe(a, "kill", e)
    }
    return AfterKill(a, io, child)
}

// Killed starts the "nest" child in its own process group, its output into a pipe, and waits
// until the nest's own "sleep" child says ready on it. It cuts a Wait on the nest with a 20 ms
// deadline, kills the group, and reads the pipe to its end before any Wait: the end comes only
// when the grandchild died with the group (C8-4), since Wait kills what remains of a group
// itself. Then it waits again.
Killed(sys: &System, a: &Arena, exe: str): str from a {
    io := sys.io
    args := [exe, "nest"]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.stdout = proc.Stdio.Pipe
    c.group = true
    (child, stdin, stdout, stderr) := proc.Spawn(a, io, &c) else e {
        return Describe(a, "spawn", e)
    }
    if x := stdin {
        x.Close()
    }
    CloseReader(stderr)
    if r := stdout {
        return Group(a, io, child, r)
    }
    child.Kill() else e {
        return Settle(a, io, child, some(e))
    }
    _ = Settle(a, io, child, none)
    return "process group: no pipe for the nest's output"
}

// Group is Killed once the nest runs with r its output.
Group(a: &Arena, io: Io, child: proc.Child, r: proc.PipeReader): str from a {
    var buf: [64]u8 = [0; 64]
    n := FirstLine(io, &r, mut buf[..])
    said := Text(buf[..n]).Trim()
    (child: kept, cut, expired) := WaitBriefly(a, io, child)
    var closed := "(the nest had ended)"
    var ended := "already ended"
    if k := kept {
        var failed: ?proc.Error = none
        k.Kill() else e {
            failed = some(e)
        }
        if failed.IsNone() {
            closed = Closed(a, io, &r)
        }
        ended = Settle(a, io, k, failed)
    }
    r.Close()
    return a.Fmt("process group: the grandchild said {said}; wait under a deadline {cut}, expired {expired}; after a kill, the group's pipe ended {closed}, the nest ended {ended}", (said: said, cut: cut, expired: expired, closed: closed, ended: ended)) else {
        "(out of memory)"
    }
}

// FailedKill is C8-3's regression: a kill that failed leaves the child detached and names the
// failure, rather than waiting for a child no kill reached. The child is the "upper" mode with
// nothing to read, so it ends at once whatever happens.
FailedKill(sys: &System, a: &Arena, exe: str): str from a {
    io := sys.io
    args := [exe, "upper"]
    var c := proc.DefaultCommand
    c.args = args[..]
    c.stdin = proc.Stdio.Null
    c.stdout = proc.Stdio.Null
    c.stderr = proc.Stdio.Null
    (child, stdin, stdout, stderr) := proc.Spawn(a, io, &c) else e {
        return Describe(a, "spawn", e)
    }
    if x := stdin {
        x.Close()
    }
    CloseReader(stdout)
    CloseReader(stderr)
    settled := Settle(a, io, child, some(proc.Unsupported))
    return a.Fmt("a kill that failed: {settled}", (settled: settled)) else {
        "(out of memory)"
    }
}

// Missing spawns a program that does not exist, and runs the "upper" child once with proc.Run.
Missing(sys: &System, a: &Arena, exe: str): str from a {
    io := sys.io
    gone := ["mk-no-such-program-anywhere"]
    var c := proc.DefaultCommand
    c.args = gone[..]
    var missing := "spawned"
    match proc.Spawn(a, io, &c) {
        ok(spawned) {
            (child, stdin, stdout, stderr) := spawned
            if x := stdin {
                x.Close()
            }
            CloseReader(stdout)
            CloseReader(stderr)
            _ = Finish(a, io, child)
        }
        err(e) {
            missing = Describe(a, "spawn", e)
        }
    }
    args := [exe, "upper"]
    out := proc.Run(a, io, args[..], "run once\n"[..].AsBytes(), 4_096) else e {
        return Describe(a, "run", e)
    }
    return a.Fmt("a missing program: {missing}; run: code {c}, stdout {o}, stderr {r}", (missing: missing, c: out.code, o: Text(out.stdout).Trim(), r: Text(out.stderr).Trim())) else {
        "(out of memory)"
    }
}

export Main(sys: &System): WriteError!void {
    if sys.args.Len() > 1 {
        match sys.args[1] {
            "upper" {
                try Upper(sys)
            }
            "env" {
                try Where(sys)
            }
            "sleep" {
                try Leaf(sys)
            }
            "nest" {
                try Nest(sys)
            }
            _ {
                try sys.PrintErr("unknown mode {m}\n", (m: sys.args[1]))
                proc.Exit(2)
            }
        }
        return
    }
    a := sys.arena
    exe := fs.Absolute(a, sys.io, sys.args[0]) else e {
        try sys.Print("absolute: {e}\n", (e: e))
        return
    }
    for line in [Piped(sys, a, exe), Moved(sys, a, exe), Killed(sys, a, exe), FailedKill(sys, a, exe), Missing(sys, a, exe)] {
        try sys.Print("{line}\n", (line: line))
    }
}
