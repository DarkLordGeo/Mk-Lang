// Why: a program that owns its listener must read its port, serve it from a
// task and serve it again after a cancel.
// What: ServeOn under a Sim and over the machine's loopback.
// How: a listener on port 0 whose port the caller reads before serving; the
// peers are SwHeadClient's raw connections (tests_servewith.mk).

import std.net
import std.raw

// SoServe runs ServeOn over l, the server a task of the caller runs:
// out becomes 1 when it returned with no error (at the scope's cancel), 2
// when it failed.
SoServe(io: Io, h: &Heap, l: &net.Listener, o: &ServerOptions, out: &raw.Atomic[u32]) {
    app := SwApp(n: 0)
    ServeOn(h, io, l, o, &app, SwEcho) else {
        out.Store(2)
        return
    }
    out.Store(1)
}

// SoRound serves l from a task while k clients in turn dial addr and send
// a request each, then cancels the server: the number of 200 replies, or
// -1 when ServeOn did not return cleanly at the cancel.
SoRound(io: Io, h: &Heap, l: &net.Listener, addr: str, k: int): int {
    o := DefaultServerOptions
    out := raw.Atomic[u32].New(0)
    var ok := 0
    scope top := io.Scope() {
        top.go SoServe(io, h, l, &o, &out)
        for _ in 0..k {
            if SwHeadClient(io, addr, 10) == 200 {
                ok += 1
            }
        }
        top.Cancel()
    }
    // Edge: the scope's join orders SoServe's Store before this Load.
    if out.Load() != 1 {
        return -1
    }
    return ok
}

// SoPortZero listens on port 0, reads the port it got, and serves two
// rounds over the one listener: two connections, then one more after the
// first ServeOn returned.
SoPortZero(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:0") else {
        return false
    }
    port := l.Port()
    var ab: [64]u8 = [0; 64]
    addr := ThAddr(mut ab, "127.0.0.1:", port)
    a := SoRound(io, h, &l, addr, 2)
    b := SoRound(io, h, &l, addr, 1)
    l.Close()
    return port > 0 && a == 2 && b == 1
}

test "ServeOn serves a listener on port 0 whose port the caller read, from a task, and leaves it open for a second ServeOn" (sys: &System) {
    Assert(CtSim(sys, 9, SoPortZero))
}

test "ServeOn runs in a task of a program's scope over the machine's loopback, with sys.io and sys.heap where ServeWith needs &System" (sys: &System) {
    Assert(SoPortZero(sys.io, sys.heap))
}
