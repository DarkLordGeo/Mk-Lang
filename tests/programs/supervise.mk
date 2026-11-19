// proc.Supervise keeps serving while a worker panics (the cookbook recipe "Keep serving
// when a worker panics", with a client in the parent).
// Three workers serve one inherited
// listener; each answers a connection's one byte with "ok\n", and the byte 'p' makes the
// worker that reads it panic after it answers. The parent's client task asks 20 times, asks
// once with 'p', asks 100 times more while that worker restarts, then cancels the scope, so
// Supervise returns Canceled once every worker has exited; then the listener is closed and a
// dial must be refused. Prints the answers counted, Supervise's result and the dial's; the
// panicking worker's report is on stderr.
//
// A worker runs Main again from the start. It tells itself apart with proc.IsWorker.
import std.net
import std.proc

Worker(sys: &System, l: &net.Listener): net.Error!void {
    for {
        conn := try l.Accept(sys.io)
        defer conn.Close()
        var q: [1]u8 = [0]
        n := try conn.Read(sys.io, mut q[..])
        try conn.WriteAll(sys.io, "ok\n".AsBytes())
        if n == 1 && q[0] == 112 {
            Panic("worker panic on request")
        }
    }
}

// Ask dials addr, sends cmd and reads the answer to its end: 1 when it is "ok\n".
Ask(io: Io, addr: str, cmd: u8): int {
    c := net.Dial(io, addr) else {
        return 0
    }
    defer c.Close()
    var q: [1]u8 = [cmd]
    c.WriteAll(io, q[..]) else {
        return 0
    }
    var got: [8]u8 = [0; 8]
    var n := 0
    for n < 8 {
        k := c.Read(io, mut got[n..]) else {
            break
        }
        if k == 0 {
            break
        }
        n += k
    }
    if n == 3 && got[0] == 111 && got[1] == 107 && got[2] == 10 {
        return 1
    }
    return 0
}

Client(s: &Scope, io: Io, addr: str, ok: mut int) {
    for _ in 0..20 {
        *ok += Ask(io, addr, 105)
    }
    *ok += Ask(io, addr, 112)
    for _ in 0..100 {
        *ok += Ask(io, addr, 105)
    }
    s.Cancel()
}

Refused(io: Io, addr: str): bool {
    c := net.Dial(io, addr) else e {
        return match e {
            net.Refused { true }
            _ { false }
        }
    }
    c.Close()
    return false
}

struct Outcome {
    ok: int
    got: str
    addr: str
}

// Parent runs the client beside Supervise and returns what it saw.
Parent(sys: &System, l: &net.Listener): OutOfMemory!Outcome from sys {
    p := l.Port()
    addr := try sys.arena.Fmt("127.0.0.1:{p}", (p))
    var ok := 0
    var got := "returned"
    scope s := sys.io.Scope() {
        s.go Client(&s, sys.io, addr, mut ok)
        proc.Supervise[net.Error](sys, 3, l, Worker) else e {
            got = match e {
                Canceled { "Canceled" }
                _ { "failed" }
            }
        }
    }
    return Outcome(ok: ok, got: got, addr: addr)
}

export Main(sys: &System): net.Error|proc.Error|WriteError|OutOfMemory!void {
    l := try net.Listen(sys.io, "127.0.0.1:0")
    if proc.IsWorker(sys) {
        defer l.Close()
        try proc.Supervise[net.Error](sys, 3, &l, Worker)
        return
    }
    r := Parent(sys, &l) else {
        l.Close()
        proc.Exit(3)
    }
    l.Close()
    var after := "served"
    if Refused(sys.io, r.addr) {
        after = "refused"
    }
    var out := sys.Stdout()
    try Print(mut out, "answered {ok} of 121\nsupervise {got}\nafter close {after}\n", (ok: r.ok, got: r.got, after))
}
