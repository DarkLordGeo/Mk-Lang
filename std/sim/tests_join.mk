// Why: main, a native task the scheduler cannot see, may wait in a Join or
// a scope's limit gate while a task of the run is blocked on the network;
// when the joined tasks end main comes back and may still send what that
// task waits for, so the Sim must not call a deadlock there.
// What: blocks on main's Join and limit gate in several shapes (JnRun's
// modes), each against the same program over the native Io, the oracle.
// How: the Sim once called a deadlock there and the blocked Read ended
// Canceled: bytes sent after a Shutdown(Read) of a connection with a Read
// parked on it never reached the peer. Each seed runs twice.

import std.core
import std.net
import std.time

// JnServer accepts one connection and reads it to its end: got bytes;
// code 0 when it ended with the end of the stream, 5 Canceled, 6 Reset,
// 9 another failure, 1 when the accept failed.
JnServer(io: Io, l: &net.Listener, got: mut int, code: mut int) {
    s := l.Accept(io) else {
        *code = 1
        return
    }
    c := s.Conn()
    var b: [64]u8 = [0; 64]
    for {
        k := c.Read(io, mut b[..]) else e {
            *code = match e {
                Canceled { 5 }
                net.Reset { 6 }
                _ { 9 }
            }
            c.Abandon()
            return
        }
        if k == 0 {
            break
        }
        *got += k
    }
    *code = 0
    c.Abandon()
}

// JnPark reads c once: k is what it returned, -1 for a failure.
JnPark(io: Io, c: &net.Conn, k: mut int) {
    var b: [64]u8 = [0; 64]
    *k = c.Read(io, mut b[..]) else {
        -1
    }
}

// JnNap sleeps 1 ms: a task main's Join or gate waits for.
JnNap(io: Io) {
    io.Sleep(time.Millis(1)) else {}
}

// JnQuick returns at once: its end is all main's Join waits for.
JnQuick(io: Io) {
}

// JnAddr writes "127.0.0.1:<port>" into b and returns it.
JnAddr(b: mut [32]u8, port: int): str from b {
    pre := "127.0.0.1:".AsBytes()
    (mut b[0..pre.Len()]).CopyFrom(pre)
    var d: [8]u8 = [0; 8]
    var n := 0
    var x := port
    for {
        d[n] = Trunc[u8](48 + x % 10)
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..n {
        b[pre.Len() + i] = d[n - 1 - i]
    }
    return str.From(b[0..pre.Len() + n]).Must()
}

// JnRun: a server task reads a connection to its end while main, on the
// other end, joins a scope of mode's shape, then sends 5 bytes and
// closes. Mode 0: a task parks a Read, main calls Shutdown(Read) 5 ms
// later, which ends it with 0, and joins it (the first case seen). Mode 1: the
// same Shutdown(Read) with no Read parked. Mode 2: main joins a task that
// sleeps 1 ms. Mode 3: main spawns two such tasks into a scope of limit
// 1, so it waits in the gate for the first. Mode 4: main joins 8 tasks
// that return at once, in a scope of limit 16, so each end wakes main's
// Join and its next wait sends its note again while the others end.
// Mode 5: the same in a scope with no limit. Mode 6: main joins a task
// that sleeps 1 ms, then spawns into a scope of io.NoCancel(), another Io
// of the run, so its note owed to io stays owed beside the new one until
// its next wait. k is the parked Read's answer (-2 when none was
// parked); got and code the server's.
JnRun(io: Io, mode: int): net.Error!(k: int, got: int, code: int) {
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var ab: [32]u8 = [0; 32]
    addr := JnAddr(mut ab, l.Port())
    var got := 0
    var code := -1
    var k := -2
    scope outer := io.Scope() {
        outer.go JnServer(io, &l, mut got, mut code)
        stream := try net.Dial(io, addr)
        c := stream.Conn()
        if mode == 3 {
            scope g := io.ScopeWith(core.Limits(tasks: 1, stack: core.StackSize.Default)) {
                g.go JnNap(io)
                g.go JnNap(io)
            }
        } else if mode == 4 {
            scope q := io.ScopeWith(core.Limits(tasks: 16, stack: core.StackSize.Default)) {
                for _ in 0..8 {
                    q.go JnQuick(io)
                }
            }
        } else if mode == 5 {
            scope q := io.Scope() {
                for _ in 0..8 {
                    q.go JnQuick(io)
                }
            }
        } else if mode == 6 {
            scope s := io.Scope() {
                s.go JnNap(io)
            }
            scope h := io.NoCancel().Scope() {
                h.go JnQuick(io)
            }
        } else {
            scope s := io.Scope() {
                if mode == 0 {
                    s.go JnPark(io, &c, mut k)
                } else if mode == 2 {
                    s.go JnNap(io)
                }
                if mode <= 1 {
                    io.Sleep(time.Millis(5)) else {}
                    c.Shutdown(io, net.Shutdown.Read) else {}
                }
            }
        }
        hello: [5]u8 = [104, 101, 108, 108, 111]
        c.WriteAll(io, hello[..]) else {}
        c.Close(io) else {
            c.Abandon()
        }
    }
    return (k: k, got: got, code: code)
}

// JnSim is JnRun in a fresh Sim of seed: the server's (got, code) packed
// as got * 16 + code, and k, or -100 when the run failed.
JnSim(a: &Arena, seed: u64, mode: int): (k: int, v: int) {
    var sm := Sim.New(a, seed) else {
        return (k: -100, v: -100)
    }
    r := JnRun(sm.Io(), mode) else {
        sm.Free()
        return (k: -100, v: -100)
    }
    sm.Free()
    return (k: r.k, v: r.got * 16 + r.code)
}

// JnRec is JnSim with the program on a Recorder's Io over the Sim, whose
// scopes are another Io of the run (the Recorder forwards its hooks).
JnRec(a: &Arena, seed: u64, mode: int): (k: int, v: int) {
    var sm := Sim.New(a, seed) else {
        return (k: -100, v: -100)
    }
    var rec := Recorder.New(sm.Io()) else {
        sm.Free()
        return (k: -100, v: -100)
    }
    r := JnRun(rec.Io(), mode) else {
        rec.Free()
        sm.Free()
        return (k: -100, v: -100)
    }
    rec.Free()
    sm.Free()
    return (k: r.k, v: r.got * 16 + r.code)
}

test "under a Sim, main's Shutdown(Read) of a connection with a Read parked in a task it joins, then WriteAll and Close: the peer reads the 5 bytes and the end of the stream, for 8 seeds, each run twice with the same result" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for seed in 1..9 {
        r := JnSim(&ar, Trunc[u64](seed), 0)
        again := JnSim(&ar, Trunc[u64](seed), 0)
        Assert(r.k == 0 && r.v == 5 * 16)
        Assert(again.k == r.k && again.v == r.v)
    }
    ar.Free()
}

test "under a Sim, main joining a task that sleeps, or waiting in a limit gate for one, while the peer's Read is blocked: the peer reads the 5 bytes main sends next, as over the native Io, for 8 seeds, each run twice with the same result" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for mode in 1..4 {
        native := JnRun(sys.io, mode) else e {
            ar.Free()
            fail e
        }
        Assert(native.got == 5 && native.code == 0)
        for seed in 1..9 {
            r := JnSim(&ar, Trunc[u64](seed), mode)
            again := JnSim(&ar, Trunc[u64](seed), mode)
            Assert(r.k == -2 && r.v == native.got * 16 + native.code)
            Assert(again.k == r.k && again.v == r.v)
        }
    }
    ar.Free()
}

test "under a Sim, main joining 8 tasks that return at once, in a scope of limit 16 and in one with no limit, while the peer's Read is blocked: the peer reads the 5 bytes main sends next, as over the native Io, for 50 seeds, each run twice with the same result" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for mode in 4..6 {
        native := JnRun(sys.io, mode) else e {
            ar.Free()
            fail e
        }
        Assert(native.got == 5 && native.code == 0)
        for seed in 1..51 {
            r := JnSim(&ar, Trunc[u64](seed), mode)
            again := JnSim(&ar, Trunc[u64](seed), mode)
            Assert(r.k == -2 && r.v == native.got * 16 + native.code)
            Assert(again.k == r.k && again.v == r.v)
        }
    }
    ar.Free()
}

test "under a Sim, main joining a task, then spawning into a scope of io.NoCancel(), and main's Join and gate on a Recorder's Io over the Sim, while the peer's Read is blocked: the peer reads the 5 bytes main sends next, as over the native Io, for 50 seeds, each run twice with the same result" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for mode in 2..7 {
        native := JnRun(sys.io, mode) else e {
            ar.Free()
            fail e
        }
        Assert(native.got == 5 && native.code == 0)
        for seed in 1..51 {
            if mode == 6 {
                r := JnSim(&ar, Trunc[u64](seed), mode)
                again := JnSim(&ar, Trunc[u64](seed), mode)
                Assert(r.k == -2 && r.v == native.got * 16 + native.code)
                Assert(again.k == r.k && again.v == r.v)
            }
            q := JnRec(&ar, Trunc[u64](seed), mode)
            qa := JnRec(&ar, Trunc[u64](seed), mode)
            Assert(q.k == -2 && q.v == native.got * 16 + native.code)
            Assert(qa.k == q.k && qa.v == q.v)
        }
    }
    ar.Free()
}
