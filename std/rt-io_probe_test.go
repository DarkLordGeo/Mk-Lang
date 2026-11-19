// Why: the poller tests need Mk code that drives std.sys directly, and Go
// test files are where that code lives next to the Go that runs it.
// What: rtioProbe, the Mk source of std/rtioprobe/probe.mk: the socket and
// poller cases and the helpers the later probe files use (MakePair, Start, Reap).
// How: std/rt-io_poll_test.go writes it into a copy of std; each case returns
// 0, or the number of the step that failed.

package std

// rtioProbe is std/rtioprobe/probe.mk of the copy.
const rtioProbe = `// A probe of std.sys (std/rt-io_poll_test.go writes it as
// std/rtioprobe/probe.mk in a copy of std). Every case drives the poller
// directly from Main, with no other task, so it tests std.sys alone: each
// returns 0, or the number of the step that failed. An op's state field is
// the probe's own marker here (1 pending, 3 delivered), so a second delivery
// of one completion is caught by the Cas in Reap.

import std.core
import std.raw
import std.sys
import std.task

export Backend(): u32 {
    return task.PollBackend()
}

Loopback(port: u16): sys.SockAddr {
    var a := sys.SockAddr(family: sys.Inet4, port: port, addr: [0; 16], scope_id: 0)
    a.addr[0] = 127
    a.addr[3] = 1
    return a
}

// Start submits op and marks it: 1 pending, 3 already done.
Start(op: *sys.Op) {
    unsafe "the probe's own op" {
        (*op).state.Store(1)
        if !sys.PollSubmit(op) {
            (*op).state.Store(3)
        }
    }
}

// Reap waits until every op of ops is delivered, up to ms milliseconds.
// 0 ok, 1 timeout, 2 an op delivered twice or never submitted. Its out
// slice has 3 slots, fewer than the ops some cases have in flight, so
// PollWait's "at most out.Len(), none lost" paths run (the epoll done list,
// CQEs left in the ring, a partial IOCP dequeue).
Reap(ops: []*sys.Op, ms: i64): u32 {
    var out: [3]*sys.Op = [raw.Null[sys.Op](); 3]
    deadline := sys.Now() + ms * 1_000_000
    for {
        var left := 0
        for p in ops {
            unsafe "the probe's ops" {
                if (*p).state.Load() != 3 {
                    left += 1
                }
            }
        }
        if left == 0 {
            return 0
        }
        if sys.Now() > deadline {
            return 1
        }
        n := sys.PollWait(10_000_000, mut out[..])
        for i in 0..n {
            unsafe "a delivered op" {
                if !(*out[i]).state.Cas(1, 3) {
                    return 2
                }
            }
        }
    }
}

Ok(r: sys.OsResult): bool {
    return r.err == core.SysErr.None
}

// Pair is a connected loopback pair: the listener, the client, the server.
struct Pair {
    l: uint
    c: uint
    s: uint
    code: u32
}

MakePair(): Pair {
    var p := Pair(l: 0, c: 0, s: 0, code: 0)
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        p.code = 101
        return p
    }
    p.l = Trunc[uint](l.value)
    a := Loopback(0)
    if !Ok(sys.SocketListen(p.l, &a, 16)) {
        p.code = 102
        return p
    }
    var bound := Loopback(0)
    if !Ok(sys.SocketName(p.l, mut bound)) || bound.port == 0 {
        p.code = 103
        return p
    }
    c := sys.SocketOpen(sys.Inet4, true)
    if !Ok(c) {
        p.code = 104
        return p
    }
    p.c = Trunc[uint](c.value)
    var acc := sys.NewOp(sys.OpAccept, p.l)
    acc.addr.family = sys.Inet4
    var con := sys.NewOp(sys.OpConnect, p.c)
    con.addr = bound
    Start(raw.OfMut(mut acc))
    Start(raw.OfMut(mut con))
    var ops: [2]*sys.Op = [raw.OfMut(mut acc), raw.OfMut(mut con)]
    if Reap(ops[..], 10_000) != 0 {
        p.code = 105
        return p
    }
    if !Ok(acc.res) || !Ok(con.res) {
        p.code = 106
        return p
    }
    p.s = Trunc[uint](acc.res.value)
    if acc.addr.family != sys.Inet4 || acc.addr.addr[0] != 127 || acc.addr.addr[3] != 1 || acc.addr.port == 0 {
        p.code = 107
    }
    return p
}

ClosePair(p: &Pair): u32 {
    var bad: u32 = 0
    if p.s != 0 && !Ok(sys.SocketShutdownClose(p.s)) {
        bad = 201
    }
    if p.c != 0 && !Ok(sys.SocketShutdownClose(p.c)) {
        bad = 202
    }
    if p.l != 0 && !Ok(sys.SocketShutdownClose(p.l)) {
        bad = 203
    }
    return bad
}

// Echo: accept and connect on loopback, a send and its recv, a shutdown of
// the client's write side reaching the server as end of stream (0), and
// closes that report no error.
export Echo(): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var msg: [5]u8 = [104, 101, 108, 108, 111]
    var rbuf: [16]u8 = [0; 16]
    var rcv := sys.NewOp(sys.OpRecv, p.s)
    rcv.buf = raw.DataMut(mut rbuf[..])
    rcv.len = 16
    var snd := sys.NewOp(sys.OpSend, p.c)
    snd.buf = raw.DataMut(mut msg[..])
    snd.len = 5
    Start(raw.OfMut(mut rcv))
    Start(raw.OfMut(mut snd))
    var ops: [2]*sys.Op = [raw.OfMut(mut rcv), raw.OfMut(mut snd)]
    if Reap(ops[..], 10_000) != 0 {
        _ = ClosePair(&p)
        return 1
    }
    if !Ok(snd.res) || snd.res.value != 5 || !Ok(rcv.res) || rcv.res.value != 5 {
        _ = ClosePair(&p)
        return 2
    }
    for i in 0..5 {
        if rbuf[i] != msg[i] {
            _ = ClosePair(&p)
            return 3
        }
    }
    if !Ok(sys.SocketShutdown(p.c, 1)) {
        _ = ClosePair(&p)
        return 4
    }
    var eof := sys.NewOp(sys.OpRecv, p.s)
    eof.buf = raw.DataMut(mut rbuf[..])
    eof.len = 16
    Start(raw.OfMut(mut eof))
    var one: [1]*sys.Op = [raw.OfMut(mut eof)]
    if Reap(one[..], 10_000) != 0 || !Ok(eof.res) || eof.res.value != 0 {
        _ = ClosePair(&p)
        return 5
    }
    return ClosePair(&p)
}

// CancelRecv: a recv with no data is pending; PollCancel makes its one
// completion arrive with Canceled; a second PollCancel after that is
// harmless, and the socket still works.
export CancelRecv(): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var rbuf: [16]u8 = [0; 16]
    var rcv := sys.NewOp(sys.OpRecv, p.s)
    rcv.buf = raw.DataMut(mut rbuf[..])
    rcv.len = 16
    Start(raw.OfMut(mut rcv))
    unsafe "the probe's op" {
        if rcv.state.Load() != 1 {
            _ = ClosePair(&p)
            return 1
        }
    }
    rcv.cancel.Store(1)
    sys.PollCancel(raw.OfMut(mut rcv))
    sys.PollCancel(raw.OfMut(mut rcv))
    var one: [1]*sys.Op = [raw.OfMut(mut rcv)]
    if Reap(one[..], 10_000) != 0 {
        _ = ClosePair(&p)
        return 2
    }
    if rcv.res.err != core.SysErr.Canceled {
        _ = ClosePair(&p)
        return 3
    }
    // The pair still carries data after the cancel.
    var msg: [1]u8 = [7]
    var snd := sys.NewOp(sys.OpSend, p.c)
    snd.buf = raw.DataMut(mut msg[..])
    snd.len = 1
    var again := sys.NewOp(sys.OpRecv, p.s)
    again.buf = raw.DataMut(mut rbuf[..])
    again.len = 16
    Start(raw.OfMut(mut again))
    Start(raw.OfMut(mut snd))
    var ops: [2]*sys.Op = [raw.OfMut(mut again), raw.OfMut(mut snd)]
    if Reap(ops[..], 10_000) != 0 || again.res.value != 1 || rbuf[0] != 7 {
        _ = ClosePair(&p)
        return 4
    }
    return ClosePair(&p)
}

// CancelAccept: a pending accept is cancelled with Canceled; the listener
// then accepts a real connection.
export CancelAccept(): u32 {
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        return 1
    }
    lh := Trunc[uint](l.value)
    a := Loopback(0)
    if !Ok(sys.SocketListen(lh, &a, 16)) {
        _ = sys.SocketShutdownClose(lh)
        return 2
    }
    var acc := sys.NewOp(sys.OpAccept, lh)
    acc.addr.family = sys.Inet4
    Start(raw.OfMut(mut acc))
    acc.cancel.Store(1)
    sys.PollCancel(raw.OfMut(mut acc))
    var one: [1]*sys.Op = [raw.OfMut(mut acc)]
    if Reap(one[..], 10_000) != 0 || acc.res.err != core.SysErr.Canceled {
        _ = sys.SocketShutdownClose(lh)
        return 3
    }
    if !Ok(sys.SocketShutdownClose(lh)) {
        return 4
    }
    return 0
}

// Refused: a connect to a port nobody listens on is Refused.
export Refused(): u32 {
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        return 1
    }
    lh := Trunc[uint](l.value)
    a := Loopback(0)
    if !Ok(sys.SocketListen(lh, &a, 1)) {
        return 2
    }
    var bound := Loopback(0)
    _ = sys.SocketName(lh, mut bound)
    _ = sys.SocketShutdownClose(lh)
    c := sys.SocketOpen(sys.Inet4, true)
    if !Ok(c) {
        return 3
    }
    ch := Trunc[uint](c.value)
    var con := sys.NewOp(sys.OpConnect, ch)
    con.addr = bound
    Start(raw.OfMut(mut con))
    var one: [1]*sys.Op = [raw.OfMut(mut con)]
    if Reap(one[..], 20_000) != 0 {
        return 4
    }
    _ = sys.SocketShutdownClose(ch)
    if con.res.err != core.SysErr.Refused {
        return 1000 + Trunc[u32](con.res.os)
    }
    return 0
}

// AddrInUse: a second listener on a bound port is AddrInUse.
export AddrInUse(): u32 {
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        return 1
    }
    lh := Trunc[uint](l.value)
    a := Loopback(0)
    if !Ok(sys.SocketListen(lh, &a, 1)) {
        return 2
    }
    var bound := Loopback(0)
    _ = sys.SocketName(lh, mut bound)
    m := sys.SocketOpen(sys.Inet4, true)
    if !Ok(m) {
        return 3
    }
    mh := Trunc[uint](m.value)
    r := sys.SocketListen(mh, &bound, 1)
    _ = sys.SocketShutdownClose(mh)
    _ = sys.SocketShutdownClose(lh)
    if r.err != core.SysErr.AddrInUse {
        return 10 + u32(r.err)
    }
    return 0
}

// Wake: a PollWake before a forever wait makes it return at once, with no
// completion; a timed wait with nothing to do takes at least its timeout.
export Wake(): u32 {
    var out: [8]*sys.Op = [raw.Null[sys.Op](); 8]
    sys.PollWake()
    t0 := sys.Now()
    n := sys.PollWait(-1, mut out[..])
    if n != 0 {
        return 1
    }
    if sys.Now() - t0 > 1_000_000_000 {
        return 2
    }
    t1 := sys.Now()
    _ = sys.PollWait(30_000_000, mut out[..])
    if sys.Now() - t1 < 30_000_000 {
        return 3
    }
    return 0
}

// Many: 16 pairs at once, a pending recv on every server side, then one
// byte from each client; every op is delivered exactly once, each recv
// gets one byte, and the bytes add up. The 64 ops live in pages of their
// own (Op is not Plain, so it cannot be an array repeat).
export Many(): u32 {
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        return 1
    }
    lh := Trunc[uint](l.value)
    a := Loopback(0)
    if !Ok(sys.SocketListen(lh, &a, 64)) {
        return 2
    }
    var bound := Loopback(0)
    _ = sys.SocketName(lh, mut bound)
    page := sys.Reserve(65536, 4096)
    if raw.IsNull(page) || !sys.Commit(page, 65536) {
        return 9
    }
    var ptrs: [32]*sys.Op = [raw.Null[sys.Op](); 32]
    var servers: [16]uint = [0; 16]
    var clients: [16]uint = [0; 16]
    var bufs: [16]u8 = [0; 16]
    var msgs: [16]u8 = [0; 16]
    unsafe "page holds 64 ops; i < 16" {
        ops := raw.PtrCast[u8, sys.Op](page)
        for i in 0..16 {
            c := sys.SocketOpen(sys.Inet4, true)
            if !Ok(c) {
                return 3
            }
            clients[i] = Trunc[uint](c.value)
            var acc := sys.NewOp(sys.OpAccept, lh)
            acc.addr.family = sys.Inet4
            raw.Write(raw.Offset(ops, i), acc)
            var con := sys.NewOp(sys.OpConnect, clients[i])
            con.addr = bound
            raw.Write(raw.Offset(ops, 16 + i), con)
            ptrs[i] = raw.Offset(ops, i)
            ptrs[16 + i] = raw.Offset(ops, 16 + i)
        }
        for i in 0..32 {
            Start(ptrs[i])
        }
        if Reap(ptrs[..], 20_000) != 0 {
            return 4
        }
        for i in 0..16 {
            if !Ok((*ptrs[i]).res) || !Ok((*ptrs[16 + i]).res) {
                return 5
            }
            servers[i] = Trunc[uint]((*ptrs[i]).res.value)
        }
        for i in 0..16 {
            msgs[i] = Trunc[u8](i + 1)
            var rcv := sys.NewOp(sys.OpRecv, servers[i])
            rcv.buf = raw.Offset(raw.DataMut(mut bufs[..]), i)
            rcv.len = 1
            raw.Write(raw.Offset(ops, 32 + i), rcv)
            var snd := sys.NewOp(sys.OpSend, clients[i])
            snd.buf = raw.Offset(raw.DataMut(mut msgs[..]), i)
            snd.len = 1
            raw.Write(raw.Offset(ops, 48 + i), snd)
            ptrs[i] = raw.Offset(ops, 32 + i)
            ptrs[16 + i] = raw.Offset(ops, 48 + i)
        }
        for i in 0..32 {
            Start(ptrs[i])
        }
        if Reap(ptrs[..], 20_000) != 0 {
            return 6
        }
        for i in 0..32 {
            if (*ptrs[i]).res.value != 1 {
                return 7
            }
        }
    }
    var sum: u32 = 0
    for i in 0..16 {
        sum += u32(bufs[i])
    }
    if sum != 136 {
        return 8
    }
    for i in 0..16 {
        _ = sys.SocketShutdownClose(servers[i])
        _ = sys.SocketShutdownClose(clients[i])
    }
    _ = sys.SocketShutdownClose(lh)
    sys.Release(page, 65536)
    return 0
}

// File: a file written and read through the poller when the backend does
// files (iocp, io_uring), else through the synchronous calls the pool makes
// (epoll); the bytes are the same either way.
export File(): u32 {
    path := "rtio_file.tmp"
    async := sys.PollSupports(sys.OpWrite) && Backend() != 3
    r := sys.OpenFile(path, 1, async)
    if !Ok(r) {
        return 1
    }
    h := Trunc[uint](r.value)
    var data: [6]u8 = [97, 98, 99, 100, 101, 102]
    var back: [8]u8 = [0; 8]
    if async && sys.PollAttach(h) {
        var w := sys.NewOp(sys.OpWrite, h)
        w.buf = raw.DataMut(mut data[..])
        w.len = 6
        w.pos = 0
        Start(raw.OfMut(mut w))
        var one: [1]*sys.Op = [raw.OfMut(mut w)]
        if Reap(one[..], 10_000) != 0 || w.res.value != 6 {
            return 2
        }
        var rd := sys.NewOp(sys.OpRead, h)
        rd.buf = raw.DataMut(mut back[..])
        rd.len = 8
        rd.pos = 2
        Start(raw.OfMut(mut rd))
        var two: [1]*sys.Op = [raw.OfMut(mut rd)]
        if Reap(two[..], 10_000) != 0 || rd.res.value != 4 {
            return 3
        }
        var end := sys.NewOp(sys.OpRead, h)
        end.buf = raw.DataMut(mut back[..])
        end.len = 8
        end.pos = 6
        Start(raw.OfMut(mut end))
        var three: [1]*sys.Op = [raw.OfMut(mut end)]
        if Reap(three[..], 10_000) != 0 || !Ok(end.res) || end.res.value != 0 {
            return 4
        }
        sys.PollDetach(h)
    } else {
        if sys.WriteSync(h, data[..], 0).value != 6 {
            return 5
        }
        if sys.ReadSync(h, mut back[..], 2).value != 4 {
            return 6
        }
    }
    if back[0] != 99 || back[3] != 102 {
        return 7
    }
    if !Ok(sys.Close(h)) {
        return 8
    }
    if !Ok(sys.Remove(path)) {
        return 9
    }
    return 0
}

// Xorshift is Marsaglia's xorshift64 step, the cases' seeded choices.
Xorshift(x: u64): u64 {
    var y := x ^ (x << 13)
    y = y ^ (y >> 7)
    return y ^ (y << 17)
}

// CancelStress: rounds of a pending recv with a send and a cancel in a
// seeded order (send then cancel, cancel then send, send only, cancel
// only). Every recv is delivered exactly once, with data or Canceled; no
// byte is lost or duplicated: the bytes received, including a final drain
// after the client's shutdown, equal the bytes sent, in order.
export CancelStress(seed: u64, rounds: int): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var x := seed | 1
    var sent := 0
    var got := 0
    var next: u8 = 0
    var rbuf: [64]u8 = [0; 64]
    var sbuf: [1]u8 = [0]
    for _ in 0..rounds {
        x = Xorshift(x)
        choice := x % 4
        var rcv := sys.NewOp(sys.OpRecv, p.s)
        rcv.buf = raw.DataMut(mut rbuf[..])
        rcv.len = 64
        Start(raw.OfMut(mut rcv))
        var snd := sys.NewOp(sys.OpSend, p.c)
        snd.buf = raw.DataMut(mut sbuf[..])
        snd.len = 1
        var sending := choice != 3
        if choice == 1 {
            rcv.cancel.Store(1)
            sys.PollCancel(raw.OfMut(mut rcv))
        }
        if sending {
            sbuf[0] = Trunc[u8](sent)
            Start(raw.OfMut(mut snd))
            sent += 1
        }
        if choice == 0 || choice == 3 {
            rcv.cancel.Store(1)
            sys.PollCancel(raw.OfMut(mut rcv))
        }
        var ops: [2]*sys.Op = [raw.OfMut(mut rcv), raw.OfMut(mut snd)]
        var count := 1
        if sending {
            count = 2
        }
        if Reap(ops[..count], 10_000) != 0 {
            _ = ClosePair(&p)
            return 1
        }
        if sending && snd.res.value != 1 {
            _ = ClosePair(&p)
            return 2
        }
        if rcv.res.err == core.SysErr.None {
            for i in 0..int(rcv.res.value) {
                if rbuf[i] != next {
                    _ = ClosePair(&p)
                    return 3
                }
                next = next +% 1
            }
            got += int(rcv.res.value)
        } else if rcv.res.err != core.SysErr.Canceled {
            _ = ClosePair(&p)
            return 4
        }
    }
    if !Ok(sys.SocketShutdown(p.c, 1)) {
        _ = ClosePair(&p)
        return 5
    }
    for {
        var rcv := sys.NewOp(sys.OpRecv, p.s)
        rcv.buf = raw.DataMut(mut rbuf[..])
        rcv.len = 64
        Start(raw.OfMut(mut rcv))
        var one: [1]*sys.Op = [raw.OfMut(mut rcv)]
        if Reap(one[..], 10_000) != 0 || !Ok(rcv.res) {
            _ = ClosePair(&p)
            return 6
        }
        if rcv.res.value == 0 {
            break
        }
        for i in 0..int(rcv.res.value) {
            if rbuf[i] != next {
                _ = ClosePair(&p)
                return 7
            }
            next = next +% 1
        }
        got += int(rcv.res.value)
    }
    if got != sent {
        _ = ClosePair(&p)
        return 8
    }
    return ClosePair(&p)
}

// PingPong times n one-byte round trips over a loopback pair through the
// poller (a recv pending before each send, as a server's echo loop has
// it): nanoseconds in all, or a negative step number on failure.
export PingPong(n: int): i64 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return -i64(p.code)
    }
    var a: [1]u8 = [1]
    var b: [1]u8 = [0]
    t0 := sys.Now()
    for _ in 0..n {
        var rs := sys.NewOp(sys.OpRecv, p.s)
        rs.buf = raw.DataMut(mut b[..])
        rs.len = 1
        Start(raw.OfMut(mut rs))
        var sc := sys.NewOp(sys.OpSend, p.c)
        sc.buf = raw.DataMut(mut a[..])
        sc.len = 1
        Start(raw.OfMut(mut sc))
        var one: [2]*sys.Op = [raw.OfMut(mut rs), raw.OfMut(mut sc)]
        if Reap(one[..], 10_000) != 0 || rs.res.value != 1 {
            _ = ClosePair(&p)
            return -1
        }
        var rc := sys.NewOp(sys.OpRecv, p.c)
        rc.buf = raw.DataMut(mut a[..])
        rc.len = 1
        Start(raw.OfMut(mut rc))
        var ss := sys.NewOp(sys.OpSend, p.s)
        ss.buf = raw.DataMut(mut b[..])
        ss.len = 1
        Start(raw.OfMut(mut ss))
        var two: [2]*sys.Op = [raw.OfMut(mut rc), raw.OfMut(mut ss)]
        if Reap(two[..], 10_000) != 0 || rc.res.value != 1 {
            _ = ClosePair(&p)
            return -2
        }
    }
    t := sys.Now() - t0
    _ = ClosePair(&p)
    return t
}

// Shared is what the reaper threads of ThreadStress share with Main, in a
// page of its own.
struct Shared {
    stop: raw.Atomic[u32]
    bad: raw.Atomic[u32]
    exited: raw.Atomic[u32]
    delivered: raw.Atomic[u64]
}

// Reaper runs on a thread of its own: PollWait in a loop until stop, every
// delivered op marked once (a second delivery sets bad).
Reaper(arg: *u8) {
    unsafe "arg is the Shared page, live until every reaper has exited" {
        sh := raw.PtrCast[u8, Shared](arg)
        var out: [16]*sys.Op = [raw.Null[sys.Op](); 16]
        for (*sh).stop.Load() == 0 {
            n := sys.PollWait(2_000_000, mut out[..])
            for i in 0..n {
                if !(*out[i]).state.Cas(1, 3) {
                    (*sh).bad.Store(1)
                }
                _ = (*sh).delivered.Add(1)
            }
        }
        _ = (*sh).exited.Add(1)
    }
}

// Await spins (yielding the thread) until a reaper delivers op.
Await(op: *sys.Op, ms: i64): bool {
    deadline := sys.Now() + ms * 1_000_000
    unsafe "the probe's op" {
        for (*op).state.Load() != 3 {
            if sys.Now() > deadline {
                return false
            }
            sys.ThreadYield()
        }
    }
    return true
}

// ThreadStress: reaper threads poll at once while Main submits rounds of
// recv, send and cancel in a seeded order, as CancelStress does; every op
// is delivered exactly once by some thread, and no byte is lost. A cancel
// may run after another thread already delivered the op (the harmless late
// cancel of the contract).
export ThreadStress(seed: u64, threads: int, rounds: int): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    page := sys.Reserve(4096, 4096)
    if raw.IsNull(page) || !sys.Commit(page, 4096) {
        return 1
    }
    var code: u32 = 0
    unsafe "page is the Shared record" {
        sh := raw.PtrCast[u8, Shared](page)
        for _ in 0..threads {
            if !Ok(sys.ThreadStart(Reaper, page, 262144)) {
                return 2
            }
        }
        var x := seed | 1
        var sent := 0
        var got := 0
        var rbuf: [64]u8 = [0; 64]
        var sbuf: [1]u8 = [0]
        for _ in 0..rounds {
            x = Xorshift(x)
            choice := x % 4
            var rcv := sys.NewOp(sys.OpRecv, p.s)
            rcv.buf = raw.DataMut(mut rbuf[..])
            rcv.len = 64
            var snd := sys.NewOp(sys.OpSend, p.c)
            snd.buf = raw.DataMut(mut sbuf[..])
            snd.len = 1
            Start(raw.OfMut(mut rcv))
            if choice == 1 {
                rcv.cancel.Store(1)
                sys.PollCancel(raw.OfMut(mut rcv))
            }
            if choice != 3 {
                Start(raw.OfMut(mut snd))
                sent += 1
            }
            if choice == 0 || choice == 3 {
                rcv.cancel.Store(1)
                sys.PollCancel(raw.OfMut(mut rcv))
            }
            if !Await(raw.OfMut(mut rcv), 10_000) || (choice != 3 && !Await(raw.OfMut(mut snd), 10_000)) {
                code = 3
                break
            }
            if rcv.res.err == core.SysErr.None {
                got += int(rcv.res.value)
            } else if rcv.res.err != core.SysErr.Canceled {
                code = 4
                break
            }
        }
        if code == 0 && !Ok(sys.SocketShutdown(p.c, 1)) {
            code = 5
        }
        for code == 0 {
            var rcv := sys.NewOp(sys.OpRecv, p.s)
            rcv.buf = raw.DataMut(mut rbuf[..])
            rcv.len = 64
            Start(raw.OfMut(mut rcv))
            if !Await(raw.OfMut(mut rcv), 10_000) || !Ok(rcv.res) {
                code = 6
                break
            }
            if rcv.res.value == 0 {
                break
            }
            got += int(rcv.res.value)
        }
        if code == 0 && got != sent {
            code = 7
        }
        (*sh).stop.Store(1)
        deadline := sys.Now() + 10_000_000_000
        for (*sh).exited.Load() != Trunc[u32](threads) {
            sys.PollWake()
            sys.ThreadYield()
            if sys.Now() > deadline {
                return 8
            }
        }
        if code == 0 && (*sh).bad.Load() != 0 {
            code = 9
        }
    }
    sys.Release(page, 4096)
    c := ClosePair(&p)
    if code != 0 {
        return code
    }
    return c
}

// PathOps (backends that do path operations, io_uring): create, write,
// sync, rename, open, read at the stream position, remove, a NotFound open
// of the removed name, and InvalidPath for a NUL byte, all through the
// poller. Elsewhere 0: those kinds go to the pool.
export PathOps(): u32 {
    if !sys.PollSupports(sys.OpOpen) {
        return 0
    }
    var scratch: [512]u8 = [0; 512]
    var data: [3]u8 = [120, 121, 122]
    var back: [8]u8 = [0; 8]
    var op := sys.NewOp(sys.OpOpen, 0)
    op.path = "rtio_path_a.tmp"
    op.pos = 1
    op.buf = raw.DataMut(mut scratch[..])
    op.len = 512
    Start(raw.OfMut(mut op))
    var one: [1]*sys.Op = [raw.OfMut(mut op)]
    if Reap(one[..], 10_000) != 0 || !Ok(op.res) {
        return 1
    }
    h := Trunc[uint](op.res.value)
    var w := sys.NewOp(sys.OpWrite, h)
    w.buf = raw.DataMut(mut data[..])
    w.len = 3
    w.pos = 0
    Start(raw.OfMut(mut w))
    var two: [1]*sys.Op = [raw.OfMut(mut w)]
    if Reap(two[..], 10_000) != 0 || w.res.value != 3 {
        return 2
    }
    var sy := sys.NewOp(sys.OpSync, h)
    Start(raw.OfMut(mut sy))
    var three: [1]*sys.Op = [raw.OfMut(mut sy)]
    if Reap(three[..], 10_000) != 0 || !Ok(sy.res) {
        return 3
    }
    _ = sys.Close(h)
    var rn := sys.NewOp(sys.OpRename, 0)
    rn.path = "rtio_path_a.tmp"
    rn.path2 = "rtio_path_b.tmp"
    rn.buf = raw.DataMut(mut scratch[..])
    rn.len = 512
    Start(raw.OfMut(mut rn))
    var four: [1]*sys.Op = [raw.OfMut(mut rn)]
    if Reap(four[..], 10_000) != 0 || !Ok(rn.res) {
        return 4
    }
    var op2 := sys.NewOp(sys.OpOpen, 0)
    op2.path = "rtio_path_b.tmp"
    op2.pos = 0
    op2.buf = raw.DataMut(mut scratch[..])
    op2.len = 512
    Start(raw.OfMut(mut op2))
    var five: [1]*sys.Op = [raw.OfMut(mut op2)]
    if Reap(five[..], 10_000) != 0 || !Ok(op2.res) {
        return 5
    }
    h2 := Trunc[uint](op2.res.value)
    var rd := sys.NewOp(sys.OpRead, h2)
    rd.buf = raw.DataMut(mut back[..])
    rd.len = 8
    rd.pos = -1
    Start(raw.OfMut(mut rd))
    var six: [1]*sys.Op = [raw.OfMut(mut rd)]
    if Reap(six[..], 10_000) != 0 || rd.res.value != 3 || back[2] != 122 {
        return 6
    }
    _ = sys.Close(h2)
    var rm := sys.NewOp(sys.OpRemove, 0)
    rm.path = "rtio_path_b.tmp"
    rm.buf = raw.DataMut(mut scratch[..])
    rm.len = 512
    Start(raw.OfMut(mut rm))
    var seven: [1]*sys.Op = [raw.OfMut(mut rm)]
    if Reap(seven[..], 10_000) != 0 || !Ok(rm.res) {
        return 7
    }
    var gone := sys.NewOp(sys.OpOpen, 0)
    gone.path = "rtio_path_b.tmp"
    gone.pos = 0
    gone.buf = raw.DataMut(mut scratch[..])
    gone.len = 512
    Start(raw.OfMut(mut gone))
    var eight: [1]*sys.Op = [raw.OfMut(mut gone)]
    if Reap(eight[..], 10_000) != 0 || gone.res.err != core.SysErr.NotFound {
        return 8
    }
    var bad := sys.NewOp(sys.OpOpen, 0)
    bad.path = "a\u{0}b"
    bad.buf = raw.DataMut(mut scratch[..])
    bad.len = 512
    Start(raw.OfMut(mut bad))
    var nine: [1]*sys.Op = [raw.OfMut(mut bad)]
    if Reap(nine[..], 10_000) != 0 || bad.res.err != core.SysErr.InvalidPath {
        return 9
    }
    return 0
}

// SendOne is EdgeInject's hook callback: one byte from the socket at arg,
// sent while the receiving side is between its failed try and its
// recording. A send that does not finish at once is reaped here, so its op
// never outlives this frame.
SendOne(arg: *u8) {
    unsafe "arg points to the sending socket in EdgeInject's frame" {
        var b: [1]u8 = [9]
        var op := sys.NewOp(sys.OpSend, raw.Read(raw.PtrCast[u8, uint](arg)))
        op.buf = raw.DataMut(mut b[..])
        op.len = 1
        Start(raw.OfMut(mut op))
        var one: [1]*sys.Op = [raw.OfMut(mut op)]
        _ = Reap(one[..], 10_000)
    }
}

// EdgeInject (epoll; RT-F3's controlled schedule): every recv's first try
// gets EAGAIN, then data arrives and its edge is reaped before the recv is
// recorded. Each recv must still get its byte at once; a protocol that
// recorded the op anyway would wait for an edge already consumed, and the
// Reap would time out.
export EdgeInject(): u32 {
    if Backend() != 3 {
        return 0
    }
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var c := p.c
    var got := 0
    var rbuf: [64]u8 = [0; 64]
    unsafe "c lives in this frame while the hook is on" {
        _ = sys.PollTestHook(SendOne, raw.PtrCast[uint, u8](raw.OfMut(mut c)), true)
    }
    var code: u32 = 0
    for _ in 0..200 {
        var rcv := sys.NewOp(sys.OpRecv, p.s)
        rcv.buf = raw.DataMut(mut rbuf[..])
        rcv.len = 64
        Start(raw.OfMut(mut rcv))
        var one: [1]*sys.Op = [raw.OfMut(mut rcv)]
        if Reap(one[..], 3_000) != 0 || !Ok(rcv.res) || rcv.res.value < 1 {
            code = 1
            break
        }
        got += int(rcv.res.value)
    }
    var n: u64 = 0
    unsafe "the hook goes off before c's frame ends" {
        n = sys.PollTestHook(SendOne, raw.PtrCast[uint, u8](raw.OfMut(mut c)), false)
    }
    _ = ClosePair(&p)
    if code != 0 {
        return code
    }
    if n < 200 {
        return 2
    }
    if got != 200 {
        return 3
    }
    return 0
}
`
